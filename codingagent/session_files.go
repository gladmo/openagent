package codingagent

// Port of core/session-manager.ts (part 2): migrations, file loading, header
// discovery, session info building, and listing.

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gladmo/openagent/ai"
)

// generateSessionID creates a UUIDv7 session id.
func generateSessionID() string {
	return ai.UUIDv7()
}

// migrateV1ToV2 adds id/parentId linkage in place (returns whether applied).
func migrateV1ToV2(entries []FileEntry) {
	seen := map[string]bool{}
	var prevID *string

	for _, entry := range entries {
		if header, ok := entry.(*SessionHeader); ok {
			version := 2
			header.Version = &version
			continue
		}

		base := entry.(SessionEntry).base()
		base.ID = generateShortID(seen)
		base.ParentID = prevID
		id := base.ID
		prevID = &id

		// firstKeptEntryIndex -> firstKeptEntryId for compaction entries.
		if compaction, ok := entry.(*CompactionEntry); ok {
			var legacy struct {
				FirstKeptEntryIndex *int `json:"firstKeptEntryIndex"`
			}
			if err := json.Unmarshal(rawOf(entry), &legacy); err == nil && legacy.FirstKeptEntryIndex != nil {
				idx := *legacy.FirstKeptEntryIndex
				if idx >= 0 && idx < len(entries) {
					if target, ok := entries[idx].(SessionEntry); ok {
						compaction.FirstKeptEntryID = target.base().ID
					}
				}
			}
		}
	}
}

func rawOf(entry FileEntry) []byte {
	data, _ := json.Marshal(entry)
	return data
}

// generateShortID makes an 8-hex-char collision-checked id.
func generateShortID(seen map[string]bool) string {
	for i := 0; i < 100; i++ {
		id := randomHex(4)
		if !seen[id] {
			seen[id] = true
			return id
		}
	}
	id := randomHex(16)
	seen[id] = true
	return id
}

func randomHex(n int) string {
	buffer := make([]byte, n)
	_, _ = rand.Read(buffer)
	return hex.EncodeToString(buffer)
}

// migrateV2ToV3 renames hookMessage roles to custom in place.
func migrateV2ToV3(entries []FileEntry) {
	for _, entry := range entries {
		if header, ok := entry.(*SessionHeader); ok {
			version := CurrentSessionVersion
			header.Version = &version
			continue
		}
		if message, ok := entry.(*SessionMessageEntry); ok {
			var probe struct {
				Role string `json:"role"`
			}
			if err := json.Unmarshal(message.Message, &probe); err == nil && probe.Role == "hookMessage" {
				var asMap map[string]any
				if err := json.Unmarshal(message.Message, &asMap); err == nil {
					asMap["role"] = "custom"
					if updated, err := json.Marshal(asMap); err == nil {
						message.Message = updated
					}
				}
			}
		}
	}
}

// MigrateSessionEntries brings entries to the current version (exported for
// tests); reports whether a migration applied.
func MigrateSessionEntries(entries []FileEntry) bool {
	var header *SessionHeader
	for _, entry := range entries {
		if typed, ok := entry.(*SessionHeader); ok {
			header = typed
			break
		}
	}
	version := 1
	if header != nil && header.Version != nil {
		version = *header.Version
	}

	if version >= CurrentSessionVersion {
		return false
	}

	if version < 2 {
		migrateV1ToV2(entries)
	}
	if version < 3 {
		migrateV2ToV3(entries)
	}
	return true
}

const (
	sessionReadBufferSize       = 1024 * 1024
	sessionHeaderReadBufferSize = 4096
	maxSessionHeaderScanBytes   = 1024 * 1024
)

// SessionHeaderScanLimitError marks oversized headers.
type SessionHeaderScanLimitError struct {
	FilePath string
}

func (e *SessionHeaderScanLimitError) Error() string {
	return fmt.Sprintf("Session header exceeds %d-byte scan limit: %s", maxSessionHeaderScanBytes, e.FilePath)
}

// LoadEntriesFromFile reads a JSONL session file, repairing missing final
// newlines; nil when absent or missing a valid header.
func LoadEntriesFromFile(filePath string) []FileEntry {
	resolvedFilePath := NormalizePathDefault(filePath)
	file, err := os.Open(resolvedFilePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	var entries []FileEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, sessionReadBufferSize), sessionReadBufferSize)
	scanned := 0
	for scanner.Scan() {
		scanned++
		if entry := ParseFileEntry(scanner.Text()); entry != nil {
			entries = append(entries, entry)
		}
	}

	if len(entries) == 0 {
		return entries
	}
	header, ok := entries[0].(*SessionHeader)
	if !ok || header.ID == "" {
		return nil
	}

	// A missing final newline is repaired.
	if data, err := os.ReadFile(resolvedFilePath); err == nil && len(data) > 0 && data[len(data)-1] != '\n' {
		_ = os.WriteFile(resolvedFilePath, append(data, '\n'), 0o644)
	}
	return entries
}

// ReadSessionHeader reads just the header line within a bounded scan.
func ReadSessionHeader(filePath string) (*SessionHeader, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, sessionHeaderReadBufferSize)
	var line []byte
	scanned := 0
	for {
		chunk, isPrefix, readErr := reader.ReadLine()
		scanned += len(chunk)
		if scanned > maxSessionHeaderScanBytes {
			return nil, &SessionHeaderScanLimitError{FilePath: filePath}
		}
		line = append(line, chunk...)
		if !isPrefix {
			break
		}
		if readErr != nil {
			break
		}
	}
	header, ok := ParseFileEntry(string(line)).(*SessionHeader)
	if !ok {
		// Not a header line: null-equivalent.
		return nil, nil
	}
	if header.ID == "" {
		return nil, nil
	}
	return header, nil
}

func readSessionHeaderForDiscovery(filePath string) *SessionHeader {
	header, err := ReadSessionHeader(filePath)
	if err != nil {
		return nil
	}
	return header
}

func sessionCwdMatches(cwd, resolvedCwd string) bool {
	return cwd != "" && ResolvePath(cwd, "", PathInputOptions{}) == resolvedCwd
}

// FindMostRecentSession returns the newest session file whose header cwd
// matches (when given).
func FindMostRecentSession(sessionDir, cwd string) string {
	resolvedSessionDir := NormalizePathDefault(sessionDir)
	resolvedCwd := ""
	if cwd != "" {
		resolvedCwd = ResolvePath(cwd, "", PathInputOptions{})
	}
	entries, err := os.ReadDir(resolvedSessionDir)
	if err != nil {
		return ""
	}

	type candidate struct {
		path  string
		mtime int64
	}
	var files []candidate
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(resolvedSessionDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, candidate{path: path, mtime: info.ModTime().UnixMilli()})
	}
	sort.Slice(files, func(a, b int) bool { return files[a].mtime > files[b].mtime })

	for _, file := range files {
		header := readSessionHeaderForDiscovery(file.path)
		if header == nil {
			continue
		}
		if resolvedCwd == "" || sessionCwdMatches(header.Cwd, resolvedCwd) {
			return file.path
		}
	}
	return ""
}

// SessionInfo summarizes a session file for listings.
type SessionInfo struct {
	Path              string
	ID                string
	Cwd               string
	Name              string
	ParentSessionPath string
	Created           time.Time
	Modified          time.Time
	MessageCount      int
	FirstMessage      string
	AllMessagesText   string
}

func extractTextContent(message json.RawMessage) string {
	var probe struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(message, &probe); err != nil {
		return ""
	}
	if len(probe.Content) > 0 && probe.Content[0] == '"' {
		var text string
		if err := json.Unmarshal(probe.Content, &text); err == nil {
			return text
		}
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(probe.Content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, " ")
}

func getMessageActivityTime(entry *SessionMessageEntry) (int64, bool) {
	var probe struct {
		Role      string   `json:"role"`
		Timestamp *float64 `json:"timestamp"`
	}
	if err := json.Unmarshal(entry.Message, &probe); err != nil {
		return 0, false
	}
	if probe.Role != "user" && probe.Role != "assistant" {
		return 0, false
	}
	if probe.Timestamp != nil {
		return int64(*probe.Timestamp), true
	}
	parsed, err := time.Parse(time.RFC3339, entry.Timestamp)
	if err != nil {
		return 0, false
	}
	return parsed.UnixMilli(), true
}

// BuildSessionInfo streams a session file into a summary.
func BuildSessionInfo(filePath string) *SessionInfo {
	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	stats, err := file.Stat()
	if err != nil {
		return nil
	}

	var header *SessionHeader
	messageCount := 0
	firstMessage := ""
	var allMessages []string
	name := ""
	var lastActivityTime int64

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, sessionReadBufferSize), sessionReadBufferSize)
	for scanner.Scan() {
		entry := ParseFileEntry(scanner.Text())
		if entry == nil {
			continue
		}

		if header == nil {
			if typed, ok := entry.(*SessionHeader); ok {
				header = typed
				continue
			}
			return nil
		}

		if info, ok := entry.(*SessionInfoEntry); ok {
			name = strings.TrimSpace(info.Name)
		}

		message, ok := entry.(*SessionMessageEntry)
		if !ok {
			continue
		}
		messageCount++

		if activityTime, hasTime := getMessageActivityTime(message); hasTime {
			if activityTime > lastActivityTime {
				lastActivityTime = activityTime
			}
		}

		var role struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(message.Message, &role); err != nil {
			continue
		}
		if role.Role != "user" && role.Role != "assistant" {
			continue
		}
		textContent := extractTextContent(message.Message)
		if textContent == "" {
			continue
		}
		allMessages = append(allMessages, textContent)
		if firstMessage == "" && role.Role == "user" {
			firstMessage = textContent
		}
	}

	if header == nil {
		return nil
	}

	created := time.Time{}
	if parsed, err := time.Parse(time.RFC3339, header.Timestamp); err == nil {
		created = parsed
	}
	modified := stats.ModTime()
	if lastActivityTime > 0 {
		modified = time.UnixMilli(lastActivityTime)
	} else if !created.IsZero() {
		modified = created
	}

	if firstMessage == "" {
		firstMessage = "(no messages)"
	}
	return &SessionInfo{
		Path:              filePath,
		ID:                header.ID,
		Cwd:               header.Cwd,
		Name:              name,
		ParentSessionPath: header.ParentSession,
		Created:           created,
		Modified:          modified,
		MessageCount:      messageCount,
		FirstMessage:      firstMessage,
		AllMessagesText:   strings.Join(allMessages, " "),
	}
}

// SessionListProgress receives listing progress.
type SessionListProgress func(loaded, total int, partialSessions []SessionInfo)

const maxConcurrentSessionInfoLoads = 10

// ListSessionsFromDir summarizes every .jsonl in dir, newest first.
func ListSessionsFromDir(dir string, onProgress SessionListProgress) []SessionInfo {
	if _, err := os.Stat(dir); err != nil {
		return nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jsonl") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	// Newest-name-first ordering mirrors the reference's descending sort.
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}

	files := make([]string, len(names))
	for i, name := range names {
		files[i] = filepath.Join(dir, name)
	}
	total := len(files)

	var (
		mu      sync.Mutex
		loaded  int
		partial []SessionInfo
	)
	results := make([]*SessionInfo, len(files))
	semaphore := make(chan struct{}, maxConcurrentSessionInfoLoads)
	var wg sync.WaitGroup
	for index, path := range files {
		wg.Add(1)
		go func(index int, path string) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			info := BuildSessionInfo(path)
			results[index] = info

			mu.Lock()
			loaded++
			if info != nil {
				partial = append(partial, *info)
			}
			currentLoaded := loaded
			currentPartial := partial
			mu.Unlock()

			if onProgress != nil {
				var publish []SessionInfo
				if currentLoaded == 1 || currentLoaded%10 == 0 || currentLoaded == total {
					publish = append([]SessionInfo(nil), currentPartial...)
					sort.Slice(publish, func(a, b int) bool {
						return publish[a].Modified.After(publish[b].Modified)
					})
				}
				onProgress(currentLoaded, total, publish)
			}
		}(index, path)
	}
	wg.Wait()

	var out []SessionInfo
	for _, info := range results {
		if info != nil {
			out = append(out, *info)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Modified.After(out[b].Modified) })
	return out
}
