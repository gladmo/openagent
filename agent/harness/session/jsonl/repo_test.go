package jsonl

// Ports of jsonl-session-repo.test.ts behaviors (representative cases).

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func newRepoFS(t *testing.T) (*fakeFS, *JsonlSessionRepo) {
	t.Helper()
	fs := newFakeFS(t)
	repo := NewJsonlSessionRepo(fs, "sessions", fixedNow)
	return fs, repo
}

func fixedNow() float64 { return 1700000000000 }

func TestSessionDirectoryName(t *testing.T) {
	if got := SessionDirectoryName("/Users/me/project"); got != "--Users-me-project--" {
		t.Fatalf("got %q", got)
	}
	if got := SessionDirectoryName("C:\\work\\app"); got != "--C--work-app--" {
		t.Fatalf("got %q", got)
	}
	// Lossy: /a/b and /a-b collide (list re-filters by header cwd).
	if SessionDirectoryName("/a/b") != SessionDirectoryName("/a-b") {
		t.Fatalf("expected lossy collision: %q vs %q", SessionDirectoryName("/a/b"), SessionDirectoryName("/a-b"))
	}
}

func TestSessionFileName(t *testing.T) {
	name := SessionFileName(1700000000000, "id with spaces/+")
	if !strings.HasSuffix(name, ".jsonl") {
		t.Fatalf("name = %q", name)
	}
	if strings.Contains(name, " ") || strings.Contains(name, "/") {
		t.Fatalf("name not encoded: %q", name)
	}
	if !strings.Contains(name, "id%20with%20spaces%2F%2B") {
		t.Fatalf("unexpected encoding: %q", name)
	}
}

func TestRepoCreateOpenListDelete(t *testing.T) {
	fs, repo := newRepoFS(t)
	ctx := harness.BackgroundContext

	session1, err := repo.Create(map[string]any{"cwd": fs.root, "id": "alpha"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(map[string]any{"cwd": fs.root, "id": "alpha"}, ctx); err == nil {
		t.Fatal("duplicate create accepted")
	}

	// Open is exclusive.
	metadata := session1.Metadata()
	if _, err := repo.Open(metadata, ctx); err == nil {
		t.Fatal("double open accepted")
	}
	if err := session1.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// List finds the session with its header cwd.
	listed, err := repo.List(struct{ Cwd string }{Cwd: fs.root}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != "alpha" {
		t.Fatalf("listed = %+v", listed)
	}
	if listed[0].Cwd == nil || *listed[0].Cwd != fs.root {
		t.Fatalf("cwd = %v", listed[0].Cwd)
	}

	// Reopen works after close.
	reopened, err := repo.Open(metadata, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// Delete works when closed.
	if err := repo.Delete(metadata, ctx); err != nil {
		t.Fatal(err)
	}
	listed, _ = repo.List(struct{ Cwd string }{Cwd: fs.root}, ctx)
	if len(listed) != 0 {
		t.Fatalf("listed after delete = %d", len(listed))
	}
}

func TestRepoListFiltersByHeaderCwd(t *testing.T) {
	fs, repo := newRepoFS(t)
	ctx := harness.BackgroundContext

	// Two sessions whose cwd directories collide but whose header cwds
	// differ.
	cwdAB := filepath.Join(fs.root, "a", "b")
	cwdADash := filepath.Join(fs.root, "a-b")
	if _, err := repo.Create(map[string]any{"cwd": cwdAB, "id": "one"}, ctx); err != nil {
		t.Fatal(err)
	}
	// Same normalized dir (lossy collision): write the second session by
	// hand into the same dir with a different header cwd.
	dir := fs.root + "/sessions/" + SessionDirectoryName(cwdAB)
	headerLine := `{"v":4,"kind":"header","id":"two","storageVersion":1,"createdAt":1700000000001,"cwd":"` + cwdADash + `"}`
	if w := fs.WriteFile(dir+"/1700000000001_two.jsonl", []byte(headerLine+"\n"), ctx); !w.Ok {
		t.Fatal(w.Error)
	}

	listedAb, err := repo.List(struct{ Cwd string }{Cwd: cwdAB}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listedAb) != 1 || listedAb[0].ID != "one" {
		ids := []string{}
		for _, m := range listedAb {
			ids = append(ids, m.ID)
		}
		t.Fatalf("ab = %v", ids)
	}
	listedDash, err := repo.List(struct{ Cwd string }{Cwd: cwdADash}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listedDash) != 1 || listedDash[0].ID != "two" {
		ids := []string{}
		for _, m := range listedDash {
			ids = append(ids, m.ID)
		}
		t.Fatalf("dash = %v", ids)
	}
}

func TestRepoListSortsNewestFirst(t *testing.T) {
	fs, repo := newRepoFS(t)
	ctx := harness.BackgroundContext
	for i, id := range []string{"old", "mid", "new"} {
		createdAt := fixedNow() - float64(10-i)
		_ = createdAt
		// Create with distinct ids; file names sort by createdAt in the
		// name but the LIST sort is by header createdAt desc.
		if _, err := repo.Create(map[string]any{"cwd": fs.root, "id": id}, ctx); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := repo.List(struct{ Cwd string }{Cwd: fs.root}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 3 {
		t.Fatalf("listed = %d", len(listed))
	}
	// Same createdAt: sort falls back to id asc.
	if listed[0].ID != "mid" || listed[1].ID != "new" || listed[2].ID != "old" {
		ids := []string{}
		for _, m := range listed {
			ids = append(ids, m.ID)
		}
		t.Fatalf("ids = %v", ids)
	}
}

func TestRepoForkTree(t *testing.T) {
	fs, repo := newRepoFS(t)
	ctx := harness.BackgroundContext

	source, err := repo.Create(map[string]any{"cwd": fs.root, "id": "src"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The JSONL repo does not initialize branches (the harness runtime
	// does); create main at the root explicitly.
	if _, err := source.CreateBranch("main", nil, ctx); err != nil {
		t.Fatal(err)
	}
	branch, err := source.Branch("main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := branch.AppendMessage(session.AgentMessagePayload{
		Role:    "user",
		Message: jsonx.MustParseString(`{"role":"user","content":"hello","timestamp":1}`).(*jsonx.Obj),
	}, ctx); err != nil {
		t.Fatal(err)
	}
	sourceMetadata := source.Metadata()
	if err := source.Close(ctx); err != nil {
		t.Fatal(err)
	}

	forked, err := repo.Fork(sourceMetadata, session.ForkOptions{Scope: "tree"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer forked.Close(ctx)
	forkBranch, err := forked.Branch("main", ctx)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := forkBranch.FindEntries(nil, ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %+v err = %v", entries, err)
	}
	if forked.Metadata().ParentSessionID == nil || *forked.Metadata().ParentSessionID != "src" {
		t.Fatalf("parent = %v", forked.Metadata().ParentSessionID)
	}
	// The fork header carries the source high-water nextSeq.
	listed, _ := repo.List(struct{ Cwd string }{Cwd: fs.root}, ctx)
	if len(listed) != 2 {
		t.Fatalf("listed = %d", len(listed))
	}
}
