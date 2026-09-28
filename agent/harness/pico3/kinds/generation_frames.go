package kinds

// generation_frames.go ports harness/pico3/kinds/generation.ts's stream
// coalescing policy: frames batch until size or time, then flush applies
// them to the turn view; delta-bearing batches mark content as flushed.

import (
	"github.com/gladmo/openagent/jsonx"
)

// FrameBatch is the coalescing buffer.
type FrameBatch struct {
	pending        []*jsonx.Obj
	pendingBytes   int
	lastFlush      float64
	FlushedContent bool
}

// NewFrameBatch builds a batch starting at now.
func NewFrameBatch(now float64) *FrameBatch {
	return &FrameBatch{lastFlush: now}
}

// Offer appends one encoded frame and reports the batch size in bytes.
func (b *FrameBatch) Offer(frame *jsonx.Obj) int {
	b.pending = append(b.pending, frame)
	b.pendingBytes += len(jsonx.Stringify(frame))
	return b.pendingBytes
}

// ShouldFlush reports whether the batch must flush: the 64KB size bound
// or the 100ms time bound since lastFlush.
func (b *FrameBatch) ShouldFlush(now float64) bool {
	if len(b.pending) == 0 {
		return false
	}
	if b.pendingBytes >= 64*1024 {
		return true
	}
	return now >= b.lastFlush+100
}

// Take drains the batch: empty batches return nil; delta-bearing batches
// mark FlushedContent.
func (b *FrameBatch) Take(now float64) []*jsonx.Obj {
	if len(b.pending) == 0 {
		return nil
	}
	batch := b.pending
	b.pending = nil
	b.pendingBytes = 0
	b.lastFlush = now
	for _, frame := range batch {
		if _, hasDelta := frame.Get("delta"); hasDelta {
			b.FlushedContent = true
			break
		}
	}
	return batch
}

// Pending reports the buffered frame count.
func (b *FrameBatch) Pending() int { return len(b.pending) }

// TerminalResult captures the stream outcome.
type TerminalResult struct {
	Terminal *jsonx.Obj
	Deferred *jsonx.Obj
}

// ClassifyDoneEvent mirrors the done-event split: reason deferred carries
// the deferred handle; anything else is the terminal message.
func ClassifyDoneEvent(event *jsonx.Obj) *TerminalResult {
	reason, _ := event.Get("reason")
	messageValue, _ := event.Get("message")
	message, _ := messageValue.(*jsonx.Obj)
	if reason == "deferred" && message != nil {
		if deferredValue, ok := message.Get("deferred"); ok {
			if deferred, ok := deferredValue.(*jsonx.Obj); ok && deferred != nil {
				return &TerminalResult{Deferred: deferred}
			}
		}
	}
	return &TerminalResult{Terminal: message}
}
