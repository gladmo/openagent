package tui

// Port of alt-screen-search.ts (index and matching; the search overlay
// component lands with the Input component batch) and
// components/alt-screen-flash.ts.

import (
	"regexp"
	"strings"
	"sync"
	"time"
)

type searchSourceSpan struct {
	textStart     int
	textEnd       int
	row           int
	startCol      int
	endCol        int
	linearColumns bool
}

type searchCorpus struct {
	text  string
	spans []searchSourceSpan
}

// AltScreenSearchSegment is one row/column range of a match.
type AltScreenSearchSegment struct {
	Row      int
	StartCol int
	EndCol   int
}

// AltScreenSearchMatch is a transcript search match spanning segments.
type AltScreenSearchMatch struct {
	Segments []AltScreenSearchSegment
}

var printableASCIIPattern = regexp.MustCompile(`^[\x20-\x7e]*$`)
var whitespaceRunPattern = regexp.MustCompile(`^\s+$`)

func buildSearchCorpus(lines []string) searchCorpus {
	var chunks []string
	var spans []searchSourceSpan
	textLength := 0
	pendingSeparator := false

	appendSeparator := func() {
		if !pendingSeparator {
			return
		}
		chunks = append(chunks, " ")
		textLength++
		pendingSeparator = false
	}

	for row := 0; row < len(lines); row++ {
		line := ""
		if row < len(lines) {
			line = StripTerminalSequences(lines[row])
		}
		column := 0

		// Rendered transcripts are overwhelmingly ASCII: index complete
		// non-space runs at once instead of per-cell segmentation.
		if printableASCIIPattern.MatchString(line) {
			index := 0
			for index < len(line) {
				if line[index] == ' ' {
					if textLength > 0 {
						pendingSeparator = true
					}
					column++
					index++
					continue
				}
				end := index + 1
				for end < len(line) && line[end] != ' ' {
					end++
				}
				appendSeparator()
				text := line[index:end]
				chunks = append(chunks, text)
				spans = append(spans, searchSourceSpan{
					textStart:     textLength,
					textEnd:       textLength + len(text),
					row:           row,
					startCol:      column,
					endCol:        column + len(text),
					linearColumns: true,
				})
				textLength += len(text)
				column += len(text)
				index = end
			}
		} else {
			for _, grapheme := range segmentGraphemes(line) {
				width := VisibleWidth(grapheme)
				if whitespaceRunPattern.MatchString(grapheme) {
					if textLength > 0 {
						pendingSeparator = true
					}
					column += width
					continue
				}
				appendSeparator()
				chunks = append(chunks, grapheme)
				spans = append(spans, searchSourceSpan{
					textStart:     textLength,
					textEnd:       textLength + len(grapheme),
					row:           row,
					startCol:      column,
					endCol:        column + width,
					linearColumns: false,
				})
				textLength += len(grapheme)
				column += width
			}
		}
		if textLength > 0 {
			pendingSeparator = true
		}
	}

	return searchCorpus{text: strings.Join(chunks, ""), spans: spans}
}

func normalizeSearchQuery(query string) string {
	return strings.TrimSpace(whitespaceCollapse.ReplaceAllString(query, " "))
}

var whitespaceCollapse = regexp.MustCompile(`\s+`)

func escapeRegExp(text string) string {
	var b strings.Builder
	for _, r := range text {
		switch r {
		case '.', '*', '+', '?', '^', '$', '{', '}', '(', ')', '|', '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func findSearchCorpusMatches(corpus searchCorpus, normalizedQuery string) []AltScreenSearchMatch {
	if normalizedQuery == "" {
		return nil
	}
	expression := regexp.MustCompile("(?i)" + escapeRegExp(normalizedQuery))
	var matches []AltScreenSearchMatch
	spanIndex := 0

	for _, loc := range expression.FindAllStringIndex(corpus.text, -1) {
		start := loc[0]
		end := loc[1]
		for spanIndex < len(corpus.spans) && corpus.spans[spanIndex].textEnd <= start {
			spanIndex++
		}

		var segments []AltScreenSearchSegment
		for index := spanIndex; index < len(corpus.spans); index++ {
			span := corpus.spans[index]
			if span.textStart >= end {
				break
			}
			if span.textEnd <= start {
				continue
			}
			var startCol, endCol int
			if span.linearColumns {
				startCol = span.startCol + maxInt(start, span.textStart) - span.textStart
				endCol = span.startCol + minInt(end, span.textEnd) - span.textStart
			} else {
				startCol = span.startCol
				endCol = span.endCol
			}
			if len(segments) > 0 {
				previous := &segments[len(segments)-1]
				if previous.Row == span.row && startCol <= previous.EndCol {
					previous.EndCol = maxInt(previous.EndCol, endCol)
					continue
				}
			}
			segments = append(segments, AltScreenSearchSegment{Row: span.row, StartCol: startCol, EndCol: endCol})
		}
		for spanIndex < len(corpus.spans) && corpus.spans[spanIndex].textEnd <= end {
			spanIndex++
		}
		if len(segments) > 0 {
			matches = append(matches, AltScreenSearchMatch{Segments: segments})
		}
	}

	return matches
}

// AltScreenSearchResult carries matches plus a changed flag.
type AltScreenSearchResult struct {
	Matches []AltScreenSearchMatch
	Changed bool
}

// AltScreenSearchIndex caches the corpus and matches while transcript lines
// stay unchanged.
type AltScreenSearchIndex struct {
	sourceLines     []string
	corpus          *searchCorpus
	normalizedQuery string
	matches         []AltScreenSearchMatch
}

// Search runs a query over the lines, rebuilding the corpus only when the
// lines changed.
func (i *AltScreenSearchIndex) Search(lines []string, query string) AltScreenSearchResult {
	sourceChanged := i.sourceLines == nil || len(i.sourceLines) != len(lines)
	if !sourceChanged && i.sourceLines != nil {
		for index := range lines {
			if i.sourceLines[index] == lines[index] {
				continue
			}
			sourceChanged = true
			break
		}
	}
	if sourceChanged || i.corpus == nil {
		i.sourceLines = append([]string(nil), lines...)
		corpus := buildSearchCorpus(lines)
		i.corpus = &corpus
	}

	normalizedQuery := normalizeSearchQuery(query)
	changed := sourceChanged || normalizedQuery != i.normalizedQuery
	if changed {
		i.normalizedQuery = normalizedQuery
		i.matches = findSearchCorpusMatches(*i.corpus, normalizedQuery)
	}
	return AltScreenSearchResult{Matches: i.matches, Changed: changed}
}

// FindAltScreenSearchMatches searches lines without caching.
func FindAltScreenSearchMatches(lines []string, query string) []AltScreenSearchMatch {
	normalizedQuery := normalizeSearchQuery(query)
	if normalizedQuery == "" {
		return nil
	}
	return findSearchCorpusMatches(buildSearchCorpus(lines), normalizedQuery)
}

// GetAltScreenSearchMatchKey builds a stable identity key for a match.
func GetAltScreenSearchMatchKey(match AltScreenSearchMatch) string {
	if len(match.Segments) == 0 {
		return ""
	}
	first := match.Segments[0]
	last := match.Segments[len(match.Segments)-1]
	return itoa(first.Row) + ":" + itoa(first.StartCol) + ":" + itoa(last.Row) + ":" + itoa(last.EndCol)
}

const altScreenFlashDefaultDurationMs = 1000

type flashEntry struct {
	id      int
	message string
	timer   *time.Timer
}

// AltScreenFlashContainer renders transient messages composited by the
// alternate-screen renderer.
type AltScreenFlashContainer struct {
	mu            sync.Mutex
	entries       []flashEntry
	nextID        int
	requestRender func()

	// After is the timer scheduler (injectable for tests).
	After func(d time.Duration, fn func()) *time.Timer
}

// NewAltScreenFlashContainer builds the flash stack.
func NewAltScreenFlashContainer(requestRender func()) *AltScreenFlashContainer {
	return &AltScreenFlashContainer{requestRender: requestRender, After: time.AfterFunc}
}

// Flash shows a message for a duration (default 1000ms).
func (c *AltScreenFlashContainer) Flash(message string, durationMs int) {
	if durationMs == 0 {
		durationMs = altScreenFlashDefaultDurationMs
	}
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	after := c.After
	c.mu.Unlock()

	timer := after(time.Duration(maxInt(0, durationMs))*time.Millisecond, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		for index, entry := range c.entries {
			if entry.id == id {
				c.entries = append(c.entries[:index], c.entries[index+1:]...)
				break
			}
		}
		if c.requestRender != nil {
			c.requestRender()
		}
	})

	c.mu.Lock()
	c.entries = append(c.entries, flashEntry{id: id, message: message, timer: timer})
	c.mu.Unlock()
	if c.requestRender != nil {
		c.requestRender()
	}
}

// Dispose cancels all flashes.
func (c *AltScreenFlashContainer) Dispose() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, entry := range c.entries {
		entry.timer.Stop()
	}
	c.entries = nil
}

// Invalidate is a no-op.
func (c *AltScreenFlashContainer) Invalidate() {}

// Render renders each flash as an inverse-video truncated line.
func (c *AltScreenFlashContainer) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	lines := make([]string, len(c.entries))
	for i, entry := range c.entries {
		message := TruncateToWidth(" "+entry.message+" ", width, "", false)
		lines[i] = "\x1b[7m" + message + "\x1b[27m"
	}
	return lines
}
