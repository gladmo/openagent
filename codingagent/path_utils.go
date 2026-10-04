package codingagent

// Port of core/tools/path-utils.ts: read-path resolution with macOS
// filename-encoding variants (AM/PM narrow no-break space, NFD, curly
// apostrophe).

import (
	"os"
	"regexp"
	"strings"
)

const narrowNoBreakSpace = " "

var ampmPattern = regexp.MustCompile(` (AM|PM)\.`)

func tryMacOSScreenshotPath(filePath string) string {
	return ampmPattern.ReplaceAllStringFunc(filePath, func(match string) string {
		return narrowNoBreakSpace + match[1:len(match)-1] + "."
	})
}

func tryNFDVariant(filePath string) string {
	return NormalizeNFD(filePath)
}

func tryCurlyQuoteVariant(filePath string) string {
	return strings.ReplaceAll(filePath, "'", "’")
}

func fileExistsAt(filePath string) bool {
	_, err := os.Stat(filePath)
	return err == nil
}

// PathExists reports whether the path is resolvable.
func PathExists(filePath string) bool {
	return fileExistsAt(filePath)
}

// ExpandPath normalizes unicode spaces and strips a leading @.
func ExpandPath(filePath string) string {
	return NormalizePath(filePath, PathInputOptions{NormalizeUnicodeSpaces: true, StripAtPrefix: true})
}

// ResolveToCwd resolves a path relative to cwd (~ and absolute handled).
func ResolveToCwd(filePath, cwd string) string {
	return ResolvePath(filePath, cwd, PathInputOptions{NormalizeUnicodeSpaces: true, StripAtPrefix: true})
}

// ResolveReadPath resolves a read target, trying macOS filename variants
// before falling back to the raw resolution.
func ResolveReadPath(filePath, cwd string) string {
	resolved := ResolveToCwd(filePath, cwd)

	if fileExistsAt(resolved) {
		return resolved
	}

	if amPmVariant := tryMacOSScreenshotPath(resolved); amPmVariant != resolved && fileExistsAt(amPmVariant) {
		return amPmVariant
	}

	nfdVariant := tryNFDVariant(resolved)
	if nfdVariant != resolved && fileExistsAt(nfdVariant) {
		return nfdVariant
	}

	if curlyVariant := tryCurlyQuoteVariant(resolved); curlyVariant != resolved && fileExistsAt(curlyVariant) {
		return curlyVariant
	}

	if nfdCurlyVariant := tryCurlyQuoteVariant(nfdVariant); nfdCurlyVariant != resolved && fileExistsAt(nfdCurlyVariant) {
		return nfdCurlyVariant
	}

	return resolved
}
