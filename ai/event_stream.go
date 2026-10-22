package ai

import (
	"sync"
	"time"
)

// EventStream ports utils/event-stream.ts: a push/pull stream with no
// backpressure, duplicate pushes silently dropped after the terminal event,
// and a single-shot Result that resolves on the first complete event and
// never rejects.
type EventStream[T any, R any] struct {
	mu         sync.Mutex
	cond       *sync.Cond
	queue      []T
	done       bool
	isComplete func(T) bool
	extract    func(T) R
	result     chan R
}

// NewEventStream creates a stream with the completeness predicate and result
// extractor.
func NewEventStream[T any, R any](isComplete func(T) bool, extract func(T) R) *EventStream[T, R] {
	s := &EventStream[T, R]{
		isComplete: isComplete,
		extract:    extract,
		result:     make(chan R, 1),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Push delivers an event to a waiting consumer or enqueues it. Pushes after
// the terminal event are dropped.
func (s *EventStream[T, R]) Push(event T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	if s.isComplete(event) {
		s.done = true
		s.result <- s.extract(event)
	}
	s.queue = append(s.queue, event)
	s.cond.Signal()
}

// End marks the stream done and wakes all waiting consumers. An optional
// final result resolves Result() if it has not resolved yet (mirroring the
// optional end(result)).
func (s *EventStream[T, R]) End(result ...R) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	if len(result) > 0 {
		select {
		case s.result <- result[0]:
		default:
		}
	}
	s.cond.Broadcast()
}

// Done reports whether the terminal event has been pushed or End was called.
func (s *EventStream[T, R]) Done() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// Next is the pull side of the async iterator. It blocks until an event is
// available; ok is false once the stream is done and drained.
func (s *EventStream[T, R]) Next() (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if len(s.queue) > 0 {
			event := s.queue[0]
			s.queue = s.queue[1:]
			return event, true
		}
		if s.done {
			var zero T
			return zero, false
		}
		s.cond.Wait()
	}
}

// ResultChan returns the single-shot result channel: it receives exactly one
// value when the first complete event is pushed (or End provides one), and
// never receives otherwise (a TS promise that never resolves).
func (s *EventStream[T, R]) ResultChan() <-chan R { return s.result }

// Result blocks until the final result resolves. Careful: like awaiting an
// unresolved TS promise, it blocks forever when the stream never terminates.
func (s *EventStream[T, R]) Result() R {
	return <-s.result
}

// AssistantMessageEventStream terminates on done/error and extracts the
// final assistant message. It also times the response: the final message
// (done or error event, or the result passed to End) gets DurationMs,
// measured with a monotonic clock from the stream's creation, unless it
// already carries a duration or predates the stream. A message created
// before the stream started belongs to a response measured elsewhere and
// stays untimed.
type AssistantMessageEventStream struct {
	*EventStream[AssistantMessageEvent, *AssistantMessage]
	startedAt        int64 // wall clock, milliseconds
	startedMonotonic time.Time
}

// NewAssistantMessageEventStream creates an empty stream.
func NewAssistantMessageEventStream() *AssistantMessageEventStream {
	now := time.Now()
	return &AssistantMessageEventStream{
		startedAt:        now.UnixMilli(),
		startedMonotonic: now,
		EventStream: NewEventStream[AssistantMessageEvent, *AssistantMessage](
			func(event AssistantMessageEvent) bool {
				return event.EventType() == "done" || event.EventType() == "error"
			},
			func(event AssistantMessageEvent) *AssistantMessage {
				switch t := event.(type) {
				case *EventDone:
					return t.Message
				case *EventError:
					return t.Error
				}
				panic("Unexpected event type for final result")
			},
		),
	}
}

// Push times the terminal message before delivering the event.
func (s *AssistantMessageEventStream) Push(event AssistantMessageEvent) {
	switch typed := event.(type) {
	case *EventDone:
		s.time(typed.Message)
	case *EventError:
		s.time(typed.Error)
	}
	s.EventStream.Push(event)
}

// End times an explicit final result before closing the stream.
func (s *AssistantMessageEventStream) End(result ...*AssistantMessage) {
	if len(result) > 0 {
		s.time(result[0])
	}
	s.EventStream.End(result...)
}

func (s *AssistantMessageEventStream) time(message *AssistantMessage) {
	if s.Done() || message == nil || message.DurationMs != nil || message.TimestampMs < float64(s.startedAt) {
		return
	}
	elapsed := float64(time.Since(s.startedMonotonic).Milliseconds())
	if elapsed < 0 {
		elapsed = 0
	}
	message.DurationMs = &elapsed
}
