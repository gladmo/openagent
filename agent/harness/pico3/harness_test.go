package pico3

// Ports of harness.ts registration behaviors.

import (
	"strings"
	"testing"
)

func newHarnessForTest(t *testing.T) *Harness {
	t.Helper()
	harness, err := OpenHarness(NewMemoryStorage(), HarnessOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = harness.Session().Close() })
	return harness
}

func TestHarnessBuiltinKinds(t *testing.T) {
	harness := newHarnessForTest(t)
	for _, name := range BuiltinKindNames {
		if _, ok := harness.Kind(name); !ok {
			t.Fatalf("builtin %s missing", name)
		}
	}
	// A user kind cannot shadow a builtin.
	if err := harness.registerKind(&KindPhases{Name: "pi.generation", Phases: map[string]Handler{}}); err == nil {
		t.Fatal("builtin shadowed")
	}
	// Reserved prefix.
	if err := harness.registerKind(&KindPhases{Name: "pi.custom", Phases: map[string]Handler{}}); err == nil {
		t.Fatal("reserved prefix accepted")
	} else if !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("err = %v", err)
	}
}

func TestHarnessUserKinds(t *testing.T) {
	harness, err := OpenHarness(NewMemoryStorage(), HarnessOptions{
		TaskKinds: []*KindPhases{
			{Name: "app.job", Phases: map[string]Handler{}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer harness.Session().Close()
	if _, ok := harness.Kind("app.job"); !ok {
		t.Fatal("user kind missing")
	}
	// Duplicate at open rejects.
	if _, err := OpenHarness(NewMemoryStorage(), HarnessOptions{
		TaskKinds: []*KindPhases{
			{Name: "app.job", Phases: map[string]Handler{}},
			{Name: "app.job", Phases: map[string]Handler{}},
		},
	}); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("err = %v", err)
	}
}

func TestHarnessRegistries(t *testing.T) {
	harness := newHarnessForTest(t)

	// Namespace: once, then clash.
	if err := harness.RegisterNamespace(&NamespaceRegistration{ID: "ui"}); err != nil {
		t.Fatal(err)
	}
	if err := harness.RegisterNamespace(&NamespaceRegistration{ID: "ui"}); err == nil {
		t.Fatal("namespace clash accepted")
	}
	if _, ok := harness.Namespace("ui"); !ok {
		t.Fatal("namespace missing")
	}

	// Tool: once, then clash; revision bumps.
	if err := harness.RegisterTool(BashTool(OutputPolicy{})); err != nil {
		t.Fatal(err)
	}
	rev := harness.ToolRevision()
	if rev != 1 {
		t.Fatalf("rev = %d", rev)
	}
	if err := harness.RegisterTool(BashTool(OutputPolicy{})); err == nil {
		t.Fatal("tool clash accepted")
	}
	if _, ok := harness.Tool("bash"); !ok {
		t.Fatal("tool missing")
	}

	// Section: once, then clash; revision bumps.
	if err := harness.RegisterSection(SectionIdentity); err != nil {
		t.Fatal(err)
	}
	if harness.SectionRevision() != 1 {
		t.Fatal("section rev")
	}
	if err := harness.RegisterSection(SectionIdentity); err == nil {
		t.Fatal("section clash accepted")
	}

	// Entry kinds.
	if err := harness.RegisterEntryKind(EntryKindWitness{KindName: "app.note"}); err != nil {
		t.Fatal(err)
	}
	if err := harness.RegisterEntryKind(EntryKindWitness{KindName: "app.note"}); err == nil {
		t.Fatal("entry kind clash accepted")
	}

	// Hooks append in order.
	harness.RegisterHook(HookRegistration{})
	harness.RegisterHook(HookRegistration{})
	if len(harness.HookRegistrations()) != 2 {
		t.Fatal("hooks")
	}
}

func TestHarnessReportIsolation(t *testing.T) {
	calls := 0
	harness, err := OpenHarness(NewMemoryStorage(), HarnessOptions{
		OnReport: func(error) {
			calls++
			panic("onReport exploded")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer harness.Session().Close()
	// A panicking onReport never escapes.
	harness.Report(errTestHarness("x"))
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestHarnessLifecycleFlags(t *testing.T) {
	harness := newHarnessForTest(t)
	if harness.Resumed() || harness.Suspended() {
		t.Fatal("flags set at open")
	}
	harness.SetResumed()
	harness.SetSuspended(true)
	if !harness.Resumed() || !harness.Suspended() {
		t.Fatal("flags not set")
	}
	harness.SetSuspended(false)
	if harness.Suspended() {
		t.Fatal("suspend not cleared")
	}
}

func errTestHarness(msg string) error { return &harnessTestError{msg} }

type harnessTestError struct{ msg string }

func (e *harnessTestError) Error() string { return e.msg }
