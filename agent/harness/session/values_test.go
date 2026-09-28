package session

// Ports of values.test.ts, mutation-line.test.ts and memory-state
// behaviors (prepare/apply/fork/scan).

import (
	"strings"
	"testing"
	"time"
)

func TestValueAddresses(t *testing.T) {
	v := NewValue("app.data", "key1")
	if v.Namespace != "app.data" || v.Key != "key1" || v.Kind != "value" {
		t.Fatalf("v = %+v", v)
	}
	l := NewList("app.items")
	if l.Kind != "list" || l.Key != "" {
		t.Fatalf("l = %+v", l)
	}
	// Empty namespace and NUL rejection.
	for _, fn := range []func(){
		func() { NewValue("") },
		func() { NewValue("a\x00b") },
		func() { NewValue("ns", "k\x00") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid address accepted")
				}
			}()
			fn()
		}()
	}
}

func TestReservedAccessors(t *testing.T) {
	if v := BranchTip("main"); v.Namespace != "pi.branch.tip" || v.Key != "main" {
		t.Fatalf("branchTip = %+v", v)
	}
	if v := OperationToolArgs("op1", "step2", 3); v.Key != "op1:step2:3" {
		t.Fatalf("toolArgs = %+v", v)
	}
	if v := PendingAssistantFrames("op1", "resp1"); v.Kind != "list" || v.Namespace != "pi.pending.assistant_frame" || v.Key != "op1:resp1" {
		t.Fatalf("frames = %+v", v)
	}
	if SessionName.Namespace != "pi.session.name" || SessionName.Key != "" {
		t.Fatalf("sessionName = %+v", SessionName)
	}
}

func TestResolveListReadOptions(t *testing.T) {
	resolved := ResolveListReadOptions(ListReadOptions{})
	if resolved.Limit != 1000 || resolved.Order != "asc" {
		t.Fatalf("default = %+v", resolved)
	}
	big := int64(50000)
	resolved = ResolveListReadOptions(ListReadOptions{Limit: &big})
	if resolved.Limit != 10000 {
		t.Fatalf("cap = %d", resolved.Limit)
	}
	zero := int64(0)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("zero limit accepted")
			}
		}()
		ResolveListReadOptions(ListReadOptions{Limit: &zero})
	}()
}

func TestMutationLineSerialization(t *testing.T) {
	line := &MutationLine{}
	var order []string
	for i := 0; i < 5; i++ {
		i := i
		<-line.Run(func() error {
			order = append(order, string(rune('a'+i)))
			return nil
		})
	}
	if strings.Join(order, "") != "abcde" {
		t.Fatalf("order = %v", order)
	}
	// Seal rejects later jobs.
	<-line.Seal(errTest("sealed"))
	<-line.Run(func() error {
		t.Fatal("job after seal ran")
		return nil
	})
}

func errTest(msg string) error { return &testError{msg} }

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func newTestState() *InMemoryStorageState { return NewInMemoryStorageState() }

func messageEntry(id string, parent *string, messageRole string) *Entry {
	return &Entry{
		EntryBase: EntryBase{ID: id, ParentID: parent, Type: EntryTypeMessage},
		Message:   AgentMessagePayload{Role: messageRole},
	}
}

func TestCommitPrepareValidateApply(t *testing.T) {
	state := newTestState()
	root := messageEntry("e1", nil, "user")
	child := messageEntry("e2", strptr("e1"), "assistant")
	timestamp := float64(time.Now().UnixMilli())
	prepared, err := state.PrepareCommit([]Write{
		InsertEntry(root),
		InsertEntry(child),
	}, timestamp)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.FirstSeq != 1 || len(prepared.Seqs) != 2 || prepared.Seqs[0] != 1 || prepared.Seqs[1] != 2 {
		t.Fatalf("prepared = %+v", prepared)
	}
	stats := state.ApplyValidated(prepared.Writes)
	if stats.MessageCount != 2 {
		t.Fatalf("stats = %+v", stats)
	}
	if state.GetNextSeq() != 3 {
		t.Fatalf("nextSeq = %d", state.GetNextSeq())
	}

	// Duplicate id rejects.
	_, err = state.PrepareCommit([]Write{InsertEntry(root)}, timestamp)
	if err == nil || !strings.Contains(err.Error(), "Duplicate entry or usage id") {
		t.Fatalf("err = %v", err)
	}
	// Missing parent rejects (absent everywhere).
	orphan := messageEntry("e3", strptr("missing"), "user")
	_, err = state.PrepareCommit([]Write{InsertEntry(orphan)}, timestamp)
	if err == nil || !strings.Contains(err.Error(), "Missing parent entry") {
		t.Fatalf("err = %v", err)
	}
	// Parent earlier in the same transaction is accepted.
	parent := messageEntry("e3", strptr("e2"), "user")
	okChild := messageEntry("e4", strptr("e3"), "user")
	prepared2, err := state.PrepareCommit([]Write{InsertEntry(parent), InsertEntry(okChild)}, timestamp)
	if err != nil {
		t.Fatal(err)
	}
	state.ApplyValidated(prepared2.Writes)
}

func TestCommitUsageSharesIDNamespace(t *testing.T) {
	state := newTestState()
	root := messageEntry("shared-id", nil, "user")
	prepared, err := state.PrepareCommit([]Write{InsertEntry(root)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	state.ApplyValidated(prepared.Writes)
	usage := UsageRow{ID: "shared-id", Usage: zeroUsage()}
	_, err = state.PrepareCommit([]Write{InsertUsage(usage)}, 2)
	if err == nil || !strings.Contains(err.Error(), "Duplicate entry or usage id") {
		t.Fatalf("err = %v", err)
	}
}

func TestValueSetReplaceDeleteRecreate(t *testing.T) {
	state := newTestState()
	address := NewValue("app", "counter")
	ts := 1.0
	for _, v := range []float64{1, 2, 3} {
		prepared, _ := state.PrepareCommit([]Write{WriteFromValue(SetValue(address, v))}, ts)
		state.ApplyValidated(prepared.Writes)
	}
	stored, _ := state.GetValue(address)
	if stored.Value != float64(3) || stored.Seq != 3 {
		t.Fatalf("stored = %+v", stored)
	}
	// Delete then recreate.
	prepared, _ := state.PrepareCommit([]Write{WriteFromValue(DeleteValue(address))}, ts)
	state.ApplyValidated(prepared.Writes)
	stored, _ = state.GetValue(address)
	if stored != nil {
		t.Fatalf("stored after delete = %+v", stored)
	}
	prepared, _ = state.PrepareCommit([]Write{WriteFromValue(SetValue(address, float64(9)))}, ts)
	state.ApplyValidated(prepared.Writes)
	stored, _ = state.GetValue(address)
	if stored.Value != float64(9) {
		t.Fatalf("recreated = %+v", stored)
	}
}

func TestListPaginationAndAtomicDelete(t *testing.T) {
	state := newTestState()
	address := NewList("app", "events")
	ts := 1.0
	for i := 1; i <= 5; i++ {
		prepared, _ := state.PrepareCommit([]Write{WriteFromList(AppendList(address, float64(i)))}, ts)
		state.ApplyValidated(prepared.Writes)
	}
	all := state.ReadList(address, nil)
	if len(all) != 5 {
		t.Fatalf("all = %d", len(all))
	}
	cursor := ListCursor{Seq: all[2].Seq}
	page := state.ReadList(address, &ListReadOptions{Cursor: &cursor})
	if len(page) != 2 || page[0].Value != float64(4) {
		t.Fatalf("page = %+v", page)
	}
	desc := state.ReadList(address, &ListReadOptions{Order: "desc"})
	if len(desc) != 5 || desc[0].Value != float64(5) {
		t.Fatalf("desc = %+v", desc)
	}
	// Atomic delete + re-append.
	prepared, _ := state.PrepareCommit([]Write{WriteFromList(DeleteList(address)), WriteFromList(AppendList(address, "fresh"))}, ts)
	state.ApplyValidated(prepared.Writes)
	after := state.ReadList(address, nil)
	if len(after) != 1 || after[0].Value != "fresh" {
		t.Fatalf("after = %+v", after)
	}
}

func TestScanBranchAndEntries(t *testing.T) {
	state := newTestState()
	ts := 1.0
	// Chain: e1 -> e2 -> e3.
	prepared, _ := state.PrepareCommit([]Write{InsertEntry(messageEntry("e1", nil, "user"))}, ts)
	state.ApplyValidated(prepared.Writes)
	prepared, _ = state.PrepareCommit([]Write{InsertEntry(messageEntry("e2", strptr("e1"), "assistant"))}, ts)
	state.ApplyValidated(prepared.Writes)
	prepared, _ = state.PrepareCommit([]Write{InsertEntry(messageEntry("e3", strptr("e2"), "user"))}, ts)
	state.ApplyValidated(prepared.Writes)

	tipValue, _ := state.GetValue(BranchTip("main"))
	if tipValue == nil {
		// branch tip not set in this test; scan from e3 directly.
	}
	entries, err := state.ScanBranch(StorageBranchScan{BranchScan: BranchScan{Order: "newestFirst"}, StartID: "e3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].ID != "e3" || entries[2].ID != "e1" {
		t.Fatalf("branch = %+v", entries)
	}
	// Stop-at-type.
	entries, err = state.ScanBranch(StorageBranchScan{BranchScan: BranchScan{StopAtType: EntryTypeMessage, Order: "newestFirst"}, StartID: "e3"})
	if err != nil || len(entries) != 1 {
		t.Fatalf("stopped = %+v err = %v", entries, err)
	}
	// scanEntries desc limit 2.
	limit := int64(2)
	scan := state.ScanEntries(EntryScan{Order: "desc", Limit: &limit})
	if len(scan) != 2 || scan[0].ID != "e3" {
		t.Fatalf("scan = %+v", scan)
	}
}

func strptr(s string) *string { return &s }

func zeroUsage() (u aiUsageAlias) { return }
