// Package harnessfacade: watch.go wires the facade's lane watch — the
// snapshot capture plus the resnapshot boundary — over the ported
// runtime capture.
package harnessfacade

import (
	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/agent/harness/runtime"
	"github.com/gladmo/openagent/jsonx"
)

// LaneWatchHandle mirrors the TS WatchHandle: an owned snapshot, a
// listener slot, and resnapshot.
type LaneWatchHandle struct {
	snapshot     *jsonx.Obj
	resnapshot   func(ctx harness.Context) (*jsonx.Obj, error)
	listener     func(event *jsonx.Obj)
	unsubscribed bool
}

// Snapshot returns the owned snapshot.
func (w *LaneWatchHandle) Snapshot() *jsonx.Obj { return w.snapshot }

// Start installs the listener.
func (w *LaneWatchHandle) Start(listener func(event *jsonx.Obj)) {
	w.listener = listener
}

// Resnapshot recaptures the lane state.
func (w *LaneWatchHandle) Resnapshot(ctx harness.Context) (*jsonx.Obj, error) {
	if w.resnapshot == nil {
		return w.snapshot, nil
	}
	fresh, err := w.resnapshot(ctx)
	if err != nil {
		return nil, err
	}
	w.snapshot = fresh
	return fresh, nil
}

// Unsubscribe drops the listener.
func (w *LaneWatchHandle) Unsubscribe() {
	w.unsubscribed = true
	w.listener = nil
}

// Watch captures the lane snapshot and returns a resnapshot handle.
func (f *FacadeLane) Watch(ctx harness.Context) (*LaneWatchHandle, error) {
	snapshot, err := runtime.CaptureLaneSnapshot(f.lane, f.harness.Session(), ctx)
	if err != nil {
		return nil, err
	}
	rendered := runtime.LaneSnapshotJSON(snapshot)
	return &LaneWatchHandle{
		snapshot: rendered,
		resnapshot: func(ctx harness.Context) (*jsonx.Obj, error) {
			fresh, err := runtime.CaptureLaneSnapshot(f.lane, f.harness.Session(), ctx)
			if err != nil {
				return nil, err
			}
			return runtime.LaneSnapshotJSON(fresh), nil
		},
	}, nil
}
