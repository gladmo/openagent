package harness

// execution_gate.go ports harness/execution/effect-gate.ts.

import (
	"fmt"

	"github.com/gladmo/openagent/abort"
)

// AbortRequested is the expected internal control flow when cancellation
// wins effect admission. Cancellation is a channel the caller can wait on.
type AbortRequested struct {
	Cancellation <-chan struct{}
}

func (e *AbortRequested) Error() string { return "Abort requested" }

// Gate is the procedure-facing synchronous admission capability.
type Gate interface {
	Admit(invoke func())
	Signal() *abort.Signal
}

// GateControl is the owner-facing lifecycle control.
type GateControl interface {
	BeginAbort(cancellation <-chan struct{})
	SignalAbort()
	Close(err error)
}

type gateState struct {
	status       string // "open" | "aborting" | "closed"
	cancellation <-chan struct{}
	err          error
}

type gateImpl struct {
	state      gateState
	controller *abort.Controller
}

// CreateGate creates separate procedure-facing and owner-facing views of
// one effect gate.
func CreateGate() (Gate, GateControl) {
	g := &gateImpl{state: gateState{status: "open"}, controller: abort.NewController()}
	return g, g
}

func (g *gateImpl) check() {
	switch g.state.status {
	case "aborting":
		panic(&AbortRequested{Cancellation: g.state.cancellation})
	case "closed":
		panic(g.state.err)
	}
}

func (g *gateImpl) Admit(invoke func()) {
	g.check()
	invoke()
}

func (g *gateImpl) Signal() *abort.Signal { return g.controller.Signal() }

func (g *gateImpl) BeginAbort(cancellation <-chan struct{}) {
	if g.state.status != "open" {
		return
	}
	g.state = gateState{status: "aborting", cancellation: cancellation}
}

func (g *gateImpl) SignalAbort() {
	if g.state.status != "aborting" || g.controller.Signal().Aborted() {
		return
	}
	g.controller.AbortReason(&AbortRequested{Cancellation: g.state.cancellation})
}

func (g *gateImpl) Close(err error) {
	if g.state.status == "closed" {
		return
	}
	if err == nil {
		err = fmt.Errorf("gate closed")
	}
	g.state = gateState{status: "closed", err: err}
	if !g.controller.Signal().Aborted() {
		g.controller.AbortReason(err)
	}
}
