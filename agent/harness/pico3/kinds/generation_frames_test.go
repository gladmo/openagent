package kinds

// Ports of the stream coalescing policy.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func TestFrameBatchSizeFlush(t *testing.T) {
	batch := NewFrameBatch(1000)
	// Small frames accumulate without flushing.
	for i := 0; i < 10; i++ {
		size := batch.Offer(jsonx.ObjFrom("type", "text_delta", "delta", strings.Repeat("x", 100)))
		if batch.ShouldFlush(1001) {
			t.Fatalf("flushed at %d bytes", size)
		}
	}
	// A large frame crosses the 64KB bound.
	batch.Offer(jsonx.ObjFrom("type", "text_delta", "delta", strings.Repeat("y", 70*1024)))
	if !batch.ShouldFlush(1001) {
		t.Fatal("size bound not triggered")
	}
}

func TestFrameBatchTimeFlush(t *testing.T) {
	batch := NewFrameBatch(1000)
	batch.Offer(jsonx.ObjFrom("type", "text_start"))
	// Before the 100ms bound: no flush.
	if batch.ShouldFlush(1099) {
		t.Fatal("flushed before time bound")
	}
	// At the bound: flush.
	if !batch.ShouldFlush(1100) {
		t.Fatal("time bound not triggered")
	}
	// Empty batch never flushes.
	empty := NewFrameBatch(1000)
	if empty.ShouldFlush(9999) {
		t.Fatal("empty batch flushed")
	}
}

func TestFrameBatchTake(t *testing.T) {
	batch := NewFrameBatch(1000)
	batch.Offer(jsonx.ObjFrom("type", "text_start"))
	batch.Offer(jsonx.ObjFrom("type", "text_delta", "delta", "hi"))
	drained := batch.Take(1200)
	if len(drained) != 2 {
		t.Fatalf("drained = %d", len(drained))
	}
	if !batch.FlushedContent {
		t.Fatal("delta batch did not mark flushed content")
	}
	if batch.Pending() != 0 {
		t.Fatal("batch not drained")
	}
	// Empty take returns nil.
	if batch.Take(1300) != nil {
		t.Fatal("empty take returned frames")
	}
	// Non-delta batch does not mark.
	batch2 := NewFrameBatch(0)
	batch2.Offer(jsonx.ObjFrom("type", "text_start"))
	batch2.Take(10)
	if batch2.FlushedContent {
		t.Fatal("non-delta batch marked content")
	}
	// lastFlush advanced.
	if batch2.lastFlush != 10 {
		t.Fatal("lastFlush not advanced")
	}
}

func TestClassifyDoneEvent(t *testing.T) {
	// Deferred reason with a handle.
	deferredMessage := jsonx.ObjFrom("deferred", jsonx.ObjFrom("provider", "p", "id", "d1"))
	event := jsonx.ObjFrom("type", "done", "reason", "deferred", "message", deferredMessage)
	result := ClassifyDoneEvent(event)
	if result.Terminal != nil || result.Deferred == nil {
		t.Fatalf("result = %+v", result)
	}
	// Deferred reason without a handle: terminal.
	event = jsonx.ObjFrom("type", "done", "reason", "deferred", "message", jsonx.ObjFrom("role", "assistant"))
	result = ClassifyDoneEvent(event)
	if result.Terminal == nil || result.Deferred != nil {
		t.Fatalf("result = %+v", result)
	}
	// Normal done: terminal message.
	event = jsonx.ObjFrom("type", "done", "reason", "stop", "message", jsonx.ObjFrom("role", "assistant"))
	result = ClassifyDoneEvent(event)
	if result.Terminal == nil || result.Deferred != nil {
		t.Fatalf("result = %+v", result)
	}
}
