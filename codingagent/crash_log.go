package codingagent

// Port of core/crash-log.ts: crash persistence with bounded history,
// extension stack matching, and unnotified-crash handoff.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CrashRecord is one persisted crash.
type CrashRecord struct {
	Timestamp   string `json:"timestamp"`
	Version     string `json:"version"`
	Kind        string `json:"kind"` // uncaught_exception | fatal_error
	Message     string `json:"message"`
	Stack       string `json:"stack"`
	SessionFile string `json:"sessionFile"`
	Cwd         string `json:"cwd"`
	Notified    bool   `json:"notified,omitempty"`
}

const (
	maxCrashRecords = 5
	maxCrashAgeMs   = 7 * 24 * 60 * 60 * 1000
)

func crashLogPath(agentDir string) string {
	if agentDir == "" {
		agentDir = GetAgentDir()
	}
	return filepath.Join(agentDir, "crashes.json")
}

// ReadCrashLog parses the crash log (empty on any failure).
func ReadCrashLog(path string) []CrashRecord {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var parsed []CrashRecord
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	var records []CrashRecord
	for _, record := range parsed {
		if record.Timestamp != "" && record.Message != "" {
			records = append(records, record)
		}
	}
	return records
}

func writeCrashLog(records []CrashRecord, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

// normalizeStackPath forward-slashes and strips trailing separators.
func normalizeStackPath(value string) string {
	normalized := strings.ReplaceAll(value, "\\", "/")
	for len(normalized) > 1 && strings.HasSuffix(normalized, "/") {
		normalized = strings.TrimSuffix(normalized, "/")
	}
	return normalized
}

var crashWindowsDrivePattern = regexp.MustCompile(`(?i)^[a-z]:/`)
var stackFramePattern = regexp.MustCompile(`^\s+at\s`)

// stackContainsPath checks a stack for a path (descendants optional;
// exact matches must end at a delimiter).
func stackContainsPath(stack, targetPath string, includeDescendants bool) bool {
	target := normalizeStackPath(targetPath)
	if target == "" || strings.HasPrefix(target, "<") {
		return false
	}
	caseInsensitive := crashWindowsDrivePattern.MatchString(target)
	haystack := stack
	needle := target
	if caseInsensitive {
		haystack = strings.ToLower(stack)
		needle = strings.ToLower(target)
	}
	if includeDescendants {
		return strings.Contains(haystack, needle+"/")
	}

	searchFrom := 0
	for {
		index := strings.Index(haystack[searchFrom:], needle)
		if index == -1 {
			return false
		}
		index += searchFrom
		next := byte(0)
		if index+len(needle) < len(haystack) {
			next = haystack[index+len(needle)]
		}
		if next == 0 || next == ':' || next == ')' || next == ' ' || next == '\t' || next == '\n' || next == '\r' {
			return true
		}
		searchFrom = index + len(needle)
	}
}

// ExtensionStackMetadata is the extension surface stack matching needs.
type ExtensionStackMetadata struct {
	Path         string
	ResolvedPath string
	SourceInfo   SourceInfo
}

var remoteSourcePattern = regexp.MustCompile(`^(?:npm:|git:|https?://|ssh://)`)
var scriptExtensionPattern = regexp.MustCompile(`\.[cm]?[jt]s$`)
var indexEntryPattern = regexp.MustCompile(`/index\.[cm]?[jt]s$`)

// FindExtensionStackMatches lists loaded extensions whose source files
// appear in a stack trace.
func FindExtensionStackMatches(stack string, extensions []ExtensionStackMetadata) []string {
	if stack == "" {
		return nil
	}

	// Normalize: skip the first line, keep "at " frames, decode URIs,
	// forward-slash.
	var frameLines []string
	for i, line := range strings.Split(stack, "\n") {
		if i == 0 {
			continue
		}
		if !stackFramePattern.MatchString(line) {
			continue
		}
		if decoded, err := url.PathUnescape(line); err == nil {
			frameLines = append(frameLines, decoded)
			continue
		}
		frameLines = append(frameLines, line)
	}
	normalizedStack := strings.ReplaceAll(strings.Join(frameLines, "\n"), "\\", "/")

	var matches []string
	seen := map[string]bool{}

	for _, extension := range extensions {
		resolvedPath := normalizeStackPath(extension.ResolvedPath)
		singleFilePackage := extension.SourceInfo.Origin == OriginPackage &&
			!remoteSourcePattern.MatchString(extension.SourceInfo.Source) &&
			scriptExtensionPattern.MatchString(extension.SourceInfo.Source)
		packageRoot := ""
		if extension.SourceInfo.Origin == OriginPackage && !singleFilePackage && extension.SourceInfo.BaseDir != "" {
			packageRoot = extension.SourceInfo.BaseDir
		}

		slashIndex := strings.LastIndexByte(resolvedPath, '/')
		directoryEntry := indexEntryPattern.MatchString(resolvedPath)

		var matched bool
		switch {
		case packageRoot != "":
			matched = stackContainsPath(normalizedStack, packageRoot, true)
		case directoryEntry && slashIndex != -1:
			matched = stackContainsPath(normalizedStack, resolvedPath[:slashIndex], true)
		default:
			matched = stackContainsPath(normalizedStack, resolvedPath, false)
		}
		if !matched {
			continue
		}

		label := extension.Path
		if extension.SourceInfo.Origin == OriginPackage && extension.SourceInfo.Source != "" {
			label = extension.SourceInfo.Source
		}
		if !seen[label] {
			seen[label] = true
			matches = append(matches, label)
		}
	}
	return matches
}

// RecordCrash persists one crash (best-effort; nil on failure).
func RecordCrash(kind string, err error, sessionFile, cwd, path string) *CrashRecord {
	if path == "" {
		path = crashLogPath("")
	}
	message := fmt.Sprintf("%v", err)
	stack := ""
	records := ReadCrashLog(path)
	records = append(records, CrashRecord{
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Version:     Version(),
		Kind:        kind,
		Message:     message,
		Stack:       stack,
		SessionFile: sessionFile,
		Cwd:         cwd,
	})
	if len(records) > maxCrashRecords {
		records = records[len(records)-maxCrashRecords:]
	}
	if err := writeCrashLog(records, path); err != nil {
		return nil
	}
	record := records[len(records)-1]
	return &record
}

// TakeUnnotifiedCrash returns the newest unannounced recent crash,
// marking all pending records notified.
func TakeUnnotifiedCrash(path string, nowMs int64) *CrashRecord {
	if path == "" {
		path = crashLogPath("")
	}
	records := ReadCrashLog(path)

	var crash *CrashRecord
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Notified {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, records[i].Timestamp)
		if err != nil {
			continue
		}
		if nowMs-parsed.UnixMilli() <= maxCrashAgeMs {
			crash = &records[i]
			break
		}
	}
	if crash == nil {
		return nil
	}

	for i := range records {
		if !records[i].Notified {
			records[i].Notified = true
		}
	}
	_ = writeCrashLog(records, path)
	return crash
}

// ClearCrashLog removes the log (best-effort).
func ClearCrashLog(path string) {
	if path == "" {
		path = crashLogPath("")
	}
	_ = os.Remove(path)
}
