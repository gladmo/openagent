package runtime

// lane.go ports the core of harness/runtime/lane.ts: the LaneState command
// machine (command / settleOperation / continueOperation), inbox selection,
// and the public queue/abort entry points. The full drive pipeline rides on
// these primitives in drive.go (next phase).

import (
	"fmt"
	"sync"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// SelectAcceptedInbox mirrors selectAcceptedInbox: writes and nextRuns are
// always taken; steer/followUp honor one-at-a-time fairness.
func SelectAcceptedInbox(inbox []session.InboxItem, steeringMode, followUpMode string) (selected, remainder []session.InboxItem) {
	steerTaken := false
	followUpTaken := false
	for _, item := range inbox {
		eligible := item.Kind == "write" ||
			item.Kind == "nextRun" ||
			(item.Kind == "steer" && (steeringMode == "all" || !steerTaken)) ||
			(item.Kind == "followUp" && (followUpMode == "all" || !followUpTaken))
		if eligible {
			selected = append(selected, item)
			if item.Kind == "steer" {
				steerTaken = true
			}
			if item.Kind == "followUp" {
				followUpTaken = true
			}
		} else {
			remainder = append(remainder, item)
		}
	}
	return selected, remainder
}

// DurableLaneStateJSON renders the durable lane.state value.
func DurableLaneStateJSON(currentOperationID *string, lastOperationID *string, inbox []session.InboxItem) *jsonx.Obj {
	obj := jsonx.NewObj()
	if currentOperationID != nil {
		obj.Set("currentOperationId", *currentOperationID)
	} else {
		obj.Set("currentOperationId", nil)
	}
	if lastOperationID != nil {
		obj.Set("lastOperationId", *lastOperationID)
	} else {
		obj.Set("lastOperationId", nil)
	}
	items := make([]any, 0, len(inbox))
	for _, item := range inbox {
		items = append(items, jsonx.ObjFrom("entryId", item.EntryID, "kind", item.Kind))
	}
	obj.Set("inbox", items)
	return obj
}

// PendingEntryWrite builds the pending entry NewEntry for queue payloads.
func PendingEntryWrite(entryID string, pending *jsonx.Obj) *session.Entry {
	pendingType, _ := pending.Get("type")
	entry := &session.Entry{EntryBase: session.EntryBase{ID: entryID, ParentID: nil}}
	if pendingType == "message" {
		entry.Type = session.EntryTypeMessage
		if payload, ok := pending.Get("payload"); ok {
			if msg, ok := payload.(*jsonx.Obj); ok {
				entry.Message = session.AgentMessagePayload{Role: stringOf(msg, "role"), Message: msg}
			}
		}
		return entry
	}
	entry.Type = session.EntryTypeCustom
	if customType, ok := pending.Get("customType"); ok {
		if s, ok := customType.(string); ok {
			entry.CustomType = &s
		}
	}
	if payload, ok := pending.Get("payload"); ok && payload != nil {
		entry.Data = payload
	}
	return entry
}

// LaneCommandKind discriminates command outcomes.
type LaneCommandKind string

const (
	LaneCommandCommit LaneCommandKind = "commit"
	LaneCommandReturn LaneCommandKind = "return"
	LaneCommandReject LaneCommandKind = "reject"
)

// LaneCommand is one effect-free decision on the lane's mutation line.
type LaneCommand struct {
	Kind        LaneCommandKind
	Writes      []session.Write
	Next        *RuntimeLaneState
	Materialize func(commit session.CommitResult)
	Events      []HarnessEvent
	Result      any
	Error       error
}

// OperationCommandKind discriminates operation command outcomes.
type OperationCommandKind string

const (
	OperationCommandCommit OperationCommandKind = "commit"
	OperationCommandFinish OperationCommandKind = "finish"
	OperationCommandReturn OperationCommandKind = "return"
	OperationCommandReject OperationCommandKind = "reject"
)

// OperationCommand is one durable operation transition.
type OperationCommand struct {
	Kind           OperationCommandKind
	Writes         []session.Write
	OperationState *session.OperationState
	Record         *session.OperationResultRecord
	Inbox          []session.InboxItem
	HasInbox       bool
	Materialize    func(commit session.CommitResult)
	Events         []HarnessEvent
	Result         any
	Error          error
}

// ContinueOperationResult mirrors the TS union.
type ContinueOperationResult struct {
	Kind     string // "cancel_requested" | "result"
	Value    any
	HasValue bool
}

// Lane is the durable lane command machine.
type Lane struct {
	Name    string
	session session.Session

	mu          sync.Mutex
	state       *RuntimeLaneState
	stateChange chan struct{}
	idleOwner   chan struct{}
	idleClaimed bool
	closed      bool
	closedErr   error
	onFault     func(err error, ctx harness.Context) error
	eventsSink  []HarnessEvent
}

// LaneOptions configures construction.
type LaneOptions struct {
	Name    string
	Session session.Session
	State   *RuntimeLaneState
	OnFault func(err error, ctx harness.Context) error
}

// NewLane builds a lane from a restored state.
func NewLane(options LaneOptions) *Lane {
	return &Lane{
		Name:        options.Name,
		session:     options.Session,
		state:       options.State,
		stateChange: make(chan struct{}, 1),
		onFault:     options.OnFault,
	}
}

// State returns the current in-memory state.
func (l *Lane) State() *RuntimeLaneState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

func (l *Lane) assertOpen() error {
	if l.closed {
		if l.closedErr != nil {
			return l.closedErr
		}
		return fmt.Errorf("Lane is closed")
	}
	return nil
}

func (l *Lane) signalStateChange() {
	select {
	case l.stateChange <- struct{}{}:
	default:
	}
}

// Command runs one effect-free decision on the session mutation line.
// The planner sees the current state and a reader; commit decisions write,
// swap in the next state, materialize synchronously, and emit events after
// leaving the line.
func (l *Lane) Command(plan func(state *RuntimeLaneState, reader session.SessionReader) *LaneCommand, ctx harness.Context) (any, error) {
	for {
		if err := l.assertOpen(); err != nil {
			return nil, err
		}
		// Wait out any exclusive idle claim.
		l.mu.Lock()
		owner := l.idleOwner
		l.mu.Unlock()
		if owner != nil {
			signal := ctx.AbortSignal()
			select {
			case <-owner:
			case <-l.stateChange:
			case <-signal.Done():
				return nil, fmt.Errorf("context canceled while waiting for idle owner")
			}
			continue
		}

		var result any
		var planErr error
		var events []HarnessEvent
		err := l.session.Mutate(func(mutator session.SessionMutator, ctx harness.Context) error {
			if err := l.assertOpen(); err != nil {
				return err
			}
			l.mu.Lock()
			if l.idleOwner != nil {
				l.mu.Unlock()
				return errIdleBlockedRetry{}
			}
			l.mu.Unlock()

			decision := plan(l.state, mutator)
			switch decision.Kind {
			case LaneCommandReturn:
				result = decision.Result
				return nil
			case LaneCommandReject:
				return decision.Error
			case LaneCommandCommit:
				commit, err := mutator.Commit(decision.Writes, ctx)
				if err != nil {
					if l.onFault != nil {
						return l.onFault(err, ctx)
					}
					return err
				}
				l.mu.Lock()
				l.state = decision.Next
				l.mu.Unlock()
				l.signalStateChange()
				if decision.Materialize != nil {
					decision.Materialize(commit)
				}
				result = decision.Result
				events = decision.Events
				return nil
			}
			return fmt.Errorf("unknown lane command kind")
		}, ctx)
		if err != nil {
			if _, retry := err.(errIdleBlockedRetry); retry {
				continue
			}
			if l.closedErr != nil {
				return nil, l.closedErr
			}
			return nil, err
		}
		_ = planErr
		// Events are delivered after leaving the line; the harness bus in
		// the facade wires this. Here we expose them for the caller.
		if len(events) > 0 {
			l.emitEvents(events, ctx)
		}
		return result, nil
	}
}

type errIdleBlockedRetry struct{}

func (errIdleBlockedRetry) Error() string { return "idle blocked; retry" }

// emitEvents is the post-line event hook (wired by the facade). Command
// callers run on their own goroutines, so the sink append takes the lane
// mutex, matching DrainEvents.
func (l *Lane) emitEvents(events []HarnessEvent, _ harness.Context) {
	// The bus arrives with the harness facade; lanes expose the batch.
	l.mu.Lock()
	l.eventsSink = append(l.eventsSink, events...)
	l.mu.Unlock()
}

// DrainEvents returns and clears collected post-line events (facade hook).
func (l *Lane) DrainEvents() []HarnessEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	events := l.eventsSink
	l.eventsSink = nil
	return events
}

// SettleOperation runs a command against the current operation even after
// cancellation: commit augments with the op.state write; finish writes the
// result record and clears the lane operation.
func (l *Lane) SettleOperation(plan func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand, ctx harness.Context) (any, error) {
	return l.Command(func(state *RuntimeLaneState, reader session.SessionReader) *LaneCommand {
		if state.Operation == nil {
			return &LaneCommand{Kind: LaneCommandReject, Error: fmt.Errorf("Lane has no active operation")}
		}
		operation := state.Operation
		decision := plan(state, &operation.State, &operation.Meta, reader)
		switch decision.Kind {
		case OperationCommandReturn:
			return &LaneCommand{Kind: LaneCommandReturn, Result: decision.Result}
		case OperationCommandReject:
			return &LaneCommand{Kind: LaneCommandReject, Error: decision.Error}
		case OperationCommandCommit:
			writes := append([]session.Write{}, decision.Writes...)
			writes = append(writes, session.WriteFromValue(session.SetValue(
				session.OperationStateValue(operation.Meta.OperationID),
				operationStateToJSON(decision.OperationState),
			)))
			nextState := *state
			nextState.Operation = &session.Operation{Meta: operation.Meta, State: *decision.OperationState}
			if decision.HasInbox {
				writes = append(writes, session.WriteFromValue(session.SetValue(
					session.LaneStateValue(l.Name),
					DurableLaneStateJSON(&operation.Meta.OperationID, state.LastOperationID, decision.Inbox),
				)))
				nextState.Inbox = decision.Inbox
			}
			return &LaneCommand{
				Kind: LaneCommandCommit, Writes: writes, Next: &nextState,
				Materialize: decision.Materialize, Events: decision.Events,
				Result: decision.Result,
			}
		case OperationCommandFinish:
			inbox := state.Inbox
			if decision.HasInbox {
				inbox = decision.Inbox
			}
			writes := append([]session.Write{}, decision.Writes...)
			writes = append(writes, session.WriteFromValue(session.SetValue(
				session.OperationResult(operation.Meta.OperationID),
				operationResultToJSON(decision.Record),
			)))
			writes = append(writes, session.WriteFromValue(session.SetValue(
				session.LaneStateValue(l.Name),
				DurableLaneStateJSON(nil, &operation.Meta.OperationID, inbox),
			)))
			nextState := *state
			nextState.Inbox = inbox
			lastID := operation.Meta.OperationID
			nextState.LastOperationID = &lastID
			nextState.Operation = nil
			return &LaneCommand{
				Kind: LaneCommandCommit, Writes: writes, Next: &nextState,
				Materialize: decision.Materialize, Events: decision.Events,
				Result: decision.Result,
			}
		}
		return &LaneCommand{Kind: LaneCommandReject, Error: fmt.Errorf("unknown operation command kind")}
	}, ctx)
}

// ContinueOperation runs an ordinary command only while durable control is
// running; cancel_requested short-circuits the planner. Commit and finish
// results are wrapped into the result union like the TS materialize does.
func (l *Lane) ContinueOperation(plan func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand, ctx harness.Context) (*ContinueOperationResult, error) {
	result, err := l.SettleOperation(func(state *RuntimeLaneState, current *session.OperationState, meta *session.OperationMeta, reader session.SessionReader) *OperationCommand {
		if current.Control.Status == "cancel_requested" {
			return &OperationCommand{Kind: OperationCommandReturn, Result: &ContinueOperationResult{Kind: "cancel_requested"}}
		}
		decision := plan(state, current, meta, reader)
		switch decision.Kind {
		case OperationCommandReturn:
			return &OperationCommand{Kind: OperationCommandReturn, Result: &ContinueOperationResult{Kind: "result", Value: decision.Result, HasValue: true}}
		case OperationCommandCommit, OperationCommandFinish:
			wrapped := *decision
			wrapped.Result = &ContinueOperationResult{Kind: "result", Value: decision.Result, HasValue: true}
			return &wrapped
		}
		return decision
	}, ctx)
	if err != nil {
		return nil, err
	}
	return result.(*ContinueOperationResult), nil
}

// eventsSink is set by the facade (single-lane tests).
// TODO: thread the event bus through the facade.

// LaneConfigJSONForTest renders a minimal valid lane config for tests.
func LaneConfigJSONForTest() *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("model", jsonx.ObjFrom("provider", "faux", "modelId", "faux-1"))
	obj.Set("thinkingLevel", "off")
	obj.Set("activeToolNames", []any{})
	return obj
}
