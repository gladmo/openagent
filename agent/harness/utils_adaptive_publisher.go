package harness

// utils_adaptive_publisher.go ports harness/utils/adaptive-publisher.ts.

import (
	"sync"
	"time"
)

// AdaptivePublisherOptions mirrors the TS interface.
type AdaptivePublisherOptions[TValue any, TUpdate any] struct {
	Snapshot             func() TValue
	Update               func(previous *TValue, current TValue) (TUpdate, bool)
	Measure              func(update TUpdate) int64
	Publish              func(update TUpdate)
	OnError              func(err error)
	MinIntervalMs        *int64
	TargetBytesPerSecond *int64
}

// AdaptivePublisher publishes the latest state without queuing
// intermediate mutations: first dirty after idle is immediate, each
// publication buys a size-proportional delay, and a single trailing timer
// guarantees eventual publication.
type AdaptivePublisher[TValue any, TUpdate any] struct {
	options              AdaptivePublisherOptions[TValue, TUpdate]
	minIntervalMs        int64
	targetBytesPerSecond int64

	mu         sync.Mutex
	published  *TValue
	dirty      bool
	nextEmitAt int64 // unix ms
	timer      *time.Timer
	disposed   bool
	nowFn      func() int64
}

// NewAdaptivePublisher builds a publisher.
func NewAdaptivePublisher[TValue any, TUpdate any](options AdaptivePublisherOptions[TValue, TUpdate]) *AdaptivePublisher[TValue, TUpdate] {
	minInterval := int64(100)
	if options.MinIntervalMs != nil {
		minInterval = *options.MinIntervalMs
	}
	target := int64(100 * 1024)
	if options.TargetBytesPerSecond != nil {
		target = *options.TargetBytesPerSecond
	}
	return &AdaptivePublisher[TValue, TUpdate]{
		options:              options,
		minIntervalMs:        minInterval,
		targetBytesPerSecond: target,
		nowFn:                func() int64 { return time.Now().UnixMilli() },
	}
}

// MarkDirty schedules publication of the latest snapshot.
func (p *AdaptivePublisher[TValue, TUpdate]) MarkDirty() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.disposed {
		return
	}
	p.dirty = true
	wait := p.nextEmitAt - p.nowFn()
	if wait <= 0 {
		p.flushLocked(false)
		return
	}
	p.armTimerLocked(wait)
}

// Flush publishes when dirty; force bypasses the interval gate.
func (p *AdaptivePublisher[TValue, TUpdate]) Flush(force bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.flushLocked(force)
}

func (p *AdaptivePublisher[TValue, TUpdate]) flushLocked(force bool) {
	if p.disposed || !p.dirty {
		return
	}
	now := p.nowFn()
	if !force && now < p.nextEmitAt {
		p.armTimerLocked(p.nextEmitAt - now)
		return
	}
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	current := p.options.Snapshot()
	update, hasUpdate := p.options.Update(p.published, current)
	if !hasUpdate {
		p.published = &current
		p.dirty = false
		return
	}
	encodedBytes := p.options.Measure(update)
	p.published = &current
	p.dirty = false
	delay := int64(float64(encodedBytes) * 1000 / float64(p.targetBytesPerSecond))
	if delay < p.minIntervalMs {
		delay = p.minIntervalMs
	}
	p.nextEmitAt = now + delay
	// Commit before delivery (see TS comment on reentrancy).
	p.options.Publish(update)
}

// Dispose stops the timer and future publications.
func (p *AdaptivePublisher[TValue, TUpdate]) Dispose() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	p.disposed = true
}

func (p *AdaptivePublisher[TValue, TUpdate]) armTimerLocked(wait int64) {
	if p.timer != nil {
		return
	}
	p.timer = time.AfterFunc(time.Duration(wait)*time.Millisecond, func() {
		p.mu.Lock()
		p.timer = nil
		unlock := func() { p.mu.Unlock() }
		// flushLocked may call Publish; run it under the lock (TS is
		// single-threaded; the mutex preserves the same serialization).
		defer func() {
			if r := recover(); r != nil {
				unlock()
				if p.options.OnError != nil {
					p.options.OnError(ToError(r))
				}
				return
			}
			unlock()
		}()
		p.flushLocked(false)
	})
}
