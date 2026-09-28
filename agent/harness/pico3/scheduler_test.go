package pico3

// Ports of scheduler validation + gate state-machine behaviors.

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func testKind() *KindPhases {
	phase := func(name string) Handler {
		return func(*Task, RuntimeLike, ContextLike) (*Step, error) {
			return &Step{NextPhase: name, HasNextPhase: true}, nil
		}
	}
	return &KindPhases{
		Name: "job",
		Initial: func(*Task, RuntimeLike, ContextLike) (*Step, error) {
			return &Step{Done: func(*Task, TxLike, ContextLike) (any, error) { return nil, nil }}, nil
		},
		Phases: map[string]Handler{
			"work":  phase("work"),
			"write": phase("write"),
		},
		Inflight: []string{"write"},
	}
}

func TestHandlerFor(t *testing.T) {
	kind := testKind()
	// Initial resolves.
	if _, err := HandlerFor(kind, nil); err != nil {
		t.Fatal(err)
	}
	// Known phase resolves.
	work := "work"
	if _, err := HandlerFor(kind, &work); err != nil {
		t.Fatal(err)
	}
	// Unknown phase faults.
	ghost := "ghost"
	if _, err := HandlerFor(kind, &ghost); err == nil {
		t.Fatal("unknown phase resolved")
	} else if fault, ok := err.(*TaskContractFault); !ok || fault.Kind != "job" {
		t.Fatalf("err = %v", err)
	}
}

func TestValidateStep(t *testing.T) {
	kind := testKind()
	// Done closure accepted.
	done := &Step{Done: func(*Task, TxLike, ContextLike) (any, error) { return nil, nil }}
	if err := ValidateStep(kind, done); err != nil {
		t.Fatal(err)
	}
	// Known phase transition accepted.
	step := &Step{NextPhase: "work", HasNextPhase: true}
	if err := ValidateStep(kind, step); err != nil {
		t.Fatal(err)
	}
	// Unknown phase faults.
	step = &Step{NextPhase: "ghost", HasNextPhase: true}
	if err := ValidateStep(kind, step); err == nil {
		t.Fatal("unknown transition accepted")
	}
	// Nil step faults.
	if err := ValidateStep(kind, nil); err == nil {
		t.Fatal("nil step accepted")
	}
}

func completionOf(status string, fields map[string]any) Outcome {
	obj := jsonx.NewObj()
	obj.Set("status", status)
	for k, v := range fields {
		obj.Set(k, v)
	}
	return obj
}

func TestValidateCompletion(t *testing.T) {
	kind := testKind()
	if err := ValidateCompletion(kind, completionOf("completed", map[string]any{"result": float64(1)})); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCompletion(kind, completionOf("failed", map[string]any{"failure": "boom"})); err != nil {
		t.Fatal(err)
	}
	// completed without result faults.
	if err := ValidateCompletion(kind, completionOf("completed", nil)); err == nil {
		t.Fatal("completed without result accepted")
	}
	// failed without failure faults.
	if err := ValidateCompletion(kind, completionOf("failed", nil)); err == nil {
		t.Fatal("failed without failure accepted")
	}
	// nil faults.
	if err := ValidateCompletion(kind, nil); err == nil {
		t.Fatal("nil completion accepted")
	}
	// Unknown status faults.
	if err := ValidateCompletion(kind, completionOf("weird", map[string]any{"result": float64(1)})); err == nil {
		t.Fatal("weird status accepted")
	}
}

func checkpointOf(phase string) Checkpoint {
	obj := jsonx.NewObj()
	if phase != "" {
		obj.Set("phase", phase)
	}
	return obj
}

func TestValidateCheckpoint(t *testing.T) {
	kind := testKind()
	if err := ValidateCheckpoint(kind, checkpointOf("work")); err != nil {
		t.Fatal(err)
	}
	// Unknown phase faults.
	if err := ValidateCheckpoint(kind, checkpointOf("ghost")); err == nil {
		t.Fatal("unknown phase accepted")
	}
	// In-flight phase faults with the guidance message.
	err := ValidateCheckpoint(kind, checkpointOf("write"))
	if err == nil {
		t.Fatal("in-flight phase accepted")
	}
	fault, ok := err.(*TaskContractFault)
	if !ok {
		t.Fatalf("err = %v", err)
	}
	if want := "transition into in-flight phase write; write it with rt.commit before the effect instead"; fault.Message != want {
		t.Fatalf("message = %q", fault.Message)
	}
	// Missing phase faults.
	if err := ValidateCheckpoint(kind, checkpointOf("")); err == nil {
		t.Fatal("missing phase accepted")
	}
	// nil faults.
	if err := ValidateCheckpoint(kind, nil); err == nil {
		t.Fatal("nil checkpoint accepted")
	}
}

func TestSchedulerGateKickOnlyWhenEnabled(t *testing.T) {
	var drains atomic.Int64
	gate := NewSchedulerGate(func() error {
		drains.Add(1)
		return nil
	})
	// Before resume, kicks do nothing.
	gate.Kick()
	if drains.Load() != 0 {
		t.Fatal("kicked while disabled")
	}
	gate.Resume()
	if drains.Load() != 1 {
		t.Fatalf("drains = %d", drains.Load())
	}
	gate.Stop()
	gate.Kick()
	if drains.Load() != 1 {
		t.Fatal("kicked after stop")
	}
}

func TestSchedulerGateHoldBlocksDrain(t *testing.T) {
	var drains atomic.Int64
	gate := NewSchedulerGate(func() error {
		drains.Add(1)
		return nil
	})
	gate.Resume()
	release := gate.Hold()
	gate.Kick()
	if drains.Load() != 1 {
		t.Fatalf("drained while held: %d", drains.Load())
	}
	// Release kicks.
	release()
	if drains.Load() != 2 {
		t.Fatalf("drains after release = %d", drains.Load())
	}
	// Double release is safe.
	release()
	if _, holds, _, _ := gate.Stats(); holds != 0 {
		t.Fatalf("holds = %d", holds)
	}
}

func TestSchedulerGateNestedHolds(t *testing.T) {
	var drains atomic.Int64
	gate := NewSchedulerGate(func() error {
		drains.Add(1)
		return nil
	})
	gate.Resume()
	r1 := gate.Hold()
	r2 := gate.Hold()
	gate.Kick()
	if drains.Load() != 1 {
		t.Fatal("drained while nested holds")
	}
	r1()
	gate.Kick()
	if drains.Load() != 1 {
		t.Fatal("drained with one hold left")
	}
	r2()
	if drains.Load() != 2 {
		t.Fatalf("drains = %d", drains.Load())
	}
}

func TestSchedulerGateConcurrentKickSingleDrain(t *testing.T) {
	var drains atomic.Int64
	var mu sync.Mutex
	gate := NewSchedulerGate(func() error {
		mu.Lock()
		drains.Add(1)
		mu.Unlock()
		return nil
	})
	gate.Resume()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gate.Kick()
		}()
	}
	wg.Wait()
	if drains.Load() < 1 {
		t.Fatalf("drains = %d", drains.Load())
	}
	if _, holds, dirty, draining := gate.Stats(); holds != 0 || draining || dirty {
		t.Fatalf("state = holds %d dirty %v draining %v", holds, dirty, draining)
	}
}
