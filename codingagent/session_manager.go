package codingagent

// Port of core/session-manager.ts (part 3): the SessionManager — append-only
// tree persistence in JSONL files with branching, labels, compaction, and
// lazy file creation.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// NewSessionOptions seed a new session.
type NewSessionOptions struct {
	ID            string
	ParentSession string
}

// getDefaultSessionDirPath encodes cwd into a safe directory name.
func getDefaultSessionDirPath(cwd, agentDir string) string {
	resolvedCwd := ResolvePath(cwd, "", PathInputOptions{})
	resolvedAgentDir := ResolvePath(agentDir, "", PathInputOptions{})
	safePath := "--" + regexp.MustCompile(`^[/\\]`).ReplaceAllString(resolvedCwd, "")
	safePath = regexp.MustCompile(`[/\\:]`).ReplaceAllString(safePath, "-") + "--"
	return filepath.Join(resolvedAgentDir, "sessions", safePath)
}

// GetDefaultSessionDir returns (creating when needed) the cwd's session dir.
func GetDefaultSessionDir(cwd, agentDir string) string {
	sessionDir := getDefaultSessionDirPath(cwd, agentDir)
	if _, err := os.Stat(sessionDir); err != nil {
		_ = os.MkdirAll(sessionDir, 0o755)
	}
	return sessionDir
}

// SessionManager owns one session file's append-only tree.
type SessionManager struct {
	sessionID           string
	sessionFile         string
	sessionDir          string
	cwd                 string
	persist             bool
	flushed             bool
	fileEntries         []FileEntry
	byID                map[string]SessionEntry
	labelsByID          map[string]string
	labelTimestampsByID map[string]string
	leafID              *string
}

// SessionManagerOptions construct a manager directly.
type SessionManagerOptions struct {
	Cwd         string
	SessionDir  string
	SessionFile string
	Persist     bool
	NewSession  *NewSessionOptions
	Preloaded   []FileEntry
}

// CreateSessionManager builds a manager (resume, preload, or fresh).
func CreateSessionManager(options SessionManagerOptions) (*SessionManager, error) {
	manager := &SessionManager{
		sessionDir:          NormalizePathDefault(options.SessionDir),
		cwd:                 ResolvePath(options.Cwd, "", PathInputOptions{}),
		persist:             options.Persist,
		byID:                map[string]SessionEntry{},
		labelsByID:          map[string]string{},
		labelTimestampsByID: map[string]string{},
	}
	if options.Persist && manager.sessionDir != "" {
		if _, err := os.Stat(manager.sessionDir); err != nil {
			if err := os.MkdirAll(manager.sessionDir, 0o755); err != nil {
				return nil, err
			}
		}
	}

	switch {
	case options.SessionFile != "":
		if err := manager.setSessionFile(options.SessionFile, options.Preloaded); err != nil {
			return nil, err
		}
	case len(options.Preloaded) > 0:
		manager.loadEntries(options.Preloaded, options.NewSession)
	default:
		manager.NewSession(options.NewSession)
	}
	return manager, nil
}

// SetSessionFile switches to another session file (resume/branching).
func (m *SessionManager) SetSessionFile(sessionFile string) error {
	return m.setSessionFile(sessionFile, nil)
}

func (m *SessionManager) setSessionFile(sessionFile string, preloaded []FileEntry) error {
	m.sessionFile = ResolvePath(sessionFile, "", PathInputOptions{})
	if _, err := os.Stat(m.sessionFile); err == nil {
		entries := preloaded
		if entries == nil {
			entries = LoadEntriesFromFile(m.sessionFile)
		}

		if len(entries) == 0 {
			explicitPath := m.sessionFile
			if info, err := os.Stat(explicitPath); err == nil && info.Size() > 0 {
				return fmt.Errorf("Session file is not a valid %s session: %s", AppName(), explicitPath)
			}
			m.NewSession(nil)
			m.sessionFile = explicitPath
			m.rewriteFile()
			m.flushed = true
			return nil
		}

		m.loadEntries(entries, nil)
		m.flushed = true
	} else {
		explicitPath := m.sessionFile
		m.NewSession(nil)
		m.sessionFile = explicitPath
	}
	return nil
}

// NewSession resets to a fresh header; returns the new file path.
func (m *SessionManager) NewSession(options *NewSessionOptions) string {
	if options != nil && options.ID != "" {
		if err := AssertValidSessionID(options.ID); err != nil {
			panic(err)
		}
		m.sessionID = options.ID
	} else {
		m.sessionID = generateSessionID()
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	header := &SessionHeader{
		Type:          EntryTypeSession,
		ID:            m.sessionID,
		Timestamp:     timestamp,
		Cwd:           m.cwd,
		ParentSession: options.parentSessionValue(),
	}
	version := CurrentSessionVersion
	header.Version = &version
	m.fileEntries = []FileEntry{header}
	m.byID = map[string]SessionEntry{}
	m.labelsByID = map[string]string{}
	m.labelTimestampsByID = map[string]string{}
	m.leafID = nil
	m.flushed = false

	if m.persist {
		fileTimestamp := regexp.MustCompile(`[:.]`).ReplaceAllString(timestamp, "-")
		m.sessionFile = filepath.Join(m.GetSessionDir(), fileTimestamp+"_"+m.sessionID+".jsonl")
	}
	return m.sessionFile
}

// parentSessionValue resolves optional parent linkage.
func (o *NewSessionOptions) parentSessionValue() string {
	if o == nil {
		return ""
	}
	return o.ParentSession
}

func (m *SessionManager) loadEntries(entries []FileEntry, options *NewSessionOptions) {
	var header *SessionHeader
	for _, entry := range entries {
		if typed, ok := entry.(*SessionHeader); ok {
			header = typed
			break
		}
	}

	if header != nil {
		m.fileEntries = entries
		m.sessionID = header.ID
		if MigrateSessionEntries(m.fileEntries) {
			m.rewriteFile()
		}
	} else {
		m.NewSession(options)
		m.fileEntries = append(m.fileEntries, entries...)
	}

	m.buildIndex()
}

func (m *SessionManager) buildIndex() {
	m.byID = map[string]SessionEntry{}
	m.labelsByID = map[string]string{}
	m.labelTimestampsByID = map[string]string{}
	m.leafID = nil
	for _, entry := range m.fileEntries {
		if _, ok := entry.(*SessionHeader); ok {
			continue
		}
		sessionEntry := entry.(SessionEntry)
		m.byID[sessionEntry.base().ID] = sessionEntry
		id := sessionEntry.base().ID
		m.leafID = &id
		if label, ok := sessionEntry.(*LabelEntry); ok {
			if label.Label != nil && *label.Label != "" {
				m.labelsByID[label.TargetID] = *label.Label
				m.labelTimestampsByID[label.TargetID] = label.Timestamp
			} else {
				delete(m.labelsByID, label.TargetID)
				delete(m.labelTimestampsByID, label.TargetID)
			}
		}
	}
}

func (m *SessionManager) rewriteFile() {
	if !m.persist || m.sessionFile == "" {
		return
	}
	file, err := os.Create(m.sessionFile)
	if err != nil {
		return
	}
	defer file.Close()
	writer := bufio.NewWriter(file)
	for _, entry := range m.fileEntries {
		data, _ := json.Marshal(entry)
		writer.Write(data)
		writer.WriteByte('\n')
	}
	writer.Flush()
}

// IsPersisted reports whether entries are written to disk.
func (m *SessionManager) IsPersisted() bool { return m.persist }

// GetCwd returns the session's working directory.
func (m *SessionManager) GetCwd() string { return m.cwd }

// GetSessionDir returns the session directory.
func (m *SessionManager) GetSessionDir() string { return m.sessionDir }

// UsesDefaultSessionDir reports whether the dir is the cwd-derived default.
func (m *SessionManager) UsesDefaultSessionDir() bool {
	return m.sessionDir == getDefaultSessionDirPath(m.cwd, GetAgentDir())
}

// GetSessionID returns the session id.
func (m *SessionManager) GetSessionID() string { return m.sessionID }

// GetSessionFile returns the session file path ("" when not persisting).
func (m *SessionManager) GetSessionFile() string { return m.sessionFile }

// hasConversation reports whether a user or assistant message exists; lazy
// file creation waits for one so setup-only sessions leave no file.
func (m *SessionManager) hasConversation() bool {
	for _, entry := range m.fileEntries {
		message, ok := entry.(*SessionMessageEntry)
		if !ok {
			continue
		}
		var probe struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(message.Message, &probe); err == nil {
			if probe.Role == "user" || probe.Role == "assistant" {
				return true
			}
		}
	}
	return false
}

func (m *SessionManager) persistEntry(entry SessionEntry) {
	if !m.persist || m.sessionFile == "" {
		return
	}

	if !m.flushed {
		if !m.hasConversation() {
			return
		}
		file, err := os.OpenFile(m.sessionFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return
		}
		writer := bufio.NewWriter(file)
		for _, existing := range m.fileEntries {
			data, _ := json.Marshal(existing)
			writer.Write(data)
			writer.WriteByte('\n')
		}
		writer.Flush()
		file.Close()
		m.flushed = true
	} else {
		data, _ := json.Marshal(entry)
		file, err := os.OpenFile(m.sessionFile, os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		writer := bufio.NewWriter(file)
		writer.Write(data)
		writer.WriteByte('\n')
		writer.Flush()
		file.Close()
	}
}

func (m *SessionManager) appendEntry(entry SessionEntry) {
	m.fileEntries = append(m.fileEntries, entry)
	m.byID[entry.base().ID] = entry
	id := entry.base().ID
	m.leafID = &id
	m.persistEntry(entry)
}

func (m *SessionManager) generateID() string {
	seen := map[string]bool{}
	for id := range m.byID {
		seen[id] = true
	}
	return generateShortID(seen)
}

func (m *SessionManager) nowISO() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// AppendMessage appends a message entry (compaction/branch summaries must
// go through their dedicated appenders).
func (m *SessionManager) AppendMessage(message any) string {
	raw := marshalAgentMessageWithRole(message)
	entry := &SessionMessageEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeMessage, ID: m.generateID(), ParentID: m.cloneLeafID(), Timestamp: m.nowISO(),
		},
		Message: json.RawMessage(raw),
	}
	m.appendEntry(entry)
	return entry.ID
}

// marshalAgentMessageWithRole serializes a message injecting its role tag
// (the ported ai message structs carry Role() methods, not JSON fields, so
// round-trips need the tag added).
func marshalAgentMessageWithRole(message any) []byte {
	raw, err := json.Marshal(message)
	if err != nil {
		return raw
	}
	role := ""
	if typed, ok := message.(interface{ Role() string }); ok {
		role = typed.Role()
	}
	if role == "" {
		return raw
	}
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil || asMap == nil {
		return raw
	}
	// Prepend role by rebuilding: map iteration order is not stable, but
	// jsonx-free deterministic output is not required for session storage.
	asMap["role"] = role
	withRole, err := json.Marshal(asMap)
	if err != nil {
		return raw
	}
	return withRole
}

func (m *SessionManager) cloneLeafID() *string {
	if m.leafID == nil {
		return nil
	}
	clone := *m.leafID
	return &clone
}

// AppendThinkingLevelChange records a thinking-level switch.
func (m *SessionManager) AppendThinkingLevelChange(thinkingLevel string) string {
	entry := &ThinkingLevelChangeEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeThinkingLevelChange, ID: m.generateID(), ParentID: m.cloneLeafID(), Timestamp: m.nowISO(),
		},
		ThinkingLevel: thinkingLevel,
	}
	m.appendEntry(entry)
	return entry.ID
}

// AppendModelChange records a model switch.
func (m *SessionManager) AppendModelChange(provider, modelID string) string {
	entry := &ModelChangeEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeModelChange, ID: m.generateID(), ParentID: m.cloneLeafID(), Timestamp: m.nowISO(),
		},
		Provider: provider,
		ModelID:  modelID,
	}
	m.appendEntry(entry)
	return entry.ID
}

// AppendUsage records model-attributed usage outside LLM context.
func (m *SessionManager) AppendUsage(kind, provider, model string, usage json.RawMessage, note string) *UsageEntry {
	entry := &UsageEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeUsage, ID: m.generateID(), ParentID: m.cloneLeafID(), Timestamp: m.nowISO(),
		},
		Kind:     kind,
		Provider: provider,
		Model:    model,
		Usage:    usage,
		Note:     note,
	}
	m.appendEntry(entry)
	return entry
}

// AppendCompaction records a compaction boundary.
func (m *SessionManager) AppendCompaction(summary string, firstKeptEntryID *string, tokensBefore int64, details json.RawMessage, fromHook bool, usage json.RawMessage) string {
	timestamp := m.nowISO()
	firstKept := ""
	if firstKeptEntryID != nil {
		firstKept = *firstKeptEntryID
	}
	id := m.generateID()
	if firstKept == "" {
		firstKept = id
	}
	entry := &CompactionEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeCompaction, ID: id, ParentID: m.cloneLeafID(), Timestamp: timestamp,
		},
		Summary:          summary,
		FirstKeptEntryID: firstKept,
		TokensBefore:     tokensBefore,
		Details:          details,
		Usage:            usage,
		FromHook:         fromHook,
	}
	m.appendEntry(entry)
	return entry.ID
}

// AppendCustomEntry stores extension state outside LLM context.
func (m *SessionManager) AppendCustomEntry(customType string, data json.RawMessage) string {
	entry := &CustomEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeCustom, ID: m.generateID(), ParentID: m.cloneLeafID(), Timestamp: m.nowISO(),
		},
		CustomType: customType,
		Data:       data,
	}
	m.appendEntry(entry)
	return entry.ID
}

// AppendSessionInfo records a display name.
func (m *SessionManager) AppendSessionInfo(name string) string {
	sanitizedName := strings.TrimSpace(regexp.MustCompile(`[\r\n]+`).ReplaceAllString(name, " "))
	entry := &SessionInfoEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeSessionInfo, ID: m.generateID(), ParentID: m.cloneLeafID(), Timestamp: m.nowISO(),
		},
		Name: sanitizedName,
	}
	m.appendEntry(entry)
	return entry.ID
}

// GetSessionName returns the latest session_info name.
func (m *SessionManager) GetSessionName() string {
	entries := m.GetEntries()
	for i := len(entries) - 1; i >= 0; i-- {
		if info, ok := entries[i].(*SessionInfoEntry); ok {
			return strings.TrimSpace(info.Name)
		}
	}
	return ""
}

// AppendCustomMessageEntry injects an LLM-visible extension message.
func (m *SessionManager) AppendCustomMessageEntry(customType string, content json.RawMessage, display bool, details json.RawMessage) string {
	entry := &CustomMessageEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeCustomMessage, ID: m.generateID(), ParentID: m.cloneLeafID(), Timestamp: m.nowISO(),
		},
		CustomType: customType,
		Content:    content,
		Display:    display,
		Details:    details,
	}
	m.appendEntry(entry)
	return entry.ID
}

// AppendContextEdit appends a branch-local edit of earlier content.
func (m *SessionManager) AppendContextEdit(targetID string, replacement *ContextEditReplacement) (string, error) {
	if replacement != nil && len(replacement.Content) == 0 {
		return "", fmt.Errorf("Context edit replacement must be null or contain string/array content")
	}
	target, ok := m.byID[targetID]
	if !ok {
		return "", fmt.Errorf("Entry %s not found", targetID)
	}
	onBranch := false
	for _, entry := range m.GetBranch("") {
		if entry.base().ID == targetID {
			onBranch = true
			break
		}
	}
	if !onBranch {
		return "", fmt.Errorf("Entry %s is not on the active branch", targetID)
	}

	editable := false
	targetRole := "custom"
	switch typed := target.(type) {
	case *CustomMessageEntry:
		editable = true
	case *SessionMessageEntry:
		var probe struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(typed.Message, &probe); err == nil {
			targetRole = probe.Role
			editable = probe.Role == "user" || probe.Role == "assistant" || probe.Role == "toolResult"
		}
	}
	if !editable {
		return "", fmt.Errorf("Entry %s does not contribute editable model content", targetID)
	}

	normalized := replacement
	if normalized != nil && (targetRole == "assistant" || targetRole == "toolResult") {
		var asString string
		if err := json.Unmarshal(normalized.Content, &asString); err == nil {
			wrapped, _ := json.Marshal([]map[string]string{{"type": "text", "text": asString}})
			normalized = &ContextEditReplacement{Content: wrapped}
		}
	}

	entry := &ContextEditEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeContextEdit, ID: m.generateID(), ParentID: m.cloneLeafID(), Timestamp: m.nowISO(),
		},
		TargetID:    targetID,
		Replacement: normalized,
	}
	m.appendEntry(entry)
	return entry.ID, nil
}

// GetLeafID returns the current leaf entry id (nil before any entry).
func (m *SessionManager) GetLeafID() *string { return m.cloneLeafID() }

// GetLeafEntry returns the leaf entry.
func (m *SessionManager) GetLeafEntry() SessionEntry {
	if m.leafID == nil {
		return nil
	}
	return m.byID[*m.leafID]
}

// GetEntry looks up an entry by id.
func (m *SessionManager) GetEntry(id string) SessionEntry { return m.byID[id] }

// GetChildren returns direct children of an entry.
func (m *SessionManager) GetChildren(parentID string) []SessionEntry {
	var children []SessionEntry
	for _, entry := range m.byID {
		if entry.base().ParentID != nil && *entry.base().ParentID == parentID {
			children = append(children, entry)
		}
	}
	return children
}

// GetLabel returns an entry's resolved label.
func (m *SessionManager) GetLabel(id string) string { return m.labelsByID[id] }

// AppendLabelChange sets or clears an entry's label.
func (m *SessionManager) AppendLabelChange(targetID string, label *string) (string, error) {
	if _, ok := m.byID[targetID]; !ok {
		return "", fmt.Errorf("Entry %s not found", targetID)
	}
	entry := &LabelEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeLabel, ID: m.generateID(), ParentID: m.cloneLeafID(), Timestamp: m.nowISO(),
		},
		TargetID: targetID,
		Label:    label,
	}
	m.appendEntry(entry)
	if label != nil && *label != "" {
		m.labelsByID[targetID] = *label
		m.labelTimestampsByID[targetID] = entry.Timestamp
	} else {
		delete(m.labelsByID, targetID)
		delete(m.labelTimestampsByID, targetID)
	}
	return entry.ID, nil
}

// GetBranch walks from an entry (default the leaf) to root in path order.
func (m *SessionManager) GetBranch(fromID string) []SessionEntry {
	startID := fromID
	if startID == "" && m.leafID != nil {
		startID = *m.leafID
	}
	var path []SessionEntry
	current := m.byID[startID]
	for current != nil {
		path = append(path, current)
		parentID := current.base().ParentID
		if parentID == nil {
			break
		}
		current = m.byID[*parentID]
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// BuildContextEntriesFromLeaf applies compaction retention from the leaf.
func (m *SessionManager) BuildContextEntriesFromLeaf() []SessionEntry {
	return BuildContextEntries(m.GetEntries(), m.cloneLeafID(), m.byID)
}

// BuildSessionProjectionFromLeaf projects the leaf path.
func (m *SessionManager) BuildSessionProjectionFromLeaf() *SessionProjection {
	return BuildSessionProjection(m.GetEntries(), m.cloneLeafID(), m.byID)
}

// GetHeader returns the session header.
func (m *SessionManager) GetHeader() *SessionHeader {
	for _, entry := range m.fileEntries {
		if header, ok := entry.(*SessionHeader); ok {
			return header
		}
	}
	return nil
}

// GetEntries returns the entries (header excluded).
func (m *SessionManager) GetEntries() []SessionEntry {
	var out []SessionEntry
	for _, entry := range m.fileEntries {
		if _, ok := entry.(*SessionHeader); ok {
			continue
		}
		out = append(out, entry.(SessionEntry))
	}
	return out
}

// SessionTreeNode is one node of the tree view.
type SessionTreeNode struct {
	Entry          SessionEntry
	Children       []*SessionTreeNode
	Label          string
	LabelTimestamp string
}

// GetTree builds a defensive tree of all entries.
func (m *SessionManager) GetTree() []*SessionTreeNode {
	entries := m.GetEntries()
	nodeMap := map[string]*SessionTreeNode{}
	var roots []*SessionTreeNode

	for _, entry := range entries {
		id := entry.base().ID
		nodeMap[id] = &SessionTreeNode{
			Entry:          entry,
			Children:       nil,
			Label:          m.labelsByID[id],
			LabelTimestamp: m.labelTimestampsByID[id],
		}
	}

	for _, entry := range entries {
		id := entry.base().ID
		node := nodeMap[id]
		parentID := entry.base().ParentID
		if parentID == nil || *parentID == id {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodeMap[*parentID]
		if ok {
			parent.Children = append(parent.Children, node)
		} else {
			roots = append(roots, node)
		}
	}

	var stack []*SessionTreeNode
	stack = append(stack, roots...)
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		sort.SliceStable(node.Children, func(a, b int) bool {
			return entryTime(node.Children[a].Entry) < entryTime(node.Children[b].Entry)
		})
		stack = append(stack, node.Children...)
	}
	return roots
}

func entryTime(entry SessionEntry) int64 {
	parsed, err := time.Parse(time.RFC3339, entry.base().Timestamp)
	if err != nil {
		return 0
	}
	return parsed.UnixMilli()
}

// Branch moves the leaf to an earlier entry.
func (m *SessionManager) Branch(branchFromID string) error {
	if _, ok := m.byID[branchFromID]; !ok {
		return fmt.Errorf("Entry %s not found", branchFromID)
	}
	m.leafID = &branchFromID
	return nil
}

// ResetLeaf moves the leaf before all entries (new root next append).
func (m *SessionManager) ResetLeaf() { m.leafID = nil }

// BranchWithSummary branches while summarizing the abandoned path.
func (m *SessionManager) BranchWithSummary(branchFromID *string, summary string, details json.RawMessage, fromHook bool, usage json.RawMessage) (string, error) {
	if branchFromID != nil {
		if _, ok := m.byID[*branchFromID]; !ok {
			return "", fmt.Errorf("Entry %s not found", *branchFromID)
		}
	}
	fromID := "root"
	if m.leafID != nil {
		fromID = *m.leafID
	}
	m.leafID = branchFromID
	entry := &BranchSummaryEntry{
		SessionEntryBase: SessionEntryBase{
			Type: EntryTypeBranchSummary, ID: m.generateID(), ParentID: cloneStringPtr(branchFromID), Timestamp: m.nowISO(),
		},
		FromID:   fromID,
		Summary:  summary,
		Details:  details,
		Usage:    usage,
		FromHook: fromHook,
	}
	m.appendEntry(entry)
	return entry.ID, nil
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

// CreateBranchedSession writes a new session file holding only the root-to-
// leaf path; label entries re-chain to the retained path.
func (m *SessionManager) CreateBranchedSession(leafID string) (string, error) {
	previousSessionFile := m.sessionFile
	path := m.GetBranch(leafID)
	if len(path) == 0 {
		return "", fmt.Errorf("Entry %s not found", leafID)
	}

	// Drop label entries from the path; later entries re-chain past them.
	var pathWithoutLabels []SessionEntry
	replacementByLabelID := map[string]string{}
	var pendingLabelIDs []string
	var pathParentID *string
	for _, entry := range path {
		if _, ok := entry.(*LabelEntry); ok {
			pendingLabelIDs = append(pendingLabelIDs, entry.base().ID)
			continue
		}
		for _, labelID := range pendingLabelIDs {
			replacementByLabelID[labelID] = entry.base().ID
		}
		pendingLabelIDs = nil
		entry.base().ParentID = pathParentID
		pathParentID = &entry.base().ID
		if compaction, ok := entry.(*CompactionEntry); ok && compaction.FirstKeptEntryID != compaction.ID {
			if replacement, ok := replacementByLabelID[compaction.FirstKeptEntryID]; ok {
				compaction.FirstKeptEntryID = replacement
			}
		}
		pathWithoutLabels = append(pathWithoutLabels, entry)
	}

	newSessionID := generateSessionID()
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	fileTimestamp := regexp.MustCompile(`[:.]`).ReplaceAllString(timestamp, "-")
	newSessionFile := filepath.Join(m.GetSessionDir(), fileTimestamp+"_"+newSessionID+".jsonl")

	parent := ""
	if m.persist {
		parent = previousSessionFile
	}
	header := &SessionHeader{
		Type:          EntryTypeSession,
		Version:       intPtrTo(CurrentSessionVersion),
		ID:            newSessionID,
		Timestamp:     timestamp,
		Cwd:           m.cwd,
		ParentSession: parent,
	}

	// Collect labels whose targets survived.
	pathEntryIDs := map[string]bool{}
	for _, entry := range pathWithoutLabels {
		pathEntryIDs[entry.base().ID] = true
	}
	type labelWrite struct {
		targetID, label, timestamp string
	}
	var labelsToWrite []labelWrite
	for targetID, label := range m.labelsByID {
		if pathEntryIDs[targetID] {
			labelsToWrite = append(labelsToWrite, labelWrite{
				targetID:  targetID,
				label:     label,
				timestamp: m.labelTimestampsByID[targetID],
			})
		}
	}
	sort.Slice(labelsToWrite, func(a, b int) bool { return labelsToWrite[a].targetID < labelsToWrite[b].targetID })

	newEntries := make([]FileEntry, 0, len(pathWithoutLabels)+len(labelsToWrite)+1)
	newEntries = append(newEntries, header)
	for _, entry := range pathWithoutLabels {
		newEntries = append(newEntries, entry)
	}

	var parentID *string
	if len(pathWithoutLabels) > 0 {
		last := pathWithoutLabels[len(pathWithoutLabels)-1].base().ID
		parentID = &last
	}
	for _, write := range labelsToWrite {
		labelValue := write.label
		labelEntry := &LabelEntry{
			SessionEntryBase: SessionEntryBase{
				Type:      EntryTypeLabel,
				ID:        generateShortID(map[string]bool{}),
				ParentID:  parentID,
				Timestamp: write.timestamp,
			},
			TargetID: write.targetID,
			Label:    &labelValue,
		}
		newEntries = append(newEntries, labelEntry)
		id := labelEntry.ID
		parentID = &id
	}

	m.fileEntries = newEntries
	m.sessionID = newSessionID
	m.sessionFile = newSessionFile
	m.buildIndex()

	if m.persist {
		// Same rule as persistEntry: write now when the branched path has a
		// conversation, else let the next append create the file.
		if m.hasConversation() {
			m.rewriteFile()
			m.flushed = true
		} else {
			m.flushed = false
		}
		return newSessionFile, nil
	}
	return "", nil
}

func intPtrTo(v int) *int { return &v }

// SessionManagerCreate opens a fresh persisted session (SessionManager.create).
func SessionManagerCreate(cwd, sessionDir string, options *NewSessionOptions) (*SessionManager, error) {
	dir := sessionDir
	if dir == "" {
		dir = GetDefaultSessionDir(cwd, GetAgentDir())
	} else {
		dir = NormalizePathDefault(dir)
	}
	return CreateSessionManager(SessionManagerOptions{Cwd: cwd, SessionDir: dir, Persist: true, NewSession: options})
}

// SessionManagerOpen opens a specific session file.
func SessionManagerOpen(path, sessionDir, cwdOverride string) (*SessionManager, error) {
	resolvedPath := ResolvePath(path, "", PathInputOptions{})
	var preloaded []FileEntry
	cwd := cwdOverride
	if cwd == "" {
		if _, err := os.Stat(resolvedPath); err == nil {
			header, headerErr := ReadSessionHeader(resolvedPath)
			if headerErr != nil {
				if _, limit := headerErr.(*SessionHeaderScanLimitError); limit {
					preloaded = LoadEntriesFromFile(resolvedPath)
					if len(preloaded) > 0 {
						if header, ok := preloaded[0].(*SessionHeader); ok {
							cwd = header.Cwd
						}
					}
				} else {
					return nil, headerErr
				}
			} else if header != nil {
				cwd = header.Cwd
			}
		}
	}
	if cwd == "" {
		if working, err := os.Getwd(); err == nil {
			cwd = working
		}
	}
	dir := sessionDir
	if dir == "" {
		dir = filepath.Dir(resolvedPath)
	} else {
		dir = NormalizePathDefault(dir)
	}
	return CreateSessionManager(SessionManagerOptions{
		Cwd: cwd, SessionDir: dir, SessionFile: resolvedPath, Persist: true, Preloaded: preloaded,
	})
}

// SessionManagerContinueRecent continues the newest session or starts one.
func SessionManagerContinueRecent(cwd, sessionDir string) (*SessionManager, error) {
	dir := sessionDir
	if dir == "" {
		dir = GetDefaultSessionDir(cwd, GetAgentDir())
	} else {
		dir = NormalizePathDefault(dir)
	}
	filterCwd := sessionDir != "" && dir != getDefaultSessionDirPath(cwd, GetAgentDir())
	filter := ""
	if filterCwd {
		filter = cwd
	}
	mostRecent := FindMostRecentSession(dir, filter)
	options := SessionManagerOptions{Cwd: cwd, SessionDir: dir, Persist: true}
	if mostRecent != "" {
		options.SessionFile = mostRecent
	}
	return CreateSessionManager(options)
}

// SessionManagerInMemory builds a non-persisting session.
func SessionManagerInMemory(cwd string, options *NewSessionOptions, entries []FileEntry) (*SessionManager, error) {
	if cwd == "" {
		if working, err := os.Getwd(); err == nil {
			cwd = working
		}
	}
	return CreateSessionManager(SessionManagerOptions{
		Cwd: cwd, SessionDir: "", Persist: false, NewSession: options, Preloaded: entries,
	})
}

// SessionManagerForkFrom copies a session into another project directory.
func SessionManagerForkFrom(sourcePath, targetCwd, sessionDir string, options *NewSessionOptions) (*SessionManager, error) {
	resolvedSourcePath := ResolvePath(sourcePath, "", PathInputOptions{})
	resolvedTargetCwd := ResolvePath(targetCwd, "", PathInputOptions{})
	sourceEntries := LoadEntriesFromFile(resolvedSourcePath)
	if len(sourceEntries) == 0 {
		return nil, fmt.Errorf("Cannot fork: source session file is empty or invalid: %s", resolvedSourcePath)
	}
	var sourceHeader *SessionHeader
	for _, entry := range sourceEntries {
		if header, ok := entry.(*SessionHeader); ok {
			sourceHeader = header
			break
		}
	}
	if sourceHeader == nil {
		return nil, fmt.Errorf("Cannot fork: source session has no header: %s", resolvedSourcePath)
	}

	dir := sessionDir
	if dir == "" {
		dir = GetDefaultSessionDir(resolvedTargetCwd, GetAgentDir())
	} else {
		dir = NormalizePathDefault(dir)
	}
	if _, err := os.Stat(dir); err != nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	if options != nil && options.ID != "" {
		if err := AssertValidSessionID(options.ID); err != nil {
			return nil, err
		}
	}
	newSessionID := generateSessionID()
	if options != nil && options.ID != "" {
		newSessionID = options.ID
	}
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	fileTimestamp := regexp.MustCompile(`[:.]`).ReplaceAllString(timestamp, "-")
	newSessionFile := filepath.Join(dir, fileTimestamp+"_"+newSessionID+".jsonl")

	header := &SessionHeader{
		Type:          EntryTypeSession,
		Version:       intPtrTo(CurrentSessionVersion),
		ID:            newSessionID,
		Timestamp:     timestamp,
		Cwd:           resolvedTargetCwd,
		ParentSession: resolvedSourcePath,
	}

	file, err := os.OpenFile(newSessionFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	writer := bufio.NewWriter(file)
	headerData, _ := json.Marshal(header)
	writer.Write(headerData)
	writer.WriteByte('\n')
	for _, entry := range sourceEntries {
		if _, ok := entry.(*SessionHeader); ok {
			continue
		}
		data, _ := json.Marshal(entry)
		writer.Write(data)
		writer.WriteByte('\n')
	}
	writer.Flush()
	file.Close()

	return CreateSessionManager(SessionManagerOptions{
		Cwd: resolvedTargetCwd, SessionDir: dir, SessionFile: newSessionFile, Persist: true,
	})
}

// SessionManagerFindByID locates a session by exact id.
func SessionManagerFindByID(cwd, id, sessionDir string) string {
	dir := sessionDir
	if dir == "" {
		dir = GetDefaultSessionDir(cwd, GetAgentDir())
	} else {
		dir = NormalizePathDefault(dir)
	}
	filterCwd := sessionDir != "" && dir != getDefaultSessionDirPath(cwd, GetAgentDir())
	resolvedCwd := ResolvePath(cwd, "", PathInputOptions{})

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		header := readSessionHeaderForDiscovery(path)
		if header == nil || header.ID != id {
			continue
		}
		if filterCwd && !sessionCwdMatches(header.Cwd, resolvedCwd) {
			continue
		}
		return path
	}
	return ""
}

// SessionManagerList lists sessions for a directory (cwd-filtered when a
// custom sessionDir is supplied).
func SessionManagerList(cwd, sessionDir string, onProgress SessionListProgress) []SessionInfo {
	dir := sessionDir
	if dir == "" {
		dir = GetDefaultSessionDir(cwd, GetAgentDir())
	} else {
		dir = NormalizePathDefault(dir)
	}
	filterCwd := sessionDir != "" && dir != getDefaultSessionDirPath(cwd, GetAgentDir())
	resolvedCwd := ResolvePath(cwd, "", PathInputOptions{})

	var progress SessionListProgress
	if onProgress != nil && filterCwd {
		progress = func(loaded, total int, partial []SessionInfo) {
			filtered := make([]SessionInfo, 0, len(partial))
			for _, session := range partial {
				if sessionCwdMatches(session.Cwd, resolvedCwd) {
					filtered = append(filtered, session)
				}
			}
			onProgress(loaded, total, filtered)
		}
	} else {
		progress = onProgress
	}

	sessions := ListSessionsFromDir(dir, progress)
	if filterCwd {
		filtered := make([]SessionInfo, 0, len(sessions))
		for _, session := range sessions {
			if sessionCwdMatches(session.Cwd, resolvedCwd) {
				filtered = append(filtered, session)
			}
		}
		sessions = filtered
	}
	return sessions
}

// SessionManagerListAll lists sessions across every project directory.
func SessionManagerListAll(sessionDir string, onProgress SessionListProgress) []SessionInfo {
	if sessionDir != "" {
		return ListSessionsFromDir(NormalizePathDefault(sessionDir), onProgress)
	}

	sessionsDir := GetSessionsDir()
	if _, err := os.Stat(sessionsDir); err != nil {
		return nil
	}
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil
	}
	var dirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			dirs = append(dirs, filepath.Join(sessionsDir, entry.Name()))
		}
	}

	var allFiles []string
	for _, dir := range dirs {
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".jsonl") {
				allFiles = append(allFiles, filepath.Join(dir, file.Name()))
			}
		}
	}

	type candidate struct {
		path  string
		mtime int64
	}
	candidates := make([]candidate, len(allFiles))
	for i, path := range allFiles {
		mtime := int64(-1) << 62
		if info, err := os.Stat(path); err == nil {
			mtime = info.ModTime().UnixMilli()
		}
		candidates[i] = candidate{path: path, mtime: mtime}
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		if candidates[a].mtime != candidates[b].mtime {
			return candidates[a].mtime > candidates[b].mtime
		}
		return filepath.Base(candidates[b].path) > filepath.Base(candidates[a].path)
	})

	var out []SessionInfo
	for _, candidate := range candidates {
		if info := BuildSessionInfo(candidate.path); info != nil {
			out = append(out, *info)
		}
	}
	return out
}
