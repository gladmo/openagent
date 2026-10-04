package codingagent

// Port of modes/interactive/components/diff.ts: colored diff rendering
// with removed/added runs, intra-line word diffing on single-line
// modifications (inverse highlight on changed tokens, leading
// whitespace excluded), and tab expansion. dynamic-border.ts's
// width-adaptive rule lands with DynamicBorderComponent.

import (
	"regexp"
	"strings"

	"github.com/gladmo/openagent/diff"
)

var diffLinePattern = regexp.MustCompile(`^([+\- ])([ ]*[0-9]*) (.*)$`)

type parsedDiffLine struct {
	prefix  string
	lineNum string
	content string
}

// parseDiffLine extracts prefix/line-number/content ("+123 text").
func parseDiffLine(line string) *parsedDiffLine {
	match := diffLinePattern.FindStringSubmatch(line)
	if match == nil {
		return nil
	}
	return &parsedDiffLine{prefix: match[1], lineNum: match[2], content: match[3]}
}

// replaceTabs expands tabs to three spaces.
func replaceTabs(text string) string {
	return strings.ReplaceAll(text, "\t", "   ")
}

var leadingWhitespacePattern = regexp.MustCompile(`^\s*`)

// renderIntraLineDiff word-diffs one modified line, inversing changed
// tokens and keeping leading whitespace unhighlighted. The ported diff
// package's tokenizer groups whitespace with adjacent words (jsdiff
// diffWords semantics).
func renderIntraLineDiff(oldContent, newContent string) (removedLine, addedLine string) {
	wordDiff := diff.DiffLines(oldContent, newContent, nil)

	isFirstRemoved := true
	isFirstAdded := true

	for _, part := range wordDiff {
		switch {
		case part.Removed:
			value := part.Value
			if isFirstRemoved {
				leading := leadingWhitespacePattern.FindString(value)
				value = value[len(leading):]
				removedLine += leading
				isFirstRemoved = false
			}
			if value != "" {
				removedLine += "\x1b[7m" + value + "\x1b[27m"
			}
		case part.Added:
			value := part.Value
			if isFirstAdded {
				leading := leadingWhitespacePattern.FindString(value)
				value = value[len(leading):]
				addedLine += leading
				isFirstAdded = false
			}
			if value != "" {
				addedLine += "\x1b[7m" + value + "\x1b[27m"
			}
		default:
			removedLine += part.Value
			addedLine += part.Value
		}
	}
	return removedLine, addedLine
}

// RenderDiff renders a diff string: context dim, removed red, added
// green, inverse on intra-line changes for single-line modifications.
func RenderDiff(diffText string) string {
	lines := strings.Split(diffText, "\n")
	var result []string

	i := 0
	for i < len(lines) {
		line := lines[i]
		parsed := parseDiffLine(line)

		if parsed == nil {
			result = append(result, line)
			i++
			continue
		}

		if parsed.prefix == "-" {
			var removedLines []parsedDiffLine
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "-" {
					break
				}
				removedLines = append(removedLines, *p)
				i++
			}
			var addedLines []parsedDiffLine
			for i < len(lines) {
				p := parseDiffLine(lines[i])
				if p == nil || p.prefix != "+" {
					break
				}
				addedLines = append(addedLines, *p)
				i++
			}

			// Intra-line diffing only on exactly-one-removed +
			// exactly-one-added (a single-line modification).
			if len(removedLines) == 1 && len(addedLines) == 1 {
				removedLine, addedLine := renderIntraLineDiff(
					replaceTabs(removedLines[0].content),
					replaceTabs(addedLines[0].content),
				)
				result = append(result, "-"+removedLines[0].lineNum+" "+removedLine)
				result = append(result, "+"+addedLines[0].lineNum+" "+addedLine)
			} else {
				for _, removed := range removedLines {
					result = append(result, "-"+removed.lineNum+" "+replaceTabs(removed.content))
				}
				for _, added := range addedLines {
					result = append(result, "+"+added.lineNum+" "+replaceTabs(added.content))
				}
			}
		} else if parsed.prefix == "+" {
			result = append(result, "+"+parsed.lineNum+" "+replaceTabs(parsed.content))
			i++
		} else {
			result = append(result, " "+parsed.lineNum+" "+replaceTabs(parsed.content))
			i++
		}
	}
	return strings.Join(result, "\n")
}
