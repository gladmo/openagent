package runtime

// Ports of drive/checkpoint.test.ts behaviors (representative cases).

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

type checkpointEnv struct {
	lane *Lane
	sess session.Session
}

func newCheckpointEnv(t *testing.T, at string) *checkpointEnv {
	t.Helper()
	storage := session.NewMemoryStorage()
	metadata := session.SessionMetadata{ID: "s1", StorageVersion: 1}
	sess := session.NewStorageBackedSession(metadata, storage)
	tip := "tip-root"
	operation := &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-1", Lane: "main", StartedAt: 1},
		State: session.OperationState{At: at, Control: session.Control{Status: "running"}},
	}
	operation.Meta.Intent = jsonx.ObjFrom("kind", "run", "promptEntryIds", []any{})
	operation.State.Settings = jsonx.ObjFrom(
		"steeringMode", "all",
		"followUpMode", "all",
	)
	// Commit a real root entry so chained parents validate.
	rootEntry := &session.Entry{
		EntryBase: session.EntryBase{ID: tip, Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "user"},
	}
	if err := sess.Mutate(func(mutator session.SessionMutator, ctx contextContextAlias) error {
		_, err := mutator.Commit([]session.Write{session.InsertEntry(rootEntry)}, ctx)
		return err
	}, harnessBackground()); err != nil {
		t.Fatal(err)
	}
	lane := NewLane(LaneOptions{
		Name:    "main",
		Session: sess,
		State: &RuntimeLaneState{
			TipID:     &tip,
			Operation: operation,
		},
	})
	// Durable operation values so SettleOperation writes validate.
	ctx := harnessBackground()
	if err := sess.SetValue(session.OperationMetaValue("op-1"), jsonx.ObjFrom("operationId", "op-1", "lane", "main", "sourceTipId", nil, "startedAt", float64(1), "intent", operation.Meta.Intent), ctx); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetValue(session.OperationStateValue("op-1"), operationStateToJSON(&operation.State), ctx); err != nil {
		t.Fatal(err)
	}
	return &checkpointEnv{lane: lane, sess: sess}
}

func TestStartRunCommitsInitialCheckpoint(t *testing.T) {
	env := newCheckpointEnv(t, session.AtStarting)
	// before_run injects one user message.
	original := BeforeRunHook
	BeforeRunHook = func(*Lane, *Drive, []*jsonx.Obj) ([]*jsonx.Obj, error) {
		return []*jsonx.Obj{jsonx.ObjFrom("role", "user", "content", "injected")}, nil
	}
	defer func() { BeforeRunHook = original }()

	result, err := StartRun(env.lane, &Drive{OperationID: "op-1", Context: harnessBackground()}, &env.lane.State().Operation.State)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "continue" {
		t.Fatalf("result = %+v", result)
	}
	state := env.lane.State().Operation.State
	if state.At != session.AtCheckpoint {
		t.Fatalf("at = %s", state.At)
	}
	if state.Continuation == nil || state.Continuation.MustGet("kind") != "need_assistant" {
		t.Fatalf("continuation = %v", state.Continuation)
	}
	// The injected entry landed and became the trigger + tip.
	if state.TriggerEntryID == "" {
		t.Fatal("trigger empty")
	}
	tip, err := env.lane.State(), error(nil)
	_ = tip
	_ = err
	ctx := harnessBackground()
	stored, err := env.sess.GetValue(session.BranchTip("main"), ctx)
	if err != nil || stored == nil {
		t.Fatalf("tip stored = %v err = %v", stored, err)
	}
	if stored.Value != state.TriggerEntryID {
		t.Fatalf("tip = %v trigger = %s", stored.Value, state.TriggerEntryID)
	}
}

func TestStartRunRejectsPendingAssistantInjection(t *testing.T) {
	env := newCheckpointEnv(t, session.AtStarting)
	original := BeforeRunHook
	BeforeRunHook = func(*Lane, *Drive, []*jsonx.Obj) ([]*jsonx.Obj, error) {
		return []*jsonx.Obj{jsonx.ObjFrom("role", "assistant", "stopReason", "pending", "content", []any{})}, nil
	}
	defer func() { BeforeRunHook = original }()

	_, err := StartRun(env.lane, &Drive{OperationID: "op-1", Context: harnessBackground()}, &env.lane.State().Operation.State)
	if err == nil || !strings.Contains(err.Error(), "pending assistant message") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartRunNonRunIntentRejected(t *testing.T) {
	env := newCheckpointEnv(t, session.AtStarting)
	env.lane.State().Operation.Meta.Intent = jsonx.ObjFrom("kind", "compaction")
	_, err := StartRun(env.lane, &Drive{OperationID: "op-1", Context: harnessBackground()}, &env.lane.State().Operation.State)
	if err == nil || !strings.Contains(err.Error(), "non-run intent") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartRunMissingPromptEntry(t *testing.T) {
	env := newCheckpointEnv(t, session.AtStarting)
	env.lane.State().Operation.Meta.Intent = jsonx.ObjFrom("kind", "run", "promptEntryIds", []any{"ghost"})
	ctx := harnessBackground()
	if err := env.sess.SetValue(session.OperationMetaValue("op-1"), jsonx.ObjFrom("operationId", "op-1", "lane", "main", "sourceTipId", nil, "startedAt", float64(1), "intent", env.lane.State().Operation.Meta.Intent), ctx); err != nil {
		t.Fatal(err)
	}
	_, err := StartRun(env.lane, &Drive{OperationID: "op-1", Context: harnessBackground()}, &env.lane.State().Operation.State)
	if err == nil || !strings.Contains(err.Error(), "missing its message") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunCheckpointSteerTriggerAdvancesToAssistantReady(t *testing.T) {
	env := newCheckpointEnv(t, session.AtCheckpoint)
	ctx := harnessBackground()
	// Seed a steer in the inbox + pending payload.
	if err := env.sess.SetValue(session.PendingEntryValue("s1"), jsonx.MustParseString(`{"type":"message","payload":{"role":"user","content":"steer me"}}`), ctx); err != nil {
		t.Fatal(err)
	}
	env.lane.State().Inbox = []session.InboxItem{{EntryID: "s1", Kind: "steer"}}
	// need_assistant continuation with a trigger already set.
	env.lane.State().Operation.State.Continuation = jsonx.ObjFrom("kind", "need_assistant", "overflowRecoveryUsed", false)
	env.lane.State().Operation.State.TriggerEntryID = "trigger-old"

	result, err := RunCheckpoint(env.lane, &Drive{OperationID: "op-1", Context: ctx}, &env.lane.State().Operation.State)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "continue" {
		t.Fatalf("result = %+v", result)
	}
	state := &env.lane.State().Operation.State
	if state.At != session.AtAssistantReady {
		t.Fatalf("at = %s", state.At)
	}
	if state.TriggerEntryID != "s1" && state.GenerationContext == nil {
		t.Fatal("generation context missing")
	}
	// Inbox drained.
	if len(env.lane.State().Inbox) != 0 {
		t.Fatalf("inbox = %+v", env.lane.State().Inbox)
	}
	// Pending payload consumed.
	stored, _ := env.sess.GetValue(session.PendingEntryValue("s1"), ctx)
	if stored != nil {
		t.Fatal("pending payload survived")
	}
}

func TestRunCheckpointThresholdDivertsToSummary(t *testing.T) {
	env := newCheckpointEnv(t, session.AtCheckpoint)
	ctx := harnessBackground()
	env.lane.State().Operation.State.Continuation = jsonx.ObjFrom("kind", "need_assistant", "overflowRecoveryUsed", false)
	env.lane.State().Operation.State.TriggerEntryID = "trigger-old"

	original := PrepareCompactionThreshold
	PrepareCompactionThreshold = func(*Lane, *Drive, *session.OperationState) (*ThresholdPreparation, bool, error) {
		return &ThresholdPreparation{TaskID: "task-1", Preparation: jsonx.ObjFrom("kind", "compaction")}, true, nil
	}
	defer func() { PrepareCompactionThreshold = original }()

	result, err := RunCheckpoint(env.lane, &Drive{OperationID: "op-1", Context: ctx}, &env.lane.State().Operation.State)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "continue" {
		t.Fatalf("result = %+v", result)
	}
	state := &env.lane.State().Operation.State
	if state.At != session.AtSummaryDeciding {
		t.Fatalf("at = %s", state.At)
	}
	if state.Task == nil {
		t.Fatal("task missing")
	}
	if state.Task.MustGet("reason") != "threshold" {
		t.Fatalf("reason = %v", state.Task.MustGet("reason"))
	}
	// The preparation landed under the operation prefix.
	stored, _ := env.sess.GetValue(session.OperationPreparation("op-1", "task-1"), ctx)
	if stored == nil {
		t.Fatal("preparation missing")
	}
}

func TestRunCheckpointMayFinishSettles(t *testing.T) {
	env := newCheckpointEnv(t, session.AtCheckpoint)
	ctx := harnessBackground()
	env.lane.State().Operation.State.Continuation = jsonx.ObjFrom("kind", "may_finish", "includeFinalAssistant", false)
	// Seed the tip value so placement resolves a tip.
	if err := env.sess.SetValue(session.BranchTip("main"), "tip-root", ctx); err != nil {
		t.Fatal(err)
	}

	result, err := RunCheckpoint(env.lane, &Drive{OperationID: "op-1", Context: ctx}, &env.lane.State().Operation.State)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "settled" {
		t.Fatalf("result = %+v", result)
	}
	record := result.Outcome.(*session.OperationResultRecord)
	if record.Status != session.StatusCompleted || record.Kind != "run" {
		t.Fatalf("record = %+v", record)
	}
	// Operation cleared.
	if env.lane.State().Operation != nil {
		t.Fatal("operation not cleared")
	}
}

func TestRunCheckpointNeedAssistantWithoutInboxKeepsTrigger(t *testing.T) {
	env := newCheckpointEnv(t, session.AtCheckpoint)
	ctx := harnessBackground()
	env.lane.State().Operation.State.Continuation = jsonx.ObjFrom("kind", "need_assistant", "overflowRecoveryUsed", false)
	env.lane.State().Operation.State.TriggerEntryID = "trigger-old"

	result, err := RunCheckpoint(env.lane, &Drive{OperationID: "op-1", Context: ctx}, &env.lane.State().Operation.State)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "continue" {
		t.Fatalf("result = %+v", result)
	}
	state := &env.lane.State().Operation.State
	if state.At != session.AtAssistantReady {
		t.Fatalf("at = %s", state.At)
	}
	// assistant.ready carries the trigger in its generation context.
	if state.GenerationContext == nil || state.GenerationContext.MustGet("triggerEntryId") != "trigger-old" {
		t.Fatalf("generation = %+v", state.GenerationContext)
	}
}
