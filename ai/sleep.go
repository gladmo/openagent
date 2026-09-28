package ai

import (
	"sync"
	"time"
)

var (
	timeAfterMu       sync.Mutex
	timeAfterOverride func(d float64, f func()) *time.Timer
)

// timeAfterFunc indirection so tests can fast-forward retry sleeps.
func timeAfterFunc(d float64, f func()) *time.Timer {
	timeAfterMu.Lock()
	override := timeAfterOverride
	timeAfterMu.Unlock()
	if override != nil {
		return override(d, f)
	}
	return time.AfterFunc(time.Duration(d)*time.Millisecond, f)
}
