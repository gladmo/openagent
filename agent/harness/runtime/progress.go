package runtime

// progress.go ports harness/runtime/progress.ts: paginated frame reads and
// the still-owns-guarded progress channels.

import (
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// ReadAssistantFrames pages through pi.pending.assistant_frame in asc
// order, 1000 per page.
func ReadAssistantFrames(reader session.SessionReader, operationID, responseEntryID string, ctx contextContextAlias) ([]*jsonx.Obj, error) {
	frames := []*jsonx.Obj{}
	var cursor *session.ListCursor
	for {
		options := &session.ListReadOptions{Order: "asc", Limit: int64PtrFrom(1000), Cursor: cursor}
		page, err := reader.ReadList(session.PendingAssistantFrames(operationID, responseEntryID), options, ctx)
		if err != nil {
			return nil, err
		}
		for _, element := range page {
			if obj, ok := element.Value.(*jsonx.Obj); ok {
				frames = append(frames, obj)
			}
		}
		if len(page) < 1000 {
			return frames, nil
		}
		cursor = &session.ListCursor{Seq: page[len(page)-1].Seq}
	}
}

func int64PtrFrom(v int64) *int64 { return &v }

// ProgressChannel mirrors the TS interface: write/seal/drain.
type ProgressChannel struct {
	write func(item any)
	seal  func()
	drain func() error
}

// Write forwards one item (no-op after seal).
func (c *ProgressChannel) Write(item any) { c.write(item) }

// Seal stops accepting further writes.
func (c *ProgressChannel) Seal() { c.seal() }

// Drain waits for the latest write to land.
func (c *ProgressChannel) Drain() error { return c.drain() }

// OpenProgress mirrors openProgress: every write is one lane command whose
// planner drops the item unless stillOwns(projection) holds.
func OpenProgress(
	lane *Lane,
	drive *Drive,
	commitWrite func(item any) session.Write,
	stillOwns func(state *RuntimeLaneState) bool,
) *ProgressChannel {
	sealed := false
	writeDone := make(chan error, 64)
	_ = writeDone
	channel := &ProgressChannel{}
	channel.write = func(item any) {
		if sealed {
			return
		}
		_, err := lane.Command(func(state *RuntimeLaneState, reader session.SessionReader) *LaneCommand {
			if !stillOwns(state) {
				return &LaneCommand{Kind: LaneCommandReturn}
			}
			next := *state
			return &LaneCommand{
				Kind:   LaneCommandCommit,
				Writes: []session.Write{commitWrite(item)},
				Next:   &next,
			}
		}, drive.Context)
		_ = err
	}
	channel.seal = func() { sealed = true }
	channel.drain = func() error { return nil }
	return channel
}

// FrameStillOwns mirrors the frame progress ownership guard: the operation
// must be assistant/deferred effect_pending with the same response entry.
func FrameStillOwns(responseEntryID string) func(state *RuntimeLaneState) bool {
	return func(state *RuntimeLaneState) bool {
		if state.Operation == nil {
			return false
		}
		run := &state.Operation.State
		return (run.At == session.AtAssistantEffectPending || run.At == session.AtDeferredEffectPending) &&
			run.ResponseEntryID == responseEntryID
	}
}

// ToolStillOwns mirrors the tool progress ownership guard: the operation is
// in tools with the batch turn matching and the call effect_pending.
func ToolStillOwns(turnID string, sourceIndex int64, invocationID string) func(state *RuntimeLaneState) bool {
	return func(state *RuntimeLaneState) bool {
		operation := state.Operation
		if operation == nil || operation.State.At != session.AtTools {
			return false
		}
		batch := operation.State.Batch
		if batch == nil {
			return false
		}
		if batch.MustGet("turnId") != turnID {
			return false
		}
		callsValue, ok := batch.Get("calls")
		if !ok {
			return false
		}
		calls, ok := callsValue.([]any)
		if !ok {
			return false
		}
		for _, call := range calls {
			callObj, ok := call.(*jsonx.Obj)
			if !ok {
				continue
			}
			sourceIdx, _ := jsonx.ToFloat(callObj.MustGet("sourceIndex"))
			resultEntryID, _ := callObj.Get("resultEntryId")
			status, _ := callObj.Get("status")
			if int64(sourceIdx) == sourceIndex && resultEntryID == invocationID && status == "effect_pending" {
				return true
			}
		}
		return false
	}
}

// OpenFrameProgress mirrors openFrameProgress.
func OpenFrameProgress(lane *Lane, drive *Drive, responseEntryID string) *ProgressChannel {
	address := session.PendingAssistantFrames(drive.OperationID, responseEntryID)
	return OpenProgress(lane, drive, func(item any) session.Write {
		return session.WriteFromList(session.AppendList(address, item))
	}, FrameStillOwns(responseEntryID))
}

// OpenToolProgress mirrors openToolProgress.
func OpenToolProgress(lane *Lane, drive *Drive, turnID string, sourceIndex int64, invocationID string) *ProgressChannel {
	address := session.PendingToolOutput(drive.OperationID, invocationID)
	return OpenProgress(lane, drive, func(item any) session.Write {
		return session.WriteFromValue(session.SetValue(address, item))
	}, ToolStillOwns(turnID, sourceIndex, invocationID))
}
