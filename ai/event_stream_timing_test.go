package ai

// Port of pi-ai test/event-stream.test.ts "AssistantMessageEventStream
// timing" block (#10549).

import (
	"testing"
	"time"
)

func timingMessage(timestamp float64, durationMs ...float64) *AssistantMessage {
	message := FauxAssistantMessage("")
	message.TimestampMs = timestamp
	if len(durationMs) > 0 {
		message.DurationMs = &durationMs[0]
	}
	return message
}

func TestStreamTimesFinalMessage(t *testing.T) {
	done := NewAssistantMessageEventStream()
	answer := timingMessage(float64(time.Now().UnixMilli()))
	time.Sleep(20 * time.Millisecond)
	done.Push(&EventDone{Reason: StopStop, Message: answer})
	if answer.DurationMs == nil || *answer.DurationMs < 15 {
		t.Fatalf("duration = %v", answer.DurationMs)
	}
	if result := done.Result(); result.DurationMs == nil || *result.DurationMs != *answer.DurationMs {
		t.Fatalf("result duration = %v", result.DurationMs)
	}

	failed := NewAssistantMessageEventStream()
	errorMessage := timingMessage(float64(time.Now().UnixMilli()))
	errorMessage.StopReason = StopError
	failed.Push(&EventError{Reason: StopError, Error: errorMessage})
	if errorMessage.DurationMs == nil {
		t.Fatal("error message not timed")
	}

	ended := NewAssistantMessageEventStream()
	result := timingMessage(float64(time.Now().UnixMilli()))
	ended.End(result)
	if result.DurationMs == nil {
		t.Fatal("ended result not timed")
	}
}

func TestStreamKeepsExistingDuration(t *testing.T) {
	outer := NewAssistantMessageEventStream()
	time.Sleep(20 * time.Millisecond)
	inner := NewAssistantMessageEventStream()
	answer := timingMessage(float64(time.Now().UnixMilli()))
	inner.Push(&EventDone{Reason: StopStop, Message: answer})
	if answer.DurationMs == nil {
		t.Fatal("inner measurement missing")
	}
	measured := *answer.DurationMs
	outer.Push(&EventDone{Reason: StopStop, Message: answer})
	if *answer.DurationMs != measured {
		t.Fatalf("duration = %v, want %v", *answer.DurationMs, measured)
	}
	if measured >= 20 {
		t.Fatalf("inner measured from its own start: %v", measured)
	}

	preset := timingMessage(float64(time.Now().UnixMilli()), 1234)
	NewAssistantMessageEventStream().Push(&EventDone{Reason: StopStop, Message: preset})
	if preset.DurationMs == nil || *preset.DurationMs != 1234 {
		t.Fatalf("preset duration = %v", preset.DurationMs)
	}
}

func TestStreamLeavesPreStreamMessagesUntimed(t *testing.T) {
	fetched := timingMessage(float64(time.Now().UnixMilli() - 60_000))
	NewAssistantMessageEventStream().Push(&EventDone{Reason: StopStop, Message: fetched})
	if fetched.DurationMs != nil {
		t.Fatalf("fetched duration = %v", *fetched.DurationMs)
	}
}

func TestStreamDoesNotTimeAfterCompletion(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	stream.Push(&EventDone{Reason: StopStop, Message: timingMessage(float64(time.Now().UnixMilli()))})
	late := timingMessage(float64(time.Now().UnixMilli()))
	stream.Push(&EventDone{Reason: StopStop, Message: late})
	if late.DurationMs != nil {
		t.Fatalf("late duration = %v", *late.DurationMs)
	}
}
