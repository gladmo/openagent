// Package harnessfacade: prompt.go wires the facade's prompt path —
// queueing a user message, admitting a run, and driving it through the
// runtime drive dispatcher.
package harnessfacade

import (
	"fmt"
	"time"

	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/runtime"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// RunResult mirrors the TS shape.
type RunResult struct {
	OperationID string
	Outcome     *runtime.DriveOutcome
}

// Prompt enqueues one user message and drives the admitted run.
func (h *AgentHarness) Prompt(laneName, text string, ctx harness.Context) (*RunResult, error) {
	lane, err := h.Lane(laneName, ctx)
	if err != nil {
		return nil, err
	}
	// Build the user message JSON.
	timestamp := float64(time.Now().UnixMilli())
	message := jsonx.NewObj()
	message.Set("role", "user")
	message.Set("content", text)
	message.Set("timestamp", timestamp)

	// Stage the pending prompt entry.
	promptID := ai.UUIDv7(timestamp)
	if err := h.session.SetValue(session.PendingEntryValue(promptID), jsonx.ObjFrom("type", "message", "payload", message), ctx); err != nil {
		return nil, err
	}
	// Admit the run.
	result, err := runtime.AcceptRun(lane, []runtime.AcceptPrompt{{ID: promptID, Message: message}},
		h.queueMode(h.options.SteeringMode), h.queueMode(h.options.FollowUpMode),
		func(float64) string { return ai.UUIDv7() }, ctx)
	if err != nil {
		return nil, err
	}
	if result.Busy != nil {
		return nil, result.Busy
	}
	if result.Invalid != nil {
		return nil, result.Invalid
	}
	// Drive the admitted operation through the registry, with the ready
	// pipeline installed so assistant.ready runs end-to-end.
	registry := runtime.NewModelsRegistry(h.options.Models)
	h.installPipeline(registry)
	registryResult, err := h.driveOperation(lane, result.OperationID, registry, ctx)
	if err != nil {
		return nil, err
	}
	return &RunResult{OperationID: result.OperationID, Outcome: registryResult}, nil
}

// driveOperation runs the drive dispatcher over the installed operation.
func (h *AgentHarness) driveOperation(lane *runtime.Lane, operationID string, registry *runtime.ModelsRegistry, ctx harness.Context) (*runtime.DriveOutcome, error) {
	gate, control := harness.CreateGate()
	drive := &runtime.Drive{OperationID: operationID, Gate: gate, Context: ctx}
	registry2 := runtime.NewDriveRegistry()
	// Register the ported procedures.
	registry2.Register(session.AtStarting, func(l *runtime.Lane, d *runtime.Drive, state *session.OperationState) (*runtime.ProcedureResult, error) {
		return runtime.StartRun(l, d, state)
	})
	registry2.Register(session.AtCheckpoint, func(l *runtime.Lane, d *runtime.Drive, state *session.OperationState) (*runtime.ProcedureResult, error) {
		return runtime.RunCheckpoint(l, d, state)
	})
	registry2.Register(session.AtAssistantReady, func(l *runtime.Lane, d *runtime.Drive, state *session.OperationState) (*runtime.ProcedureResult, error) {
		return runtime.RunGeneration(l, d, state)
	})
	registry2.Register(session.AtAssistantRetryWait, func(l *runtime.Lane, d *runtime.Drive, state *session.OperationState) (*runtime.ProcedureResult, error) {
		return runtime.RunGeneration(l, d, state)
	})
	registry2.Register(session.AtNavigationReadyToCommit, func(l *runtime.Lane, d *runtime.Drive, state *session.OperationState) (*runtime.ProcedureResult, error) {
		return runtime.CommitNavigation(l, d, state)
	})
	_ = control
	return runtime.DriveOperation(lane, drive, registry2)
}

func (h *AgentHarness) queueMode(mode string) string {
	if mode == "" {
		return "all"
	}
	return mode
}

var _ = fmt.Sprintf

// installPipeline wires the default ready pipeline over the registry.
func (h *AgentHarness) installPipeline(registry *runtime.ModelsRegistry) {
	runtime.InstallReadyPipeline(&runtime.PipelineDeps{
		Registry:       registry,
		AvailableTools: nil,
		SystemPrompt:   h.options.SystemPrompt,
		NextID:         func() string { return ai.UUIDv7() },
		ReadBounded:    func(*runtime.Lane, *runtime.Drive) ([]*jsonx.Obj, bool, error) { return nil, false, nil },
		Perform: func(lane *runtime.Lane, drive *runtime.Drive, prepared *runtime.PreparedGeneration, intent *runtime.GenerationIntent) (*jsonx.Obj, error) {
			return runtime.PerformGeneration(registry, prepared, "off", nil, &ai.StreamOptions{})
		},
		ClassifyAndPublish: func(lane *runtime.Lane, drive *runtime.Drive, at string, response *jsonx.Obj, intent *runtime.GenerationIntent, prepared *runtime.PreparedGeneration) (*runtime.ProcedureResult, error) {
			// Plain answers settle at the finish checkpoint; the drive
			// loop continues to checkpoint -> may_finish -> settled.
			return &runtime.ProcedureResult{Kind: "continue"}, nil
		},
	})
}
