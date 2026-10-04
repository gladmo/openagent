package tui

import "regexp"

// Port of word-navigation.ts. Cursor offsets are rune offsets into text.

// WordNavigationOptions customizes word movement.
type WordNavigationOptions struct {
	// Segment returns word segments for the given text; nil uses the default
	// UAX #29 word segmentation.
	Segment func(text string) []WordSegment
	// IsAtomicSegment identifies atomic segments treated as single units
	// (e.g. paste markers).
	IsAtomicSegment func(segment string) bool
}

var punctuationGlobal = regexp.MustCompile("[(){}\\[\\]<>.,;:'\"!?+\\-=*/\\\\|&%^$#@~`]")

// FindWordBackward finds the cursor position after moving one word backward
// from cursor. Skips trailing whitespace, then stops at the next
// word/punctuation boundary. Pure function.
func FindWordBackward(text string, cursor int, options *WordNavigationOptions) int {
	if cursor <= 0 {
		return 0
	}

	runes := []rune(text)
	if cursor > len(runes) {
		cursor = len(runes)
	}
	textBeforeCursor := string(runes[:cursor])

	var segments []WordSegment
	if options != nil && options.Segment != nil {
		segments = options.Segment(textBeforeCursor)
	} else {
		segments = segmentWords(textBeforeCursor)
	}
	var isAtomic func(string) bool
	if options != nil {
		isAtomic = options.IsAtomicSegment
	}

	newCursor := cursor
	// Segment rune lengths; positions map back into the pre-cursor text.
	segmentRunes := make([][]rune, len(segments))
	for i, seg := range segments {
		segmentRunes[i] = []rune(seg.Segment)
	}

	// Skip trailing whitespace
	for len(segments) > 0 {
		last := segments[len(segments)-1]
		lastRunes := segmentRunes[len(segmentRunes)-1]
		if isAtomic != nil && isAtomic(last.Segment) {
			break
		}
		if !IsWhitespaceRune(lastRunes[0]) {
			break
		}
		newCursor -= len(lastRunes)
		segments = segments[:len(segments)-1]
		segmentRunes = segmentRunes[:len(segmentRunes)-1]
	}

	if len(segments) == 0 {
		return newCursor
	}

	last := segments[len(segments)-1]
	lastRunes := segmentRunes[len(segmentRunes)-1]

	if isAtomic != nil && isAtomic(last.Segment) {
		// Skip one atomic segment
		newCursor -= len(lastRunes)
	} else if last.IsWordLike {
		// Skip inside one word-like segment, preserving ASCII punctuation
		// boundaries
		segment := last.Segment
		locs := punctuationGlobal.FindAllStringIndex(segment, -1)
		if len(locs) <= 0 {
			newCursor -= len([]rune(segment))
		} else {
			lastMatch := locs[len(locs)-1]
			newCursor -= len([]rune(segment)) - runeIndexUpTo(segment, lastMatch[1])
		}
	} else {
		// Skip non-word non-whitespace run (punctuation)
		for len(segments) > 0 {
			last := segments[len(segments)-1]
			lastRunes := segmentRunes[len(segmentRunes)-1]
			if isAtomic != nil && isAtomic(last.Segment) {
				break
			}
			if last.IsWordLike || IsWhitespaceRune(lastRunes[0]) {
				break
			}
			newCursor -= len(lastRunes)
			segments = segments[:len(segments)-1]
			segmentRunes = segmentRunes[:len(segmentRunes)-1]
		}
	}

	return newCursor
}

// runeIndexUpTo returns the rune index corresponding to the byte offset in s.
func runeIndexUpTo(s string, byteOffset int) int {
	count := 0
	for i := range s {
		if i >= byteOffset {
			break
		}
		count++
	}
	return count
}

// FindWordForward finds the cursor position after moving one word forward
// from cursor. Skips leading whitespace, then stops at the next
// word/punctuation boundary. Pure function.
func FindWordForward(text string, cursor int, options *WordNavigationOptions) int {
	runes := []rune(text)
	if cursor >= len(runes) {
		return len(runes)
	}

	textAfterCursor := string(runes[cursor:])
	var segments []WordSegment
	if options != nil && options.Segment != nil {
		segments = options.Segment(textAfterCursor)
	} else {
		segments = segmentWords(textAfterCursor)
	}
	var isAtomic func(string) bool
	if options != nil {
		isAtomic = options.IsAtomicSegment
	}

	newCursor := cursor
	index := 0

	segmentRunes := make([][]rune, len(segments))
	for i, seg := range segments {
		segmentRunes[i] = []rune(seg.Segment)
	}

	// Skip leading whitespace
	for index < len(segments) {
		seg := segments[index]
		segRunes := segmentRunes[index]
		if isAtomic != nil && isAtomic(seg.Segment) {
			break
		}
		if !IsWhitespaceRune(segRunes[0]) {
			break
		}
		newCursor += len(segRunes)
		index++
	}

	if index >= len(segments) {
		return newCursor
	}

	seg := segments[index]
	segRunes := segmentRunes[index]

	if isAtomic != nil && isAtomic(seg.Segment) {
		// Skip one atomic segment
		newCursor += len(segRunes)
	} else if seg.IsWordLike {
		// Skip inside one word-like segment, preserving ASCII punctuation
		// boundaries
		loc := punctuationGlobal.FindStringIndex(seg.Segment)
		if loc != nil {
			newCursor += runeIndexUpTo(seg.Segment, loc[0])
		} else {
			newCursor += len(segRunes)
		}
	} else {
		// Skip non-word non-whitespace run (punctuation)
		for index < len(segments) {
			seg := segments[index]
			segRunes := segmentRunes[index]
			if isAtomic != nil && isAtomic(seg.Segment) {
				break
			}
			if seg.IsWordLike || IsWhitespaceRune(segRunes[0]) {
				break
			}
			newCursor += len(segRunes)
			index++
		}
	}

	return newCursor
}

// GetWordSegmenter exposes the default word segmentation for callers that
// pass a custom segment function elsewhere.
func GetWordSegmenter() func(string) []WordSegment {
	return segmentWords
}
