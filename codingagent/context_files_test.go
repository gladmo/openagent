package codingagent

// Tests for git discovery and project-context loading (ported from
// resource-loader context-file surfaces).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindGitPathsOrdinaryRepo(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	os.MkdirAll(filepath.Join(repo, ".git", "refs"), 0o755)
	os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)

	// From a nested dir, discovery walks up.
	nested := filepath.Join(repo, "a", "b")
	os.MkdirAll(nested, 0o755)
	paths := FindGitPaths(nested)
	if paths == nil {
		t.Fatal("no repo found")
	}
	if paths.RepoDir != repo {
		t.Fatalf("repo = %q", paths.RepoDir)
	}
	if paths.CommonGitDir != filepath.Join(repo, ".git") {
		t.Fatalf("common = %q", paths.CommonGitDir)
	}
}

func TestFindGitPathsWorktreeFile(t *testing.T) {
	dir := t.TempDir()
	mainRepo := filepath.Join(dir, "main")
	os.MkdirAll(filepath.Join(mainRepo, ".git"), 0o755)
	os.WriteFile(filepath.Join(mainRepo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)

	// Linked worktree: .git is a file pointing at the main repo's gitdir.
	worktree := filepath.Join(mainRepo, "nested-worktree")
	os.MkdirAll(worktree, 0o755)
	gitDir := filepath.Join(mainRepo, ".git", "worktrees", "nested")
	os.MkdirAll(gitDir, 0o755)
	os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/feat\n"), 0o644)
	os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../../\n"), 0o644)
	os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644)

	paths := FindGitPaths(worktree)
	if paths == nil {
		t.Fatal("worktree not found")
	}
	if paths.RepoDir != worktree {
		t.Fatalf("repo = %q", paths.RepoDir)
	}
	if paths.CommonGitDir != filepath.Join(mainRepo, ".git") {
		t.Fatalf("common = %q", paths.CommonGitDir)
	}
}

func TestFindGitPathsNone(t *testing.T) {
	if paths := FindGitPaths(t.TempDir()); paths != nil {
		t.Fatalf("found = %+v", paths)
	}
}

func TestLoadProjectContextFilesGlobalAndCwd(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "agent")
	cwd := filepath.Join(base, "work")
	os.MkdirAll(agentDir, 0o755)
	os.MkdirAll(cwd, 0o755)

	os.WriteFile(filepath.Join(agentDir, "AGENTS.md"), []byte("global rules"), 0o644)
	os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("project rules"), 0o644)

	files := LoadProjectContextFiles(cwd, agentDir)
	if len(files) != 2 {
		t.Fatalf("files = %d", len(files))
	}
	if files[0].Content != "global rules" {
		t.Fatalf("global first = %q", files[0].Content)
	}
	if files[1].Content != "project rules" {
		t.Fatalf("project = %q", files[1].Content)
	}
}

func TestLoadProjectContextFilesPrecedence(t *testing.T) {
	base := t.TempDir()
	cwd := filepath.Join(base, "work")
	os.MkdirAll(cwd, 0o755)

	// AGENTS.md wins over CLAUDE.md.
	os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("agents"), 0o644)
	os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("claude"), 0o644)

	files := LoadProjectContextFiles(cwd, base)
	if len(files) != 1 || files[0].Content != "agents" {
		t.Fatalf("precedence = %+v", files)
	}

	// AGENTS.override.md wins over AGENTS.md.
	os.WriteFile(filepath.Join(cwd, "AGENTS.override.md"), []byte("override"), 0o644)
	files = LoadProjectContextFiles(cwd, base)
	if len(files) != 1 || files[0].Content != "override" {
		t.Fatalf("override = %+v", files)
	}
}

func TestLoadProjectContextFilesAncestorWalk(t *testing.T) {
	base := t.TempDir()
	agentDir := filepath.Join(base, "agent")
	os.MkdirAll(agentDir, 0o755)

	// Ancestors from root-out: parent then child.
	parent := filepath.Join(base, "parent")
	child := filepath.Join(parent, "child")
	os.MkdirAll(child, 0o755)
	os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("parent rules"), 0o644)
	os.WriteFile(filepath.Join(child, "AGENTS.md"), []byte("child rules"), 0o644)

	files := LoadProjectContextFiles(child, agentDir)
	if len(files) != 2 {
		t.Fatalf("files = %d", len(files))
	}
	if files[0].Content != "parent rules" {
		t.Fatalf("order = %q then %q", files[0].Content, files[1].Content)
	}
}

func TestLoadProjectContextFilesWorktreeShadowing(t *testing.T) {
	dir := t.TempDir()
	mainRepo := filepath.Join(dir, "main")
	os.MkdirAll(filepath.Join(mainRepo, ".git"), 0o755)
	os.WriteFile(filepath.Join(mainRepo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
	os.WriteFile(filepath.Join(mainRepo, "AGENTS.md"), []byte("main rules"), 0o644)

	// Nested linked worktree under the main repo.
	worktree := filepath.Join(mainRepo, "feature")
	os.MkdirAll(worktree, 0o755)
	gitDir := filepath.Join(mainRepo, ".git", "worktrees", "feature")
	os.MkdirAll(gitDir, 0o755)
	os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/feat\n"), 0o644)
	os.WriteFile(filepath.Join(gitDir, "commondir"), []byte("../../\n"), 0o644)
	os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644)
	os.WriteFile(filepath.Join(worktree, "AGENTS.md"), []byte("worktree rules"), 0o644)

	agentDir := filepath.Join(dir, "agent")
	os.MkdirAll(agentDir, 0o755)

	files := LoadProjectContextFiles(worktree, agentDir)
	// The main repo's copy shadows (the worktree file IS the shadowed copy's
	// ancestor context): only the worktree's own file loads.
	if len(files) != 1 {
		t.Fatalf("files = %d (%+v)", len(files), files)
	}
	if files[0].Content != "worktree rules" {
		t.Fatalf("content = %q", files[0].Content)
	}
}

func TestLoadProjectContextFilesBom(t *testing.T) {
	base := t.TempDir()
	cwd := filepath.Join(base, "work")
	os.MkdirAll(cwd, 0o755)
	os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("\uFEFFbom-stripped"), 0o644)

	files := LoadProjectContextFiles(cwd, base)
	if len(files) != 1 || files[0].Content != "bom-stripped" {
		t.Fatalf("bom = %+v", files)
	}
	_ = strings.TrimSpace
}
