// Package diff is a 1:1 port of the npm jsdiff 8.0.4 subset pi uses:
// diffLines and createTwoFilesPatch/structuredPatch/formatPatch with
// FILE_HEADERS_ONLY header options.
//
// The Myers diff core mirrors libesm/diff/base.js including its diagonal
// pruning optimizations and component coalescing, so hunk boundaries and
// change ordering match the JS output exactly.
package diff

import (
	"fmt"
	"strings"
)

// Options mirrors the jsdiff option subset relevant to line diffing and patch
// generation.
type Options struct {
	IgnoreWhitespace   bool
	IgnoreNewlineAtEOF bool
	StripTrailingCR    bool
	NewlineIsToken     bool
	MaxEditLength      int // 0 = unlimited
	Context            int // patch context lines; default 4 when patching
}

// HeaderOptions mirrors jsdiff header option presets.
type HeaderOptions struct {
	IncludeIndex       bool
	IncludeUnderline   bool
	IncludeFileHeaders bool
}

// IncludeHeaders mirrors jsdiff INCLUDE_HEADERS.
var IncludeHeaders = HeaderOptions{true, true, true}

// FileHeadersOnly mirrors jsdiff FILE_HEADERS_ONLY.
var FileHeadersOnly = HeaderOptions{false, false, true}

// OmitHeaders mirrors jsdiff OMIT_HEADERS.
var OmitHeaders = HeaderOptions{false, false, false}

// Change is one diff component.
type Change struct {
	Count   int
	Added   bool
	Removed bool
	Value   string

	lines []string `json:"-"`
}

type component struct {
	count    int
	added    bool
	removed  bool
	previous *component
}

type pathNode struct {
	oldPos        int
	lastComponent *component
}

// DiffLines diffs two strings line by line, mirroring jsdiff diffLines.
func DiffLines(oldStr, newStr string, options *Options) []Change {
	if options == nil {
		options = &Options{}
	}
	oldTokens := removeEmpty(tokenize(oldStr, options))
	newTokens := removeEmpty(tokenize(newStr, options))
	return diffWithOptionsObj(oldTokens, newTokens, options)
}

// tokenize ports jsdiff's line tokenizer: split on \n or \r\n keeping the
// separator, then merge separators into the preceding line (unless
// newlineIsToken).
func tokenize(value string, options *Options) []string {
	if options.StripTrailingCR {
		value = strings.ReplaceAll(value, "\r\n", "\n")
	}
	parts := splitKeepSeparators(value)
	// Drop the final empty token when the string ends with a newline.
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	var ret []string
	for i, line := range parts {
		if i%2 == 1 && !options.NewlineIsToken {
			if len(ret) > 0 {
				ret[len(ret)-1] += line
			} else {
				ret = append(ret, line)
			}
		} else {
			ret = append(ret, line)
		}
	}
	return ret
}

// splitKeepSeparators splits on \n and \r\n capturing separators, mirroring
// value.split(/(\n|\r\n)/).
func splitKeepSeparators(value string) []string {
	var out []string
	start := 0
	i := 0
	for i < len(value) {
		if value[i] == '\n' {
			out = append(out, value[start:i])
			sep := "\n"
			if i > start && value[i-1] == '\r' {
				out[len(out)-1] = value[start : i-1]
				sep = "\r\n"
			}
			out = append(out, sep)
			i++
			start = i
		} else {
			i++
		}
	}
	out = append(out, value[start:])
	return out
}

func equals(left, right string, options *Options) bool {
	if options.IgnoreWhitespace {
		if !options.NewlineIsToken || !strings.Contains(left, "\n") {
			left = strings.TrimSpace(left)
		}
		if !options.NewlineIsToken || !strings.Contains(right, "\n") {
			right = strings.TrimSpace(right)
		}
	} else if options.IgnoreNewlineAtEOF && !options.NewlineIsToken {
		left = strings.TrimSuffix(left, "\n")
		right = strings.TrimSuffix(right, "\n")
	}
	return left == right
}

func removeEmpty(tokens []string) []string {
	ret := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if t != "" {
			ret = append(ret, t)
		}
	}
	return ret
}

func diffWithOptionsObj(oldTokens, newTokens []string, options *Options) []Change {
	newLen, oldLen := len(newTokens), len(oldTokens)
	editLength := 1
	maxEditLength := newLen + oldLen
	if options.MaxEditLength != 0 && options.MaxEditLength < maxEditLength {
		maxEditLength = options.MaxEditLength
	}
	bestPath := map[int]*pathNode{0: {oldPos: -1}}
	newPos := extractCommon(bestPath[0], newTokens, oldTokens, 0, options)
	if bestPath[0].oldPos+1 >= oldLen && newPos+1 >= newLen {
		return buildValues(bestPath[0].lastComponent, newTokens, oldTokens)
	}
	minDiagonalToConsider := -1 << 30
	maxDiagonalToConsider := 1 << 30
	execEditLength := func() ([]Change, bool) {
		for diagonalPath := maxInt(minDiagonalToConsider, -editLength); diagonalPath <= minInt(maxDiagonalToConsider, editLength); diagonalPath += 2 {
			var basePath *pathNode
			removePath := bestPath[diagonalPath-1]
			addPath := bestPath[diagonalPath+1]
			if removePath != nil {
				delete(bestPath, diagonalPath-1)
			}
			canAdd := false
			if addPath != nil {
				addPathNewPos := addPath.oldPos - diagonalPath
				canAdd = 0 <= addPathNewPos && addPathNewPos < newLen
			}
			canRemove := removePath != nil && removePath.oldPos+1 < oldLen
			if !canAdd && !canRemove {
				delete(bestPath, diagonalPath)
				continue
			}
			if !canRemove || (canAdd && removePath.oldPos < addPath.oldPos) {
				basePath = addToPath(addPath, true, false, 0, options)
			} else {
				basePath = addToPath(removePath, false, true, 1, options)
			}
			newPos = extractCommon(basePath, newTokens, oldTokens, diagonalPath, options)
			if basePath.oldPos+1 >= oldLen && newPos+1 >= newLen {
				return buildValues(basePath.lastComponent, newTokens, oldTokens), true
			}
			bestPath[diagonalPath] = basePath
			if basePath.oldPos+1 >= oldLen {
				maxDiagonalToConsider = minInt(maxDiagonalToConsider, diagonalPath-1)
			}
			if newPos+1 >= newLen {
				minDiagonalToConsider = maxInt(minDiagonalToConsider, diagonalPath+1)
			}
		}
		editLength++
		return nil, false
	}
	for editLength <= maxEditLength {
		if ret, done := execEditLength(); done {
			return ret
		}
	}
	return nil
}

func addToPath(path *pathNode, added, removed bool, oldPosInc int, options *Options) *pathNode {
	last := path.lastComponent
	if last != nil && last.added == added && last.removed == removed {
		return &pathNode{
			oldPos:        path.oldPos + oldPosInc,
			lastComponent: &component{count: last.count + 1, added: added, removed: removed, previous: last.previous},
		}
	}
	return &pathNode{
		oldPos:        path.oldPos + oldPosInc,
		lastComponent: &component{count: 1, added: added, removed: removed, previous: last},
	}
}

func extractCommon(basePath *pathNode, newTokens, oldTokens []string, diagonalPath int, options *Options) int {
	newLen, oldLen := len(newTokens), len(oldTokens)
	oldPos := basePath.oldPos
	newPos := oldPos - diagonalPath
	commonCount := 0
	for newPos+1 < newLen && oldPos+1 < oldLen && equals(oldTokens[oldPos+1], newTokens[newPos+1], options) {
		newPos++
		oldPos++
		commonCount++
	}
	if commonCount > 0 {
		basePath.lastComponent = &component{count: commonCount, previous: basePath.lastComponent}
	}
	basePath.oldPos = oldPos
	return newPos
}

func buildValues(lastComponent *component, newTokens, oldTokens []string) []Change {
	var components []*component
	for lastComponent != nil {
		components = append(components, lastComponent)
		lastComponent = lastComponent.previous
	}
	// reverse
	for i, j := 0, len(components)-1; i < j; i, j = i+1, j-1 {
		components[i], components[j] = components[j], components[i]
	}
	out := make([]Change, 0, len(components))
	newPos, oldPos := 0, 0
	for _, c := range components {
		if !c.removed {
			out = append(out, Change{
				Count:   c.count,
				Added:   c.added,
				Removed: c.removed,
				Value:   strings.Join(newTokens[newPos:newPos+c.count], ""),
			})
			newPos += c.count
			if !c.added {
				oldPos += c.count
			}
		} else {
			out = append(out, Change{
				Count:   c.count,
				Added:   c.added,
				Removed: c.removed,
				Value:   strings.Join(oldTokens[oldPos:oldPos+c.count], ""),
			})
			oldPos += c.count
		}
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Patch generation (patch/create.js)
// ---------------------------------------------------------------------------

// Hunk is one unified-diff hunk.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Lines              []string
}

// StructuredPatch mirrors jsdiff structuredPatch.
func StructuredPatch(oldFileName, newFileName, oldStr, newStr string, oldHeader, newHeader *string, options *Options) *PatchObject {
	opts := Options{Context: 4}
	if options != nil {
		opts = *options
		if options.Context == 0 {
			opts.Context = 4
		}
	}
	return diffLinesResultToPatch(DiffLines(oldStr, newStr, &opts), oldFileName, newFileName, oldHeader, newHeader, opts.Context)
}

// PatchObject mirrors jsdiff's structured patch object.
type PatchObject struct {
	OldFileName, NewFileName string
	OldHeader, NewHeader     *string
	Hunks                    []*Hunk
}

func diffLinesResultToPatch(changes []Change, oldFileName, newFileName string, oldHeader, newHeader *string, context int) *PatchObject {
	changes = append(append([]Change{}, changes...), Change{Value: "", lines: []string{}})
	hunks := []*Hunk{}
	oldRangeStart, newRangeStart := 0, 0
	var curRange []string
	oldLine, newLine := 1, 1
	for i := 0; i < len(changes); i++ {
		current := changes[i]
		lines := current.lines
		if lines == nil {
			lines = splitLines(current.Value)
			current.lines = lines
			changes[i] = current
		}
		if current.Added || current.Removed {
			if oldRangeStart == 0 {
				oldRangeStart, newRangeStart = oldLine, newLine
				if i > 0 {
					prev := changes[i-1].lines
					curRange = nil
					if context > 0 {
						tail := prev
						if len(tail) > context {
							tail = tail[len(tail)-context:]
						}
						for _, entry := range tail {
							curRange = append(curRange, " "+entry)
						}
					}
					oldRangeStart -= len(curRange)
					newRangeStart -= len(curRange)
				}
			}
			prefix := "-"
			if current.Added {
				prefix = "+"
			}
			for _, line := range lines {
				curRange = append(curRange, prefix+line)
			}
			if current.Added {
				newLine += len(lines)
			} else {
				oldLine += len(lines)
			}
		} else {
			if oldRangeStart != 0 {
				if len(lines) <= context*2 && i < len(changes)-2 {
					for _, line := range lines {
						curRange = append(curRange, " "+line)
					}
				} else {
					contextSize := minInt(len(lines), context)
					for _, line := range lines[:contextSize] {
						curRange = append(curRange, " "+line)
					}
					hunks = append(hunks, &Hunk{
						OldStart: oldRangeStart,
						OldLines: oldLine - oldRangeStart + contextSize,
						NewStart: newRangeStart,
						NewLines: newLine - newRangeStart + contextSize,
						Lines:    curRange,
					})
					oldRangeStart, newRangeStart = 0, 0
					curRange = nil
				}
			}
			oldLine += len(lines)
			newLine += len(lines)
		}
	}
	// Step 2: strip trailing newlines; mark missing ones.
	for _, hunk := range hunks {
		for i := 0; i < len(hunk.Lines); i++ {
			if strings.HasSuffix(hunk.Lines[i], "\n") {
				hunk.Lines[i] = hunk.Lines[i][:len(hunk.Lines[i])-1]
			} else {
				rest := append([]string{"\\ No newline at end of file"}, hunk.Lines[i+1:]...)
				hunk.Lines = append(hunk.Lines[:i+1], rest...)
				i++
			}
		}
	}
	return &PatchObject{
		OldFileName: oldFileName,
		NewFileName: newFileName,
		OldHeader:   oldHeader,
		NewHeader:   newHeader,
		Hunks:       hunks,
	}
}

// splitLines splits text into lines each including its trailing newline,
// mirroring jsdiff's private splitLines.
func splitLines(text string) []string {
	hasTrailingNl := strings.HasSuffix(text, "\n")
	parts := strings.Split(text, "\n")
	result := make([]string, 0, len(parts))
	for _, line := range parts {
		result = append(result, line+"\n")
	}
	if hasTrailingNl {
		result = result[:len(result)-1]
	} else {
		last := result[len(result)-1]
		result[len(result)-1] = last[:len(last)-1]
	}
	return result
}

// FormatPatch mirrors jsdiff formatPatch for a single patch object.
func FormatPatch(patch *PatchObject, headerOptions *HeaderOptions) string {
	if headerOptions == nil {
		headerOptions = &IncludeHeaders
	}
	var ret []string
	if headerOptions.IncludeIndex && patch.OldFileName == patch.NewFileName {
		ret = append(ret, "Index: "+patch.OldFileName)
	}
	if headerOptions.IncludeUnderline {
		ret = append(ret, "===================================================================")
	}
	if headerOptions.IncludeFileHeaders {
		line := "--- " + patch.OldFileName
		if patch.OldHeader != nil {
			line += "\t" + *patch.OldHeader
		}
		ret = append(ret, line)
		line = "+++ " + patch.NewFileName
		if patch.NewHeader != nil {
			line += "\t" + *patch.NewHeader
		}
		ret = append(ret, line)
	}
	for _, hunk := range patch.Hunks {
		oldStart, oldLines := hunk.OldStart, hunk.OldLines
		newStart, newLines := hunk.NewStart, hunk.NewLines
		if oldLines == 0 {
			oldStart--
		}
		if newLines == 0 {
			newStart--
		}
		ret = append(ret, fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, oldLines, newStart, newLines))
		ret = append(ret, hunk.Lines...)
	}
	return strings.Join(ret, "\n") + "\n"
}

// CreateTwoFilesPatch mirrors jsdiff createTwoFilesPatch.
func CreateTwoFilesPatch(oldFileName, newFileName, oldStr, newStr string, oldHeader, newHeader *string, options *Options, headerOptions *HeaderOptions) string {
	patchObj := StructuredPatch(oldFileName, newFileName, oldStr, newStr, oldHeader, newHeader, options)
	return FormatPatch(patchObj, headerOptions)
}

// CreatePatch mirrors jsdiff createPatch.
func CreatePatch(fileName, oldStr, newStr string, oldHeader, newHeader *string, options *Options, headerOptions *HeaderOptions) string {
	return CreateTwoFilesPatch(fileName, fileName, oldStr, newStr, oldHeader, newHeader, options, headerOptions)
}
