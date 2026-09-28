package pico3

// Ports of chord.ts bridge behaviors.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/chord/services"
	"github.com/gladmo/openagent/jsonx"
)

func TestPicoServiceDefinitions(t *testing.T) {
	if PicoHarnessService.Mode != services.ServiceModeLocal {
		t.Fatalf("harness mode = %s", PicoHarnessService.Mode)
	}
	if PicoHarnessService.Name != "pi.harness" {
		t.Fatalf("name = %s", PicoHarnessService.Name)
	}
	if PicoConversationService.Mode != services.ServiceModeBoth {
		t.Fatalf("conversation mode = %s", PicoConversationService.Mode)
	}
}

func TestAttachChordViewInitial(t *testing.T) {
	initial := jsonx.ObjFrom("conversation", jsonx.ObjFrom("id", float64(1)))
	bridge, err := AttachChordView(initial,
		func(initial *jsonx.Obj) *jsonx.Obj { return initial },
		func(view *jsonx.Obj, envelope *Envelope) (*jsonx.Obj, error) { return view, nil },
		ChordViewBridgeOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	// The view carries the initial shape plus an empty commit block.
	view := bridge.View()
	if view.MustGet("conversation") == nil {
		t.Fatal("initial content lost")
	}
	commit := view.MustGet("commit").(*jsonx.Obj)
	if commit.MustGet("events") == nil {
		t.Fatal("commit block missing")
	}
	if bridge.Closed() {
		t.Fatal("closed at attach")
	}
}

func TestBridgeEnqueueApply(t *testing.T) {
	initial := jsonx.ObjFrom("revision", float64(0))
	var published []*Envelope
	bridge, err := AttachChordView(initial,
		func(initial *jsonx.Obj) *jsonx.Obj { return initial },
		func(view *jsonx.Obj, envelope *Envelope) (*jsonx.Obj, error) {
			published = append(published, envelope)
			next := jsonx.NewObj()
			next.Set("revision", float64(envelope.Revision))
			return next, nil
		},
		ChordViewBridgeOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Two envelopes enqueue then apply atomically.
	if err := bridge.Enqueue(&Envelope{Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if err := bridge.Enqueue(&Envelope{Revision: 2}); err != nil {
		t.Fatal(err)
	}
	if len(published) != 0 {
		t.Fatal("published before apply")
	}
	if err := bridge.Apply(); err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 {
		t.Fatalf("published = %d", len(published))
	}
	if bridge.View().MustGet("revision") != float64(2) {
		t.Fatal("view not advanced")
	}
	// The queue drained.
	if err := bridge.Apply(); err != nil || len(published) != 2 {
		t.Fatal("apply drained")
	}
}

func TestBridgeCapacityOverflow(t *testing.T) {
	var failed error
	bridge, err := AttachChordView(jsonx.NewObj(),
		func(initial *jsonx.Obj) *jsonx.Obj { return initial },
		func(view *jsonx.Obj, envelope *Envelope) (*jsonx.Obj, error) { return view, nil },
		ChordViewBridgeOptions{Capacity: 2, OnFailure: func(err error) { failed = err }},
	)
	if err != nil {
		t.Fatal(err)
	}
	bridge.Enqueue(&Envelope{Revision: 1})
	bridge.Enqueue(&Envelope{Revision: 2})
	bridge.Enqueue(&Envelope{Revision: 3}) // overflow
	if !bridge.Closed() {
		t.Fatal("bridge survived overflow")
	}
	if failed == nil || !strings.Contains(failed.Error(), "capacity 2 exceeded") {
		t.Fatalf("failed = %v", failed)
	}
	// Post-close enqueues are no-ops.
	bridge.Enqueue(&Envelope{Revision: 4})
	if bridge.Apply() != nil {
		t.Fatal("apply after close")
	}
}

func TestBridgeApplyFailureTearsDown(t *testing.T) {
	var failed error
	bridge, _ := AttachChordView(jsonx.NewObj(),
		func(initial *jsonx.Obj) *jsonx.Obj { return initial },
		func(view *jsonx.Obj, envelope *Envelope) (*jsonx.Obj, error) {
			return nil, &publishFailedError{}
		},
		ChordViewBridgeOptions{OnFailure: func(err error) { failed = err }},
	)
	bridge.Enqueue(&Envelope{Revision: 1})
	if err := bridge.Apply(); err == nil {
		t.Fatal("apply error swallowed")
	}
	if !bridge.Closed() {
		t.Fatal("bridge not torn down")
	}
	if failed == nil {
		t.Fatal("onFailure not called")
	}
}

func TestBridgeInvalidCapacity(t *testing.T) {
	if _, err := AttachChordView(jsonx.NewObj(),
		func(initial *jsonx.Obj) *jsonx.Obj { return initial },
		func(view *jsonx.Obj, envelope *Envelope) (*jsonx.Obj, error) { return view, nil },
		ChordViewBridgeOptions{Capacity: -1},
	); err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("err = %v", err)
	}
}

func TestBridgeClose(t *testing.T) {
	bridge, _ := AttachChordView(jsonx.NewObj(),
		func(initial *jsonx.Obj) *jsonx.Obj { return initial },
		func(view *jsonx.Obj, envelope *Envelope) (*jsonx.Obj, error) { return view, nil },
		ChordViewBridgeOptions{},
	)
	bridge.Enqueue(&Envelope{Revision: 1})
	bridge.Close()
	if !bridge.Closed() {
		t.Fatal("not closed")
	}
	// Double close safe; onFailure not called without a cause.
	bridge.Close()
}

type publishFailedError struct{}

func (e *publishFailedError) Error() string { return "publish failed" }
