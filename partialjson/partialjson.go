// Package partialjson is a 1:1 port of npm partial-json 0.1.7 (parseJSON),
// which parses incomplete JSON text by discarding unfinished trailing values.
//
// Results use the jsonx value model (*Obj preserves insertion order).
package partialjson

import (
	"fmt"
	"math"
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// Allow is a bitmask controlling which value kinds may be partially parsed.
type Allow int

const (
	AllowSTR       Allow = 0b000000001
	AllowNUM       Allow = 0b000000010
	AllowARR       Allow = 0b000000100
	AllowOBJ       Allow = 0b000001000
	AllowNULL      Allow = 0b000010000
	AllowBOOL      Allow = 0b000100000
	AllowNAN       Allow = 0b001000000
	AllowINFINITY  Allow = 0b010000000
	AllowNINFINITY Allow = 0b100000000

	AllowINF        = AllowINFINITY | AllowNINFINITY
	AllowSPECIAL    = AllowNULL | AllowBOOL | AllowINF | AllowNAN
	AllowATOM       = AllowSTR | AllowNUM | AllowSPECIAL
	AllowCOLLECTION = AllowARR | AllowOBJ
	AllowALL        = AllowATOM | AllowCOLLECTION
)

// PartialError mirrors partial-json's PartialJSON error: the input ended in
// the middle of a value whose kind is not allowed to be partial.
type PartialError struct{ Message string }

func (e *PartialError) Error() string { return e.Message }

// MalformedError mirrors partial-json's MalformedJSON error.
type MalformedError struct{ Message string }

func (e *MalformedError) Error() string { return e.Message }

type parser struct {
	s     string
	index int
	allow Allow
}

// Parse parses possibly-incomplete JSON, allowing every value kind to be
// partial (Allow.ALL), exactly like partial-json's parseJSON default.
func Parse(s string) (any, error) { return ParseAllow(s, AllowALL) }

// ParseAllow parses possibly-incomplete JSON with an Allow bitmask.
func ParseAllow(s string, allow Allow) (any, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("%s is empty", s)
	}
	p := &parser{s: strings.TrimSpace(s), allow: allow}
	return p.parseAny()
}

func (p *parser) markPartialJSON(msg string) error {
	return &PartialError{Message: fmt.Sprintf("%s at position %d", msg, p.index)}
}

func (p *parser) throwMalformedError(msg string) error {
	return &MalformedError{Message: fmt.Sprintf("%s at position %d", msg, p.index)}
}

func (p *parser) hasPrefixAt(prefix string) bool {
	return strings.HasPrefix(p.s[p.min(p.index, len(p.s)):], prefix)
}

func (p *parser) min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (p *parser) startsWithPartial(literal string, flag Allow) bool {
	rest := len(p.s) - p.index
	full := len(literal)
	return flag&p.allow != 0 && rest < full && strings.HasPrefix(literal, p.s[p.index:])
}

func (p *parser) parseAny() (any, error) {
	p.skipBlank()
	if p.index >= len(p.s) {
		return nil, p.markPartialJSON("Unexpected end of input")
	}
	switch {
	case p.s[p.index] == '"':
		return p.parseStr()
	case p.s[p.index] == '{':
		return p.parseObj()
	case p.s[p.index] == '[':
		return p.parseArr()
	case p.hasPrefixAt("null") || p.startsWithPartial("null", AllowNULL):
		p.index += 4
		return nil, nil
	case p.hasPrefixAt("true") || p.startsWithPartial("true", AllowBOOL):
		p.index += 4
		return true, nil
	case p.hasPrefixAt("false") || p.startsWithPartial("false", AllowBOOL):
		p.index += 5
		return false, nil
	case p.hasPrefixAt("Infinity") || p.startsWithPartial("Infinity", AllowINFINITY):
		p.index += 8
		return math.Inf(1), nil
	case p.hasPrefixAt("-Infinity") || (AllowNINFINITY&p.allow != 0 && 1 < len(p.s)-p.index && len(p.s)-p.index < 9 && strings.HasPrefix("-Infinity", p.s[p.index:])):
		p.index += 9
		return math.Inf(-1), nil
	case p.hasPrefixAt("NaN") || p.startsWithPartial("NaN", AllowNAN):
		p.index += 3
		return math.NaN(), nil
	}
	return p.parseNum()
}

func (p *parser) parseStr() (any, error) {
	start := p.index
	escape := false
	p.index++ // skip initial quote
	for p.index < len(p.s) && (p.s[p.index] != '"' || (escape && p.s[p.index-1] == '\\')) {
		if p.s[p.index] == '\\' {
			escape = !escape
		} else {
			escape = false
		}
		p.index++
	}
	boolNum := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	if p.index < len(p.s) && p.s[p.index] == '"' {
		p.index++
		quoted := p.s[start : p.index-boolNum(escape)]
		v, err := jsonx.Parse(quoted)
		if err != nil {
			return nil, p.throwMalformedError(err.Error())
		}
		return v, nil
	} else if AllowSTR&p.allow != 0 {
		cut := p.index - boolNum(escape)
		if cut < start {
			cut = start
		}
		v, err := jsonx.Parse(p.s[start:cut] + `"`)
		if err != nil {
			// SyntaxError: Invalid escape sequence -> truncate to last backslash.
			last := strings.LastIndex(p.s, "\\")
			if last < start {
				last = start
			}
			v, err = jsonx.Parse(p.s[start:last] + `"`)
			if err != nil {
				return nil, p.throwMalformedError(err.Error())
			}
		}
		return v, nil
	}
	return nil, p.markPartialJSON("Unterminated string literal")
}

func (p *parser) parseObj() (any, error) {
	p.index++ // skip initial brace
	p.skipBlank()
	obj := jsonx.NewObj()
	for {
		if !p.at('}') {
			p.skipBlank()
			if p.index >= len(p.s) && AllowOBJ&p.allow != 0 {
				return obj, nil
			}
			key, err := p.parseStr()
			if err != nil {
				if AllowOBJ&p.allow != 0 {
					return obj, nil
				}
				return nil, p.markPartialJSON("Expected '}' at end of object")
			}
			p.skipBlank()
			p.index++ // skip colon
			value, err := p.parseAny()
			if err != nil {
				if AllowOBJ&p.allow != 0 {
					return obj, nil
				}
				return nil, err
			}
			obj.Set(key.(string), value)
			p.skipBlank()
			if p.at(',') {
				p.index++ // skip comma
			}
			continue
		}
		break
	}
	p.index++ // skip final brace
	return obj, nil
}

func (p *parser) at(ch byte) bool {
	return p.index < len(p.s) && p.s[p.index] == ch
}

func (p *parser) parseArr() (any, error) {
	p.index++ // skip initial bracket
	arr := []any{}
	for {
		if !p.at(']') {
			v, err := p.parseAny()
			if err != nil {
				if AllowARR&p.allow != 0 {
					return arr, nil
				}
				return nil, p.markPartialJSON("Expected ']' at end of array")
			}
			arr = append(arr, v)
			p.skipBlank()
			if p.at(',') {
				p.index++ // skip comma
			}
			continue
		}
		break
	}
	p.index++ // skip final bracket
	return arr, nil
}

func (p *parser) parseNum() (any, error) {
	if p.index == 0 {
		if p.s == "-" {
			return nil, p.throwMalformedError("Not sure what '-' is")
		}
		v, err := jsonx.Parse(p.s)
		if err == nil {
			return v, nil
		}
		if AllowNUM&p.allow != 0 {
			if cut := strings.LastIndex(p.s, "e"); cut > 0 {
				if v2, err2 := jsonx.Parse(p.s[:cut]); err2 == nil {
					return v2, nil
				}
			}
		}
		return nil, p.throwMalformedError(err.Error())
	}
	start := p.index
	if p.at('-') {
		p.index++
	}
	for p.index < len(p.s) && !strings.ContainsRune(",]}", rune(p.s[p.index])) {
		p.index++
	}
	if p.index == len(p.s) && AllowNUM&p.allow == 0 {
		return nil, p.markPartialJSON("Unterminated number literal")
	}
	v, err := jsonx.Parse(p.s[start:p.index])
	if err != nil {
		if p.s[start:p.index] == "-" {
			return nil, p.markPartialJSON("Not sure what '-' is")
		}
		if cut := strings.LastIndex(p.s, "e"); cut >= start {
			if v2, err2 := jsonx.Parse(p.s[start:cut]); err2 == nil {
				return v2, nil
			}
		}
		return nil, p.throwMalformedError(err.Error())
	}
	return v, nil
}

func (p *parser) skipBlank() {
	for p.index < len(p.s) && strings.ContainsRune(" \n\r\t", rune(p.s[p.index])) {
		p.index++
	}
}
