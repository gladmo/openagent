package codingagent

// Tests for the session manager (ported from session-manager.test.ts
// surfaces: parsing, migration, tree/branching, projection, persistence).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestSessionManager(t *testing.T, persist bool) *SessionManager {
	t.Helper()
	dir := t.TempDir()
	manager, err := CreateSessionManager(SessionManagerOptions{
		Cwd:        dir,
		SessionDir: filepath.Join(dir, "sessions"),
		Persist:    persist,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return manager
}

func userMessage(t *testing.T, text string) json.RawMessage {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"role": "user", "content": text})
	return json.RawMessage(raw)
}

func TestSessionAppendAndBranch(t *testing.T) {
	manager := newTestSessionManager(t, false)

	firstID := manager.AppendMessage(userMessage(t, "first"))
	secondID := manager.AppendMessage(userMessage(t, "second"))

	if manager.GetLeafID() == nil || *manager.GetLeafID() != secondID {
		t.Fatalf("leaf = %v", manager.GetLeafID())
	}
	if len(manager.GetEntries()) != 2 {
		t.Fatalf("entries = %d", len(manager.GetEntries()))
	}

	// Branch from the first entry: the path excludes the second.
	if err := manager.Branch(firstID); err != nil {
		t.Fatalf("branch: %v", err)
	}
	thirdID := manager.AppendMessage(userMessage(t, "third"))
	if manager.GetEntry(thirdID).base().ParentID == nil || *manager.GetEntry(thirdID).base().ParentID != firstID {
		t.Fatalf("third parent = %v", manager.GetEntry(thirdID).base().ParentID)
	}

	branch := manager.GetBranch("")
	if len(branch) != 2 {
		t.Fatalf("branch = %d entries", len(branch))
	}
	if branch[0].base().ID != firstID || branch[1].base().ID != thirdID {
		t.Fatalf("branch order = %v", branch)
	}
	_ = secondID
}

func TestSessionTree(t *testing.T) {
	manager := newTestSessionManager(t, false)

	rootID := manager.AppendMessage(userMessage(t, "root"))
	manager.AppendMessage(userMessage(t, "a"))
	manager.Branch(rootID)
	manager.AppendMessage(userMessage(t, "b"))

	tree := manager.GetTree()
	if len(tree) != 1 {
		t.Fatalf("roots = %d", len(tree))
	}
	if tree[0].Entry.base().ID != rootID {
		t.Fatalf("root = %v", tree[0].Entry)
	}
	if len(tree[0].Children) != 2 {
		t.Fatalf("children = %d", len(tree[0].Children))
	}
}

func TestSessionLabels(t *testing.T) {
	manager := newTestSessionManager(t, false)
	entryID := manager.AppendMessage(userMessage(t, "x"))

	label := "bookmark"
	id, err := manager.AppendLabelChange(entryID, &label)
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	if manager.GetLabel(entryID) != "bookmark" {
		t.Fatalf("label = %q", manager.GetLabel(entryID))
	}

	// Clearing removes the label.
	empty := ""
	if _, err := manager.AppendLabelChange(entryID, &empty); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if manager.GetLabel(entryID) != "" {
		t.Fatalf("cleared = %q", manager.GetLabel(entryID))
	}
	_ = id
}

func TestSessionPersistenceLazy(t *testing.T) {
	dir := t.TempDir()
	manager, err := CreateSessionManager(SessionManagerOptions{
		Cwd:        dir,
		SessionDir: filepath.Join(dir, "sessions"),
		Persist:    true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Setup-only entries do not create the file.
	manager.AppendThinkingLevelChange("high")
	if _, err := os.Stat(manager.GetSessionFile()); !os.IsNotExist(err) {
		t.Fatal("setup-only session created a file")
	}

	// The first user message creates it.
	manager.AppendMessage(userMessage(t, "hello"))
	if _, err := os.Stat(manager.GetSessionFile()); err != nil {
		t.Fatalf("conversation file missing: %v", err)
	}

	// Reload and verify.
	reloaded, err := CreateSessionManager(SessionManagerOptions{
		Cwd:         dir,
		SessionDir:  filepath.Join(dir, "sessions"),
		SessionFile: manager.GetSessionFile(),
		Persist:     true,
	})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.GetSessionID() != manager.GetSessionID() {
		t.Fatalf("id = %s, want %s", reloaded.GetSessionID(), manager.GetSessionID())
	}
	entries := reloaded.GetEntries()
	if len(entries) != 2 {
		t.Fatalf("reloaded entries = %d", len(entries))
	}
}

func TestSessionProjection(t *testing.T) {
	manager := newTestSessionManager(t, false)

	manager.AppendMessage(userMessage(t, "early"))
	keptID := manager.AppendMessage(userMessage(t, "kept message"))
	manager.AppendMessage(userMessage(t, "late"))

	// Compaction retains only the kept entry onwards.
	summary := "compacted history"
	compactionID := manager.AppendCompaction(summary, &keptID, 100, nil, false, nil)

	entries := manager.BuildContextEntriesFromLeaf()
	if len(entries) < 2 {
		t.Fatalf("context entries = %d", len(entries))
	}
	// The compaction entry leads the retained context.
	if entries[0].base().ID != compactionID {
		t.Fatalf("first = %v", entries[0])
	}
	// Summarized entries drop out; kept remains.
	joined := ""
	for _, entry := range entries {
		if message, ok := entry.(*SessionMessageEntry); ok {
			var probe struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(message.Message, &probe)
			joined += probe.Content + "|"
		}
	}
	if strings.Contains(joined, "early") {
		t.Fatalf("summarized entry retained: %q", joined)
	}
	if !strings.Contains(joined, "kept message") || !strings.Contains(joined, "late") {
		t.Fatalf("kept entries missing: %q", joined)
	}

	// Projection carries the thinking level and compaction message.
	projection := manager.BuildSessionProjectionFromLeaf()
	if projection.ThinkingLevel != "off" {
		t.Fatalf("thinking = %q", projection.ThinkingLevel)
	}
}

func TestSessionContextSettings(t *testing.T) {
	manager := newTestSessionManager(t, false)
	manager.AppendThinkingLevelChange("high")
	manager.AppendModelChange("anthropic", "claude-sonnet")

	projection := manager.BuildSessionProjectionFromLeaf()
	if projection.ThinkingLevel != "high" {
		t.Fatalf("thinking = %q", projection.ThinkingLevel)
	}
	if projection.Model == nil || projection.Model.Provider != "anthropic" || projection.Model.ModelID != "claude-sonnet" {
		t.Fatalf("model = %+v", projection.Model)
	}
}

func TestSessionContextEdit(t *testing.T) {
	manager := newTestSessionManager(t, false)
	targetID := manager.AppendMessage(userMessage(t, "original content"))

	replacement := &ContextEditReplacement{Content: json.RawMessage(`"edited content"`)}
	editID, err := manager.AppendContextEdit(targetID, replacement)
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if editID == "" {
		t.Fatal("no edit id")
	}

	projection := manager.BuildSessionProjectionFromLeaf()
	foundEdited := false
	for _, entry := range projection.Entries {
		if entry.SourceEntry.base().ID == targetID {
			for _, message := range entry.Messages {
				if replaced, ok := message.(replacedContentMessage); ok {
					var content string
					if err := json.Unmarshal(replaced.content, &content); err == nil && content == "edited content" {
						foundEdited = true
					}
				}
			}
		}
	}
	if !foundEdited {
		t.Fatal("edit not applied in projection")
	}

	// Null replacement omits the target.
	nullEdit := &ContextEditEntry{}
	_ = nullEdit
	if _, err := manager.AppendContextEdit(targetID, nil); err != nil {
		t.Fatalf("null edit: %v", err)
	}
	projection = manager.BuildSessionProjectionFromLeaf()
	for _, entry := range projection.Entries {
		if entry.SourceEntry.base().ID == targetID && len(entry.Messages) > 0 {
			t.Fatal("null replacement should omit the target")
		}
	}

	// Non-editable target errors (thinking-level entries carry no content).
	thinkingID := manager.AppendThinkingLevelChange("off")
	if _, err := manager.AppendContextEdit(thinkingID, replacement); err == nil {
		t.Fatal("non-editable target should error")
	}
}

func TestSessionResetLeaf(t *testing.T) {
	manager := newTestSessionManager(t, false)
	manager.AppendMessage(userMessage(t, "first"))
	manager.ResetLeaf()
	newRoot := manager.AppendMessage(userMessage(t, "new root"))
	if manager.GetEntry(newRoot).base().ParentID != nil {
		t.Fatalf("new root has parent %v", manager.GetEntry(newRoot).base().ParentID)
	}
}

func TestSessionBranchWithSummary(t *testing.T) {
	manager := newTestSessionManager(t, false)
	firstID := manager.AppendMessage(userMessage(t, "first"))
	manager.AppendMessage(userMessage(t, "abandoned"))

	if _, err := manager.BranchWithSummary(&firstID, "went back", nil, false, nil); err != nil {
		t.Fatalf("branch: %v", err)
	}
	entries := manager.GetEntries()
	summary := entries[len(entries)-1].(*BranchSummaryEntry)
	if summary.Summary != "went back" || summary.FromID == "" {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestParseSessionEntries(t *testing.T) {
	content := `{"type":"session","version":3,"id":"s1","timestamp":"2024-01-01T00:00:00Z","cwd":"/tmp"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2024-01-01T00:00:01Z","message":{"role":"user","content":"hi"}}
not json at all
{"type":"label","id":"l1","parentId":"m1","timestamp":"2024-01-01T00:00:02Z","targetId":"m1","label":"x"}`
	entries := ParseSessionEntries(content)
	if len(entries) != 3 {
		t.Fatalf("entries = %d", len(entries))
	}
	if entries[0].(*SessionHeader).ID != "s1" {
		t.Fatal("header missing")
	}
	if entries[1].(*SessionMessageEntry).ID != "m1" {
		t.Fatal("message missing")
	}
}

func TestMigrateV1ToV2(t *testing.T) {
	// v1 has no version, no ids.
	content := `{"type":"session","id":"s1","timestamp":"2024-01-01T00:00:00Z","cwd":"/tmp"}
{"type":"message","timestamp":"2024-01-01T00:00:01Z","message":{"role":"user","content":"a"}}
{"type":"message","timestamp":"2024-01-01T00:00:02Z","message":{"role":"assistant","content":"b"}}`
	entries := ParseSessionEntries(content)
	if !MigrateSessionEntries(entries) {
		t.Fatal("migration not applied")
	}
	header := entries[0].(*SessionHeader)
	if header.Version == nil || *header.Version != CurrentSessionVersion {
		t.Fatalf("version = %v", header.Version)
	}
	first := entries[1].(SessionEntry).base()
	second := entries[2].(SessionEntry).base()
	if first.ParentID != nil {
		t.Fatalf("first parent = %v", first.ParentID)
	}
	if second.ParentID == nil || *second.ParentID != first.ID {
		t.Fatalf("second parent = %v", second.ParentID)
	}

	// Re-running is a no-op.
	if MigrateSessionEntries(entries) {
		t.Fatal("migration re-applied")
	}
}

func TestMigrateV2ToV3(t *testing.T) {
	version := 2
	entries := []FileEntry{
		&SessionHeader{Type: EntryTypeSession, Version: &version, ID: "s", Timestamp: "2024-01-01T00:00:00Z", Cwd: "/"},
		&SessionMessageEntry{
			SessionEntryBase: SessionEntryBase{Type: EntryTypeMessage, ID: "m", Timestamp: "2024-01-01T00:00:01Z"},
			Message:          json.RawMessage(`{"role":"hookMessage","content":"x"}`),
		},
	}
	if !MigrateSessionEntries(entries) {
		t.Fatal("migration not applied")
	}
	var probe struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(entries[1].(*SessionMessageEntry).Message, &probe); err != nil || probe.Role != "custom" {
		t.Fatalf("role = %q err=%v", probe.Role, err)
	}
}

func TestAssertValidSessionID(t *testing.T) {
	for _, valid := range []string{"a", "abc123", "a-b_c.d", "0"} {
		if err := AssertValidSessionID(valid); err != nil {
			t.Errorf("valid %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", "-abc", "abc-", "a b", "a/b"} {
		if err := AssertValidSessionID(invalid); err == nil {
			t.Errorf("invalid %q accepted", invalid)
		}
	}
}

func TestBuildSessionPath(t *testing.T) {
	manager := newTestSessionManager(t, false)
	a := manager.AppendMessage(userMessage(t, "a"))
	b := manager.AppendMessage(userMessage(t, "b"))

	path := BuildSessionPath(manager.GetEntries(), nil, nil)
	if len(path) != 2 || path[0].base().ID != a || path[1].base().ID != b {
		t.Fatalf("path = %v", path)
	}

	// Explicit leaf.
	path = BuildSessionPath(manager.GetEntries(), &a, nil)
	if len(path) != 1 || path[0].base().ID != a {
		t.Fatalf("leaf path = %v", path)
	}
}

func TestFindMostRecentSession(t *testing.T) {
	dir := t.TempDir()
	cwd := "/project"

	writeSession := func(name, sessionCwd string, age time.Duration) {
		t.Helper()
		header, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": name, "timestamp": "2024-01-01T00:00:00Z", "cwd": sessionCwd})
		path := filepath.Join(dir, name+".jsonl")
		os.WriteFile(path, header, 0o644)
		past := time.Now().Add(-age)
		os.Chtimes(path, past, past)
	}

	writeSession("older", cwd, 2*time.Hour)
	writeSession("newer", cwd, 1*time.Hour)
	writeSession("other-cwd", "/elsewhere", 30*time.Minute)

	found := FindMostRecentSession(dir, cwd)
	if found == "" || !strings.HasSuffix(found, "newer.jsonl") {
		t.Fatalf("found = %q", found)
	}

	// No cwd filter: the newest overall wins.
	found = FindMostRecentSession(dir, "")
	if found == "" || !strings.HasSuffix(found, "other-cwd.jsonl") {
		t.Fatalf("unfiltered = %q", found)
	}

	// Empty dir.
	if got := FindMostRecentSession(filepath.Join(dir, "nope"), ""); got != "" {
		t.Fatalf("missing dir = %q", got)
	}
}

func TestBuildSessionInfo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	content := `{"type":"session","version":3,"id":"s1","timestamp":"2024-01-01T00:00:00Z","cwd":"/proj","parentSession":"/parent.jsonl"}
{"type":"session_info","id":"i1","parentId":null,"timestamp":"2024-01-01T00:00:01Z","name":"my session"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2024-01-01T00:00:02Z","message":{"role":"user","content":"first question"}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2024-01-01T00:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"answer"}]}}
{"type":"usage","id":"u1","parentId":"m2","timestamp":"2024-01-01T00:00:04Z","kind":"llm","provider":"p","model":"m","usage":{}}`
	os.WriteFile(path, []byte(content), 0o644)

	info := BuildSessionInfo(path)
	if info == nil {
		t.Fatal("info nil")
	}
	if info.ID != "s1" || info.Cwd != "/proj" || info.Name != "my session" {
		t.Fatalf("info = %+v", info)
	}
	if info.ParentSessionPath != "/parent.jsonl" {
		t.Fatalf("parent = %q", info.ParentSessionPath)
	}
	if info.MessageCount != 2 {
		t.Fatalf("count = %d", info.MessageCount)
	}
	if info.FirstMessage != "first question" {
		t.Fatalf("first = %q", info.FirstMessage)
	}
	if !strings.Contains(info.AllMessagesText, "answer") {
		t.Fatalf("all = %q", info.AllMessagesText)
	}
}

func TestListSessionsFromDir(t *testing.T) {
	dir := t.TempDir()
	for i, name := range []string{"a", "b"} {
		path := filepath.Join(dir, name+".jsonl")
		header, _ := json.Marshal(map[string]any{"type": "session", "version": 3, "id": name, "timestamp": "2024-01-01T00:00:00Z", "cwd": "/x"})
		os.WriteFile(path, header, 0o644)
		// b is newer.
		past := time.Now().Add(-time.Duration(i+1) * time.Hour)
		os.Chtimes(path, past, past)
	}
	// A non-session file is skipped.
	os.WriteFile(filepath.Join(dir, "junk.jsonl"), []byte("not json"), 0o644)

	var progressMu sync.Mutex
	progressCalls := 0
	sessions := ListSessionsFromDir(dir, func(loaded, total int, partial []SessionInfo) {
		progressMu.Lock()
		progressCalls++
		progressMu.Unlock()
	})
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d", len(sessions))
	}
	if sessions[0].ID != "b" {
		t.Fatalf("newest first = %q", sessions[0].ID)
	}
	if progressCalls == 0 {
		t.Fatal("no progress callbacks")
	}
}

func TestGetLatestCompactionEntry(t *testing.T) {
	manager := newTestSessionManager(t, false)
	manager.AppendMessage(userMessage(t, "x"))
	firstKept := manager.AppendMessage(userMessage(t, "y"))
	compactionID := manager.AppendCompaction("sum", &firstKept, 10, nil, false, nil)

	latest := GetLatestCompactionEntry(manager.GetEntries())
	if latest == nil || latest.ID != compactionID {
		t.Fatalf("latest = %+v", latest)
	}

	// None.
	manager2 := newTestSessionManager(t, false)
	manager2.AppendMessage(userMessage(t, "x"))
	if GetLatestCompactionEntry(manager2.GetEntries()) != nil {
		t.Fatal("unexpected compaction")
	}
}

func TestSessionAppendUsage(t *testing.T) {
	manager := newTestSessionManager(t, false)
	entry := manager.AppendUsage("llm", "anthropic", "m1", json.RawMessage(`{"inputTokens":10}`), "note")
	if entry.Kind != "llm" || entry.Provider != "anthropic" || entry.Model != "m1" || entry.Note != "note" {
		t.Fatalf("entry = %+v", entry)
	}
	var usage struct {
		InputTokens int `json:"inputTokens"`
	}
	if err := json.Unmarshal(entry.Usage, &usage); err != nil || usage.InputTokens != 10 {
		t.Fatalf("usage = %s err=%v", entry.Usage, err)
	}
}

func TestSessionSessionInfo(t *testing.T) {
	manager := newTestSessionManager(t, false)
	manager.AppendSessionInfo("  my\nsession  ")
	if got := manager.GetSessionName(); got != "my session" {
		t.Fatalf("name = %q", got)
	}
	// Empty clears.
	manager.AppendSessionInfo("")
	if got := manager.GetSessionName(); got != "" {
		t.Fatalf("cleared = %q", got)
	}
}

func TestSessionInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	os.WriteFile(path, []byte("this is not a session"), 0o644)

	if _, err := CreateSessionManager(SessionManagerOptions{
		Cwd:         dir,
		SessionDir:  dir,
		SessionFile: path,
		Persist:     true,
	}); err == nil || !strings.Contains(err.Error(), "not a valid") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateBranchedSession(t *testing.T) {
	manager := newTestSessionManager(t, true)
	manager.AppendMessage(userMessage(t, "root msg"))
	mid := manager.AppendMessage(userMessage(t, "middle"))
	manager.AppendMessage(userMessage(t, "abandoned tail"))

	label := "keep"
	manager.AppendLabelChange(mid, &label)

	newFile, err := manager.CreateBranchedSession(mid)
	if err != nil {
		t.Fatalf("branched: %v", err)
	}
	if newFile == "" {
		t.Fatal("no file path")
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Fatalf("file: %v", err)
	}

	// New identity, retained path only.
	if manager.GetSessionID() == "" || manager.GetSessionFile() != newFile {
		t.Fatalf("manager = %s", manager.GetSessionFile())
	}
	// Two messages plus the re-chained label entry.
	entries := manager.GetEntries()
	messageCount := 0
	labelCount := 0
	var contents []string
	for _, entry := range entries {
		switch typed := entry.(type) {
		case *SessionMessageEntry:
			messageCount++
			var probe struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(typed.Message, &probe)
			contents = append(contents, probe.Content)
		case *LabelEntry:
			labelCount++
		}
	}
	if messageCount != 2 || labelCount != 1 {
		t.Fatalf("messages = %d labels = %d (entries = %d)", messageCount, labelCount, len(entries))
	}
	if len(contents) != 2 || contents[0] != "root msg" || contents[1] != "middle" {
		t.Fatalf("contents = %v", contents)
	}
	if manager.GetLabel(mid) != "keep" {
		t.Fatalf("label lost = %q", manager.GetLabel(mid))
	}

	// Reload from the file.
	reloaded, err := SessionManagerOpen(newFile, "", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if reloaded.GetSessionID() != manager.GetSessionID() {
		t.Fatalf("reload id = %s", reloaded.GetSessionID())
	}
	if len(reloaded.GetEntries()) != 3 {
		t.Fatalf("reload entries = %d", len(reloaded.GetEntries()))
	}
}

func TestSessionManagerCreateOpenContinue(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")

	created, err := SessionManagerCreate(dir, sessionsDir, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created.IsPersisted() || created.GetSessionDir() != sessionsDir {
		t.Fatalf("created = %+v", created)
	}
	created.AppendMessage(userMessage(t, "persisted message"))

	// Open the file by path.
	opened, err := SessionManagerOpen(created.GetSessionFile(), "", "")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if opened.GetSessionID() != created.GetSessionID() {
		t.Fatal("open id mismatch")
	}

	// ContinueRecent finds the newest session.
	continued, err := SessionManagerContinueRecent(dir, sessionsDir)
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if continued.GetSessionID() != created.GetSessionID() {
		t.Fatalf("continue id = %s, want %s", continued.GetSessionID(), created.GetSessionID())
	}

	// FindByID locates it.
	if found := SessionManagerFindByID(dir, created.GetSessionID(), sessionsDir); found == "" || found != created.GetSessionFile() {
		t.Fatalf("findById = %q", found)
	}
	if found := SessionManagerFindByID(dir, "nonexistent", sessionsDir); found != "" {
		t.Fatalf("findById miss = %q", found)
	}
}

func TestSessionManagerInMemoryAndFork(t *testing.T) {
	dir := t.TempDir()
	sessionsDir := filepath.Join(dir, "sessions")

	source, err := SessionManagerCreate(dir, sessionsDir, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	source.AppendMessage(userMessage(t, "fork me"))

	// In-memory session from preloaded entries.
	inMemory, err := SessionManagerInMemory(dir, nil, nil)
	if err != nil {
		t.Fatalf("inMemory: %v", err)
	}
	if inMemory.IsPersisted() || inMemory.GetSessionFile() != "" {
		t.Fatalf("inMemory = %+v", inMemory)
	}

	// Fork into another project.
	otherDir := t.TempDir()
	forked, err := SessionManagerForkFrom(source.GetSessionFile(), otherDir, "", nil)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if forked.GetSessionID() == source.GetSessionID() {
		t.Fatal("fork reused source id")
	}
	entries := forked.GetEntries()
	if len(entries) != 1 {
		t.Fatalf("fork entries = %d", len(entries))
	}
	var probe struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(entries[0].(*SessionMessageEntry).Message, &probe)
	if probe.Content != "fork me" {
		t.Fatalf("fork content = %q", probe.Content)
	}

	// Fork of a missing source errors.
	if _, err := SessionManagerForkFrom(filepath.Join(dir, "nope.jsonl"), otherDir, "", nil); err == nil {
		t.Fatal("missing source should error")
	}
}

func TestSessionManagerListAndListAll(t *testing.T) {
	base := t.TempDir()
	// Two project dirs under one agent dir so ListAll sees both.
	agentDir := filepath.Join(base, "agent")
	t.Setenv(EnvAgentDirName(), agentDir)

	projectA := filepath.Join(base, "a")
	projectB := filepath.Join(base, "b")
	for _, project := range []string{projectA, projectB} {
		manager, err := SessionManagerCreate(project, "", nil)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		manager.AppendMessage(userMessage(t, "msg for "+project))
	}

	// List for one project.
	listed := SessionManagerList(projectA, "", nil)
	if len(listed) != 1 {
		t.Fatalf("list = %d", len(listed))
	}
	if listed[0].Cwd != projectA {
		t.Fatalf("listed cwd = %q", listed[0].Cwd)
	}

	// ListAll spans both.
	all := SessionManagerListAll("", nil)
	if len(all) != 2 {
		t.Fatalf("listAll = %d", len(all))
	}
}

func TestGetDefaultSessionDir(t *testing.T) {
	dir := t.TempDir()
	sessionDir := GetDefaultSessionDir("/some/project", dir)
	if !strings.Contains(sessionDir, "sessions") || !strings.Contains(sessionDir, "--") {
		t.Fatalf("dir = %q", sessionDir)
	}
	// The cwd is encoded safely.
	if strings.Contains(filepath.Base(sessionDir), "/") {
		t.Fatalf("unsafe dir name = %q", filepath.Base(sessionDir))
	}
}
