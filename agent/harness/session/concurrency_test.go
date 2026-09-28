package session

// concurrency_test.go pins the storage concurrency contracts: reads
// concurrent with commits stay race-free, Commit after Close fails with an
// error instead of panicking, branch-scope forks walk to the root, and a
// memory-repo reopen restores the recorded state.

import (
	"strings"
	"sync"
	"testing"
)

func TestConcurrentReadsDuringCommits(t *testing.T) {
	storage := NewMemoryStorage()
	branch := &StorageBackedSession{storage: storage}
	_ = branch
	writer := func(n int, done chan struct{}) {
		defer close(done)
		for i := 0; i < n; i++ {
			write := InsertEntry(&Entry{
				EntryBase: EntryBase{ID: entryIDLetter(i), Type: EntryTypeMessage, Seq: int64(i + 1)},
				Message:   userPayload("m"),
			})
			if _, err := storage.Commit([]Write{write}, BackgroundContext); err != nil {
				t.Errorf("commit %d: %v", i, err)
				return
			}
		}
	}
	reader := func(stop chan struct{}, done chan struct{}) {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = storage.GetEntries([]string{"a"}, BackgroundContext)
			_, _ = storage.GetValue(BranchTip("main"), BackgroundContext)
			_, _ = storage.ScanValues(BranchTip("main"), BackgroundContext)
			_, _ = storage.ScanEntries(EntryScan{}, BackgroundContext)
			_, _ = storage.GetStats(BackgroundContext)
		}
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			reader(stop, make(chan struct{}))
		}()
	}
	writeDone := make(chan struct{})
	go writer(200, writeDone)
	<-writeDone
	close(stop)
	readers.Wait()
}

func entryIDLetter(i int) string { return string(rune('a'+i%26)) + "-" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := ""
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return digits
}

func TestCommitAfterCloseErrors(t *testing.T) {
	storage := NewMemoryStorage()
	write := InsertEntry(&Entry{
		EntryBase: EntryBase{ID: "e1", Type: EntryTypeMessage, Seq: 1},
		Message:   userPayload("m"),
	})
	if _, err := storage.Commit([]Write{write}, BackgroundContext); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := storage.Close(BackgroundContext); err != nil {
		t.Fatalf("close: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Commit after Close panicked: %v", r)
		}
	}()
	if _, err := storage.Commit([]Write{write}, BackgroundContext); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("err = %v, want closed error", err)
	}
}

// A Commit racing Close must resolve to commit-or-error, never a panic and
// never a send on the closed queue.
func TestCommitRacingClose(t *testing.T) {
	for iteration := 0; iteration < 50; iteration++ {
		storage := NewMemoryStorage()
		write := InsertEntry(&Entry{
			EntryBase: EntryBase{ID: "e1", Type: EntryTypeMessage, Seq: 1},
			Message:   userPayload("m"),
		})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("panic: %v", r)
						}
					}()
					_, _ = storage.Commit([]Write{write}, BackgroundContext)
				}()
			}
		}()
		go func() {
			defer wg.Done()
			_ = storage.Close(BackgroundContext)
		}()
		wg.Wait()
	}
}

func TestBranchScopeForkWalksToRoot(t *testing.T) {
	repo := NewMemorySessionRepo()
	source, err := repo.Create(map[string]any{"id": "src"}, BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	branch, _ := source.Branch("main", BackgroundContext)
	if _, err := branch.AppendMessage(userPayload("one"), BackgroundContext); err != nil {
		t.Fatal(err)
	}
	if _, err := branch.AppendMessage(userPayload("two"), BackgroundContext); err != nil {
		t.Fatal(err)
	}
	// selectForkPlan requires the source branch to be a configured lane.
	if err := source.SetValue(LaneConfig("main"), mustObj(`{"model":{}}`), BackgroundContext); err != nil {
		t.Fatal(err)
	}
	if err := source.SetValue(LaneStateValue("main"), mustObj(`{"currentOperationId":null}`), BackgroundContext); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(BackgroundContext); err != nil {
		t.Fatal(err)
	}
	forked, err := repo.Fork(SessionMetadata{ID: "src"}, ForkOptions{Scope: "branch", Branch: "main"}, BackgroundContext)
	if err != nil {
		t.Fatalf("branch fork failed: %v", err)
	}
	forkBranch, err := forked.Branch("main", BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := forkBranch.FindEntries(nil, BackgroundContext)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v err = %v", entries, err)
	}
}

func TestMemoryRepoReopenKeepsState(t *testing.T) {
	repo := NewMemorySessionRepo()
	session, err := repo.Create(map[string]any{"id": "s1"}, BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	branch, _ := session.Branch("main", BackgroundContext)
	if _, err := branch.AppendMessage(userPayload("persisted"), BackgroundContext); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(BackgroundContext); err != nil {
		t.Fatal(err)
	}
	reopened, err := repo.Open(SessionMetadata{ID: "s1"}, BackgroundContext)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	reopenedBranch, err := reopened.Branch("main", BackgroundContext)
	if err != nil {
		t.Fatalf("branch after reopen: %v", err)
	}
	entries, err := reopenedBranch.FindEntries(nil, BackgroundContext)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries after reopen = %+v err = %v", entries, err)
	}
	// The reopened session accepts appends (tip intact).
	if _, err := reopenedBranch.AppendMessage(userPayload("more"), BackgroundContext); err != nil {
		t.Fatalf("append after reopen: %v", err)
	}
}
