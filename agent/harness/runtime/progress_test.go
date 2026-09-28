package runtime

// Ports of progress.test.ts behaviors (representative cases).

import (
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func newProgressEnv(t *testing.T) session.Session {
	t.Helper()
	storage := session.NewMemoryStorage()
	metadata := session.SessionMetadata{ID: "s1", StorageVersion: 1}
	return session.NewStorageBackedSession(metadata, storage)
}

func TestReadAssistantFramesPagination(t *testing.T) {
	sess := newProgressEnv(t)
	ctx := harnessBackground()
	address := session.PendingAssistantFrames("op-1", "resp-1")

	// Seed 1500 frames: forces two pages.
	for i := 0; i < 1500; i++ {
		frame := jsonx.NewObj()
		frame.Set("index", float64(i))
		if err := sess.AppendList(address, frame, ctx); err != nil {
			t.Fatal(err)
		}
	}
	frames, err := ReadAssistantFrames(sess, "op-1", "resp-1", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1500 {
		t.Fatalf("frames = %d", len(frames))
	}
	// Asc order preserved across pages.
	if frames[0].MustGet("index") != float64(0) || frames[1499].MustGet("index") != float64(1499) {
		t.Fatal("order broken")
	}
	// Empty list returns empty.
	frames, err = ReadAssistantFrames(sess, "op-1", "resp-other", ctx)
	if err != nil || len(frames) != 0 {
		t.Fatalf("frames = %d err = %v", len(frames), err)
	}
}

func effectPendingOperation(responseEntryID string) *session.Operation {
	return &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-1", Lane: "main"},
		State: session.OperationState{At: session.AtAssistantEffectPending, ResponseEntryID: responseEntryID},
	}
}

func TestFrameStillOwns(t *testing.T) {
	guard := FrameStillOwns("resp-1")
	// Matching assistant.effect_pending.
	state := &RuntimeLaneState{Operation: effectPendingOperation("resp-1")}
	if !guard(state) {
		t.Fatal("matching state rejected")
	}
	// Different response entry.
	if guard(&RuntimeLaneState{Operation: effectPendingOperation("resp-2")}) {
		t.Fatal("wrong response accepted")
	}
	// Deferred effect_pending also qualifies.
	deferred := effectPendingOperation("resp-1")
	deferred.State.At = session.AtDeferredEffectPending
	if !guard(&RuntimeLaneState{Operation: deferred}) {
		t.Fatal("deferred state rejected")
	}
	// Other leaves reject.
	other := effectPendingOperation("resp-1")
	other.State.At = session.AtTools
	if guard(&RuntimeLaneState{Operation: other}) {
		t.Fatal("tools state accepted")
	}
	// No operation rejects.
	if guard(&RuntimeLaneState{}) {
		t.Fatal("nil operation accepted")
	}
}

func toolsOperation(turnID string, calls ...*jsonx.Obj) *session.Operation {
	batch := jsonx.NewObj()
	batch.Set("turnId", turnID)
	items := make([]any, 0, len(calls))
	for _, call := range calls {
		items = append(items, call)
	}
	batch.Set("calls", items)
	return &session.Operation{
		Meta:  session.OperationMeta{OperationID: "op-1", Lane: "main"},
		State: session.OperationState{At: session.AtTools, Batch: batch},
	}
}

func callObj(sourceIndex int64, resultEntryID, status string) *jsonx.Obj {
	return jsonx.ObjFrom(
		"sourceIndex", float64(sourceIndex),
		"resultEntryId", resultEntryID,
		"status", status,
	)
}

func TestToolStillOwns(t *testing.T) {
	guard := ToolStillOwns("turn-1", 2, "inv-9")
	// Matching effect_pending call.
	state := &RuntimeLaneState{Operation: toolsOperation("turn-1",
		callObj(0, "inv-1", "planned"),
		callObj(2, "inv-9", "effect_pending"),
	)}
	if !guard(state) {
		t.Fatal("matching call rejected")
	}
	// Wrong turn.
	if guard(&RuntimeLaneState{Operation: toolsOperation("turn-2", callObj(2, "inv-9", "effect_pending"))}) {
		t.Fatal("wrong turn accepted")
	}
	// Wrong source index.
	if guard(&RuntimeLaneState{Operation: toolsOperation("turn-1", callObj(1, "inv-9", "effect_pending"))}) {
		t.Fatal("wrong source index accepted")
	}
	// Wrong invocation.
	if guard(&RuntimeLaneState{Operation: toolsOperation("turn-1", callObj(2, "inv-8", "effect_pending"))}) {
		t.Fatal("wrong invocation accepted")
	}
	// Status not effect_pending.
	if guard(&RuntimeLaneState{Operation: toolsOperation("turn-1", callObj(2, "inv-9", "completed"))}) {
		t.Fatal("completed call accepted")
	}
	// Non-tools leaf.
	gen := effectPendingOperation("resp-1")
	if guard(&RuntimeLaneState{Operation: gen}) {
		t.Fatal("non-tools state accepted")
	}
}

func TestOpenProgressWriteDropsWhenOwnershipLost(t *testing.T) {
	sess := newProgressEnv(t)
	ctx := harnessBackground()
	tip := "tip-1"
	// Lane with an operation that does NOT match the guard.
	lane := NewLane(LaneOptions{
		Name:    "main",
		Session: sess,
		State: &RuntimeLaneState{
			TipID:     &tip,
			Operation: effectPendingOperation("resp-OTHER"),
		},
	})
	drive := &Drive{OperationID: "op-1", Context: ctx}
	address := session.SessionName
	channel := OpenProgress(lane, drive, func(item any) session.Write {
		return session.WriteFromValue(session.SetValue(address, item))
	}, FrameStillOwns("resp-1"))

	channel.Write("dropped")
	channel.Seal()
	channel.Drain()

	stored, _ := sess.GetValue(address, ctx)
	if stored != nil {
		t.Fatalf("write landed without ownership: %+v", stored)
	}
}

func TestOpenProgressWriteCommitsWhenOwned(t *testing.T) {
	sess := newProgressEnv(t)
	ctx := harnessBackground()
	tip := "tip-1"
	// Lane whose operation matches the guard.
	lane := NewLane(LaneOptions{
		Name:    "main",
		Session: sess,
		State: &RuntimeLaneState{
			TipID:     &tip,
			Operation: effectPendingOperation("resp-1"),
		},
	})
	drive := &Drive{OperationID: "op-1", Context: ctx}
	address := session.SessionName
	channel := OpenProgress(lane, drive, func(item any) session.Write {
		return session.WriteFromValue(session.SetValue(address, item))
	}, FrameStillOwns("resp-1"))

	channel.Write("landed")
	channel.Seal()
	channel.Drain()

	stored, _ := sess.GetValue(address, ctx)
	if stored == nil || stored.Value != "landed" {
		t.Fatalf("stored = %+v", stored)
	}

	// Writes after seal are dropped.
	channel.Write("sealed-out")
	stored, _ = sess.GetValue(address, ctx)
	if stored.Value != "landed" {
		t.Fatalf("post-seal write landed: %+v", stored)
	}
}

func TestOpenToolProgressAppendsFramesAndSetsOutput(t *testing.T) {
	sess := newProgressEnv(t)
	ctx := harnessBackground()
	tip := "tip-1"
	lane := NewLane(LaneOptions{
		Name:    "main",
		Session: sess,
		State: &RuntimeLaneState{
			TipID: &tip,
			Operation: toolsOperation("turn-1",
				callObj(0, "inv-1", "effect_pending"),
			),
		},
	})
	drive := &Drive{OperationID: "op-1", Context: ctx}

	// Tool progress writes pi.pending.tool_output.
	toolChannel := OpenToolProgress(lane, drive, "turn-1", 0, "inv-1")
	toolChannel.Write(jsonx.MustParseString(`{"content":[]}`))
	toolChannel.Seal()
	toolChannel.Drain()

	stored, _ := sess.GetValue(session.PendingToolOutput("op-1", "inv-1"), ctx)
	if stored == nil {
		t.Fatal("tool output not stored")
	}

	// Frame progress (no matching operation) drops its write.
	frameChannel := OpenFrameProgress(lane, drive, "resp-1")
	frameChannel.Write(jsonx.MustParseString(`{"content":[]}`))
	frameChannel.Seal()
	frameChannel.Drain()

	frames, _ := sess.ReadList(session.PendingAssistantFrames("op-1", "resp-1"), nil, ctx)
	if len(frames) != 0 {
		t.Fatalf("frames = %d", len(frames))
	}
}
