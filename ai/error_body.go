package ai

// error_body.go ports utils/error-body.ts. The TS version probes SDK error
// field shapes (Mistral/openai/@google/genai/Bedrock) to recover the HTTP
// body; the Go port owns its error type (ProviderHTTPError), so
// normalization reads the status/body/message directly and keeps the display
// composition rules identical.

import (
	"strconv"
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// MaxProviderErrorBodyChars caps raw error bodies surfaced to users.
const MaxProviderErrorBodyChars = 4000

// NormalizedProviderError mirrors the TS interface.
type NormalizedProviderError struct {
	Status             int
	Body               string
	Message            string
	MessageCarriesBody bool
}

// NormalizeProviderError normalizes any error into the display struct.
func NormalizeProviderError(err error) NormalizedProviderError {
	if providerErr, ok := err.(*ProviderHTTPError); ok {
		body := ""
		if trimmed := strings.TrimSpace(providerErr.Body); trimmed != "" {
			body = TruncateErrorText(trimmed, MaxProviderErrorBodyChars)
		}
		message := providerErr.Message
		if message == "" {
			message = providerErr.Error()
		}
		return NormalizedProviderError{
			Status:             providerErr.Status,
			Body:               body,
			Message:            message,
			MessageCarriesBody: body == "" || strings.Contains(message, body),
		}
	}
	if err != nil {
		return NormalizedProviderError{Message: err.Error()}
	}
	return NormalizedProviderError{Message: "unknown error"}
}

// FormatProviderError composes a display string from a normalized error.
// An empty prefix means no prefix.
func FormatProviderError(norm NormalizedProviderError, prefix string) string {
	hasPrefix := prefix != ""
	if norm.MessageCarriesBody || norm.Status == 0 || norm.Body == "" {
		if hasPrefix && norm.Status != 0 {
			return prefix + " (" + strconv.Itoa(norm.Status) + "): " + norm.Message
		}
		return norm.Message
	}
	if hasPrefix {
		return prefix + " (" + strconv.Itoa(norm.Status) + "): " + norm.Body
	}
	return strconv.Itoa(norm.Status) + ": " + norm.Body
}

// TruncateErrorText truncates text to maxChars with an explicit marker.
func TruncateErrorText(text string, maxChars int) string {
	if len(text) <= maxChars {
		return text
	}
	return text[:maxChars] + "... [truncated " + strconv.Itoa(len(text)-maxChars) + " chars]"
}

// SafeJSONStringify serializes any value, falling back to FormatThrownValue.
func SafeJSONStringify(value any) string {
	serialized := jsonx.Stringify(value)
	if serialized == "" && value != nil {
		return FormatThrownValue(value)
	}
	return serialized
}

// WrapStainlessHTTPError converts an HTTP failure into the message shape the
// pinned OpenAI/Anthropic SDKs produce. Both SDKs parse the error body with
// safeJSON and pass the WHOLE parsed body to APIError.makeMessage, whose
// message becomes "<status> <JSON.stringify(body)>" (a parsed body carries no
// top-level .message). The raw text body is used verbatim when it is not
// JSON; an empty body yields "<status> status code (no body)".
func WrapStainlessHTTPError(err error) error {
	httpErr, ok := err.(*ProviderHTTPError)
	if !ok {
		return err
	}
	if httpErr.Message != "" {
		return httpErr
	}
	msg := ""
	parsed, parseErr := ParseJSONWithRepair(httpErr.Body)
	if parseErr == nil && parsed != nil {
		msg = jsonx.Stringify(parsed)
	} else if trimmed := strings.TrimSpace(httpErr.Body); trimmed != "" {
		msg = trimmed
	}
	status := httpErr.Status
	switch {
	case status != 0 && msg != "":
		httpErr.Message = strconv.Itoa(status) + " " + msg
	case status != 0:
		httpErr.Message = strconv.Itoa(status) + " status code (no body)"
	case msg != "":
		httpErr.Message = msg
	default:
		httpErr.Message = "(no status code or body)"
	}
	return httpErr
}
