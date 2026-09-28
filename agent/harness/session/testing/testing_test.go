package sessiontesting

// Ports of instrumented-storage / gating-storage test behaviors plus a
// storage conformance suite runner over the in-memory backend.

import (
	"sync"
	"testing"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
)

func memStorage(t *testing.T) session.Storage {
	t.Helper()
	return session.NewMemoryStorage()
}

func userWrite(id string) session.Write {
	entry := &session.Entry{
		EntryBase: session.EntryBase{ID: id, Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "user"},
	}
	return session.InsertEntry(entry)
}

func TestInstrumentedStorageRecords(t *testing.T) {
	inner := memStorage(t)
	instrumented := NewInstrumentedStorage(inner)
	ctx := harness.BackgroundContext
	if _, err := instrumented.Commit([]session.Write{userWrite("e1")}, ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := instrumented.Commit([]session.Write{userWrite("e2")}, ctx); err != nil {
		t.Fatal(err)
	}
	attempts := instrumented.GetCommitAttempts()
	if len(attempts) != 2 || attempts[0][0].Entry.ID != "e1" || attempts[1][0].Entry.ID != "e2" {
		t.Fatalf("attempts = %d", len(attempts))
	}
	instrumented.ClearCommitAttempts()
	if len(instrumented.GetCommitAttempts()) != 0 {
		t.Fatal("clear failed")
	}
	// Reads pass through.
	if _, err := instrumented.GetEntries([]string{"e1"}, ctx); err != nil {
		t.Fatal(err)
	}
}

func TestGatingStorageParksAndReleases(t *testing.T) {
	inner := memStorage(t)
	gating := NewGatingStorage(inner)
	ctx := harness.BackgroundContext

	// Before arming, commits pass through.
	if _, err := gating.Commit([]session.Write{userWrite("seed")}, ctx); err != nil {
		t.Fatal(err)
	}

	gating.Arm()

	var wg sync.WaitGroup
	results := make([]error, 3)
	for i := 0; i < 3; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := gating.Commit([]session.Write{userWrite(parkedID(i))}, ctx)
			results[i] = err
		}()
	}
	if err := gating.WaitPending(3); err != nil {
		t.Fatal(err)
	}
	if gating.Pending() != 3 {
		t.Fatalf("pending = %d", gating.Pending())
	}
	// FIFO release: first two land, third stays parked.
	if err := gating.Next(2); err != nil {
		t.Fatal(err)
	}
	if gating.Pending() != 1 {
		t.Fatalf("pending after next(2) = %d", gating.Pending())
	}
	if err := gating.Next(1); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for i, err := range results {
		if err != nil {
			t.Fatalf("commit[%d] err = %v", i, err)
		}
	}
	// All entries landed.
	entries, err := inner.GetEntries([]string{parkedID(0), parkedID(1), parkedID(2)}, ctx)
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %d err = %v", len(entries), err)
	}
}

func parkedID(i int) string { return []string{"p1", "p2", "p3"}[i] }

func TestGatingStorageDiscard(t *testing.T) {
	inner := memStorage(t)
	gating := NewGatingStorage(inner)
	ctx := harness.BackgroundContext
	gating.Arm()

	errs := make(chan error, 1)
	go func() {
		_, err := gating.Commit([]session.Write{userWrite("doomed")}, ctx)
		errs <- err
	}()
	if err := gating.WaitPending(1); err != nil {
		t.Fatal(err)
	}
	gating.Discard()
	err := <-errs
	if err == nil {
		t.Fatal("parked commit not rejected")
	}
	if _, ok := err.(*CommitDiscarded); !ok {
		t.Fatalf("err type %T", err)
	}
	// Later commits reject immediately.
	if _, err := gating.Commit([]session.Write{userWrite("after")}, ctx); err == nil {
		t.Fatal("post-discard commit accepted")
	}
	// WaitPending rejects after discard.
	if err := gating.WaitPending(1); err == nil {
		t.Fatal("waitPending accepted after discard")
	}
}

// memFixture adapts MemoryStorage into a StorageFixture.
type memFixture struct{ storage session.Storage }

func (f *memFixture) Storage() session.Storage { return f.storage }
func (f *memFixture) Dispose() error {
	return f.storage.Close(harness.BackgroundContext)
}

// NewMemoryFixture is the conformance factory for the in-memory backend.
func NewMemoryFixture() (StorageFixture, error) {
	return &memFixture{storage: session.NewMemoryStorage()}, nil
}

// CreateStorageConformance mirrors the storage conformance suite: value
// semantics, list semantics, entry trees, usage ledger, back-to-back
// serialization, close idempotency.
func CreateStorageConformance(factory func() (StorageFixture, error)) []ConformanceCase {
	cases := []ConformanceCase{}
	add := func(group, name string, run func(fixture StorageFixture) error) {
		cases = append(cases, ConformanceCase{
			Group: group, Name: name,
			Run: func() error {
				fixture, err := factory()
				if err != nil {
					return err
				}
				defer fixture.Dispose()
				return run(fixture)
			},
		})
	}
	ctx := harness.BackgroundContext

	add("values", "set replace delete recreate", func(f StorageFixture) error {
		s := f.Storage()
		address := session.NewValue("app", "k")
		for _, v := range []float64{1, 2} {
			if _, err := s.Commit([]session.Write{session.WriteFromValue(session.SetValue(address, v))}, ctx); err != nil {
				return err
			}
		}
		stored, err := s.GetValue(address, ctx)
		if err != nil || stored == nil || stored.Value != float64(2) {
			return errValueRoundTrip(err)
		}
		if _, err := s.Commit([]session.Write{session.WriteFromValue(session.DeleteValue(address))}, ctx); err != nil {
			return err
		}
		stored, _ = s.GetValue(address, ctx)
		if stored != nil {
			return errValueNotDeleted()
		}
		if _, err := s.Commit([]session.Write{session.WriteFromValue(session.SetValue(address, "recreated"))}, ctx); err != nil {
			return err
		}
		stored, _ = s.GetValue(address, ctx)
		if stored == nil || stored.Value != "recreated" {
			return errValueRoundTrip(nil)
		}
		return nil
	})

	add("lists", "pagination cursors and atomic delete+reappend", func(f StorageFixture) error {
		s := f.Storage()
		address := session.NewList("app", "items")
		for i := 1; i <= 5; i++ {
			if _, err := s.Commit([]session.Write{session.WriteFromList(session.AppendList(address, float64(i)))}, ctx); err != nil {
				return err
			}
		}
		all, err := s.ReadList(address, nil, ctx)
		if err != nil || len(all) != 5 {
			return errListRoundTrip(err)
		}
		cursor := session.ListCursor{Seq: all[1].Seq}
		page, err := s.ReadList(address, &session.ListReadOptions{Cursor: &cursor}, ctx)
		if err != nil || len(page) != 3 {
			return errListRoundTrip(err)
		}
		desc, err := s.ReadList(address, &session.ListReadOptions{Order: "desc"}, ctx)
		if err != nil || len(desc) != 5 || desc[0].Value != float64(5) {
			return errListRoundTrip(err)
		}
		// Atomic delete + reappend in one transaction.
		if _, err := s.Commit([]session.Write{
			session.WriteFromList(session.DeleteList(address)),
			session.WriteFromList(session.AppendList(address, "fresh")),
		}, ctx); err != nil {
			return err
		}
		after, err := s.ReadList(address, nil, ctx)
		if err != nil || len(after) != 1 || after[0].Value != "fresh" {
			return errListRoundTrip(err)
		}
		return nil
	})

	add("entries", "parent resolution and duplicate id rejection", func(f StorageFixture) error {
		s := f.Storage()
		root := &session.Entry{EntryBase: session.EntryBase{ID: "r1", Type: session.EntryTypeMessage}, Message: session.AgentMessagePayload{Role: "user"}}
		child := &session.Entry{EntryBase: session.EntryBase{ID: "c1", ParentID: strPtr("r1"), Type: session.EntryTypeMessage}, Message: session.AgentMessagePayload{Role: "assistant"}}
		if _, err := s.Commit([]session.Write{session.InsertEntry(root), session.InsertEntry(child)}, ctx); err != nil {
			return err
		}
		// Same-transaction parent resolution worked. Now a duplicate id
		// rejects.
		if _, err := s.Commit([]session.Write{session.InsertEntry(root)}, ctx); err == nil {
			return errDuplicateAccepted()
		}
		// Missing parent rejects.
		orphan := &session.Entry{EntryBase: session.EntryBase{ID: "o1", ParentID: strPtr("missing"), Type: session.EntryTypeMessage}}
		if _, err := s.Commit([]session.Write{session.InsertEntry(orphan)}, ctx); err == nil {
			return errMissingParentAccepted()
		}
		return nil
	})

	add("usage", "ledger rows accumulate into stats", func(f StorageFixture) error {
		s := f.Storage()
		usage := session.UsageRow{ID: "u1", Usage: zeroUsageWith(10, 5)}
		if _, err := s.Commit([]session.Write{session.InsertUsage(usage)}, ctx); err != nil {
			return err
		}
		usage2 := session.UsageRow{ID: "u2", Usage: zeroUsageWith(3, 4)}
		if _, err := s.Commit([]session.Write{session.InsertUsage(usage2)}, ctx); err != nil {
			return err
		}
		rows, err := s.ScanUsage(session.UsageScan{}, ctx)
		if err != nil || len(rows) != 2 {
			return errUsageRoundTrip(err)
		}
		stats, err := s.GetStats(ctx)
		if err != nil || stats.Usage.Input != 13 || stats.Usage.Output != 9 {
			return errUsageTotals(stats)
		}
		return nil
	})

	add("serialization", "back-to-back commits are serialized in admission order", func(f StorageFixture) error {
		s := f.Storage()
		for i := 0; i < 10; i++ {
			entry := &session.Entry{EntryBase: session.EntryBase{ID: seqID(i), Type: session.EntryTypeMessage}, Message: session.AgentMessagePayload{Role: "user"}}
			if _, err := s.Commit([]session.Write{session.InsertEntry(entry)}, ctx); err != nil {
				return err
			}
		}
		entries, err := s.ScanEntries(session.EntryScan{Order: "asc"}, ctx)
		if err != nil || len(entries) != 10 {
			return errSerialization(err)
		}
		for i, entry := range entries {
			if entry.ID != seqID(i) {
				return errSerialization(nil)
			}
		}
		return nil
	})

	add("close", "drains pending and is idempotent", func(f StorageFixture) error {
		s := f.Storage()
		entry := &session.Entry{EntryBase: session.EntryBase{ID: "last", Type: session.EntryTypeMessage}, Message: session.AgentMessagePayload{Role: "user"}}
		if _, err := s.Commit([]session.Write{session.InsertEntry(entry)}, ctx); err != nil {
			return err
		}
		if err := s.Close(ctx); err != nil {
			return err
		}
		// Idempotent.
		if err := s.Close(ctx); err != nil {
			return err
		}
		return nil
	})

	return cases
}

func strPtr(s string) *string { return &s }

func seqID(i int) string {
	return "e" + string(rune('0'+i))
}

func zeroUsageWith(in, out float64) (u harnessUsage) {
	u.Input = in
	u.Output = out
	u.TotalTokens = in + out
	return u
}

type harnessUsage = usageAlias

// error helpers keep the conformance closures readable.
func errValueRoundTrip(err error) error { return wrapConformance("value round trip", err) }
func errValueNotDeleted() error         { return wrapConformance("value not deleted", nil) }
func errListRoundTrip(err error) error  { return wrapConformance("list round trip", err) }
func errDuplicateAccepted() error       { return wrapConformance("duplicate id accepted", nil) }
func errMissingParentAccepted() error   { return wrapConformance("missing parent accepted", nil) }
func errUsageRoundTrip(err error) error { return wrapConformance("usage round trip", err) }
func errUsageTotals(stats session.SessionStats) error {
	return wrapConformance("usage totals mismatch", nil)
}
func errSerialization(err error) error { return wrapConformance("serialization order", err) }

func wrapConformance(what string, err error) error {
	if err != nil {
		return err
	}
	return errConformance(what)
}

type conformanceError struct{ what string }

func (e *conformanceError) Error() string { return "storage conformance failed: " + e.what }

func errConformance(what string) error { return &conformanceError{what} }

func TestMemoryStorageConformance(t *testing.T) {
	cases := CreateStorageConformance(NewMemoryFixture)
	if len(cases) < 6 {
		t.Fatalf("cases = %d", len(cases))
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.Group+"/"+testCase.Name, func(t *testing.T) {
			if err := testCase.Run(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
