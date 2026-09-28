package runtime

// Ports of the ready-pipeline assembly.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func pipelineDeps(t *testing.T) *PipelineDeps {
	t.Helper()
	return &PipelineDeps{
		Registry:       prepRegistry(t),
		ActiveTools:    nil,
		AvailableTools: nil,
		SystemPrompt:   "You are pi.",
		NextID:         func() string { return ai.UUIDv7() },
		ReadBounded: func(*Lane, *Drive) ([]*jsonx.Obj, bool, error) {
			return []*jsonx.Obj{jsonx.ObjFrom("role", "user", "content", "hi")}, false, nil
		},
		Perform: func(*Lane, *Drive, *PreparedGeneration, *GenerationIntent) (*jsonx.Obj, error) {
			// A plain answer: no tool calls.
			return jsonx.ObjFrom("role", "assistant", "stopReason", "stop", "content", []any{
				jsonx.ObjFrom("type", "text", "text", "hello"),
			}), nil
		},
		ClassifyAndPublish: func(lane *Lane, drive *Drive, at string, response *jsonx.Obj, intent *GenerationIntent, prepared *PreparedGeneration) (*ProcedureResult, error) {
			scope := &session.OperationState{Control: session.Control{Status: "running"}}
			disposition := ClassifyResponse(at, response, scope, intent.ResponseEntryID, false, false,
				struct{ Recovery bool }{}, retryStruct(1, 4), neverRetryFn, func() float64 { return 0 }, nil)
			if disposition.Settled == nil || disposition.Settled.At != session.AtCheckpoint {
				t.Fatalf("disposition = %+v", disposition)
			}
			return &ProcedureResult{Kind: "settled", Outcome: &session.OperationResultRecord{
				OperationID: drive.OperationID, Status: session.StatusCompleted,
			}}, nil
		},
	}
}

func retryStruct(attempt, max float64) struct {
	Attempt         float64
	MaxAttempts     float64
	RetryEnabled    bool
	MaxRetries      int
	BaseDelayMS     float64
	MaxAgentDelayMS *float64
} {
	return struct {
		Attempt         float64
		MaxAttempts     float64
		RetryEnabled    bool
		MaxRetries      int
		BaseDelayMS     float64
		MaxAgentDelayMS *float64
	}{Attempt: attempt, MaxAttempts: max, RetryEnabled: true, MaxRetries: 3, BaseDelayMS: 1000}
}

func neverRetryFn(*jsonx.Obj) bool { return false }

func newReadyLane(t *testing.T) (*Lane, *Drive) {
	t.Helper()
	lane, _, drive := newReconcileEnv(t, session.AtAssistantReady, "run")
	lane.State().Operation.State.Control.Status = "running"
	lane.State().Operation.State.NextAttempt = 1
	lane.State().Operation.State.GenerationContext = jsonx.ObjFrom(
		"stepId", "step-1",
		"configuration", jsonx.ObjFrom(
			"model", jsonx.ObjFrom("provider", ai.FauxDefaultProvider, "modelId", ai.FauxDefaultModelID),
			"activeToolNames", []any{},
		),
	)
	ctx := harnessBackground()
	if err := lane.session.SetValue(session.OperationStateValue("op-1"), operationStateToJSON(&lane.State().Operation.State), ctx); err != nil {
		t.Fatal(err)
	}
	return lane, drive
}

func TestRunReadyPipelineHappyPath(t *testing.T) {
	lane, drive := newReadyLane(t)
	performCalled := false
	classifyCalled := false
	deps := pipelineDeps(t)
	deps.Perform = func(lane *Lane, drive *Drive, prepared *PreparedGeneration, intent *GenerationIntent) (*jsonx.Obj, error) {
		performCalled = true
		if prepared.SystemPrompt != "You are pi." {
			t.Fatalf("prompt = %q", prepared.SystemPrompt)
		}
		if intent.ResponseEntryID == "" {
			t.Fatal("intent without response entry")
		}
		return jsonx.ObjFrom("role", "assistant", "stopReason", "stop", "content", []any{}), nil
	}
	deps.ClassifyAndPublish = func(*Lane, *Drive, string, *jsonx.Obj, *GenerationIntent, *PreparedGeneration) (*ProcedureResult, error) {
		classifyCalled = true
		return &ProcedureResult{Kind: "settled"}, nil
	}

	result, err := RunReadyPipeline(lane, drive, &lane.State().Operation.State, deps)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "settled" {
		t.Fatalf("result = %+v", result)
	}
	if !performCalled || !classifyCalled {
		t.Fatalf("perform = %v classify = %v", performCalled, classifyCalled)
	}
	// The lane moved to effect_pending after the intent.
	if lane.State().Operation == nil || lane.State().Operation.State.At != session.AtAssistantEffectPending {
		t.Fatalf("at = %+v", lane.State().Operation)
	}
}

func TestRunReadyPipelineModelFailure(t *testing.T) {
	lane, drive := newReadyLane(t)
	// Break the identity so the model resolves nil.
	lane.State().Operation.State.GenerationContext = jsonx.ObjFrom(
		"stepId", "step-1",
		"configuration", jsonx.ObjFrom(
			"model", jsonx.ObjFrom("provider", "ghost", "modelId", "x"),
			"activeToolNames", []any{},
		),
	)
	ctx := harnessBackground()
	if err := lane.session.SetValue(session.OperationStateValue("op-1"), operationStateToJSON(&lane.State().Operation.State), ctx); err != nil {
		t.Fatal(err)
	}
	deps := pipelineDeps(t)
	result, err := RunReadyPipeline(lane, drive, &lane.State().Operation.State, deps)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "settled" {
		t.Fatalf("result = %+v", result)
	}
	record := result.Outcome.(*session.OperationResultRecord)
	if record.Status != session.StatusFailed || record.Error == nil || record.Error.Code != "model_unavailable" {
		t.Fatalf("record = %+v", record)
	}
	// Operation cleared (configuration failure settles).
	if lane.State().Operation != nil {
		t.Fatal("operation not cleared")
	}
}

func TestInstallReadyPipeline(t *testing.T) {
	original := ReadyProcedure
	defer func() { ReadyProcedure = original }()
	InstallReadyPipeline(pipelineDeps(t))
	if ReadyProcedure == nil {
		t.Fatal("pipeline not installed")
	}
	lane, drive := newReadyLane(t)
	result, err := ReadyProcedure(lane, drive, &lane.State().Operation.State)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != "settled" {
		t.Fatalf("result = %+v", result)
	}
}

func TestGenerationContextIdentity(t *testing.T) {
	ready := &session.OperationState{GenerationContext: jsonx.ObjFrom(
		"configuration", jsonx.ObjFrom(
			"model", jsonx.ObjFrom("provider", "p", "modelId", "m"),
			"activeToolNames", []any{"bash", "read"},
		),
	)}
	identity, active := generationContextIdentity(ready)
	if identity == nil || identity.MustGet("provider") != "p" {
		t.Fatalf("identity = %v", identity)
	}
	if len(active) != 2 || active[0] != "bash" {
		t.Fatalf("active = %v", active)
	}
	// Missing context -> nil/nil.
	if identity, active := generationContextIdentity(&session.OperationState{}); identity != nil || active != nil {
		t.Fatal("empty context")
	}
	_ = strings.TrimSpace
}
