package runtime

// Ports of runtime/reducer.test.ts (representative cases).

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func event(typ string, pairs ...any) HarnessEvent {
	obj := jsonx.NewObj()
	obj.Set("type", typ)
	obj.Set("lane", "main")
	for i := 0; i+1 < len(pairs); i += 2 {
		obj.Set(pairs[i].(string), pairs[i+1])
	}
	return obj
}

func newSnapshot() *LaneSnapshot {
	tip := "tip-0"
	return &LaneSnapshot{
		Lane:          "main",
		TipID:         &tip,
		Configuration: laneConfigOf("prov", "model", "low"),
	}
}

func laneConfigOf(provider, model, thinking string) laneConfigAlias {
	return laneConfigAlias{
		Model:           laneModelAlias{Provider: provider, ModelID: model},
		ThinkingLevel:   thinking,
		ActiveToolNames: []string{"read"},
	}
}

func TestReducerOperationLifecycle(t *testing.T) {
	snapshot := newSnapshot()
	ReduceLaneSnapshot(snapshot, event("run_start", "runId", "run-1", "startedAt", float64(10)))
	if snapshot.Operation == nil || snapshot.Operation.Kind != "run" || snapshot.Operation.ID != "run-1" {
		t.Fatalf("operation = %+v", snapshot.Operation)
	}
	// compaction_start while an operation is open is ignored.
	ReduceLaneSnapshot(snapshot, event("compaction_start", "runId", "c-1"))
	if snapshot.Operation.Kind != "run" {
		t.Fatal("compaction overrode open run")
	}
	// Abort flips status.
	ReduceLaneSnapshot(snapshot, event("operation_abort", "operationId", "run-1"))
	if snapshot.Operation.Status != "aborting" {
		t.Fatalf("status = %s", snapshot.Operation.Status)
	}
	// Foreign operation ids ignored.
	ReduceLaneSnapshot(snapshot, event("operation_abort", "operationId", "other"))
	if snapshot.Operation.Status != "aborting" {
		t.Fatal("foreign abort applied")
	}
	// run_end records lastResult and clears the operation.
	ReduceLaneSnapshot(snapshot, event("run_end", "runId", "run-1", "status", "completed", "fromTipId", "tip-0", "tipId", "tip-9", "endedAt", float64(20)))
	if snapshot.Operation != nil {
		t.Fatal("operation not cleared")
	}
	if snapshot.LastResult == nil || snapshot.LastResult.Status != "completed" || *snapshot.LastResult.TipID != "tip-9" {
		t.Fatalf("lastResult = %+v", snapshot.LastResult)
	}
}

func TestReducerCompactionOpenOnlyWhenIdle(t *testing.T) {
	idle := newSnapshot()
	ReduceLaneSnapshot(idle, event("compaction_start", "runId", "c-1", "startedAt", float64(1)))
	if idle.Operation == nil || idle.Operation.Kind != "compaction" {
		t.Fatal("idle compaction not opened")
	}
	ReduceLaneSnapshot(idle, event("compaction_end", "runId", "c-1", "status", "declined", "endedAt", float64(2)))
	if idle.Operation != nil || idle.LastResult.Status != "declined" {
		t.Fatalf("lastResult = %+v", idle.LastResult)
	}
}

func TestReducerStreamingAndTools(t *testing.T) {
	snapshot := newSnapshot()
	ReduceLaneSnapshot(snapshot, event("run_start", "runId", "r", "startedAt", float64(0)))

	// Only pending assistant message_start sets streaming.
	ReduceLaneSnapshot(snapshot, event("message_start", "runId", "r",
		"message", jsonx.MustParseString(`{"role":"assistant","stopReason":"pending","content":[]}`)))
	if snapshot.Operation.StreamingMessage == nil {
		t.Fatal("streaming not set")
	}
	ReduceLaneSnapshot(snapshot, event("message_update", "runId", "r",
		"message", jsonx.MustParseString(`{"role":"assistant","stopReason":"pending","content":[{"type":"text","text":"x"}]}`)))
	if snapshot.Operation.StreamingMessage == nil {
		t.Fatal("update lost streaming")
	}
	ReduceLaneSnapshot(snapshot, event("message_end", "runId", "r", "message", nil))
	if snapshot.Operation.StreamingMessage != nil {
		t.Fatal("end did not clear streaming")
	}

	// Tool lifecycle: start upserts, update fills result while running,
	// end settles.
	ReduceLaneSnapshot(snapshot, event("tool_start", "runId", "r", "toolCallId", "t1", "toolName", "read", "args", jsonx.NewObj()))
	if len(snapshot.Operation.RunningTools) != 1 || snapshot.Operation.RunningTools[0].Status != "running" {
		t.Fatalf("tools = %+v", snapshot.Operation.RunningTools)
	}
	ReduceLaneSnapshot(snapshot, event("tool_update", "runId", "r", "toolCallId", "t1", "partialResult", "partial"))
	if snapshot.Operation.RunningTools[0].Result != "partial" {
		t.Fatalf("result = %v", snapshot.Operation.RunningTools[0].Result)
	}
	ReduceLaneSnapshot(snapshot, event("tool_end", "runId", "r", "toolCallId", "t1", "toolName", "read", "result", "final", "isError", false))
	if snapshot.Operation.RunningTools[0].Status != "settled" || snapshot.Operation.RunningTools[0].Result != "final" {
		t.Fatalf("tools = %+v", snapshot.Operation.RunningTools)
	}

	// toolResult entry_added removes the settled tool.
	ReduceLaneSnapshot(snapshot, event("entry_added", "entry", jsonx.MustParseString(`{"id":"tr-1","parentId":null,"type":"message","message":{"role":"toolResult","toolCallId":"t1"}}`)))
	if len(snapshot.Operation.RunningTools) != 0 {
		t.Fatalf("tools after entry = %+v", snapshot.Operation.RunningTools)
	}
	if len(snapshot.Transcript) != 1 || *snapshot.TipID != "tr-1" || snapshot.Stats.MessageCount != 1 {
		t.Fatalf("transcript = %d tip = %v count = %d", len(snapshot.Transcript), snapshot.TipID, snapshot.Stats.MessageCount)
	}
}

func TestReducerCompactionEntryReplacesTranscript(t *testing.T) {
	snapshot := newSnapshot()
	ReduceLaneSnapshot(snapshot, event("run_start", "runId", "r", "startedAt", float64(0)))
	ReduceLaneSnapshot(snapshot, event("entry_added", "entry", jsonx.MustParseString(`{"id":"m1","type":"message","message":{"role":"user"}}`)))
	ReduceLaneSnapshot(snapshot, event("entry_added", "entry", jsonx.MustParseString(`{"id":"c-1","type":"compaction","summary":"s"}`)))
	if len(snapshot.Transcript) != 1 || snapshot.Transcript[0].ID != "c-1" {
		t.Fatalf("transcript = %+v", snapshot.Transcript)
	}
	// Compaction entries do not bump messageCount.
	if snapshot.Stats.MessageCount != 1 {
		t.Fatalf("count = %d", snapshot.Stats.MessageCount)
	}
}

func TestReducerDeferredAndRetry(t *testing.T) {
	snapshot := newSnapshot()
	ReduceLaneSnapshot(snapshot, event("run_start", "runId", "r", "startedAt", float64(0)))
	ReduceLaneSnapshot(snapshot, event("run_suspend", "runId", "r",
		"deferred", jsonx.MustParseString(`{"provider":"p","id":"d1"}`), "poll", float64(2)))
	if snapshot.Operation.Deferred == nil || snapshot.Operation.Deferred.Poll != 2 {
		t.Fatalf("deferred = %+v", snapshot.Operation.Deferred)
	}
	ReduceLaneSnapshot(snapshot, event("run_resume", "runId", "r"))
	if snapshot.Operation.Deferred != nil {
		t.Fatal("resume did not clear deferred")
	}
	ReduceLaneSnapshot(snapshot, event("retry_scheduled", "runId", "r", "attempt", float64(1), "maxAttempts", float64(4), "notBefore", float64(99)))
	if snapshot.Operation.Retry == nil || snapshot.Operation.Retry.NextAttemptAt != 99 {
		t.Fatalf("retry = %+v", snapshot.Operation.Retry)
	}
	ReduceLaneSnapshot(snapshot, event("retry_start", "runId", "r"))
	if snapshot.Operation.Retry != nil {
		t.Fatal("retry_start did not clear retry")
	}
}

func TestReducerForeignLaneIgnored(t *testing.T) {
	snapshot := newSnapshot()
	foreign := event("run_start", "runId", "r", "startedAt", float64(0))
	foreign.Set("lane", "other")
	ReduceLaneSnapshot(snapshot, foreign)
	if snapshot.Operation != nil {
		t.Fatal("foreign lane event applied")
	}
	// usage is the exception: cross-lane totals apply.
	usageEvent := event("usage", "totals", jsonx.MustParseString(`{"input":5,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":7}`))
	usageEvent.Set("lane", "other")
	ReduceLaneSnapshot(snapshot, usageEvent)
	if snapshot.Stats.Usage.Input != 5 || snapshot.Stats.Usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v", snapshot.Stats.Usage)
	}
}

func TestReducerConfigUpdate(t *testing.T) {
	snapshot := newSnapshot()
	ReduceLaneSnapshot(snapshot, event("config_update", "property", "model",
		"value", jsonx.MustParseString(`{"provider":"p2","modelId":"m2"}`)))
	if snapshot.Configuration.Model.Provider != "p2" || snapshot.Configuration.Model.ModelID != "m2" {
		t.Fatalf("model = %+v", snapshot.Configuration.Model)
	}
	ReduceLaneSnapshot(snapshot, event("config_update", "property", "thinkingLevel", "value", "high"))
	if snapshot.Configuration.ThinkingLevel != "high" {
		t.Fatal("thinkingLevel")
	}
	ReduceLaneSnapshot(snapshot, event("config_update", "property", "activeTools", "value", []any{"write"}))
	if len(snapshot.Configuration.ActiveToolNames) != 1 || snapshot.Configuration.ActiveToolNames[0] != "write" {
		t.Fatalf("tools = %v", snapshot.Configuration.ActiveToolNames)
	}
	// Foreign-lane config_update ignored.
	foreign := event("config_update", "property", "thinkingLevel", "value", "off")
	foreign.Set("lane", "other")
	ReduceLaneSnapshot(snapshot, foreign)
	if snapshot.Configuration.ThinkingLevel != "high" {
		t.Fatal("foreign config applied")
	}
}

func TestReducerNavigationEndRebases(t *testing.T) {
	snapshot := newSnapshot()
	if got := ReduceLaneSnapshot(snapshot, event("navigation_end", "runId", "n")); got != "rebase" {
		t.Fatalf("got %q", got)
	}
}

func TestReducerFaultAndNoOps(t *testing.T) {
	snapshot := newSnapshot()
	ReduceLaneSnapshot(snapshot, event("fault"))
	if !snapshot.Faulted {
		t.Fatal("fault not recorded")
	}
	// No-op event types leave the snapshot untouched.
	for _, typ := range []string{"handler_error", "turn_start", "turn_end", "value_update", "lane_created"} {
		ReduceLaneSnapshot(snapshot, event(typ))
	}
	if !snapshot.Faulted || snapshot.Operation != nil {
		t.Fatal("no-op events mutated state")
	}
}

func TestReducerRunEndFailedCarriesError(t *testing.T) {
	snapshot := newSnapshot()
	ReduceLaneSnapshot(snapshot, event("run_start", "runId", "r", "startedAt", float64(1)))
	ReduceLaneSnapshot(snapshot, event("run_end", "runId", "r", "status", "failed",
		"error", jsonx.MustParseString(`{"code":"boom","message":"it broke"}`),
		"fromTipId", "t0", "tipId", "t1", "endedAt", float64(2)))
	if snapshot.LastResult.Status != "failed" || snapshot.LastResult.Error == nil || snapshot.LastResult.Error.Code != "boom" {
		t.Fatalf("lastResult = %+v", snapshot.LastResult)
	}
}
