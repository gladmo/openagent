// Package ignore is a semantic port of npm ignore 7.0.8 (gitignore matching)
// for the subset pi uses: New(), Add(patterns), Ignores(path), Test(path).
//
// The npm package compiles patterns to JS regexes (which rely on lookaround
// Go's RE2 cannot express); this port implements the same observable
// gitignore semantics with a backtracking wildmatch over parsed patterns:
// basename-only vs anchored patterns, ** across segments, character classes
// with POSIX names, escapes, trailing-slash directory-only rules,
// case-insensitive matching by default, last-match-wins negation, the
// rule-skip table from npm's RuleManager.test, parent-directory inheritance
// ("cannot re-include a file if a parent directory is excluded"), and result
// caches.
package ignore

import (
	"fmt"
	"strings"
)

// TestResult mirrors the npm ignore test result.
type TestResult struct {
	Ignored   bool
	Unignored bool
}

type segment struct {
	tokens []segToken
	// star2 marks a '**' segment
	star2 bool
	// minOne: a trailing '**' must consume at least one path segment
	// ('a/**' does not match 'a', 'a/**/' does not match 'a/').
	minOne bool
}

type segToken struct {
	kind byte // 'l' literal, '*' star, '?' question, '[' class
	ch   rune
	// class fields
	negated bool
	members []classMember
}

type classMember struct {
	lo, hi rune // hi == lo for single members
	posix  string
}

type rule struct {
	pattern      string
	negative     bool
	dirOnly      bool
	anchored     bool
	segments     []segment
	hasStar2     bool
	basenameOnly bool
}

// Ignore mirrors the npm Ignore class.
type Ignore struct {
	rules       []*rule
	ignoreCase  bool
	ignoreCache map[string]TestResult
	testCache   map[string]TestResult
}

// Options mirrors the npm constructor options subset.
type Options struct {
	// IgnoreCase defaults to true, like npm ignore.
	IgnoreCase *bool
}

// New creates an empty matcher (npm ignore()).
func New(options *Options) *Ignore {
	ignoreCase := true
	if options != nil && options.IgnoreCase != nil {
		ignoreCase = *options.IgnoreCase
	}
	return &Ignore{
		ignoreCase:  ignoreCase,
		ignoreCache: map[string]TestResult{},
		testCache:   map[string]TestResult{},
	}
}

// Add adds patterns (splitting multi-line strings like npm) and returns the
// matcher for chaining.
func (ig *Ignore) Add(patterns ...string) *Ignore {
	added := false
	for _, p := range patterns {
		for _, line := range strings.Split(p, "\r\n") {
			for _, l := range strings.Split(line, "\n") {
				if ig.addOne(l) {
					added = true
				}
			}
		}
	}
	if added {
		ig.ignoreCache = map[string]TestResult{}
		ig.testCache = map[string]TestResult{}
	}
	return ig
}

// checkPattern mirrors npm: blank / comment / invalid-trailing-backslash
// lines match nothing.
func checkPattern(pattern string) bool {
	if pattern == "" {
		return false
	}
	if len(pattern) > 0 && pattern[0] == '#' {
		return false
	}
	// A line of only spaces is blank.
	if strings.TrimLeft(pattern, " ") == "" && strings.Contains(pattern, " ") {
		return false
	}
	// (?:[^\\]|^)\\$
	if strings.HasSuffix(pattern, "\\") {
		backslashes := 0
		for i := len(pattern) - 1; i >= 0 && pattern[i] == '\\'; i-- {
			backslashes++
		}
		if backslashes%2 == 1 {
			return false
		}
	}
	return true
}

func (ig *Ignore) addOne(pattern string) bool {
	// Trailing spaces are trimmed unless escaped with a backslash.
	trimmed := trimTrailingSpaces(pattern)
	if !checkPattern(trimmed) {
		return false
	}
	r := &rule{pattern: trimmed}
	body := trimmed
	if strings.HasPrefix(body, "!") {
		r.negative = true
		body = body[1:]
	} else if strings.HasPrefix(body, `\!`) {
		body = "!" + body[2:]
	}
	if strings.HasPrefix(body, `\#`) {
		body = "#" + body[2:]
	}
	if strings.HasSuffix(body, "/") {
		r.dirOnly = true
		body = body[:len(body)-1]
	}
	if strings.HasPrefix(body, "/") {
		r.anchored = true
		body = body[1:]
	}
	r.basenameOnly = !r.anchored && !strings.Contains(body, "/")
	r.segments = parseSegments(body, ig.ignoreCase)
	r.hasStar2 = false
	for _, s := range r.segments {
		if s.star2 {
			r.hasStar2 = true
		}
	}
	if n := len(r.segments); n > 0 && r.segments[n-1].star2 {
		// A trailing "/**" matches everything inside but not the folder
		// itself; likewise a trailing "/**/" requires one directory segment.
		r.segments[n-1].minOne = true
	}
	ig.rules = append(ig.rules, r)
	return true
}

// trimTrailingSpaces mirrors git: trailing runs of ' ' are dropped unless the
// final one is backslash-escaped ((a\ ) -> (a ), (a ) -> (a)).
func trimTrailingSpaces(pattern string) string {
	i := len(pattern)
	for i > 0 && pattern[i-1] == ' ' {
		i--
	}
	if i == len(pattern) {
		return pattern
	}
	// Count backslashes directly before the trailing spaces.
	backslashes := 0
	for j := i - 1; j >= 0 && pattern[j] == '\\'; j-- {
		backslashes++
	}
	if backslashes%2 == 1 {
		// The last space is escaped: keep exactly one.
		return pattern[:i] + " "
	}
	return pattern[:i]
}

func parseSegments(body string, ignoreCase bool) []segment {
	if body == "" {
		return []segment{{}}
	}
	parts := strings.Split(body, "/")
	out := make([]segment, 0, len(parts))
	for _, p := range parts {
		out = append(out, parseSegment(p, ignoreCase))
	}
	return out
}

func parseSegment(p string, ignoreCase bool) segment {
	runes := []rune(p)
	if ignoreCase {
		for i, r := range runes {
			runes[i] = []rune(strings.ToLower(string(r)))[0]
		}
	}
	if p == "**" {
		return segment{star2: true}
	}
	seg := segment{}
	i := 0
	for i < len(runes) {
		c := runes[i]
		switch c {
		case '\\':
			if i+1 < len(runes) {
				i++
				seg.tokens = append(seg.tokens, segToken{kind: 'l', ch: lowerIf(runes[i], ignoreCase)})
			} else {
				seg.tokens = append(seg.tokens, segToken{kind: 'l', ch: '\\'})
			}
		case '*':
			seg.tokens = append(seg.tokens, segToken{kind: '*'})
		case '?':
			seg.tokens = append(seg.tokens, segToken{kind: '?'})
		case '[':
			tok, next, ok := parseClass(runes, i, ignoreCase)
			if !ok {
				// Unterminated: literal '['.
				seg.tokens = append(seg.tokens, segToken{kind: 'l', ch: '['})
				i++
				continue
			}
			seg.tokens = append(seg.tokens, tok)
			i = next + 1
			continue
		default:
			seg.tokens = append(seg.tokens, segToken{kind: 'l', ch: lowerIf(c, ignoreCase)})
		}
		i++
	}
	return seg
}

func lowerIf(r rune, ignoreCase bool) rune {
	if !ignoreCase {
		return r
	}
	l := []rune(strings.ToLower(string(r)))
	if len(l) == 1 {
		return l[0]
	}
	return r
}

// parseClass parses a bracket expression starting at '['.
// Mirrors git wildmatch: leading ]/!/^ handling, ranges, POSIX classes,
// backslash escapes, '/' never matching.
func parseClass(runes []rune, start int, ignoreCase bool) (segToken, int, bool) {
	i := start + 1
	tok := segToken{kind: '['}
	if i < len(runes) && (runes[i] == '!' || runes[i] == '^') {
		tok.negated = true
		i++
	}
	first := true
	for i < len(runes) {
		c := runes[i]
		if c == ']' && !first {
			return tok, i, true
		}
		first = false
		// POSIX class [:alpha:]
		if c == '[' && i+1 < len(runes) && runes[i+1] == ':' {
			end := i + 2
			for end < len(runes) && runes[end] != ']' {
				end++
			}
			if end < len(runes) && end > i+2 && runes[end-1] == ':' {
				name := string(runes[i+2 : end-1])
				if _, ok := posixClasses[name]; !ok {
					// Unknown class: whole pattern matches nothing.
					tok.members = append(tok.members, classMember{posix: "\x00invalid"})
				} else {
					tok.members = append(tok.members, classMember{posix: name})
				}
				// Skip past the inner ':]' bracket so the following character
				// (often the class terminator) is examined next.
				i = end + 1
				continue
			}
		}
		lo := c
		if c == '\\' && i+1 < len(runes) {
			i++
			lo = runes[i]
		}
		lo = lowerIf(lo, ignoreCase)
		// Range?
		if i+2 < len(runes) && runes[i+1] == '-' && runes[i+2] != ']' {
			hi := runes[i+2]
			j := i + 2
			if hi == '\\' && j+1 < len(runes) {
				j++
				hi = runes[j]
			}
			hi = lowerIf(hi, ignoreCase)
			if lo <= hi {
				tok.members = append(tok.members, classMember{lo: lo, hi: hi})
			}
			// Out-of-order ranges: keep the lower bound only.
			i = j
			continue
		}
		tok.members = append(tok.members, classMember{lo: lo, hi: lo})
		i++
	}
	return tok, i, false
}

var posixClasses = map[string]string{
	"alnum":  "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
	"alpha":  "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
	"blank":  " \t",
	"cntrl":  "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f\x7f",
	"digit":  "0123456789",
	"graph":  "!\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~",
	"lower":  "abcdefghijklmnopqrstuvwxyz",
	"print":  " !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~",
	"punct":  "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~",
	"space":  " \t\n\r",
	"upper":  "ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	"xdigit": "0123456789ABCDEFabcdef",
}

func (t segToken) classMatches(r rune) bool {
	if r == '/' {
		// A bracket expression never matches a path separator, even when
		// negated.
		return false
	}
	matched := false
	for _, m := range t.members {
		if m.posix != "" {
			if m.posix == "\x00invalid" {
				continue
			}
			if strings.ContainsRune(posixClasses[m.posix], r) {
				matched = true
				break
			}
			continue
		}
		if r >= m.lo && r <= m.hi {
			matched = true
			break
		}
	}
	if t.negated {
		if matched {
			return false
		}
		// Negated class still must not match '/'.
		return r != '/'
	}
	return matched
}

// matchSegment matches one pattern segment against one path segment.
func matchSegment(seg segment, s string) bool {
	if seg.star2 {
		return false // handled at path level
	}
	runes := []rune(s)
	tokens := seg.tokens
	var m func(ti, ri int) bool
	m = func(ti, ri int) bool {
		for ti < len(tokens) {
			tok := tokens[ti]
			switch tok.kind {
			case 'l':
				if ri >= len(runes) || runes[ri] != tok.ch {
					return false
				}
				ti++
				ri++
			case '?':
				if ri >= len(runes) || runes[ri] == '/' {
					return false
				}
				ti++
				ri++
			case '[':
				if ri >= len(runes) || runes[ri] == '/' || !tok.classMatches(runes[ri]) {
					return false
				}
				ti++
				ri++
			case '*':
				// Try consuming 0..n non-slash chars.
				for k := len(runes); k >= ri; k-- {
					if m(ti+1, k) {
						return true
					}
				}
				return false
			}
		}
		return ri == len(runes)
	}
	return m(0, 0)
}

// matchesPath matches a rule against a path (already lowercased when
// case-insensitive). isDir marks a directory path.
func (r *rule) matchesPath(path string, isDir bool) bool {
	if r.dirOnly && !isDir {
		return false
	}
	trimmed := path
	if strings.HasSuffix(trimmed, "/") {
		trimmed = trimmed[:len(trimmed)-1]
	}
	segs := strings.Split(trimmed, "/")
	if len(segs) == 1 && segs[0] == "" {
		segs = nil
	}
	if r.basenameOnly {
		// Match the last segment at any depth ('**/' prefix implied).
		last := ""
		if len(segs) > 0 {
			last = segs[len(segs)-1]
		}
		if len(r.segments) != 1 {
			return false
		}
		if r.segments[0].star2 {
			// A lone '**' matches everything.
			return true
		}
		return matchSegment(r.segments[0], last)
	}
	return matchSegments(r.segments, segs)
}

// matchSegments matches parsed pattern segments against path segments with **
// support.
func matchSegments(pattern []segment, path []string) bool {
	var m func(pi, si int) bool
	m = func(pi, si int) bool {
		if pi >= len(pattern) {
			return si >= len(path)
		}
		seg := pattern[pi]
		if seg.star2 {
			// '**' matches zero or more segments (at least one when it is a
			// trailing globstar).
			start := si
			if seg.minOne {
				start = si + 1
			}
			for k := len(path); k >= start; k-- {
				if m(pi+1, k) {
					return true
				}
			}
			return false
		}
		if si >= len(path) {
			return false
		}
		if !matchSegment(seg, path[si]) {
			return false
		}
		return m(pi+1, si+1)
	}
	return m(0, 0)
}

// testRules is the RuleManager.test port: scan rules in order applying the
// skip table; last match wins.
func (ig *Ignore) testRules(path string, isDir bool, checkUnignored bool) TestResult {
	ignored := false
	unignored := false
	if ig.ignoreCase {
		path = strings.ToLower(path)
	}
	for _, rule := range ig.rules {
		negative := rule.negative
		//          |           ignored : unignored
		// -------- | ---------------------------------------
		// negative |   0:0   |   0:1   |   1:0   |   1:1
		//     0    |  TEST   |  TEST   |  SKIP   |    X
		//     1    |  TESTIF |  SKIP   |  TEST   |    X
		skip := (unignored == negative && ignored != unignored) ||
			(negative && !ignored && !unignored && !checkUnignored)
		if !skip && rule.matchesPath(path, isDir) {
			ignored = !negative
			unignored = negative
		}
	}
	return TestResult{Ignored: ignored, Unignored: unignored}
}

func parentOf(path string) string {
	trimmed := strings.TrimSuffix(path, "/")
	idx := strings.LastIndex(trimmed, "/")
	if idx < 0 {
		return ""
	}
	return path[:idx+1]
}

func (ig *Ignore) checkPath(path string) {
	if path == "" {
		panic("path must not be empty")
	}
	if strings.HasPrefix(path, "/") {
		panic(fmt.Sprintf("path should be a `path.relative()`d string, but got %q", path))
	}
	if strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") || path == "." || path == ".." {
		panic(fmt.Sprintf("path should be a `path.relative()`d string, but got %q", path))
	}
}

func (ig *Ignore) t(path string, cache map[string]TestResult, checkUnignored bool) TestResult {
	if res, ok := cache[path]; ok {
		return res
	}
	parent := ""
	if p := parentOf(path); p != "" {
		parent = p
	}
	var res TestResult
	if parent != "" {
		pRes := ig.t(parent, cache, checkUnignored)
		if pRes.Ignored {
			// It is not possible to re-include a file if a parent directory
			// of that file is excluded.
			res = pRes
			cache[path] = res
			return res
		}
	}
	isDir := strings.HasSuffix(path, "/")
	res = ig.testRules(path, isDir, checkUnignored)
	cache[path] = res
	return res
}

// Ignores mirrors npm ignores(): parent-aware, checkUnignored=false.
func (ig *Ignore) Ignores(path string) bool {
	ig.checkPath(path)
	return ig.t(path, ig.ignoreCache, false).Ignored
}

// Test mirrors npm test(): parent-aware, checkUnignored=true.
func (ig *Ignore) Test(path string) TestResult {
	ig.checkPath(path)
	return ig.t(path, ig.testCache, true)
}

// IsPathValid reports whether the path is valid for this matcher.
func IsPathValid(path string) bool {
	if path == "" {
		return false
	}
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") {
		return false
	}
	return path != "." && path != ".."
}
