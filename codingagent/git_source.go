package codingagent

// Port of utils/git.ts: git source URL parsing (scp-like, https, bare
// host/path forms) with ref splitting and install-path safety validation.

import (
	"net/url"
	"regexp"
	"strings"
)

// GitSource is a parsed git URL.
type GitSource struct {
	// Repo is the clone URL (no ref suffix).
	Repo string
	// Host is the git domain (e.g., "github.com").
	Host string
	// Path is the repository path (e.g., "user/repo").
	Path string
	// Ref is the branch/tag/commit when specified.
	Ref string
	// Pinned reports an explicit ref (no auto-update).
	Pinned bool
}

type gitSplit struct {
	repo string
	ref  string
}

var scpLikePattern = regexp.MustCompile(`^git@([^:]+):(.+)$`)

// splitRef separates repo from @ref across the three URL forms.
func splitRef(rawURL string) gitSplit {
	if match := scpLikePattern.FindStringSubmatch(rawURL); match != nil {
		pathWithMaybeRef := match[2]
		refSeparator := strings.IndexByte(pathWithMaybeRef, '@')
		if refSeparator < 0 {
			return gitSplit{repo: rawURL}
		}
		repoPath := pathWithMaybeRef[:refSeparator]
		ref := pathWithMaybeRef[refSeparator+1:]
		if repoPath == "" || ref == "" {
			return gitSplit{repo: rawURL}
		}
		return gitSplit{repo: "git@" + match[1] + ":" + repoPath, ref: ref}
	}

	if strings.Contains(rawURL, "://") {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return gitSplit{repo: rawURL}
		}
		pathWithMaybeRef := strings.TrimLeft(parsed.Path, "/")
		refSeparator := strings.IndexByte(pathWithMaybeRef, '@')
		if refSeparator < 0 {
			return gitSplit{repo: rawURL}
		}
		repoPath := pathWithMaybeRef[:refSeparator]
		ref := pathWithMaybeRef[refSeparator+1:]
		if repoPath == "" || ref == "" {
			return gitSplit{repo: rawURL}
		}
		parsed.Path = "/" + repoPath
		repo := strings.TrimSuffix(parsed.String(), "/")
		return gitSplit{repo: repo, ref: ref}
	}

	slashIndex := strings.IndexByte(rawURL, '/')
	if slashIndex < 0 {
		return gitSplit{repo: rawURL}
	}
	host := rawURL[:slashIndex]
	pathWithMaybeRef := rawURL[slashIndex+1:]
	refSeparator := strings.IndexByte(pathWithMaybeRef, '@')
	if refSeparator < 0 {
		return gitSplit{repo: rawURL}
	}
	repoPath := pathWithMaybeRef[:refSeparator]
	ref := pathWithMaybeRef[refSeparator+1:]
	if repoPath == "" || ref == "" {
		return gitSplit{repo: rawURL}
	}
	return gitSplit{repo: host + "/" + repoPath, ref: ref}
}

// decodeForValidation percent-decodes (nil on failure).
func decodeForValidation(value string) (string, bool) {
	decoded, err := url.PathUnescape(value)
	if err != nil {
		return "", false
	}
	return decoded, true
}

// hasUnsafeGitInstallPart rejects traversal, backslash, leading slash, and
// (optionally) embedded slashes.
func hasUnsafeGitInstallPart(value string, allowSlash bool) bool {
	decoded, ok := decodeForValidation(value)
	if !ok {
		return true
	}
	for _, candidate := range []string{value, decoded} {
		if strings.ContainsRune(candidate, 0) || strings.Contains(candidate, `\`) || strings.HasPrefix(candidate, "/") {
			return true
		}
		if !allowSlash && strings.Contains(candidate, "/") {
			return true
		}
		for _, part := range strings.Split(candidate, "/") {
			if part == ".." {
				return true
			}
		}
	}
	return false
}

var hostedGitPattern = regexp.MustCompile(`^(?:(?:https?|ssh|git)://|git@)?(?:([^/:@]+)[/:])?([^#@]+)/([^#]*?)(?:\.git)?(?:#(.*))?$`)

// hostedGitInfo extracts host/user/project from a candidate URL.
func hostedGitInfoFromURL(candidate string) (host, user, project, committish string, ok bool) {
	match := hostedGitPattern.FindStringSubmatch(candidate)
	if match == nil {
		return "", "", "", "", false
	}
	host = match[1]
	if host == "" {
		host = "github.com"
	}
	return host, match[2], match[3], match[4], true
}

// buildGitSource validates and assembles a GitSource.
func buildGitSource(repo, host, path, ref string) *GitSource {
	if strings.HasPrefix(path, "/") {
		return nil
	}
	normalizedPath := strings.TrimSuffix(strings.TrimLeft(path, "/"), ".git")
	if host == "" || normalizedPath == "" || len(strings.Split(normalizedPath, "/")) < 2 {
		return nil
	}
	if hasUnsafeGitInstallPart(host, false) || hasUnsafeGitInstallPart(normalizedPath, true) {
		return nil
	}
	return &GitSource{
		Repo:   repo,
		Host:   host,
		Path:   normalizedPath,
		Ref:    ref,
		Pinned: ref != "",
	}
}

var gitSchemePattern = regexp.MustCompile(`(?i)^(https?|ssh|git)://`)

// ParseGitUrl parses a git source URL; nil when not a git URL.
func ParseGitUrl(source string) *GitSource {
	trimmed := strings.TrimSpace(source)
	hasGitPrefix := strings.HasPrefix(trimmed, "git:")
	rawURL := trimmed
	if hasGitPrefix {
		rawURL = strings.TrimSpace(trimmed[len("git:"):])
	}

	if !hasGitPrefix && !gitSchemePattern.MatchString(rawURL) {
		return nil
	}

	split := splitRef(rawURL)

	// Hosted candidates: with-ref form first, then the raw URL.
	hostedCandidates := []string{}
	if split.ref != "" {
		hostedCandidates = append(hostedCandidates, split.repo+"#"+split.ref)
	}
	hostedCandidates = append(hostedCandidates, rawURL)

	for _, candidate := range hostedCandidates {
		host, user, project, committish, ok := hostedGitInfoFromURL(candidate)
		if !ok {
			continue
		}
		if split.ref != "" && strings.Contains(project, "@") {
			continue
		}
		useHttpsPrefix := !strings.HasPrefix(split.repo, "http://") &&
			!strings.HasPrefix(split.repo, "https://") &&
			!strings.HasPrefix(split.repo, "ssh://") &&
			!strings.HasPrefix(split.repo, "git://") &&
			!strings.HasPrefix(split.repo, "git@")
		repo := split.repo
		if useHttpsPrefix {
			repo = "https://" + split.repo
		}
		ref := committish
		if ref == "" {
			ref = split.ref
		}
		return buildGitSource(repo, host, user+"/"+project, ref)
	}

	// https://-prefixed fallbacks.
	httpsCandidates := []string{}
	if split.ref != "" {
		httpsCandidates = append(httpsCandidates, "https://"+split.repo+"#"+split.ref)
	}
	httpsCandidates = append(httpsCandidates, "https://"+rawURL)

	for _, candidate := range httpsCandidates {
		host, user, project, committish, ok := hostedGitInfoFromURL(candidate)
		if !ok {
			continue
		}
		if split.ref != "" && strings.Contains(project, "@") {
			continue
		}
		ref := committish
		if ref == "" {
			ref = split.ref
		}
		return buildGitSource("https://"+split.repo, host, user+"/"+project, ref)
	}

	return nil
}
