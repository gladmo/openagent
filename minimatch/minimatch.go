// Package minimatch is a port of the npm "minimatch" package (v10.2.6)
// glob matcher subset used by pi: basic wildcards (*, ?, [...]), globstar
// (**), and nocase. Extglob (+(...) etc.) and brace expansion land with
// their first in-repo consumer.
package minimatch

import (
	"regexp"
	"strings"
)

// Options mirror the npm package's IOptions subset in use.
type Options struct {
	// NoCase compares case-insensitively (nocase).
	NoCase bool
	// Dot lets * and ? match leading dots.
	Dot bool
}

// Minimatch is one compiled pattern.
type Minimatch struct {
	Pattern string
	Options Options

	segments []string // split on / with ** kept
	regexp   *regexp.Regexp
}

// New compiles a pattern.
func New(pattern string, options ...Options) *Minimatch {
	m := &Minimatch{Pattern: pattern}
	if len(options) > 0 {
		m.Options = options[0]
	}
	m.segments = strings.Split(pattern, "/")
	m.regexp = compilePattern(pattern, m.Options)
	return m
}

// Match tests a path against the pattern.
func (m *Minimatch) Match(path string) bool {
	if m.regexp == nil {
		return false
	}
	return m.regexp.MatchString(path)
}

// Minimatch is the functional form.
func MinimatchMatch(path, pattern string, options ...Options) bool {
	return New(pattern, options...).Match(path)
}

func compilePattern(pattern string, options Options) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?s)^")

	// Split into segments; ** crosses separators.
	segments := strings.Split(pattern, "/")
	for i, segment := range segments {
		if i > 0 {
			// Separator consumed either literally or skipped by a preceding **.
			if segments[i-1] == "**" {
				// After ** the slash is optional (leading).
			} else {
				b.WriteString("/")
			}
		}
		if segment == "**" {
			b.WriteString(".*")
			continue
		}
		writeSegment(&b, segment, options)
	}
	b.WriteString("$")

	prefix := ""
	if options.NoCase {
		prefix = "(?i)"
	}
	compiled, err := regexp.Compile(prefix + b.String())
	if err != nil {
		return nil
	}
	return compiled
}

func writeSegment(b *strings.Builder, segment string, options Options) {
	for i := 0; i < len(segment); i++ {
		ch := segment[i]
		switch ch {
		case '*':
			// * does not cross separators (already scoped per segment).
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		case '[':
			classEnd := strings.IndexByte(segment[i+1:], ']')
			if classEnd == -1 {
				b.WriteString(regexp.QuoteMeta("["))
				continue
			}
			class := segment[i+1 : i+1+classEnd]
			// Leading ! negates the class in globs.
			if strings.HasPrefix(class, "!") {
				b.WriteString("[^" + translateClass(class[1:]) + "]")
			} else {
				b.WriteString("[" + translateClass(class) + "]")
			}
			i += 1 + classEnd
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
}

// translateClass escapes regex-significant characters inside a character
// class (only backslash needs care; - keeps its range meaning).
func translateClass(class string) string {
	return strings.ReplaceAll(class, `\`, `\\`)
}
