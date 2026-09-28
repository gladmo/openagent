// trajectory/trajectory_test.go: the trajectory object's contracts —
// commit ordering, subscription repeatability and isolation, kind
// registration, defensive copies, close semantics.
package trajectory

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func fixedClock(start float64) func() float64 {
	current := start
	return func() float64 {
		current++
		return current
	}
}

func TestAppendAssignsDenseSeqAndClockTime(t *testing.T) {
	traj := New(Options{ID: "s1", Now: fixedClock(1000)})
	a, err := traj.Append(Record{Type: KindSessionStart, Data: jsonx.NewObj()})
	if err != nil {
		t.Fatal(err)
	}
	if a.Seq != 1 || a.TimeMs != 1001 {
		t.Fatalf("first record = seq %d time %v", a.Seq, a.TimeMs)
	}
	b, err := traj.Append(Record{Type: KindTurnStart, Turn: 1, Data: jsonx.NewObj()})
	if err != nil {
		t.Fatal(err)
	}
	if b.Seq != 2 || b.TimeMs != 1002 {
		t.Fatalf("second record = seq %d time %v", b.Seq, b.TimeMs)
	}
	if traj.Len() != 2 {
		t.Fatalf("len = %d", traj.Len())
	}
}

func TestAppendRejectsUnknownKindAndScopeViolations(t *testing.T) {
	traj := New(Options{ID: "s1", Now: fixedClock(1)})
	if _, err := traj.Append(Record{Type: "custom/thing", Data: jsonx.NewObj()}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	// session-scoped kind carrying a turn.
	if _, err := traj.Append(Record{Type: KindSessionStart, Turn: 1, Data: jsonx.NewObj()}); err == nil {
		t.Fatal("session kind with turn accepted")
	}
	// step-scoped kind without a step.
	if _, err := traj.Append(Record{Type: KindToolCall, Turn: 1, Data: jsonx.NewObj()}); err == nil {
		t.Fatal("step kind without step accepted")
	}
	// non-JSON payload.
	if _, err := traj.Append(Record{Type: KindError, Data: func() {}}); err == nil {
		t.Fatal("non-JSON payload accepted")
	}
	if traj.Len() != 0 {
		t.Fatalf("rejected records committed: %d", traj.Len())
	}
}

func TestRegisteredExtensionKindsAreIgnorableUnlessRequired(t *testing.T) {
	traj := New(Options{ID: "s1", Now: fixedClock(1)})
	if err := traj.RegisterKind("plugin/note", false); err != nil {
		t.Fatal(err)
	}
	if err := traj.RegisterKind("plugin/vital", true); err != nil {
		t.Fatal(err)
	}
	optional, err := traj.Append(Record{Type: "plugin/note", Data: jsonx.NewObj()})
	if err != nil {
		t.Fatal(err)
	}
	if !optional.Ignorable {
		t.Fatal("optional extension kind not marked ignorable")
	}
	required, err := traj.Append(Record{Type: "plugin/vital", Data: jsonx.NewObj()})
	if err != nil {
		t.Fatal(err)
	}
	if required.Ignorable {
		t.Fatal("required extension kind marked ignorable")
	}
	if err := traj.RegisterKind("plugin/note", false); err == nil {
		t.Fatal("duplicate registration accepted")
	}
}

func TestSubscribeRepeatableOrderedIsolated(t *testing.T) {
	var order []string
	var mu sync.Mutex
	errors := 0
	traj := New(Options{ID: "s1", Now: fixedClock(1), OnListenerError: func(*Record, error) { errors++ }})

	first := traj.Subscribe(func(*Record) {
		mu.Lock()
		order = append(order, "first")
		mu.Unlock()
		panic("observer defect")
	})
	second := traj.Subscribe(func(rec *Record) {
		mu.Lock()
		order = append(order, "second:"+rec.Type)
		mu.Unlock()
	})
	third := traj.Subscribe(func(*Record) {
		mu.Lock()
		order = append(order, "third")
		mu.Unlock()
	})

	if _, err := traj.Append(Record{Type: KindSessionStart, Data: jsonx.NewObj()}); err != nil {
		t.Fatal(err)
	}
	second()
	second() // idempotent
	if _, err := traj.Append(Record{Type: KindSessionEnd, Data: jsonx.NewObj()}); err != nil {
		t.Fatal(err)
	}
	first()
	third()

	mu.Lock()
	defer mu.Unlock()
	// The second listener was disposed before the second append; it must
	// not receive it, and the panicking first listener must not starve the
	// third.
	want := []string{"first", "second:session/start", "third", "first", "third"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("delivery order = %v, want %v", order, want)
	}
	if errors != 2 {
		t.Fatalf("contained panics reported %d times, want 2", errors)
	}
}

func TestSubscribeHasNoHistoryReplay(t *testing.T) {
	traj := New(Options{ID: "s1", Now: fixedClock(1)})
	if _, err := traj.Append(Record{Type: KindSessionStart, Data: jsonx.NewObj()}); err != nil {
		t.Fatal(err)
	}
	seen := 0
	unsubscribe := traj.Subscribe(func(*Record) { seen++ })
	defer unsubscribe()
	if seen != 0 {
		t.Fatalf("subscriber replayed history: saw %d", seen)
	}
	if _, err := traj.Append(Record{Type: KindSessionEnd, Data: jsonx.NewObj()}); err != nil {
		t.Fatal(err)
	}
	if seen != 1 {
		t.Fatalf("live record not delivered: saw %d", seen)
	}
}

func TestSubscribeAfterDeliversBacklogThenLiveAtomically(t *testing.T) {
	traj := New(Options{ID: "s1", Now: fixedClock(1)})
	if _, err := traj.Append(Record{Type: KindSessionStart, Data: jsonx.NewObj()}); err != nil {
		t.Fatal(err)
	}
	if _, err := traj.Append(Record{Type: KindTurnStart, Turn: 1, Data: jsonx.NewObj()}); err != nil {
		t.Fatal(err)
	}
	var order []int64
	stop := traj.SubscribeAfter(1, func(rec *Record) { order = append(order, rec.Seq) })
	if len(order) != 1 || order[0] != 2 {
		t.Fatalf("backlog = %v", order)
	}
	if _, err := traj.Append(Record{Type: KindTurnEnd, Turn: 1, Data: jsonx.NewObj()}); err != nil {
		t.Fatal(err)
	}
	stop()
	if len(order) != 2 || order[1] != 3 {
		t.Fatalf("live after backlog = %v", order)
	}
	// Full replay from zero sees everything already committed.
	var all int
	traj.SubscribeAfter(0, func(*Record) { all++ })
	if all != 3 {
		t.Fatalf("full replay = %d", all)
	}
}

func TestReentrantAppendFails(t *testing.T) {
	traj := New(Options{ID: "s1", Now: fixedClock(1)})
	var innerErr error
	unsubscribe := traj.Subscribe(func(rec *Record) {
		if rec.Type == KindSessionStart {
			_, innerErr = traj.Append(Record{Type: KindSessionEnd, Data: jsonx.NewObj()})
		}
	})
	defer unsubscribe()
	if _, err := traj.Append(Record{Type: KindSessionStart, Data: jsonx.NewObj()}); err != nil {
		t.Fatal(err)
	}
	if innerErr == nil {
		t.Fatal("reentrant append succeeded")
	}
	if traj.Len() != 1 {
		t.Fatalf("reentrant append committed: len = %d", traj.Len())
	}
}

func TestSnapshotIsStableAndDefensivelyCopied(t *testing.T) {
	traj := New(Options{ID: "s1", Now: fixedClock(1)})
	payload := jsonx.ObjFrom("k", "v")
	if _, err := traj.Append(Record{Type: KindError, Data: payload}); err != nil {
		t.Fatal(err)
	}
	payload.Set("k", "mutated")
	snap := traj.Snapshot()
	snap[0].Data.(*jsonx.Obj).Set("k", "mutated2")
	fresh := traj.Snapshot()
	if got := fresh[0].Data.(*jsonx.Obj).MustGet("k"); got != "v" {
		t.Fatalf("payload not defensively copied: %v", got)
	}
}

func TestCloseFreezesAppend(t *testing.T) {
	traj := New(Options{ID: "s1", Now: fixedClock(1)})
	cause := errors.New("done")
	traj.Close(cause)
	if _, err := traj.Append(Record{Type: KindSessionStart, Data: jsonx.NewObj()}); err == nil {
		t.Fatal("append after close succeeded")
	}
	if traj.Closed() == nil {
		t.Fatal("Closed missing cause")
	}
	traj.Close(nil) // idempotent
}

func TestQueryFilters(t *testing.T) {
	traj := New(Options{ID: "s1", Now: fixedClock(1)})
	mustAppend := func(kind string, turn, step int, text string) {
		t.Helper()
		if _, err := traj.Append(Record{Type: kind, Turn: turn, Step: step,
			Data: jsonx.ObjFrom("needle", text)}); err != nil {
			t.Fatal(err)
		}
	}
	mustAppend(KindToolCall, 1, 1, "read file")
	mustAppend(KindToolResult, 1, 1, "contents with ERROR inside")
	mustAppend(KindToolCall, 2, 1, "write file")

	byType := traj.Query(Query{Types: []string{KindToolCall}})
	if len(byType) != 2 {
		t.Fatalf("type filter = %d records", len(byType))
	}
	byTurn := traj.Query(Query{Turn: 1})
	if len(byTurn) != 2 {
		t.Fatalf("turn filter = %d records", len(byTurn))
	}
	byText := traj.Query(Query{Text: "error inside"})
	if len(byText) != 1 || byText[0].Type != KindToolResult {
		t.Fatalf("text filter = %+v", byText)
	}
	desc := traj.Query(Query{Types: []string{KindToolCall}, Order: "desc"})
	if desc[0].Turn != 2 {
		t.Fatalf("desc order wrong: %+v", desc)
	}
	limited := traj.Query(Query{Limit: 1})
	if len(limited) != 1 {
		t.Fatalf("limit ignored")
	}
}
