package session

// Ports of memory-storage / storage-backed-session / session test
// behaviors (representative cases).

import (
	"strings"
	"testing"
)

func userPayload(text string) AgentMessagePayload {
	return AgentMessagePayload{Role: "user", Message: mustObj(`{"role":"user","content":"` + text + `","timestamp":1}`)}
}

func assistantPayload(text string) AgentMessagePayload {
	return AgentMessagePayload{Role: "assistant", Message: mustObj(`{"role":"assistant","content":[{"type":"text","text":"` + text + `"}],"api":"x","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","timestamp":1}`)}
}

func mustObj(s string) (o *jsonxObjAlias) {
	v := parseJSONForTest(s)
	return v
}

func newMemorySession(t *testing.T) (Session, *MemorySessionRepo) {
	t.Helper()
	repo := NewMemorySessionRepo()
	session, err := repo.Create(map[string]any{"id": "s1"}, BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	return session, repo
}

func TestStorageBackedSessionAppendAndScan(t *testing.T) {
	session, _ := newMemorySession(t)
	branch, err := session.Branch("main", BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	id1, err := branch.AppendMessage(userPayload("hello"), BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := branch.AppendMessage(assistantPayload("hi"), BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if id1 == id2 {
		t.Fatal("ids collide")
	}
	tip, err := branch.GetTipID(BackgroundContext)
	if err != nil || tip == nil || *tip != id2 {
		t.Fatalf("tip = %v err = %v", tip, err)
	}
	// Newest-first scan returns assistant then user.
	entries, err := branch.FindEntries(nil, BackgroundContext)
	if err != nil || len(entries) != 2 || entries[0].ID != id2 {
		t.Fatalf("entries = %+v err = %v", entries, err)
	}
	stats, _ := session.GetStats(BackgroundContext)
	if stats.MessageCount != 2 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestSessionPendingAssistantRejected(t *testing.T) {
	session, _ := newMemorySession(t)
	branch, _ := session.Branch("main", BackgroundContext)
	pending := assistantPayload("partial")
	pending.Message = mustObj(`{"role":"assistant","content":[],"api":"x","provider":"p","model":"m","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"pending","timestamp":1}`)
	_, err := branch.AppendMessage(pending, BackgroundContext)
	if _, ok := err.(*SessionPendingAssistantMessageError); !ok {
		t.Fatalf("err = %v (%T)", err, err)
	}
}

func TestSessionBranchValidation(t *testing.T) {
	session, _ := newMemorySession(t)
	if _, err := session.Branch("", BackgroundContext); err != nil {
		if _, ok := err.(*SessionInvalidBranchError); !ok {
			t.Fatalf("empty err = %v", err)
		}
	} else {
		t.Fatal("empty branch accepted")
	}
	if _, err := session.Branch("a\x00b", BackgroundContext); err != nil {
		if _, ok := err.(*SessionInvalidBranchError); !ok {
			t.Fatalf("nul err = %v", err)
		}
	} else {
		t.Fatal("nul branch accepted")
	}
	// Duplicate branch.
	if _, err := session.CreateBranch("main", nil, BackgroundContext); err == nil {
		t.Fatal("duplicate branch accepted")
	} else if _, ok := err.(*SessionBranchExistsError); !ok {
		t.Fatalf("dup err = %v", err)
	}
	// Create at an unknown target.
	missing := "missing-entry"
	if _, err := session.CreateBranch("feature", &missing, BackgroundContext); err == nil {
		t.Fatal("unknown target accepted")
	} else if _, ok := err.(*SessionUnknownTargetError); !ok {
		t.Fatalf("target err = %v", err)
	}
}

func TestSessionNameAndLabels(t *testing.T) {
	session, _ := newMemorySession(t)
	if err := session.SetName(strptrS("my session"), BackgroundContext); err != nil {
		t.Fatal(err)
	}
	name, _ := session.GetName(BackgroundContext)
	if name == nil || *name != "my session" {
		t.Fatalf("name = %v", name)
	}
	if err := session.SetName(nil, BackgroundContext); err != nil {
		t.Fatal(err)
	}
	name, _ = session.GetName(BackgroundContext)
	if name != nil {
		t.Fatalf("name after delete = %v", *name)
	}
	// Labels.
	if err := session.SetLabel("e1", strptrS("checkpoint"), BackgroundContext); err != nil {
		t.Fatal(err)
	}
	label, _ := session.GetLabel("e1", BackgroundContext)
	if label == nil || *label != "checkpoint" {
		t.Fatalf("label = %v", label)
	}
	if err := session.SetLabel("e1", nil, BackgroundContext); err != nil {
		t.Fatal(err)
	}
	label, _ = session.GetLabel("e1", BackgroundContext)
	if label != nil {
		t.Fatalf("label after delete = %v", *label)
	}
}

func TestSessionMutateExclusiveCommit(t *testing.T) {
	session, _ := newMemorySession(t)
	err := session.Mutate(func(mutator SessionMutator, ctx Context) error {
		// Second commit attempt rejects.
		if _, err := mutator.Commit([]Write{WriteFromValue(SetValue(SessionName, "x"))}, ctx); err != nil {
			return err
		}
		if _, err := mutator.Commit(nil, ctx); err == nil || !strings.Contains(err.Error(), "already attempted") {
			t.Fatalf("second commit err = %v", err)
		}
		return nil
	}, BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := session.GetName(BackgroundContext)
	if name == nil || *name != "x" {
		t.Fatalf("name = %v", name)
	}
}

func TestMemoryStorageFork(t *testing.T) {
	repo := NewMemorySessionRepo()
	source, err := repo.Create(map[string]any{"id": "src"}, BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	branch, _ := source.Branch("main", BackgroundContext)
	if _, err := branch.AppendMessage(userPayload("one"), BackgroundContext); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(BackgroundContext); err != nil {
		t.Fatal(err)
	}
	forked, err := repo.Fork(SessionMetadata{ID: "src"}, ForkOptions{Scope: "tree"}, BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	forkBranch, err := forked.Branch("main", BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := forkBranch.FindEntries(nil, BackgroundContext)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %+v err = %v", entries, err)
	}
}

func TestMemoryRepoOpenExclusive(t *testing.T) {
	repo := NewMemorySessionRepo()
	session, err := repo.Create(map[string]any{"id": "ex"}, BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Open(SessionMetadata{ID: "ex"}, BackgroundContext); err == nil {
		t.Fatal("double open accepted")
	}
	if err := session.Close(BackgroundContext); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(SessionMetadata{ID: "ex"}, BackgroundContext); err != nil {
		t.Fatal(err)
	}
}

func strptrS(s string) *string { return &s }
