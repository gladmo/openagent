package codingagent

// Port of core/footer-data-provider.ts (git discovery) and the
// resource-loader context-file walk: AGENTS/CLAUDE discovery with worktree
// shadowing. The remaining resource-loader subsystem (extension/package
// loading) lands with the extensions phase.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// GitPaths describes the enclosing repository.
type GitPaths struct {
	// RepoDir is the worktree root.
	RepoDir string
	// CommonGitDir is the shared .git dir.
	CommonGitDir string
	// HeadPath is the HEAD file path.
	HeadPath string
}

// FindGitPaths walks up from cwd to locate the enclosing git repository
// (worktree file pointers included); nil when none.
func FindGitPaths(cwd string) *GitPaths {
	dir := cwd
	for {
		gitPath := filepath.Join(dir, ".git")
		if info, err := os.Stat(gitPath); err == nil {
			if info.Mode().IsRegular() {
				content, err := os.ReadFile(gitPath)
				if err == nil {
					trimmed := strings.TrimSpace(string(content))
					if strings.HasPrefix(trimmed, "gitdir: ") {
						gitDir := ResolvePath(strings.TrimSpace(trimmed[len("gitdir: "):]), dir, PathInputOptions{})
						headPath := filepath.Join(gitDir, "HEAD")
						if _, err := os.Stat(headPath); err != nil {
							return nil
						}
						commonDirPath := filepath.Join(gitDir, "commondir")
						commonGitDir := gitDir
						if raw, err := os.ReadFile(commonDirPath); err == nil {
							commonGitDir = ResolvePath(strings.TrimSpace(string(raw)), gitDir, PathInputOptions{})
						}
						return &GitPaths{RepoDir: dir, CommonGitDir: commonGitDir, HeadPath: headPath}
					}
				}
			} else if info.IsDir() {
				headPath := filepath.Join(gitPath, "HEAD")
				if _, err := os.Stat(headPath); err != nil {
					return nil
				}
				return &GitPaths{RepoDir: dir, CommonGitDir: gitPath, HeadPath: headPath}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
		dir = parent
	}
}

// ContextFile is one loaded project instruction file.
type ContextFileWithPath struct {
	Path    string
	Content string
}

// contextFileCandidates in discovery order.
var contextFileCandidates = []string{
	"AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD",
}

// loadContextFileFromDir finds the first context file in dir.
func loadContextFileFromDir(dir string) *ContextFileWithPath {
	for _, filename := range contextFileCandidates {
		filePath := filepath.Join(dir, filename)
		info, err := os.Stat(filePath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		raw, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}
		return &ContextFileWithPath{Path: filePath, Content: StripBom(string(raw))}
	}
	return nil
}

// findShadowedContextFile returns the main-repo context file a nested
// linked worktree's own copy shadows ("" when nothing is shadowed).
// Canonicalized because git writes gitdir targets in realpath form while
// cwd may still be symlinked.
func findShadowedContextFile(cwd string) string {
	gitPaths := FindGitPaths(cwd)
	if gitPaths == nil {
		return ""
	}
	commonGitDir := CanonicalizePath(gitPaths.CommonGitDir)
	worktreeRoot := CanonicalizePath(gitPaths.RepoDir)
	mainRepoRoot := filepath.Dir(commonGitDir)
	// Ordinary repos (same dir) and sibling worktrees (main repo not an
	// ancestor) do not shadow.
	if !strings.HasPrefix(worktreeRoot, mainRepoRoot+string(os.PathSeparator)) {
		return ""
	}
	// The main worktree root only carries a checkout when its .git dir
	// resolves to the common dir; bare layouts and submodules do not.
	if CanonicalizePath(filepath.Join(mainRepoRoot, ".git")) != commonGitDir {
		return ""
	}
	worktreeContextFile := loadContextFileFromDir(worktreeRoot)
	if worktreeContextFile == nil {
		return ""
	}
	return filepath.Join(mainRepoRoot, filepath.Base(worktreeContextFile.Path))
}

// LoadProjectContextFiles walks the agent dir (global) then cwd's ancestor
// chain (closest-to-root first), shadowed duplicates skipped.
func LoadProjectContextFiles(cwd, agentDir string) []ContextFileWithPath {
	resolvedCwd := ResolvePath(cwd, "", PathInputOptions{})
	resolvedAgentDir := ResolvePath(agentDir, "", PathInputOptions{})

	var contextFiles []ContextFileWithPath
	seenPaths := map[string]bool{}

	globalContext := loadContextFileFromDir(resolvedAgentDir)
	if globalContext != nil {
		contextFiles = append(contextFiles, *globalContext)
		seenPaths[globalContext.Path] = true
	}

	var ancestorContextFiles []ContextFileWithPath

	shadowedContextFile := findShadowedContextFile(resolvedCwd)
	currentDir := resolvedCwd

	for {
		contextFile := loadContextFileFromDir(currentDir)
		if contextFile != nil {
			// Compare the directory's real path, not the file's: a
			// worktree AGENTS.md that symlinks to the main repo's file
			// resolves to the shadowed path itself, so the shadow check
			// would drop the worktree's own copy.
			isShadowed := shadowedContextFile != "" &&
				filepath.Join(CanonicalizePath(currentDir), filepath.Base(contextFile.Path)) == shadowedContextFile
			if !isShadowed && !seenPaths[contextFile.Path] {
				ancestorContextFiles = append([]ContextFileWithPath{*contextFile}, ancestorContextFiles...)
				seenPaths[contextFile.Path] = true
			}
		}

		parentDir := filepath.Dir(currentDir)
		if parentDir == currentDir {
			break
		}
		currentDir = parentDir
	}

	contextFiles = append(contextFiles, ancestorContextFiles...)
	return contextFiles
}

// ResolveBranchWithGit asks git for the current branch ("" on detached
// HEAD or when git is unavailable).
func ResolveBranchWithGit(repoDir string) string {
	cmd := execCommand("git", "--no-optional-locks", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = repoDir
	cmd.Stdin = nil
	cmd.Stderr = nil
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// IsWSLEnvironment reports WSL for git-HEAD polling decisions.
func IsWSLEnvironment() bool {
	return runtimeGOOS() == "linux" && (osGetenv("WSL_DISTRO_NAME") != "" || osGetenv("WSL_INTEROP") != "")
}

var windowsMountedRepoPattern = regexp.MustCompile(`(?i)^/mnt/[a-z](/|$)`)

// IsWindowsMountedRepoPath reports /mnt/<drive> paths under WSL.
func IsWindowsMountedRepoPath(repoDir string) bool {
	return windowsMountedRepoPattern.MatchString(repoDir)
}

// ShouldPollGitHead decides between polling and symbolic-ref.
func ShouldPollGitHead(repoDir string) bool {
	return IsWSLEnvironment() && IsWindowsMountedRepoPath(repoDir)
}
