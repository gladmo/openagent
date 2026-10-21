// Package toolset composes harness tools onto the bare agent loop. It is
// openagent-native (not a 1:1 port; same provenance class as
// agent/harness/compaction): the seam decision is recorded in
// .agents/notes/implemented/architecture/.
//
// Two tiers: Adapt is a pure wrapper for callers that already own an
// execution env and gate; Local bundles one env + gate behind a single
// idempotent disposer.
package toolset

import (
	"errors"
	"strings"
	"sync"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/env/local"
	"github.com/gladmo/openagent/agent/harness/tools"
	"github.com/gladmo/openagent/ai"
	chordcontext "github.com/gladmo/openagent/chord/context"
	"github.com/gladmo/openagent/jsonx"
)

// Coding returns the coding toolset: read, write, edit, bash.
func Coding() []*harness.AgentHarnessTool {
	return []*harness.AgentHarnessTool{
		tools.CreateReadTool(),
		tools.CreateWriteTool(),
		tools.CreateEditTool(),
		tools.CreateBashTool(),
	}
}

// Adapt wraps harness tools onto the agent loop's Execute signature. Pure:
// it creates and owns nothing — env and gate stay with the caller.
//
// Each call runs the pi execution pipeline: PrepareToolCall (typebox
// validation) → ApplyBeforeToolDecision → ExecuteToolCall (panic-to-error
// conversion, gate admission) → FinalizeToolCall. The loop's abort.Signal
// is woven into the chord Context; a failed or blocked call returns an
// error whose text carries the tool output.
func Adapt(toolset []*harness.AgentHarnessTool, env harness.ExecutionEnv, gate harness.Gate) []*agent.AgentTool {
	if env == nil {
		panic("toolset.Adapt requires a non-nil execution env")
	}
	if gate == nil {
		panic("toolset.Adapt requires a non-nil gate")
	}
	adapted := make([]*agent.AgentTool, 0, len(toolset))
	for _, tool := range toolset {
		adapted = append(adapted, adaptOne(tool, env, gate))
	}
	return adapted
}

// Local owns one local execution env and gate over a harness toolset and
// exposes the agent-ready tools. Close is the single shutdown path; it is
// idempotent. The cwd must already exist — NewLocal never creates it.
type Local struct {
	env       *local.LocalExecutionEnv
	gate      harness.Gate
	control   harness.GateControl
	adapted   []*agent.AgentTool
	closeOnce sync.Once
	closeErr  error
}

// NewLocal builds the bundle over cwd. It allocates the env and gate
// immediately; everything is released by Close.
func NewLocal(cwd string, toolset []*harness.AgentHarnessTool) *Local {
	env := local.New(cwd)
	gate, control := harness.CreateGate()
	return &Local{
		env:     env,
		gate:    gate,
		control: control,
		adapted: Adapt(toolset, env, gate),
	}
}

// AgentTools returns the adapted tools for AgentOptions (initial state or
// per-turn assignment).
func (l *Local) AgentTools() []*agent.AgentTool {
	return l.adapted
}

// Close shuts the bundle down in order: the gate stops admitting, tracked
// shell children are killed, env cleanup runs, and the file mutation queue
// state for the env is released. Repeated calls return the first result.
func (l *Local) Close() error {
	l.closeOnce.Do(func() {
		l.control.Close(nil)
		ctx := harness.BackgroundContext
		var errs []error
		if err := l.env.CleanupShell(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := l.env.Cleanup(ctx); err != nil {
			errs = append(errs, err)
		}
		tools.ReleaseMutationQueue(l.env)
		l.closeErr = errors.Join(errs...)
	})
	return l.closeErr
}

// adaptOne wires one harness tool through the execution pipeline.
func adaptOne(tool *harness.AgentHarnessTool, env harness.ExecutionEnv, gate harness.Gate) *agent.AgentTool {
	return &agent.AgentTool{
		Name:        tool.Name,
		Label:       tool.Label,
		Description: tool.Description,
		Parameters:  tool.Parameters,
		Execute: func(toolCallID string, params any, signal *abort.Signal, onUpdate agent.AgentToolUpdateCallback) (*agent.AgentToolResult, error) {
			arguments, _ := params.(*jsonx.Obj)
			if arguments == nil {
				arguments = jsonx.NewObj()
			}
			call := &ai.ToolCall{ID: toolCallID, Name: tool.Name, Arguments: arguments}
			// The pipeline signals admission rejection by unwinding
			// (AbortRequested, gate-closed); the loop's Execute contract
			// is an error return, so the adapter converts both at this
			// seam — a panic past here would crash the loop.
			return runPipelinedCall(call, tool, env, gate, signal, onUpdate)
		},
	}
}

// runPipelinedCall drives one call through Prepare → Before → Execute →
// Finalize, converting admission panics to error returns.
func runPipelinedCall(
	call *ai.ToolCall,
	tool *harness.AgentHarnessTool,
	env harness.ExecutionEnv,
	gate harness.Gate,
	signal *abort.Signal,
	onUpdate agent.AgentToolUpdateCallback,
) (result *agent.AgentToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			if _, isAbort := r.(*harness.AbortRequested); isAbort {
				err = errors.New("Operation aborted")
				return
			}
			err = errors.New(harness.ToError(r).Error())
		}
	}()
	prepared, immediate := harness.PrepareToolCall(call, []*harness.AgentHarnessTool{tool})
	if immediate != nil {
		return nil, errors.New(resultText(immediate.Result))
	}
	cleared, immediate := harness.ApplyBeforeToolDecision(prepared, nil)
	if immediate != nil {
		return nil, errors.New(resultText(immediate.Result))
	}
	ctx := chordcontext.WithAbortSignal(signal, chordcontext.BackgroundContext)
	executed, _ := harness.ExecuteToolCall(cleared, gate, adaptUpdateCallback(onUpdate), tools.ExecutionToolContext{Env: env}, nil, ctx)
	finalized := harness.FinalizeToolCall(cleared, executed, nil)
	if finalized.IsError {
		return nil, errors.New(resultText(finalized.Result))
	}
	return finalized.Result, nil
}

// adaptUpdateCallback maps the loop's progress callback onto the harness
// callback signature.
func adaptUpdateCallback(onUpdate agent.AgentToolUpdateCallback) harness.AgentHarnessToolUpdateCallback {
	if onUpdate == nil {
		return nil
	}
	return func(partial *agent.AgentToolResult, _ *harness.AgentHarnessToolUpdateOptions) {
		onUpdate(partial)
	}
}

// resultText extracts every text block of a tool result (a bash failure
// keeps its stdout/stderr in the error text).
func resultText(result *agent.AgentToolResult) string {
	if result == nil {
		return "tool failed without a result"
	}
	var out strings.Builder
	for _, block := range result.Content {
		if text, ok := block.(ai.TextContent); ok {
			out.WriteString(text.Text)
		}
	}
	return out.String()
}
