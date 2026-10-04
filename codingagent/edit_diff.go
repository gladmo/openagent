package codingagent

// Port of core/tools/edit-diff.ts: shared diff computation for the edit
// tool — line-ending handling, fuzzy matching, replacement application with
// unchanged-line preservation, unified patches, and display diffs.

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gladmo/openagent/diff"
)

// DetectLineEnding reports the dominant line ending by first occurrence.
func DetectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 || crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

// NormalizeToLF converts CRLF and lone CR to LF.
func NormalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// RestoreLineEndings re-applies the detected ending.
func RestoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

var (
	smartSingleQuotes = []rune{'\u2018', '\u2019', '\u201A', '\u201B'}
	smartDoubleQuotes = []rune{'\u201C', '\u201D', '\u201E', '\u201F'}
	unicodeDashes     = []rune{'\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212'}
	unicodeSpaces     = []rune{'\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200A', '\u202F', '\u205F', '\u3000'}
)

func replaceRunes(text string, runes []rune, replacement string) string {
	set := map[rune]bool{}
	for _, r := range runes {
		set[r] = true
	}
	var b strings.Builder
	for _, r := range text {
		if set[r] {
			b.WriteString(replacement)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// NormalizeForFuzzyMatch applies progressive transformations: NFKC, per-line
// trailing-whitespace strip, smart quotes to ASCII, Unicode dashes to
// hyphen, special spaces to space.
func NormalizeForFuzzyMatch(text string) string {
	normalized := NormalizeNFKC(text)
	lines := strings.Split(normalized, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r\v\f")
	}
	normalized = strings.Join(lines, "\n")
	normalized = replaceRunes(normalized, smartSingleQuotes, "'")
	normalized = replaceRunes(normalized, smartDoubleQuotes, `"`)
	normalized = replaceRunes(normalized, unicodeDashes, "-")
	normalized = replaceRunes(normalized, unicodeSpaces, " ")
	return normalized
}

// splitLinesWithEndings splits keeping terminators.
func splitLinesWithEndings(content string) []string {
	var lines []string
	runes := []rune(content)
	start := 0
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\n' {
			lines = append(lines, string(runes[start:i+1]))
			start = i + 1
		}
	}
	if start < len(runes) {
		lines = append(lines, string(runes[start:]))
	}
	return lines
}

type lineSpan struct {
	start int
	end   int
}

type textReplacement struct {
	matchIndex  int
	matchLength int
	newText     string
}

type matchedEdit struct {
	editIndex   int
	matchIndex  int
	matchLength int
	newText     string
}

func getLineSpans(content string) []lineSpan {
	var spans []lineSpan
	offset := 0
	for _, line := range splitLinesWithEndings(content) {
		spans = append(spans, lineSpan{start: offset, end: offset + len(line)})
		offset += len(line)
	}
	return spans
}

type replacementLineRange struct {
	startLine int
	endLine   int // exclusive
}

// getReplacementLineRange returns the replacement's line span; ok=false
// when the range falls outside the base content (an error upstream, not
// a crash — fuzzed or whitespace-only oldText can reach this).
func getReplacementLineRange(lines []lineSpan, replacement textReplacement) (replacementLineRange, bool) {
	replacementStart := replacement.matchIndex
	replacementEnd := replacement.matchIndex + replacement.matchLength

	startLine := -1
	for i := 0; i < len(lines); i++ {
		if replacementStart >= lines[i].start && replacementStart < lines[i].end {
			startLine = i
			break
		}
	}
	if startLine == -1 {
		return replacementLineRange{}, false
	}

	endLine := startLine
	for endLine < len(lines) && lines[endLine].end < replacementEnd {
		endLine++
	}
	if endLine >= len(lines) {
		return replacementLineRange{}, false
	}

	return replacementLineRange{startLine: startLine, endLine: endLine + 1}, true
}

func applyReplacements(content string, replacements []textReplacement, offset int) string {
	result := content
	for i := len(replacements) - 1; i >= 0; i-- {
		replacement := replacements[i]
		matchIndex := replacement.matchIndex - offset
		result = result[:matchIndex] + replacement.newText + result[matchIndex+replacement.matchLength:]
	}
	return result
}

// ApplyReplacementsPreservingUnchangedLines applies replacements matched
// against baseContent to originalContent, preserving unchanged line blocks
// from the original byte-for-byte.
func ApplyReplacementsPreservingUnchangedLines(originalContent, baseContent string, replacements []textReplacement) (string, error) {
	originalLines := splitLinesWithEndings(originalContent)
	baseLines := getLineSpans(baseContent)
	if len(originalLines) != len(baseLines) {
		return "", fmt.Errorf("Cannot preserve unchanged lines because the base content has a different line count.")
	}

	type replacementGroup struct {
		startLine    int
		endLine      int
		replacements []textReplacement
	}
	sorted := append([]textReplacement(nil), replacements...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].matchIndex < sorted[b].matchIndex })

	var groups []replacementGroup
	for _, replacement := range sorted {
		lineRange, ok := getReplacementLineRange(baseLines, replacement)
		if !ok {
			return "", errors.New("replacement range is outside the base content")
		}
		if n := len(groups); n > 0 && lineRange.startLine < groups[n-1].endLine {
			groups[n-1].endLine = maxIntGo(groups[n-1].endLine, lineRange.endLine)
			groups[n-1].replacements = append(groups[n-1].replacements, replacement)
			continue
		}
		groups = append(groups, replacementGroup{
			startLine:    lineRange.startLine,
			endLine:      lineRange.endLine,
			replacements: []textReplacement{replacement},
		})
	}

	originalLineIndex := 0
	var result strings.Builder
	for _, group := range groups {
		for _, line := range originalLines[originalLineIndex:group.startLine] {
			result.WriteString(line)
		}

		groupStartOffset := baseLines[group.startLine].start
		groupEndOffset := baseLines[group.endLine-1].end
		result.WriteString(applyReplacements(
			baseContent[groupStartOffset:groupEndOffset],
			group.replacements,
			groupStartOffset,
		))
		originalLineIndex = group.endLine
	}
	for _, line := range originalLines[originalLineIndex:] {
		result.WriteString(line)
	}

	return result.String(), nil
}

// FuzzyMatchResult reports one oldText lookup.
type FuzzyMatchResult struct {
	Found                 bool
	Index                 int
	MatchLength           int
	UsedFuzzyMatch        bool
	ContentForReplacement string
}

// Edit is one exact-text replacement.
type Edit struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// FuzzyFindText finds oldText exactly first, then in fuzzy-normalized space.
func FuzzyFindText(content, oldText string) FuzzyMatchResult {
	if exactIndex := strings.Index(content, oldText); exactIndex != -1 {
		return FuzzyMatchResult{
			Found:                 true,
			Index:                 exactIndex,
			MatchLength:           len(oldText),
			UsedFuzzyMatch:        false,
			ContentForReplacement: content,
		}
	}

	fuzzyContent := NormalizeForFuzzyMatch(content)
	fuzzyOldText := NormalizeForFuzzyMatch(oldText)
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)
	if fuzzyIndex == -1 {
		return FuzzyMatchResult{Found: false, Index: -1}
	}

	return FuzzyMatchResult{
		Found:                 true,
		Index:                 fuzzyIndex,
		MatchLength:           len(fuzzyOldText),
		UsedFuzzyMatch:        true,
		ContentForReplacement: fuzzyContent,
	}
}

func countOccurrences(content, oldText string) int {
	fuzzyContent := NormalizeForFuzzyMatch(content)
	fuzzyOldText := NormalizeForFuzzyMatch(oldText)
	return strings.Count(fuzzyContent, fuzzyOldText)
}

func getNotFoundError(path string, editIndex, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("Could not find the exact text in %s. The old text must match exactly including all whitespace and newlines.", path)
	}
	return fmt.Errorf("Could not find edits[%d] in %s. The oldText must match exactly including all whitespace and newlines.", editIndex, path)
}

func getDuplicateError(path string, editIndex, totalEdits, occurrences int) error {
	if totalEdits == 1 {
		return fmt.Errorf("Found %d occurrences of the text in %s. The text must be unique. Please provide more context to make it unique.", occurrences, path)
	}
	return fmt.Errorf("Found %d occurrences of edits[%d] in %s. Each oldText must be unique. Please provide more context to make it unique.", occurrences, editIndex, path)
}

func getEmptyOldTextError(path string, editIndex, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("oldText must not be empty in %s.", path)
	}
	return fmt.Errorf("edits[%d].oldText must not be empty in %s.", editIndex, path)
}

func getNoChangeError(path string, totalEdits int) error {
	if totalEdits == 1 {
		return fmt.Errorf("No changes made to %s. The replacement produced identical content. This might indicate an issue with special characters or the text not existing as expected.", path)
	}
	return fmt.Errorf("No changes made to %s. The replacements produced identical content.", path)
}

// AppliedEditsResult carries the base and edited content.
type AppliedEditsResult struct {
	BaseContent string
	NewContent  string
}

// ApplyEditsToNormalizedContent applies exact-text replacements to
// LF-normalized content; fuzzy edits run in normalized space then preserve
// unchanged original lines.
func ApplyEditsToNormalizedContent(normalizedContent string, edits []Edit, path string) (*AppliedEditsResult, error) {
	normalizedEdits := make([]Edit, len(edits))
	for i, edit := range edits {
		normalizedEdits[i] = Edit{OldText: NormalizeToLF(edit.OldText), NewText: NormalizeToLF(edit.NewText)}
	}

	for i := range normalizedEdits {
		if len(normalizedEdits[i].OldText) == 0 {
			return nil, getEmptyOldTextError(path, i, len(normalizedEdits))
		}
	}

	usedFuzzyMatch := false
	for _, edit := range normalizedEdits {
		if match := FuzzyFindText(normalizedContent, edit.OldText); match.UsedFuzzyMatch {
			usedFuzzyMatch = true
			break
		}
	}
	replacementBaseContent := normalizedContent
	if usedFuzzyMatch {
		replacementBaseContent = NormalizeForFuzzyMatch(normalizedContent)
	}

	var matchedEdits []matchedEdit
	for i := range normalizedEdits {
		edit := normalizedEdits[i]
		matchResult := FuzzyFindText(replacementBaseContent, edit.OldText)
		if !matchResult.Found {
			return nil, getNotFoundError(path, i, len(normalizedEdits))
		}

		occurrences := countOccurrences(replacementBaseContent, edit.OldText)
		if occurrences > 1 {
			return nil, getDuplicateError(path, i, len(normalizedEdits), occurrences)
		}

		matchedEdits = append(matchedEdits, matchedEdit{
			editIndex:   i,
			matchIndex:  matchResult.Index,
			matchLength: matchResult.MatchLength,
			newText:     edit.NewText,
		})
	}

	sort.SliceStable(matchedEdits, func(a, b int) bool { return matchedEdits[a].matchIndex < matchedEdits[b].matchIndex })
	for i := 1; i < len(matchedEdits); i++ {
		previous := matchedEdits[i-1]
		current := matchedEdits[i]
		if previous.matchIndex+previous.matchLength > current.matchIndex {
			return nil, fmt.Errorf("edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.",
				previous.editIndex, current.editIndex, path)
		}
	}

	baseContent := normalizedContent
	var newContent string
	if usedFuzzyMatch {
		preserved, err := ApplyReplacementsPreservingUnchangedLines(normalizedContent, replacementBaseContent, toTextReplacements(matchedEdits))
		if err != nil {
			return nil, err
		}
		newContent = preserved
	} else {
		newContent = applyReplacements(replacementBaseContent, toTextReplacements(matchedEdits), 0)
	}

	if baseContent == newContent {
		return nil, getNoChangeError(path, len(normalizedEdits))
	}

	return &AppliedEditsResult{BaseContent: baseContent, NewContent: newContent}, nil
}

func toTextReplacements(matched []matchedEdit) []textReplacement {
	out := make([]textReplacement, len(matched))
	for i, m := range matched {
		out[i] = textReplacement{matchIndex: m.matchIndex, matchLength: m.matchLength, newText: m.newText}
	}
	return out
}

// GenerateUnifiedPatch builds a standard unified patch (4 context lines).
func GenerateUnifiedPatch(path, oldContent, newContent string, contextLines int) string {
	if contextLines == 0 {
		contextLines = 4
	}
	return diff.CreateTwoFilesPatch(path, path, oldContent, newContent, nil, nil,
		&diff.Options{Context: contextLines}, &diff.FileHeadersOnly)
}

// EditDiffResult is the display diff plus first changed line.
type EditDiffResult struct {
	Diff             string
	FirstChangedLine int
	HasFirstChanged  bool
}

// GenerateDiffString renders a line-numbered diff with context elision.
func GenerateDiffString(oldContent, newContent string, contextLines int) *EditDiffResult {
	if contextLines == 0 {
		contextLines = 4
	}
	parts := diff.DiffLines(oldContent, newContent, nil)
	var output []string

	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	maxLineNum := len(oldLines)
	if len(newLines) > maxLineNum {
		maxLineNum = len(newLines)
	}
	lineNumWidth := len(fmt.Sprintf("%d", maxLineNum))

	oldLineNum := 1
	newLineNum := 1
	lastWasChange := false
	firstChangedLine := 0
	hasFirstChanged := false

	padNum := func(n int) string { return fmt.Sprintf("%*d", lineNumWidth, n) }

	for i := 0; i < len(parts); i++ {
		part := parts[i]
		raw := strings.Split(part.Value, "\n")
		if len(raw) > 0 && raw[len(raw)-1] == "" {
			raw = raw[:len(raw)-1]
		}

		if part.Added || part.Removed {
			if !hasFirstChanged {
				firstChangedLine = newLineNum
				hasFirstChanged = true
			}

			for _, line := range raw {
				if part.Added {
					output = append(output, "+"+padNum(newLineNum)+" "+line)
					newLineNum++
				} else {
					output = append(output, "-"+padNum(oldLineNum)+" "+line)
					oldLineNum++
				}
			}
			lastWasChange = true
		} else {
			nextPartIsChange := i < len(parts)-1 && (parts[i+1].Added || parts[i+1].Removed)
			hasLeadingChange := lastWasChange
			hasTrailingChange := nextPartIsChange

			emitContext := func(lines []string) {
				for _, line := range lines {
					output = append(output, " "+padNum(oldLineNum)+" "+line)
					oldLineNum++
					newLineNum++
				}
			}
			emitSkipped := func(count int) {
				output = append(output, " "+strings.Repeat(" ", lineNumWidth)+" ...")
				oldLineNum += count
				newLineNum += count
			}

			switch {
			case hasLeadingChange && hasTrailingChange:
				if len(raw) <= contextLines*2 {
					emitContext(raw)
				} else {
					leading := raw[:contextLines]
					trailing := raw[len(raw)-contextLines:]
					skipped := len(raw) - len(leading) - len(trailing)
					emitContext(leading)
					emitSkipped(skipped)
					emitContext(trailing)
				}
			case hasLeadingChange:
				shown := raw
				if len(raw) > contextLines {
					shown = raw[:contextLines]
				}
				skipped := len(raw) - len(shown)
				emitContext(shown)
				if skipped > 0 {
					emitSkipped(skipped)
				}
			case hasTrailingChange:
				skipped := len(raw) - contextLines
				if skipped < 0 {
					skipped = 0
				}
				if skipped > 0 {
					emitSkipped(skipped)
				}
				emitContext(raw[skipped:])
			default:
				oldLineNum += len(raw)
				newLineNum += len(raw)
			}

			lastWasChange = false
		}
	}

	return &EditDiffResult{Diff: strings.Join(output, "\n"), FirstChangedLine: firstChangedLine, HasFirstChanged: hasFirstChanged}
}

// ComputeEditsDiff previews edits without applying them.
func ComputeEditsDiff(path string, edits []Edit, cwd string) (*EditDiffResult, string) {
	absolutePath := ResolveToCwd(path, cwd)

	if _, err := os.Stat(absolutePath); err != nil {
		return nil, fmt.Sprintf("Could not edit file: %s. Error code: %s.", path, "ENOENT")
	}

	raw, err := os.ReadFile(absolutePath)
	if err != nil {
		return nil, fmt.Sprintf("Could not edit file: %s. %s.", path, err.Error())
	}

	_, content := SplitBom(string(raw))
	normalizedContent := NormalizeToLF(content)
	applied, err := ApplyEditsToNormalizedContent(normalizedContent, edits, path)
	if err != nil {
		return nil, err.Error()
	}

	return GenerateDiffString(applied.BaseContent, applied.NewContent, 4), ""
}

// ComputeEditDiff previews a single edit.
func ComputeEditDiff(path, oldText, newText, cwd string) (*EditDiffResult, string) {
	return ComputeEditsDiff(path, []Edit{{OldText: oldText, NewText: newText}}, cwd)
}
