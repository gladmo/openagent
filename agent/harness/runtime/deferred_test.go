package runtime

// Ports of drive/deferred.ts decisions.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func newDeferredEnv(t *testing.T, stop string, handle *jsonx.Obj) session.Session {
	t.Helper()
	sess := session.NewStorageBackedSession(session.SessionMetadata{ID: "s1", StorageVersion: 1}, session.NewMemoryStorage())
	root := &session.Entry{
		EntryBase: session.EntryBase{ID: "root", Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "user"},
	}
	source := &session.Entry{
		EntryBase: session.EntryBase{ID: "resp-1", ParentID: strptrRT2("root"), Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "assistant"},
	}
	message := jsonx.ObjFrom("role", "assistant", "stopReason", stop, "api", "openai")
	if handle != nil {
		message.Set("deferred", handle)
	}
	source.Message.Message = message
	if err := sess.Mutate(func(mutator session.SessionMutator, ctx sessionCtxAlias) error {
		_, err := mutator.Commit([]session.Write{session.InsertEntry(root), session.InsertEntry(source)}, ctx)
		return err
	}, harnessBackground()); err != nil {
		t.Fatal(err)
	}
	return sess
}

func validHandle() *jsonx.Obj {
	return jsonx.ObjFrom("id", "d1", "provider", "p", "modelId", "m", "api", "openai")
}

func TestReadDeferredSourceHandle(t *testing.T) {
	identity := jsonx.ObjFrom("provider", "p", "modelId", "m")
	sess := newDeferredEnv(t, "deferred", validHandle())
	handle, err := ReadDeferredSourceHandle(sess, "resp-1", identity, harnessBackground())
	if err != nil || handle == nil {
		t.Fatalf("handle = %v err = %v", handle, err)
	}
	if v, _ := handle.Get("id"); v != "d1" {
		t.Fatalf("handle = %v", handle)
	}
}

func TestReadDeferredSourceHandleInvalid(t *testing.T) {
	identity := jsonx.ObjFrom("provider", "p", "modelId", "m")
	// Missing entry.
	sess := newDeferredEnv(t, "deferred", validHandle())
	if _, err := ReadDeferredSourceHandle(sess, "ghost", identity, harnessBackground()); err == nil || !strings.Contains(err.Error(), "missing its assistant handle") {
		t.Fatalf("err = %v", err)
	}
	// Wrong stop reason.
	sess = newDeferredEnv(t, "stop", nil)
	if _, err := ReadDeferredSourceHandle(sess, "resp-1", identity, harnessBackground()); err == nil || !strings.Contains(err.Error(), "missing its assistant handle") {
		t.Fatalf("err = %v", err)
	}
	// Missing handle.
	sess = newDeferredEnv(t, "deferred", nil)
	if _, err := ReadDeferredSourceHandle(sess, "resp-1", identity, harnessBackground()); err == nil {
		t.Fatal("missing handle accepted")
	}
	// Identity mismatches -> invalid handle.
	badIdentity := jsonx.ObjFrom("provider", "other", "modelId", "m")
	sess = newDeferredEnv(t, "deferred", validHandle())
	if _, err := ReadDeferredSourceHandle(sess, "resp-1", badIdentity, harnessBackground()); err == nil || !strings.Contains(err.Error(), "invalid handle") {
		t.Fatalf("err = %v", err)
	}
	// Empty id.
	emptyID := jsonx.ObjFrom("id", "", "provider", "p", "modelId", "m", "api", "openai")
	sess = newDeferredEnv(t, "deferred", emptyID)
	if _, err := ReadDeferredSourceHandle(sess, "resp-1", identity, harnessBackground()); err == nil || !strings.Contains(err.Error(), "invalid handle") {
		t.Fatalf("err = %v", err)
	}
	// api mismatch with the source message.
	wrongAPI := jsonx.ObjFrom("id", "d1", "provider", "p", "modelId", "m", "api", "anthropic")
	sess = newDeferredEnv(t, "deferred", wrongAPI)
	if _, err := ReadDeferredSourceHandle(sess, "resp-1", identity, harnessBackground()); err == nil || !strings.Contains(err.Error(), "invalid handle") {
		t.Fatalf("err = %v", err)
	}
}

func TestPrepareDeferredPoll(t *testing.T) {
	handle := validHandle()
	// Cancelled short-circuits everything.
	prep := PrepareDeferredPoll(true, false, handle, 1, 0, 0)
	if prep.Kind != DeferredPollCancelRequested {
		t.Fatalf("kind = %s", prep.Kind)
	}
	// Model unavailable.
	prep = PrepareDeferredPoll(false, false, handle, 1, 0, 9999)
	if prep.Kind != DeferredPollConfigFailure {
		t.Fatalf("kind = %s", prep.Kind)
	}
	// Future pollAt waits.
	prep = PrepareDeferredPoll(false, true, handle, 1, 5000, 1000)
	if prep.Kind != DeferredPollWaiting || prep.Handle == nil {
		t.Fatalf("prep = %+v", prep)
	}
	// Due poll is ready.
	prep = PrepareDeferredPoll(false, true, handle, 3, 1000, 1000)
	if prep.Kind != DeferredPollReady || prep.Poll != 3 {
		t.Fatalf("prep = %+v", prep)
	}
}

func TestDeferredFramesCleanupWrite(t *testing.T) {
	write := DeferredFramesCleanup("op1", "resp-1")
	if write.Kind != "list" || write.Op != "delete" || write.Namespace != "pi.pending.assistant_frame" {
		t.Fatalf("write = %+v", write)
	}
	if write.Key != "op1:resp-1" {
		t.Fatalf("key = %s", write.Key)
	}
}

func TestDeferredSuspension(t *testing.T) {
	scope := &session.OperationState{Control: session.Control{Status: "running"}}
	config := jsonx.ObjFrom("provider", "p", "modelId", "m")
	next := DeferredSuspension(scope, "step-1", "resp-1", 2, config)
	if next.At != session.AtDeferredSuspended || next.StepID != "step-1" || next.SourceEntryID != "resp-1" || next.Poll != 2 {
		t.Fatalf("next = %+v", next)
	}
	if next.Configuration != config {
		t.Fatal("configuration not carried")
	}
}

func strptrRT2(s string) *string { return &s }
