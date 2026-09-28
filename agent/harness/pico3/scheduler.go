package pico3

// scheduler.go ports harness/pico3/scheduler.ts: the erased-kind handler
// validation core and the scheduler's hold/kick/drain state machine. The
// session-line integration (reserveEligible, leases, invocations) rides on
// the Session/TxImpl port and lands with it.

import (
	"encoding/json"
	"fmt"
	"sync"
)

// TaskContractFault mirrors the TS error: a kind broke its contract.
type TaskContractFault struct {
	Kind    string
	Message string
}

func (e *TaskContractFault) Error() string {
	return fmt.Sprintf("task contract fault in kind %s: %s", e.Kind, e.Message)
}

// Step is the handler result union (JSON-shaped).
type Step struct {
	// Done is non-nil when the handler returned a closure.
	Done func(task *Task, tx TxLike, ctx ContextLike) (any, error)
	// Next is set when the handler transitions phases.
	NextPhase    string
	HasNextPhase bool
	// Checkpoint rides along with a phase transition.
	Checkpoint Checkpoint
	// Raw carries the original value for pass-through.
	Raw any
}

// TxLike is the minimal transaction surface handlers see.
type TxLike interface {
	Task(id Id) (*Task, error)
	SetTask(task *Task) error
}

// ContextLike is the minimal context surface handlers see.
type ContextLike interface {
	Aborted() bool
}

// Handler is the erased-kind adapter signature.
type Handler func(task *Task, rt RuntimeLike, ctx ContextLike) (*Step, error)

// RuntimeLike is the runtime surface handed to handlers.
type RuntimeLike interface{}

// KindPhases describes one task kind for validation.
type KindPhases struct {
	Name     string
	Initial  Handler
	Phases   map[string]Handler
	Inflight []string
}

// HandlerFor mirrors handlerFor: resolves the handler for a phase (nil
// checkpoint = initial).
func HandlerFor(kind *KindPhases, phase *string) (Handler, error) {
	if phase == nil {
		if kind.Initial == nil {
			return nil, &TaskContractFault{Kind: kind.Name, Message: "no handler for phase initial"}
		}
		return kind.Initial, nil
	}
	handler, ok := kind.Phases[*phase]
	if !ok || handler == nil {
		return nil, &TaskContractFault{Kind: kind.Name, Message: "no handler for phase " + *phase}
	}
	return handler, nil
}

// ValidateStep mirrors validateStep: done function, next function, or a
// {phase} object naming a known phase.
func ValidateStep(kind *KindPhases, step *Step) error {
	if step == nil {
		return &TaskContractFault{Kind: kind.Name, Message: "handler returned a non-object step"}
	}
	if step.Done != nil {
		return nil
	}
	if step.HasNextPhase {
		if _, ok := kind.Phases[step.NextPhase]; !ok {
			return &TaskContractFault{Kind: kind.Name, Message: "transition to unknown phase " + step.NextPhase}
		}
		return nil
	}
	if step.Raw != nil {
		return nil // passthrough already validated by construction
	}
	return &TaskContractFault{Kind: kind.Name, Message: "handler returned an invalid step"}
}

// ValidateCompletion mirrors validateCompletion: completed needs result,
// failed needs failure.
func ValidateCompletion(kind *KindPhases, completion Outcome) error {
	if completion == nil {
		return &TaskContractFault{Kind: kind.Name, Message: "closure returned an invalid completion"}
	}
	status, _ := completion.Get("status")
	hasResult, resultOK := completion.Get("result")
	hasFailure, failureOK := completion.Get("failure")
	if status == "completed" && resultOK && hasResult != nil {
		return nil
	}
	if status == "failed" && failureOK && hasFailure != nil {
		return nil
	}
	_ = hasResult
	return &TaskContractFault{Kind: kind.Name, Message: "closure returned an invalid completion"}
}

// ValidateCheckpoint mirrors validateCheckpoint: object with a string
// phase naming a known, non-in-flight phase; must be strict JSON.
func ValidateCheckpoint(kind *KindPhases, checkpoint Checkpoint) error {
	if checkpoint == nil {
		return &TaskContractFault{Kind: kind.Name, Message: "invalid checkpoint"}
	}
	phaseValue, ok := checkpoint.Get("phase")
	if !ok {
		return &TaskContractFault{Kind: kind.Name, Message: "invalid checkpoint"}
	}
	phase, ok := phaseValue.(string)
	if !ok {
		return &TaskContractFault{Kind: kind.Name, Message: "invalid checkpoint"}
	}
	if _, known := kind.Phases[phase]; !known {
		return &TaskContractFault{Kind: kind.Name, Message: "checkpoint names unknown phase " + phase}
	}
	for _, inflight := range kind.Inflight {
		if inflight == phase {
			return &TaskContractFault{
				Kind:    kind.Name,
				Message: "transition into in-flight phase " + phase + "; write it with rt.commit before the effect instead",
			}
		}
	}
	if _, err := json.Marshal(checkpoint); err != nil {
		return &TaskContractFault{Kind: kind.Name, Message: "invalid checkpoint"}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Scheduler state machine (hold / kick / drain)
// ---------------------------------------------------------------------------

// SchedulerGate is the drain-loop state machine independent of the session
// line: resume/stop, hold with idempotent release, kick marking dirty and
// draining when unheld.
type SchedulerGate struct {
	mu       sync.Mutex
	enabled  bool
	holds    int
	dirty    bool
	draining bool
	onDrain  func() error
}

// NewSchedulerGate builds a gate around the drain callback.
func NewSchedulerGate(onDrain func() error) *SchedulerGate {
	return &SchedulerGate{onDrain: onDrain}
}

// Resume enables and kicks.
func (g *SchedulerGate) Resume() {
	g.mu.Lock()
	g.enabled = true
	g.mu.Unlock()
	g.Kick()
}

// Stop disables.
func (g *SchedulerGate) Stop() {
	g.mu.Lock()
	g.enabled = false
	g.mu.Unlock()
}

// Hold increments the hold count; the returned release decrements it
// idempotently and kicks at zero.
func (g *SchedulerGate) Hold() (release func()) {
	g.mu.Lock()
	g.holds++
	g.mu.Unlock()
	released := false
	return func() {
		g.mu.Lock()
		if released {
			g.mu.Unlock()
			return
		}
		released = true
		g.holds--
		kick := g.holds == 0
		g.mu.Unlock()
		if kick {
			g.Kick()
		}
	}
}

// Kick marks dirty and drains when enabled, unheld, and not already
// draining.
func (g *SchedulerGate) Kick() {
	g.mu.Lock()
	if !g.enabled {
		g.mu.Unlock()
		return
	}
	g.dirty = true
	if g.holds > 0 || g.draining {
		g.mu.Unlock()
		return
	}
	g.draining = true
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		g.draining = false
		g.mu.Unlock()
	}()
	for {
		g.mu.Lock()
		if !g.dirty || !g.enabled || g.holds > 0 {
			g.mu.Unlock()
			return
		}
		g.dirty = false
		g.mu.Unlock()
		if err := g.onDrain(); err != nil {
			// Reported by the caller's onReport; keep draining.
			_ = err
		}
	}
}

// Stats exposes the machine counters for tests.
func (g *SchedulerGate) Stats() (enabled bool, holds int, dirty bool, draining bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.enabled, g.holds, g.dirty, g.draining
}
