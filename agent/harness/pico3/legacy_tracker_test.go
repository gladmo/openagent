package pico3

// Ports of legacy-tracker behaviors.

import (
	"testing"

	chorddelta "github.com/gladmo/openagent/chord/delta"
	"github.com/gladmo/openagent/jsonx"
)

func objFromPairs(pairs ...any) *jsonx.Obj {
	obj := jsonx.NewObj()
	for i := 0; i+1 < len(pairs); i += 2 {
		obj.Set(pairs[i].(string), pairs[i+1])
	}
	return obj
}

func TestTrackerFirstFlushEmitsBase(t *testing.T) {
	initial := objFromPairs("a", float64(1))
	tracker := Track(initial)
	if !tracker.Dirty() {
		t.Fatal("fresh tracker not dirty")
	}
	ops := tracker.Flush()
	if len(ops) != 1 || !chorddelta.IsBase(ops) {
		t.Fatalf("ops = %v", ops)
	}
	if tracker.Dirty() {
		t.Fatal("flush left tracker dirty")
	}
}

func TestTrackerCleanFlushIsEmpty(t *testing.T) {
	tracker := Track(objFromPairs("a", float64(1)))
	tracker.Flush()
	if ops := tracker.Flush(); len(ops) != 0 {
		t.Fatalf("clean flush = %v", ops)
	}
}

func TestTrackerSetDiff(t *testing.T) {
	tracker := Track(objFromPairs("a", float64(1), "b", float64(2)))
	tracker.Flush()

	working := tracker.State()
	working.Set("b", float64(3))
	working.Set("c", float64(4))

	if !tracker.Dirty() {
		t.Fatal("mutation not dirty")
	}
	ops := tracker.Flush()
	if len(ops) != 2 {
		t.Fatalf("ops = %v", ops)
	}
	sets := 0
	for _, op := range ops {
		if set, ok := op.(*chorddelta.Set); ok {
			sets++
			switch set.Path[0] {
			case "b":
				if set.Value != float64(3) {
					t.Fatalf("b = %v", set.Value)
				}
			case "c":
				if set.Value != float64(4) {
					t.Fatalf("c = %v", set.Value)
				}
			default:
				t.Fatalf("unexpected set %v", set.Path)
			}
		}
	}
	if sets != 2 {
		t.Fatalf("sets = %d", sets)
	}
	// Target advanced: applying ops to the old base yields the working
	// state.
	applied, err := chorddelta.ApplyImmutable(objFromPairs("a", float64(1), "b", float64(2)), ops)
	if err != nil {
		t.Fatal(err)
	}
	if jsonx.Stringify(applied) != jsonx.Stringify(tracker.Target()) {
		t.Fatalf("applied = %v target = %v", applied, tracker.Target())
	}
}

func TestTrackerDeleteDiff(t *testing.T) {
	tracker := Track(objFromPairs("a", float64(1), "b", float64(2)))
	tracker.Flush()
	working := tracker.State()
	working.Delete("a")
	ops := tracker.Flush()
	if len(ops) != 1 {
		t.Fatalf("ops = %v", ops)
	}
	del, ok := ops[0].(*chorddelta.Delete)
	if !ok || del.Path[0] != "a" {
		t.Fatalf("op = %v", ops[0])
	}
	// Target reflects the deletion.
	if _, still := tracker.Target().Get("a"); still {
		t.Fatal("delete not adopted")
	}
}

func TestTrackerRebaseForcesBase(t *testing.T) {
	tracker := Track(objFromPairs("a", float64(1)))
	tracker.Flush()
	tracker.Rebase()
	if !tracker.Dirty() {
		t.Fatal("rebase not dirty")
	}
	ops := tracker.Flush()
	if len(ops) != 1 || !chorddelta.IsBase(ops) {
		t.Fatalf("ops = %v", ops)
	}
}

func TestTrackerWorkingCopyIndependent(t *testing.T) {
	initial := objFromPairs("a", float64(1))
	tracker := Track(initial)
	working := tracker.State()
	working.Set("a", float64(99))
	// The caller's initial object is untouched.
	if initial.MustGet("a") != float64(1) {
		t.Fatal("initial mutated")
	}
	// Target only moves at flush.
	if tracker.Target().MustGet("a") != float64(1) {
		t.Fatal("target moved before flush")
	}
	tracker.Flush()
	if tracker.Target().MustGet("a") != float64(99) {
		t.Fatal("target not adopted")
	}
}
