package tools

// edit_diff.go ports harness/tools/edit-diff.ts.

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/gladmo/openagent/diff"
)

// DetectLineEnding mirrors detectLineEnding.
func DetectLineEnding(content string) string {
	crlfIdx := strings.Index(content, "\r\n")
	lfIdx := strings.Index(content, "\n")
	if lfIdx == -1 {
		return "\n"
	}
	if crlfIdx == -1 {
		return "\n"
	}
	if crlfIdx < lfIdx {
		return "\r\n"
	}
	return "\n"
}

// NormalizeToLF mirrors normalizeToLF.
func NormalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// RestoreLineEndings mirrors restoreLineEndings.
func RestoreLineEndings(text, ending string) string {
	if ending == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

// NormalizeForFuzzyMatch mirrors normalizeForFuzzyMatch: strip trailing
// whitespace, smart quotes -> ASCII, Unicode dashes -> hyphen, special
// spaces -> space. (NFKC normalization is approximated by the explicit
// character mappings, which cover the tested transformations.)
func NormalizeForFuzzyMatch(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = trimTrailingWhitespace(line)
	}
	out := strings.Join(lines, "\n")
	replacements := []struct{ from, to string }{
		{"‘", "'"}, {"’", "'"}, {"‚", "'"}, {"‛", "'"},
		{"“", `"`}, {"”", `"`}, {"„", `"`}, {"‟", `"`},
		{"‐", "-"}, {"‑", "-"}, {"‒", "-"}, {"–", "-"}, {"—", "-"}, {"―", "-"}, {"−", "-"},
		{" ", " "}, {" ", " "}, {" ", " "}, {" ", " "}, {" ", " "},
	}
	for _, r := range replacements {
		out = strings.ReplaceAll(out, r.from, r.to)
	}
	// Remaining special spaces (U+2002-U+200A, U+202F, U+205F, U+3000).
	out = strings.Map(func(r rune) rune {
		switch {
		case r >= 0x2002 && r <= 0x200A, r == 0x202F, r == 0x205F, r == 0x3000:
			return ' '
		}
		return r
	}, out)
	return out
}

func trimTrailingWhitespace(line string) string {
	return strings.TrimRightFunc(line, unicode.IsSpace)
}

func splitLinesWithEndings(content string) []string {
	if content == "" {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			lines = append(lines, content[start:i+1])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}

type lineSpan struct{ start, end int }

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
	offset := 0
	spans := splitLinesWithEndings(content)
	out := make([]lineSpan, 0, len(spans))
	for _, line := range spans {
		out = append(out, lineSpan{start: offset, end: offset + len(line)})
		offset += len(line)
	}
	return out
}

func getReplacementLineRange(lines []lineSpan, replacement textReplacement) (int, int, error) {
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
		return 0, 0, fmt.Errorf("Replacement range is outside the base content.")
	}

	endLine := startLine
	for endLine < len(lines) && lines[endLine].end < replacementEnd {
		endLine++
	}
	if endLine >= len(lines) {
		return 0, 0, fmt.Errorf("Replacement range is outside the base content.")
	}
	return startLine, endLine + 1, nil
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

// ApplyReplacementsPreservingUnchangedLines overlays line-level normalized
// replacements onto the original bytes.
func ApplyReplacementsPreservingUnchangedLines(originalContent, baseContent string, replacements []textReplacement) (string, error) {
	originalLines := splitLinesWithEndings(originalContent)
	baseLines := getLineSpans(baseContent)
	if len(originalLines) != len(baseLines) {
		return "", fmt.Errorf("Cannot preserve unchanged lines because the base content has a different line count.")
	}

	type group struct {
		startLine, endLine int
		replacements       []textReplacement
	}
	var groups []group
	sorted := append([]textReplacement{}, replacements...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].matchIndex < sorted[j].matchIndex })
	for _, replacement := range sorted {
		startLine, endLine, err := getReplacementLineRange(baseLines, replacement)
		if err != nil {
			return "", err
		}
		if len(groups) > 0 && startLine < groups[len(groups)-1].endLine {
			current := &groups[len(groups)-1]
			if endLine > current.endLine {
				current.endLine = endLine
			}
			current.replacements = append(current.replacements, replacement)
			continue
		}
		groups = append(groups, group{startLine, endLine, []textReplacement{replacement}})
	}

	originalLineIndex := 0
	result := ""
	for _, g := range groups {
		for _, line := range originalLines[originalLineIndex:g.startLine] {
			result += line
		}
		groupStartOffset := baseLines[g.startLine].start
		groupEndOffset := baseLines[g.endLine-1].end
		result += applyReplacements(baseContent[groupStartOffset:groupEndOffset], g.replacements, groupStartOffset)
		originalLineIndex = g.endLine
	}
	for _, line := range originalLines[originalLineIndex:] {
		result += line
	}
	return result, nil
}

// FuzzyMatchResult mirrors the TS interface.
type FuzzyMatchResult struct {
	Found                 bool
	Index                 int
	MatchLength           int
	UsedFuzzyMatch        bool
	ContentForReplacement string
}

// FuzzyFindText finds oldText in content: exact match first, then fuzzy.
func FuzzyFindText(content, oldText string) FuzzyMatchResult {
	if index := strings.Index(content, oldText); index != -1 {
		return FuzzyMatchResult{Found: true, Index: index, MatchLength: len(oldText), ContentForReplacement: content}
	}
	fuzzyContent := NormalizeForFuzzyMatch(content)
	fuzzyOldText := NormalizeForFuzzyMatch(oldText)
	fuzzyIndex := strings.Index(fuzzyContent, fuzzyOldText)
	if fuzzyIndex == -1 {
		return FuzzyMatchResult{Index: -1, ContentForReplacement: content}
	}
	return FuzzyMatchResult{
		Found: true, Index: fuzzyIndex, MatchLength: len(fuzzyOldText),
		UsedFuzzyMatch: true, ContentForReplacement: fuzzyContent,
	}
}

// utf8Bom is the UTF-8 byte order mark (U+FEFF, encoded as 3 bytes).
const utf8Bom = "\ufeff"

// StripBom strips a UTF-8 BOM.
func StripBom(content string) (string, string) {
	if strings.HasPrefix(content, utf8Bom) {
		return utf8Bom, content[len(utf8Bom):]
	}
	return "", content
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

// ApplyEditsToNormalizedContent applies exact-text replacements to
// LF-normalized content with fuzzy fallback.
func ApplyEditsToNormalizedContent(normalizedContent string, edits []Edit, path string) (string, string, error) {
	normalizedEdits := make([]Edit, len(edits))
	for i, edit := range edits {
		normalizedEdits[i] = Edit{OldText: NormalizeToLF(edit.OldText), NewText: NormalizeToLF(edit.NewText)}
	}
	for i := range normalizedEdits {
		if len(normalizedEdits[i].OldText) == 0 {
			return "", "", getEmptyOldTextError(path, i, len(normalizedEdits))
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
	for i, edit := range normalizedEdits {
		matchResult := FuzzyFindText(replacementBaseContent, edit.OldText)
		if !matchResult.Found {
			return "", "", getNotFoundError(path, i, len(normalizedEdits))
		}
		occurrences := countOccurrences(replacementBaseContent, edit.OldText)
		if occurrences > 1 {
			return "", "", getDuplicateError(path, i, len(normalizedEdits), occurrences)
		}
		matchedEdits = append(matchedEdits, matchedEdit{
			editIndex: i, matchIndex: matchResult.Index, matchLength: matchResult.MatchLength, newText: edit.NewText,
		})
	}

	sort.SliceStable(matchedEdits, func(i, j int) bool { return matchedEdits[i].matchIndex < matchedEdits[j].matchIndex })
	for i := 1; i < len(matchedEdits); i++ {
		previous := matchedEdits[i-1]
		current := matchedEdits[i]
		if previous.matchIndex+previous.matchLength > current.matchIndex {
			return "", "", fmt.Errorf("edits[%d] and edits[%d] overlap in %s. Merge them into one edit or target disjoint regions.", previous.editIndex, current.editIndex, path)
		}
	}

	baseContent := normalizedContent
	replacements := make([]textReplacement, len(matchedEdits))
	for i, m := range matchedEdits {
		replacements[i] = textReplacement{matchIndex: m.matchIndex, matchLength: m.matchLength, newText: m.newText}
	}
	var newContent string
	if usedFuzzyMatch {
		preserved, err := ApplyReplacementsPreservingUnchangedLines(normalizedContent, replacementBaseContent, replacements)
		if err != nil {
			return "", "", err
		}
		newContent = preserved
	} else {
		newContent = applyReplacements(replacementBaseContent, replacements, 0)
	}

	if baseContent == newContent {
		return "", "", getNoChangeError(path, len(normalizedEdits))
	}
	return baseContent, newContent, nil
}

// GenerateUnifiedPatch mirrors generateUnifiedPatch.
func GenerateUnifiedPatch(path, oldContent, newContent string, contextLines int) string {
	if contextLines == 0 {
		contextLines = 4
	}
	return diff.CreateTwoFilesPatch(path, path, oldContent, newContent, nil, nil,
		&diff.Options{Context: contextLines}, &diff.FileHeadersOnly)
}

// GenerateDiffString mirrors generateDiffString: numbered display diff
// with context elision; returns (diff, firstChangedLine or 0).
func GenerateDiffString(oldContent, newContent string, contextLines int) (string, int) {
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

	padNum := func(n int) string {
		return padLeft(fmt.Sprintf("%d", n), lineNumWidth)
	}

	oldLineNum := 1
	newLineNum := 1
	lastWasChange := false
	firstChangedLine := 0

	splitPart := func(value string) []string {
		raw := strings.Split(value, "\n")
		if len(raw) > 0 && raw[len(raw)-1] == "" {
			raw = raw[:len(raw)-1]
		}
		return raw
	}
	isChange := func(i int) bool {
		return parts[i].Added || parts[i].Removed
	}

	for i := 0; i < len(parts); i++ {
		part := parts[i]
		raw := splitPart(part.Value)
		if isChange(i) {
			if firstChangedLine == 0 {
				firstChangedLine = newLineNum
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
			nextPartIsChange := i < len(parts)-1 && isChange(i+1)
			hasLeadingChange := lastWasChange
			hasTrailingChange := nextPartIsChange
			emit := func(lines []string) {
				for _, line := range lines {
					output = append(output, " "+padNum(oldLineNum)+" "+line)
					oldLineNum++
					newLineNum++
				}
			}
			switch {
			case hasLeadingChange && hasTrailingChange:
				if len(raw) <= contextLines*2 {
					emit(raw)
				} else {
					leading := raw[:contextLines]
					trailing := raw[len(raw)-contextLines:]
					skipped := len(raw) - len(leading) - len(trailing)
					emit(leading)
					output = append(output, " "+padLeft("", lineNumWidth)+" ...")
					oldLineNum += skipped
					newLineNum += skipped
					emit(trailing)
				}
			case hasLeadingChange:
				shown := raw
				skipped := 0
				if len(raw) > contextLines {
					shown = raw[:contextLines]
					skipped = len(raw) - len(shown)
				}
				emit(shown)
				if skipped > 0 {
					output = append(output, " "+padLeft("", lineNumWidth)+" ...")
					oldLineNum += skipped
					newLineNum += skipped
				}
			case hasTrailingChange:
				skipped := len(raw) - contextLines
				if skipped < 0 {
					skipped = 0
				}
				if skipped > 0 {
					output = append(output, " "+padLeft("", lineNumWidth)+" ...")
					oldLineNum += skipped
					newLineNum += skipped
				}
				emit(raw[skipped:])
			default:
				oldLineNum += len(raw)
				newLineNum += len(raw)
			}
			lastWasChange = false
		}
	}
	return strings.Join(output, "\n"), firstChangedLine
}

func padLeft(s string, width int) string {
	for len(s) < width {
		s = " " + s
	}
	return s
}
