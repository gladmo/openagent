package codingagent

// Port of utils/paths.ts (path normalization and resolution), utils/text.ts
// (BOM handling), utils/json.ts (JSONC comment stripping), and
// utils/pi-user-agent.ts.

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/ai"
)

// PathInputOptions configure NormalizePath.
type PathInputOptions struct {
	// Trim leading/trailing whitespace before normalization.
	Trim bool
	// ExpandTilde expands a leading ~ to a home directory (default true).
	ExpandTilde *bool
	// HomeDir overrides the home directory for ~ expansion.
	HomeDir string
	// StripAtPrefix strips a leading @ (CLI @file paths).
	StripAtPrefix bool
	// NormalizeUnicodeSpaces converts unicode space variants to spaces.
	NormalizeUnicodeSpaces bool
}

var unicodeSpacesPattern = regexp.MustCompile("[\u00a0\u2000-\u200a\u202f\u205f\u3000]")

// NormalizePathDefault normalizes with default options (~ expansion on).
func NormalizePathDefault(input string) string {
	return NormalizePath(input, PathInputOptions{})
}

// NormalizePath applies the input options to a path string.
func NormalizePath(input string, options PathInputOptions) string {
	normalized := input
	if options.Trim {
		normalized = strings.TrimSpace(normalized)
	}
	if options.NormalizeUnicodeSpaces {
		normalized = unicodeSpacesPattern.ReplaceAllString(normalized, " ")
	}
	if options.StripAtPrefix && strings.HasPrefix(normalized, "@") {
		normalized = normalized[1:]
	}
	if runtime.GOOS == "windows" {
		normalized = NormalizeWindowsShellPath(normalized)
	}

	expand := true
	if options.ExpandTilde != nil {
		expand = *options.ExpandTilde
	}
	if expand {
		home := options.HomeDir
		if home == "" {
			if resolved, err := os.UserHomeDir(); err == nil {
				home = resolved
			}
		}
		if normalized == "~" {
			return home
		}
		if strings.HasPrefix(normalized, "~/") || (runtime.GOOS == "windows" && strings.HasPrefix(normalized, "~\\")) {
			return filepath.Join(home, normalized[2:])
		}
	}

	if strings.HasPrefix(normalized, "file://") {
		return fileURLToPath(normalized)
	}

	return normalized
}

func fileURLToPath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		return raw // UNC paths not resolved
	}
	path := parsed.Path
	if runtime.GOOS == "windows" && strings.HasPrefix(path, "/") {
		path = strings.TrimPrefix(path, "/")
	}
	return path
}

// IsLocalPath reports the value is NOT a package source (npm:, git:, etc.)
// or remote URL protocol. Bare names, relative paths, and file: URLs are
// local.
func IsLocalPath(value string) bool {
	trimmed := strings.TrimSpace(value)
	for _, prefix := range []string{"npm:", "git:", "github:", "http:", "https:", "ssh:"} {
		if strings.HasPrefix(trimmed, prefix) {
			return false
		}
	}
	return true
}

var windowsDrivePattern = regexp.MustCompile(`^/(?:mnt/|cygdrive/)?([a-z])(?:/(.*))?$`)

// NormalizeWindowsShellPath converts Git Bash, MSYS, Cygwin, and WSL drive
// paths to native Windows form.
func NormalizeWindowsShellPath(filePath string) string {
	if !strings.HasPrefix(filePath, "/") || strings.HasPrefix(filePath, "//") || strings.Contains(filePath, "\\") {
		return filePath
	}
	match := windowsDrivePattern.FindStringSubmatch(filePath)
	if match == nil {
		return filePath
	}
	suffix := strings.ReplaceAll(match[2], "/", "\\")
	return strings.ToUpper(match[1]) + ":\\" + suffix
}

// ResolvePath normalizes then resolves input against baseDir.
func ResolvePath(input, baseDir string, options PathInputOptions) string {
	normalized := NormalizePath(input, options)
	normalizedBaseDir := NormalizePathDefault(baseDir)
	if filepath.IsAbs(normalized) {
		return resolveClean(normalized)
	}
	return resolveClean(normalizedBaseDir, normalized)
}

func resolveClean(paths ...string) string {
	absolute, err := filepath.Abs(filepath.Join(paths...))
	if err != nil {
		return filepath.Join(paths...)
	}
	return absolute
}

// GetCwdRelativePath returns the cwd-relative form when filePath is inside
// cwd, else nil.
func GetCwdRelativePath(filePath, cwd string) (string, bool) {
	resolvedCwd := ResolvePath(cwd, "", PathInputOptions{})
	resolvedPath := ResolvePath(filePath, resolvedCwd, PathInputOptions{})
	relativePath, err := filepath.Rel(resolvedCwd, resolvedPath)
	if err != nil {
		return "", false
	}
	isInsideCwd := relativePath == "" ||
		(relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) && !filepath.IsAbs(relativePath))
	if !isInsideCwd {
		return "", false
	}
	if relativePath == "" {
		return ".", true
	}
	return relativePath, true
}

// FormatPathRelativeToCwdOrAbsolute renders inside-cwd paths relative and
// others absolute.
func FormatPathRelativeToCwdOrAbsolute(filePath, cwd string) string {
	if relative, ok := GetCwdRelativePath(filePath, cwd); ok {
		return relative
	}
	return ResolvePath(filePath, cwd, PathInputOptions{})
}

// CanonicalizePath resolves to the real (symlink-followed) form, falling
// back to the raw path.
func CanonicalizePath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

// FileRevision identifies a file's content revision for change detection.
func FileRevision(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	return fileIdentity(info), true
}

// =============================================================================
// utils/text.ts
// =============================================================================

// SplitBom splits a leading UTF-8 byte order mark.
func SplitBom(content string) (bom, text string) {
	if strings.HasPrefix(content, "\uFEFF") {
		return "\uFEFF", content[len("\uFEFF"):]
	}
	return "", content
}

// StripBom removes a leading UTF-8 byte order mark.
func StripBom(content string) string {
	_, text := SplitBom(content)
	return text
}

// =============================================================================
// utils/json.ts
// =============================================================================

var jsoncStringOrComment = regexp.MustCompile(`"(?:\\.|[^"\\])*"|//[^\n]*`)
var jsoncStringOrComma = regexp.MustCompile(`"(?:\\.|[^"\\])*"|,(\s*[}\]])`)

// StripJsonComments strips // line comments and trailing commas, leaving
// string literals untouched.
func StripJsonComments(input string) string {
	input = jsoncStringOrComment.ReplaceAllStringFunc(input, func(match string) string {
		if strings.HasPrefix(match, `"`) {
			return match
		}
		return ""
	})
	return jsoncStringOrComma.ReplaceAllStringFunc(input, func(match string) string {
		if strings.HasPrefix(match, `"`) {
			return match
		}
		return strings.TrimPrefix(match, ",")
	})
}

// =============================================================================
// utils/pi-user-agent.ts
// =============================================================================

// GetPiUserAgent builds pi's User-Agent for its own network requests; the
// same detailed form pi-ai sends to providers.
func GetPiUserAgent(version string) string {
	return ai.FormatUserAgent(&ai.ClientIdentity{UserAgent: ai.DetailedUserAgent(), Version: version})
}

// fileIdentity mirrors the reference dev:ino:size:mtimeNs:ctimeNs revision.
// Go's os.FileInfo lacks dev/ino on all platforms; size+mtime+mode carry the
// same change-detection contract (documented equivalence).
func fileIdentity(info os.FileInfo) string {
	identity, _ := json.Marshal([]any{info.Size(), info.ModTime().UnixNano(), info.Mode().IsDir()})
	return string(identity)
}

func execCommand(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

func runtimeGOOS() string { return runtime.GOOS }

func osEnviron() []string { return os.Environ() }

func nowISOUtility() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func abortSignalFromContext(ctx context.Context) *abort.Signal {
	if ctx == nil {
		return nil
	}
	controller := abort.NewController()
	go func() {
		<-ctx.Done()
		controller.Abort()
	}()
	return controller.Signal()
}
