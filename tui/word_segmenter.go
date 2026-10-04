package tui

import "unicode"

// UAX #29 default word boundaries (rev 45), porting the Intl.Segmenter
// granularity "word" behavior the TypeScript reference relies on. DEVIATION
// (documented in PARITY.md): ICU applies CLDR dictionary tailoring for
// CJK/Thai/Khmer under locale-dependent conditions; those merges are not
// ported — CJK ideographs and Hiragana segment individually (identical to a
// small-icu Node build). Katakana runs join via WB13.

type wordBreakClass int

const (
	wbAny wordBreakClass = iota
	wbCR
	wbLF
	wbNewline
	wbExtend
	wbZWJ
	wbRegionalIndicator
	wbFormat
	wbKatakana
	wbHebrewLetter
	wbALetter
	wbSingleQuote
	wbDoubleQuote
	wbMidNumLet
	wbMidLetter
	wbMidNum
	wbNumeric
	wbExtendNumLet
	wbWSegSpace
)

func wordBreakClassOf(r rune) wordBreakClass {
	switch {
	case inRuneRanges(r, wbtExtend):
		return wbExtend
	case inRuneRanges(r, wbtALetter):
		return wbALetter
	case inRuneRanges(r, wbtNumeric):
		return wbNumeric
	case inRuneRanges(r, wbtKatakana):
		return wbKatakana
	case inRuneRanges(r, wbtMidNumLet):
		return wbMidNumLet
	case inRuneRanges(r, wbtMidLetter):
		return wbMidLetter
	case inRuneRanges(r, wbtMidNum):
		return wbMidNum
	case inRuneRanges(r, wbtExtendNumLet):
		return wbExtendNumLet
	case inRuneRanges(r, wbtHebrewLetter):
		return wbHebrewLetter
	case inRuneRanges(r, wbtFormat):
		return wbFormat
	case inRuneRanges(r, wbtWSegSpace):
		return wbWSegSpace
	case inRuneRanges(r, wbtRegionalIndicator):
		return wbRegionalIndicator
	case inRuneRanges(r, wbtNewline):
		return wbNewline
	case inRuneRanges(r, wbtZWJ):
		return wbZWJ
	case inRuneRanges(r, wbtDoubleQuote):
		return wbDoubleQuote
	case inRuneRanges(r, wbtSingleQuote):
		return wbSingleQuote
	case r == '\r':
		return wbCR
	case r == '\n':
		return wbLF
	}
	return wbAny
}

// wordUnit is one significant rune plus its trailing ignored runes
// (Extend|Format|ZWJ, per WB4).
type wordUnit struct {
	runes     []rune
	class     wordBreakClass
	lastIsZWJ bool // the trailing ignored run ends with ZWJ (WB3c)
}

// WordSegment is one word-granularity segment.
type WordSegment struct {
	Segment    string
	IsWordLike bool
}

func isAHLetter(c wordBreakClass) bool {
	return c == wbALetter || c == wbHebrewLetter
}

func isMidNumLetQ(c wordBreakClass) bool {
	return c == wbMidNumLet || c == wbSingleQuote
}

// segmentWords splits s per UAX #29 default word boundaries, tagging each
// segment word-like when it contains an ALetter, Hebrew_Letter, Numeric, or
// Katakana rune.
func segmentWords(s string) []WordSegment {
	runes := []rune(s)
	var segments []WordSegment

	var units []wordUnit
	flushSegment := func(units []wordUnit) {
		var out []rune
		wordLike := false
		for _, u := range units {
			out = append(out, u.runes...)
			if u.class == wbALetter || u.class == wbHebrewLetter || u.class == wbNumeric || u.class == wbKatakana {
				wordLike = true
			}
			// ICU's dictionary tailoring marks CJK segments word-like; the
			// default rules do not. Treating letter-category segments as
			// word-like keeps word navigation stepping through CJK text
			// (per-char) instead of skipping whole runs as punctuation.
			if unicode.IsLetter(u.runes[0]) {
				wordLike = true
			}
		}
		if len(out) > 0 {
			segments = append(segments, WordSegment{Segment: string(out), IsWordLike: wordLike})
		}
	}

	appendUnit := func(r rune) {
		units = append(units, wordUnit{runes: []rune{r}, class: wordBreakClassOf(r), lastIsZWJ: r == 0x200d})
	}

	if len(runes) == 0 {
		return segments
	}

	// A first rune that is itself ignorable keeps its own unit: matches
	// Intl.Segmenter's separate leading segment for a lone combining mark.
	appendUnit(runes[0])
	for i := 1; i < len(runes); i++ {
		class := wordBreakClassOf(runes[i])
		if class == wbExtend || class == wbFormat || class == wbZWJ {
			u := &units[len(units)-1]
			u.runes = append(u.runes, runes[i])
			u.lastIsZWJ = class == wbZWJ
			continue
		}
		appendUnit(runes[i])
	}

	// Decide boundaries between consecutive units.
	var current []wordUnit
	riRun := 0
	if units[0].class == wbRegionalIndicator {
		riRun = 1
	}
	current = append(current, units[0])

	for i := 1; i < len(units); i++ {
		noBreak := wordNoBreakBetween(units, i, riRun)
		cur := units[i]

		if noBreak {
			current = append(current, cur)
		} else {
			flushSegment(current)
			current = current[:0]
			current = append(current, cur)
		}

		if cur.class == wbRegionalIndicator {
			riRun++
		} else {
			riRun = 0
		}
	}
	flushSegment(current)
	return segments
}

// wordNoBreakBetween evaluates WB5-WB16 between units[i-1] and units[i].
// riRun counts consecutive RI units ending at units[i-1].
func wordNoBreakBetween(units []wordUnit, i, riRun int) bool {
	prev := units[i-1]
	cur := units[i]
	var prevPrev *wordUnit
	if i >= 2 {
		prevPrev = &units[i-2]
	}

	switch {
	case prev.class == wbCR && cur.class == wbLF: // WB3
		return true
	case prev.class == wbNewline || prev.class == wbCR || prev.class == wbLF: // WB3a
		return false
	case cur.class == wbNewline || cur.class == wbCR || cur.class == wbLF: // WB3b
		return false
	case prev.lastIsZWJ && isExtendedPictographic(cur.runes[0]): // WB3c
		return true
	case prev.class == wbWSegSpace && cur.class == wbWSegSpace: // WB3d
		return true
	case isAHLetter(prev.class) && isAHLetter(cur.class): // WB5
		return true
	case isAHLetter(prev.class) && (cur.class == wbMidLetter || isMidNumLetQ(cur.class)) && // WB6
		i+1 < len(units) && isAHLetter(units[i+1].class):
		return true
	case prevPrev != nil && isAHLetter(prevPrev.class) && // WB7
		(prev.class == wbMidLetter || isMidNumLetQ(prev.class)) && isAHLetter(cur.class):
		return true
	case prev.class == wbHebrewLetter && cur.class == wbSingleQuote: // WB7a
		return true
	case prev.class == wbHebrewLetter && cur.class == wbDoubleQuote && // WB7b
		i+1 < len(units) && units[i+1].class == wbHebrewLetter:
		return true
	case prevPrev != nil && prevPrev.class == wbHebrewLetter && // WB7c
		prev.class == wbDoubleQuote && cur.class == wbHebrewLetter:
		return true
	case prev.class == wbNumeric && cur.class == wbNumeric: // WB8
		return true
	case isAHLetter(prev.class) && cur.class == wbNumeric: // WB9
		return true
	case prev.class == wbNumeric && isAHLetter(cur.class): // WB10
		return true
	case prevPrev != nil && prevPrev.class == wbNumeric && // WB11
		(prev.class == wbMidNum || isMidNumLetQ(prev.class)) && cur.class == wbNumeric:
		return true
	case prev.class == wbNumeric && (cur.class == wbMidNum || isMidNumLetQ(cur.class)) && // WB12
		i+1 < len(units) && units[i+1].class == wbNumeric:
		return true
	case prev.class == wbKatakana && cur.class == wbKatakana: // WB13
		return true
	case (isAHLetter(prev.class) || prev.class == wbNumeric || prev.class == wbKatakana ||
		prev.class == wbExtendNumLet) && cur.class == wbExtendNumLet: // WB13a
		return true
	case prev.class == wbExtendNumLet && (isAHLetter(cur.class) || cur.class == wbNumeric ||
		cur.class == wbKatakana): // WB13b
		return true
	case prev.class == wbRegionalIndicator && cur.class == wbRegionalIndicator && riRun%2 == 1: // WB15/16
		return true
	}
	return false // WB999
}
