package tui

// Port of program-status.ts (503c60552): the Program Status Protocol
// (OSC 7501). A program tells the terminal whether it is idle, working,
// blocked on the user, done, or failed. Only the root record is supported.
//
// Spec: https://www.superlogical.com/rex/docs/build/program-status

import (
	"encoding/base64"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ProgramStatus is one status report.
type ProgramStatus struct {
	// State is idle, working, blocked, done, error, or clear; clear removes
	// the status instead of reporting one.
	State string `json:"state"`
	// App is a stable program name, [A-Za-z0-9_.+-]{1,32}. Other values are
	// omitted.
	App string `json:"app,omitempty"`
	// Kind is what a blocked program waits for. Omitted for other states.
	Kind string `json:"kind,omitempty"` // permission | question | auth
	// Message is one human-readable line. Control characters become
	// spaces; longer text is cut to the spec limit.
	Message string `json:"message,omitempty"`
}

// programStatusQueryBody is the query body a supporting terminal echoes.
const programStatusQueryBody = "7501;?"

// ProgramStatusQuery is the feature detection query; a supporting terminal
// replies with the same body.
const ProgramStatusQuery = "\x1b]" + programStatusQueryBody + "\x1b\\"

// programStatusReplyPattern matches the reply to ProgramStatusQuery. Later
// spec revisions may add pairs after the "?".
var programStatusReplyPattern = regexp.MustCompile(`^\x1b\]7501;\?[^\x07\x1b]*(?:\x07|\x1b\\)$`)

// IsProgramStatusReply reports whether sequence is an OSC 7501 reply.
func IsProgramStatusReply(sequence string) bool {
	return programStatusReplyPattern.MatchString(sequence)
}

var programStatusAppPattern = regexp.MustCompile(`^[A-Za-z0-9_.+-]{1,32}$`)

var programStatusControlCharacters = regexp.MustCompile("[\\x00-\\x1f\\x7f-\\x9f]+")

// maxProgramStatusMessageBytes is the decoded msg limit; its base64
// encoding stays under the 2732-byte encoded limit.
const maxProgramStatusMessageBytes = 2048

func truncateProgramStatusUTF8(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	bytes := 0
	end := 0
	for index, char := range text {
		width := utf8.RuneLen(char)
		if char == utf8.RuneError {
			// Range reports invalid bytes as RuneError; only a literal
			// U+FFFD has width 3, others are single bytes.
			if !(index+3 <= len(text) && text[index:index+3] == string(utf8.RuneError)) {
				width = 1
			}
		}
		if bytes+width > maxBytes {
			break
		}
		bytes += width
		end = index + width
	}
	return text[:end]
}

// FormatProgramStatus encodes a status report. Terminals discard reports
// whose text contains control characters, so they are replaced.
func FormatProgramStatus(status ProgramStatus) string {
	pairs := []string{"state=" + status.State}
	if status.App != "" && programStatusAppPattern.MatchString(status.App) {
		pairs = append(pairs, "app="+status.App)
	}
	if status.State == "blocked" && status.Kind != "" {
		pairs = append(pairs, "kind="+status.Kind)
	}
	message := truncateProgramStatusUTF8(
		strings.TrimSpace(programStatusControlCharacters.ReplaceAllString(status.Message, " ")),
		maxProgramStatusMessageBytes)
	if message != "" {
		pairs = append(pairs, "msg="+base64.StdEncoding.EncodeToString([]byte(message)))
	}
	return "\x1b]7501;" + strings.Join(pairs, ":") + "\x1b\\"
}
