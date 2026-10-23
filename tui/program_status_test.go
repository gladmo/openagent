package tui

// Port of program-status.test.ts (#10607).

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"
)

func decodeProgramStatusMessage(t *testing.T, sequence string) string {
	t.Helper()
	match := regexp.MustCompile(`:msg=([A-Za-z0-9+/=]*)`).FindStringSubmatch(sequence)
	if match == nil {
		t.Fatalf("no msg in %q", sequence)
	}
	decoded, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatal(err)
	}
	return string(decoded)
}

func TestFormatProgramStatusEncodesFields(t *testing.T) {
	got := FormatProgramStatus(ProgramStatus{State: "blocked", App: "pi", Kind: "permission", Message: "Allow bash?"})
	want := "\x1b]7501;state=blocked:app=pi:kind=permission:msg=" + base64.StdEncoding.EncodeToString([]byte("Allow bash?")) + "\x1b\\"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if FormatProgramStatus(ProgramStatus{State: "clear"}) != "\x1b]7501;state=clear\x1b\\" {
		t.Fatal("clear form wrong")
	}
}

func TestFormatProgramStatusOmissions(t *testing.T) {
	got := FormatProgramStatus(ProgramStatus{State: "working", App: "my app", Kind: "auth", Message: " \n "})
	if got != "\x1b]7501;state=working\x1b\\" {
		t.Fatalf("omissions = %q", got)
	}
	if !strings.Contains(FormatProgramStatus(ProgramStatus{State: "idle", App: strings.Repeat("a", 32)}), ":app="+strings.Repeat("a", 32)) {
		t.Fatal("32-char app rejected")
	}
	if strings.Contains(FormatProgramStatus(ProgramStatus{State: "idle", App: strings.Repeat("a", 33)}), "app=") {
		t.Fatal("33-char app accepted")
	}
}

func TestFormatProgramStatusControlCharacters(t *testing.T) {
	sequence := FormatProgramStatus(ProgramStatus{State: "error", Message: "first\nsecond\x1b[31mthird\t"})
	if message := decodeProgramStatusMessage(t, sequence); message != "first second [31m third" {
		t.Fatalf("message = %q", message)
	}
}

func TestFormatProgramStatusTruncatesUTF8(t *testing.T) {
	sequence := FormatProgramStatus(ProgramStatus{State: "working", App: "pi", Message: strings.Repeat("é", 2000)})
	message := decodeProgramStatusMessage(t, sequence)
	if message != strings.Repeat("é", 1024) {
		t.Fatalf("message length = %d runes", len([]rune(message)))
	}
	if len(message) > 2048 {
		t.Fatalf("message bytes = %d", len(message))
	}
}

func TestIsProgramStatusReply(t *testing.T) {
	cases := []struct {
		sequence string
		want     bool
	}{
		{"\x1b]7501;?\x1b\\", true},
		{"\x1b]7501;?\x07", true},
		{"\x1b]7501;?version=2\x1b\\", true},
		{"\x1b]7501;state=idle\x1b\\", false},
		{"\x1b]11;rgb:0000/0000/0000\x07", false},
	}
	for _, tc := range cases {
		if IsProgramStatusReply(tc.sequence) != tc.want {
			t.Fatalf("IsProgramStatusReply(%q) != %v", tc.sequence, tc.want)
		}
	}
}
