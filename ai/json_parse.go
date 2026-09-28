package ai

import (
	"regexp"
	"strings"

	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/partialjson"
)

// ---------------------------------------------------------------------------
// json-parse.ts
// ---------------------------------------------------------------------------

var validJSONEscapes = map[byte]bool{
	'"': true, '\\': true, '/': true, 'b': true, 'f': true, 'n': true, 'r': true, 't': true, 'u': true,
}

var unicodeEscapeRe = regexp.MustCompile(`^[0-9a-fA-F]{4}$`)

func isControlChar(ch byte) bool { return ch <= 0x1f }

func escapeControlChar(ch byte) string {
	switch ch {
	case '\b':
		return "\\b"
	case '\f':
		return "\\f"
	case '\n':
		return "\\n"
	case '\r':
		return "\\r"
	case '\t':
		return "\\t"
	default:
		const hexDigits = "0123456789abcdef"
		v := uint32(ch)
		return "\\u" + string([]byte{
			hexDigits[(v>>12)&0xf], hexDigits[(v>>8)&0xf], hexDigits[(v>>4)&0xf], hexDigits[v&0xf],
		})
	}
}

// RepairJSON repairs malformed JSON string literals by escaping raw control
// characters inside strings and doubling backslashes before invalid escape
// characters.
func RepairJSON(jsonText string) string {
	var repaired strings.Builder
	inString := false
	for index := 0; index < len(jsonText); index++ {
		ch := jsonText[index]
		if !inString {
			repaired.WriteByte(ch)
			if ch == '"' {
				inString = true
			}
			continue
		}
		if ch == '"' {
			repaired.WriteByte(ch)
			inString = false
			continue
		}
		if ch == '\\' {
			if index+1 >= len(jsonText) {
				repaired.WriteString("\\\\")
				continue
			}
			next := jsonText[index+1]
			if next == 'u' {
				if index+6 <= len(jsonText) && unicodeEscapeRe.MatchString(jsonText[index+2:index+6]) {
					repaired.WriteString("\\u" + jsonText[index+2:index+6])
					index += 5
					continue
				}
			}
			if validJSONEscapes[next] {
				repaired.WriteByte('\\')
				repaired.WriteByte(next)
				index++
				continue
			}
			repaired.WriteString("\\\\")
			continue
		}
		if isControlChar(ch) {
			repaired.WriteString(escapeControlChar(ch))
		} else {
			repaired.WriteByte(ch)
		}
	}
	return repaired.String()
}

// ParseJSONWithRepair parses JSON, repairing malformed string literals on
// failure.
func ParseJSONWithRepair(jsonText string) (any, error) {
	v, err := jsonx.Parse(jsonText)
	if err == nil {
		return v, nil
	}
	repaired := RepairJSON(jsonText)
	if repaired != jsonText {
		return jsonx.Parse(repaired)
	}
	return nil, err
}

// ParseStreamingJSON attempts to parse potentially incomplete JSON during
// streaming. Always returns a valid value, defaulting to an empty object.
func ParseStreamingJSON(partial string) any {
	if strings.TrimSpace(partial) == "" {
		return jsonx.NewObj()
	}
	if v, err := ParseJSONWithRepair(partial); err == nil {
		return v
	}
	if v, err := partialjson.Parse(partial); err == nil {
		if v != nil {
			return v
		}
		return jsonx.NewObj()
	}
	if v, err := partialjson.Parse(RepairJSON(partial)); err == nil {
		if v != nil {
			return v
		}
		return jsonx.NewObj()
	}
	return jsonx.NewObj()
}

// ParseStreamingJSONObject is ParseStreamingJSON specialized to objects (the
// TS default type parameter): non-object successful parses return the empty
// object, mirroring callers that index into the result.
func ParseStreamingJSONObject(partial string) *jsonx.Obj {
	v := ParseStreamingJSON(partial)
	if obj, ok := v.(*jsonx.Obj); ok {
		return obj
	}
	return jsonx.NewObj()
}

// ---------------------------------------------------------------------------
// overflow.ts
// ---------------------------------------------------------------------------

// overflowPatterns are compiled from the TS regex list; JS `.?` loose
// matching is preserved (Go regexp supports `.?`).
var overflowPatterns = compileAll(
	`prompt (?:is )?too long`,
	`request_too_large`,
	`input is too long for requested model`,
	`exceeds the context window`,
	`exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))`,
	`input token count.*exceeds the maximum`,
	`maximum prompt length is \d+`,
	`reduce the length of the messages`,
	`maximum context length is \d+ tokens`,
	`exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`,
	`input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`,
	`exceeds the limit of \d+`,
	`exceeds the available context size`,
	`greater than the context length`,
	`context window exceeds limit`,
	`exceeded model token limit`,
	`too large for model with \d+ maximum context length`,
	`prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?`,
	`model_context_window_exceeded`,
	`prompt too long; exceeded (?:max )?context length`,
	`range of input length should be`,
	`context[_ ]length[_ ]exceeded`,
	`too many tokens`,
	`token limit exceeded`,
)

var nonOverflowPatterns = compileAll(
	`^(Throttling error|Service unavailable):`,
	`rate limit`,
	`too many requests`,
)

var cerebrasBodylessOverflow = regexp.MustCompile(`^4(?:00|13)\s*(?:status code)?\s*\(no body\)`)

func compileAll(patterns ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, regexp.MustCompile(`(?i)`+p))
	}
	return out
}

// IsContextOverflow reports whether an assistant message represents a context
// overflow error (error patterns, silent overflow, or length-stop overflow).
func IsContextOverflow(message *AssistantMessage, contextWindow ...float64) bool {
	window := 0.0
	if len(contextWindow) > 0 {
		window = contextWindow[0]
	}
	// Case 1: error message patterns.
	if message.StopReason == StopError && message.ErrorMessage != nil {
		msg := *message.ErrorMessage
		for _, p := range nonOverflowPatterns {
			if p.MatchString(msg) {
				goto silent
			}
		}
		for _, p := range overflowPatterns {
			if p.MatchString(msg) {
				return true
			}
		}
		if message.Provider == "cerebras" && cerebrasBodylessOverflow.MatchString(msg) {
			return true
		}
	}
silent:
	// Case 2: silent overflow (z.ai style).
	if window != 0 && message.StopReason == StopStop {
		inputTokens := message.Usage.Input + message.Usage.CacheRead
		if inputTokens > window {
			return true
		}
	}
	// Case 3: length-stop overflow (Xiaomi MiMo style).
	if window != 0 && message.StopReason == StopLength && message.Usage.Output == 0 {
		inputTokens := message.Usage.Input + message.Usage.CacheRead
		if inputTokens >= window*0.99 {
			return true
		}
	}
	return false
}

// IsRecoverableLength reports whether a length stop ended below the intended
// output limit.
func IsRecoverableLength(message *AssistantMessage, desiredMaxOutput float64) bool {
	return message.StopReason == StopLength && desiredMaxOutput > 0 && message.Usage.Output < desiredMaxOutput
}

// OverflowPatternSources returns the pattern sources (for test parity with
// getOverflowPatterns()).
func OverflowPatternSources() []string {
	return []string{
		`prompt (?:is )?too long`,
		`request_too_large`,
		`input is too long for requested model`,
		`exceeds the context window`,
		`exceeds (?:the )?(?:model'?s )?maximum context length(?: of [\d,]+ tokens?|\s*\([\d,]+\))`,
		`input token count.*exceeds the maximum`,
		`maximum prompt length is \d+`,
		`reduce the length of the messages`,
		`maximum context length is \d+ tokens`,
		`exceeds (?:the )?maximum allowed input length of [\d,]+ tokens?`,
		`input \(\d+ tokens\) is longer than the model'?s context length \(\d+ tokens\)`,
		`exceeds the limit of \d+`,
		`exceeds the available context size`,
		`greater than the context length`,
		`context window exceeds limit`,
		`exceeded model token limit`,
		`too large for model with \d+ maximum context length`,
		`prompt has [\d,]+ tokens?, but the configured context size is [\d,]+ tokens?`,
		`model_context_window_exceeded`,
		`prompt too long; exceeded (?:max )?context length`,
		`range of input length should be`,
		`context[_ ]length[_ ]exceeded`,
		`too many tokens`,
		`token limit exceeded`,
	}
}
