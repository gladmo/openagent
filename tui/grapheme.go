package tui

import "sort"

// This file ports the Intl.Segmenter(granularity: "grapheme") behavior the
// TypeScript reference relies on: extended grapheme clusters per UAX #29
// (Unicode 16), including GB9c Indic conjuncts and GB11 emoji ZWJ joins.

type graphemeBreakClass int

const (
	classAny graphemeBreakClass = iota
	classCR
	classLF
	classControl
	classExtend
	classZWJ
	classRegionalIndicator
	classPrepend
	classSpacingMark
	classL
	classV
	classT
	classLV
	classLVT
)

func inRuneRanges(r rune, ranges [][2]rune) bool {
	index := sort.Search(len(ranges), func(i int) bool { return ranges[i][1] >= r })
	return index < len(ranges) && ranges[index][0] <= r
}

func graphemeBreakClassOf(r rune) graphemeBreakClass {
	// The GCB tables are disjoint; ordered by expected frequency.
	if inRuneRanges(r, gcbExtend) {
		return classExtend
	}
	if inRuneRanges(r, gcbControl) {
		return classControl
	}
	if inRuneRanges(r, gcbSpacingMark) {
		return classSpacingMark
	}
	if inRuneRanges(r, gcbPrepend) {
		return classPrepend
	}
	if inRuneRanges(r, gcbL) {
		return classL
	}
	if inRuneRanges(r, gcbV) {
		return classV
	}
	if inRuneRanges(r, gcbT) {
		return classT
	}
	if inRuneRanges(r, gcbLV) {
		return classLV
	}
	if inRuneRanges(r, gcbLVT) {
		return classLVT
	}
	if inRuneRanges(r, gcbRegionalIndicator) {
		return classRegionalIndicator
	}
	if inRuneRanges(r, gcbZWJ) {
		return classZWJ
	}
	if r == '\r' {
		return classCR
	}
	if r == '\n' {
		return classLF
	}
	return classAny
}

func isDefaultIgnorable(r rune) bool {
	return inRuneRanges(r, propDefaultIgnorable)
}

func isExtendedPictographic(r rune) bool {
	return inRuneRanges(r, propExtendedPictographic)
}

func isIncbLinker(r rune) bool {
	return inRuneRanges(r, incbLinker)
}

func isIncbConsonant(r rune) bool {
	return inRuneRanges(r, incbConsonant)
}

func isIncbExtend(r rune) bool {
	return inRuneRanges(r, incbExtend)
}

// incbState tracks GB9c's left context "Consonant [Extend|Linker]* Linker
// [Extend|Linker]*" within the current cluster.
type incbState int

const (
	incbNone incbState = iota
	incbConsonantSeen
	incbLinkerSeen
)

func incbAfter(state incbState, r rune) incbState {
	switch {
	case isIncbConsonant(r):
		return incbConsonantSeen
	case isIncbLinker(r):
		if state >= incbConsonantSeen {
			return incbLinkerSeen
		}
		return incbNone
	case isIncbExtend(r):
		return state
	default:
		return incbNone
	}
}

// pictState tracks GB11's "\p{Extended_Pictographic} Extend* ZWJ" prefix
// within the current cluster.
type pictState int

const (
	pictNone pictState = iota
	pictSeen
	pictSeenExtends
	pictZWJ
)

// segmentGraphemes splits s into extended grapheme clusters.
func segmentGraphemes(s string) []string {
	var segments []string
	runes := []rune(s)
	if len(runes) == 0 {
		return segments
	}

	first := runes[0]
	prev := graphemeBreakClassOf(first)
	pict := pictNone
	if isExtendedPictographic(first) {
		pict = pictSeen
	}
	riCount := 0
	if prev == classRegionalIndicator {
		riCount = 1
	}
	incb := incbAfter(incbNone, first)
	clusterStart := 0

	join := func(r rune, class graphemeBreakClass) {
		switch pict {
		case pictSeen, pictSeenExtends:
			switch {
			case isExtendedPictographic(r):
				pict = pictSeen
			case class == classExtend:
				pict = pictSeenExtends
			case class == classZWJ:
				pict = pictZWJ
			default:
				pict = pictNone
			}
		case pictZWJ:
			if isExtendedPictographic(r) {
				pict = pictSeen
			} else {
				pict = pictNone
			}
		default:
			if isExtendedPictographic(r) {
				pict = pictSeen
			}
		}

		if class == classRegionalIndicator {
			riCount++
		} else {
			riCount = 0
		}

		incb = incbAfter(incb, r)
	}

	for i := 1; i < len(runes); i++ {
		r := runes[i]
		cur := graphemeBreakClassOf(r)

		noBreak := false
		if prev == classCR && cur == classLF { // GB3
			noBreak = true
		} else if prev == classControl || prev == classCR || prev == classLF { // GB4
			noBreak = false
		} else if cur == classControl || cur == classCR || cur == classLF { // GB5
			noBreak = false
		} else if prev == classL && (cur == classL || cur == classV || cur == classLV || cur == classLVT) { // GB6
			noBreak = true
		} else if (prev == classLV || prev == classV) && (cur == classV || cur == classT) { // GB7
			noBreak = true
		} else if (prev == classLVT || prev == classT) && cur == classT { // GB8
			noBreak = true
		} else if cur == classExtend || cur == classZWJ { // GB9
			noBreak = true
		} else if cur == classSpacingMark { // GB9a
			noBreak = true
		} else if prev == classPrepend { // GB9b
			noBreak = true
		} else if incb == incbLinkerSeen && isIncbConsonant(r) { // GB9c
			noBreak = true
		} else if pict == pictZWJ && isExtendedPictographic(r) { // GB11
			noBreak = true
		} else if prev == classRegionalIndicator && cur == classRegionalIndicator && riCount%2 == 1 { // GB12/13
			noBreak = true
		}

		if noBreak {
			join(r, cur)
		} else {
			segments = append(segments, string(runes[clusterStart:i]))
			clusterStart = i
			if isExtendedPictographic(r) {
				pict = pictSeen
			} else {
				pict = pictNone
			}
			if cur == classRegionalIndicator {
				riCount = 1
			} else {
				riCount = 0
			}
			incb = incbAfter(incbNone, r)
		}
		prev = cur
	}
	segments = append(segments, string(runes[clusterStart:]))
	return segments
}

// isRGIEmoji mirrors the v-flag regex /^\p{RGI_Emoji}$/ over a grapheme
// cluster: the exact RGI sequence sets from UCD, not a heuristic.
func isRGIEmoji(segment string) bool {
	if _, ok := rgiBasicEmoji[segment]; ok {
		return true
	}
	if _, ok := rgiFlagSequences[segment]; ok {
		return true
	}
	if _, ok := rgiTagSequences[segment]; ok {
		return true
	}
	if _, ok := rgiModifierSequences[segment]; ok {
		return true
	}
	_, ok := rgiZwjSequences[segment]
	return ok
}
