package runtime

// Ports of restore.test.ts (representative cases) and transcript helpers.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func newSessionForRestore(t *testing.T) (session.Session, func()) {
	t.Helper()
	storage := session.NewMemoryStorage()
	metadata := session.SessionMetadata{ID: "s1", StorageVersion: 1}
	sess := session.NewStorageBackedSession(metadata, storage)
	return sess, func() { _ = sess.Close(harnessBackground()) }
}

// setupLane writes tip + config + state for a lane.
func setupLane(t *testing.T, sess session.Session, lane string, tip *string, withConfig, withState bool, state *jsonx.Obj) {
	t.Helper()
	ctx := harnessBackground()
	if tip != nil || true {
		// Always write tip (nil means root tip).
		if err := sess.SetValue(session.BranchTip(lane), tipValue(tip), ctx); err != nil {
			t.Fatal(err)
		}
	}
	if withConfig {
		if err := sess.SetValue(session.LaneConfig(lane), jsonx.MustParseString(`{"model":{"provider":"p","modelId":"m"},"thinkingLevel":"low","activeToolNames":["read"]}`), ctx); err != nil {
			t.Fatal(err)
		}
	}
	if withState {
		if state == nil {
			state = jsonx.MustParseString(`{"currentOperationId":null,"lastOperationId":null,"inbox":[]}`).(*jsonx.Obj)
		}
		if err := sess.SetValue(session.LaneStateValue(lane), state, ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func tipValue(tip *string) any {
	if tip == nil {
		return nil
	}
	return *tip
}

func TestClassifyLaneStorage(t *testing.T) {
	sess, done := newSessionForRestore(t)
	defer done()
	ctx := harnessBackground()

	// absent
	classified, err := ReadLaneStorage(sess, "ghost", ctx)
	if err != nil || classified.Kind != "absent" {
		t.Fatalf("absent = %+v err = %v", classified, err)
	}
	// branch-only (tip, no config/state)
	tip := "e1"
	if err := sess.SetValue(session.BranchTip("b"), tip, ctx); err != nil {
		t.Fatal(err)
	}
	classified, err = ReadLaneStorage(sess, "b", ctx)
	if err != nil || classified.Kind != "branch" {
		t.Fatalf("branch = %+v err = %v", classified, err)
	}
	// partial: tip + config but no state -> invariant error.
	setupLane(t, sess, "partial", &tip, true, false, nil)
	if _, err := ReadLaneStorage(sess, "partial", ctx); err == nil {
		t.Fatal("partial lane accepted")
	}
	// complete lane.
	setupLane(t, sess, "full", &tip, true, true, nil)
	classified, err = ReadLaneStorage(sess, "full", ctx)
	if err != nil || classified.Kind != "lane" {
		t.Fatalf("lane = %+v err = %v", classified, err)
	}
}

func TestRestoreLaneValidatesOperation(t *testing.T) {
	sess, done := newSessionForRestore(t)
	defer done()
	ctx := harnessBackground()
	tip := "e1"
	setupLane(t, sess, "main", &tip, true, true, jsonx.MustParseString(`{"currentOperationId":"op-1","lastOperationId":null,"inbox":[]}`).(*jsonx.Obj))

	// Missing op.meta -> invariant error.
	state, err := RestoreLane(sess, "main", ctx)
	if err == nil {
		t.Fatal("missing op.meta accepted")
	}
	_ = state

	// Write meta + state with matching run intent.
	if err := sess.SetValue(session.OperationMetaValue("op-1"), jsonx.MustParseString(`{"operationId":"op-1","lane":"main","sourceTipId":null,"startedAt":1,"intent":{"kind":"run","promptEntryIds":[]}}`), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.OperationStateValue("op-1"), jsonx.MustParseString(`{"at":"starting","control":{"status":"running"},"latestAssistantEntryId":null}`), ctx); err != nil {
		t.Fatal(err)
	}
	laneState, err := RestoreLane(sess, "main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if laneState.Operation == nil || laneState.Operation.State.At != "starting" {
		t.Fatalf("operation = %+v", laneState.Operation)
	}
	if laneState.TipID == nil || *laneState.TipID != "e1" {
		t.Fatalf("tip = %v", laneState.TipID)
	}
	if laneState.Configuration.Model.Provider != "p" {
		t.Fatalf("config = %+v", laneState.Configuration)
	}
}

func TestStateMatchesIntentRun(t *testing.T) {
	runMeta := metaOf(`{"kind":"run"}`)
	// Non-navigation non-summary states match run.
	if !StateMatchesIntent(runMeta, stateOf(`{"at":"assistant.ready"}`)) {
		t.Fatal("assistant.ready rejected for run")
	}
	// navigation.ready_to_commit does not.
	if StateMatchesIntent(runMeta, stateOf(`{"at":"navigation.ready_to_commit"}`)) {
		t.Fatal("navigation leaf matched run")
	}
	// Summary states only match when boundary is resume_checkpoint.
	if StateMatchesIntent(runMeta, stateOfTask(`summary.deciding`, `{"kind":"finish"}`)) {
		t.Fatal("summary.finish matched run")
	}
	if !StateMatchesIntent(runMeta, stateOfTask(`summary.deciding`, `{"kind":"resume_checkpoint","resumeAfter":{"kind":"need_assistant"}}`)) {
		t.Fatal("summary.resume_checkpoint rejected for run")
	}
}

func TestStateMatchesIntentCompaction(t *testing.T) {
	compactionMeta := metaOf(`{"kind":"compaction"}`)
	if !StateMatchesIntent(compactionMeta, stateOfTask(`summary.ready`, `{"kind":"finish"}`)) {
		t.Fatal("summary.finish rejected for compaction")
	}
	if StateMatchesIntent(compactionMeta, stateOfTask(`summary.ready`, `{"kind":"resume_checkpoint","resumeAfter":{"kind":"need_assistant"}}`)) {
		t.Fatal("summary.resume_checkpoint matched compaction")
	}
}

func TestStateMatchesIntentNavigation(t *testing.T) {
	// Unsummarized: navigation.ready_to_commit with matching target+label.
	navMeta := metaOf(`{"kind":"navigation","targetId":"e9","summarize":false,"label":null}`)
	if !StateMatchesIntent(navMeta, stateOf(`{"at":"navigation.ready_to_commit","targetId":"e9","label":null}`)) {
		t.Fatal("matching ready_to_commit rejected")
	}
	if StateMatchesIntent(navMeta, stateOf(`{"at":"navigation.ready_to_commit","targetId":"other","label":null}`)) {
		t.Fatal("mismatched target accepted")
	}
	// Summarized: summary.deciding with commit_navigation boundary.
	sumMeta := metaOf(`{"kind":"navigation","targetId":"e9","summarize":true,"label":"home","customInstructions":"fast"}`)
	matching := stateOfTask(`summary.deciding`, `{"kind":"commit_navigation","targetId":"e9","label":"home"}`)
	matching.Task.Set("customInstructions", "fast")
	if !StateMatchesIntent(sumMeta, matching) {
		t.Fatal("matching summary rejected")
	}
	wrongLabel := stateOfTask(`summary.deciding`, `{"kind":"commit_navigation","targetId":"e9","label":"wrong"}`)
	if StateMatchesIntent(sumMeta, wrongLabel) {
		t.Fatal("mismatched label accepted")
	}
}

func metaOf(intentJSON string) *session.OperationMeta {
	meta := &session.OperationMeta{OperationID: "op", Lane: "main", StartedAt: 1}
	meta.Intent = jsonx.MustParseString(intentJSON).(*jsonx.Obj)
	return meta
}

func stateOf(stateJSON string) *session.OperationState {
	return stateOfTask(stateJSON, "")
}

func stateOfTask(stateJSON, boundaryJSON string) *session.OperationState {
	var obj *jsonx.Obj
	if strings.HasPrefix(stateJSON, "{") {
		obj = jsonx.MustParseString(stateJSON).(*jsonx.Obj)
	} else {
		obj = jsonx.NewObj()
		obj.Set("at", stateJSON)
	}
	state := &session.OperationState{At: stringOf(obj, "at")}
	if v, ok := obj.Get("targetId"); ok {
		if s, ok := v.(string); ok {
			state.TargetID = &s
		}
	}
	if boundaryJSON != "" {
		state.Task = jsonx.MustParseString(`{"boundary":` + boundaryJSON + `}`).(*jsonx.Obj)
	}
	return state
}

func TestChainEntries(t *testing.T) {
	entries := []*session.Entry{
		{EntryBase: session.EntryBase{ID: "a"}},
		{EntryBase: session.EntryBase{ID: "b"}},
		{EntryBase: session.EntryBase{ID: "c"}},
	}
	root := "root"
	chained := ChainEntries(&root, entries)
	if *chained[0].ParentID != "root" || *chained[1].ParentID != "a" || *chained[2].ParentID != "b" {
		t.Fatalf("chain = %v", chained)
	}
	// Originals untouched.
	if entries[0].ParentID != nil {
		t.Fatal("source mutated")
	}
}

func TestEntryLifecycleEvents(t *testing.T) {
	message := &session.Entry{
		EntryBase: session.EntryBase{ID: "m1", Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "user", Message: jsonx.MustParseString(`{"role":"user","content":"hi"}`).(*jsonx.Obj)},
	}
	events := EntryLifecycleEvents(message, "main", "run-1")
	if len(events) != 3 || EventType(events[0]) != "message_start" || EventType(events[1]) != "message_end" || EventType(events[2]) != "entry_added" {
		t.Fatalf("events = %v", events)
	}
	if events[0].MustGet("runId") != "run-1" || events[0].MustGet("lane") != "main" {
		t.Fatalf("event fields = %s", jsonx.Stringify(events[0]))
	}
	custom := &session.Entry{
		EntryBase: session.EntryBase{ID: "c1", Type: session.EntryTypeCustom},
	}
	events = EntryLifecycleEvents(custom, "main")
	if len(events) != 1 || EventType(events[0]) != "entry_added" {
		t.Fatalf("events = %v", events)
	}
	if _, hasRun := events[0].Get("runId"); hasRun {
		t.Fatal("runId present without run")
	}
}
