package main

// End-to-end regression for the interactive TUI entry: the main screen
// must paint a first frame (the editor chrome) and the /quit command must
// end the run through the real stdin pipeline. Pins the defect where the
// layout root was never attached to the main screen, leaving a blank,
// unresponsive terminal.

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/codingagent"
	"github.com/gladmo/openagent/tui"
)

// stripANSI removes CSI/OSC escape sequences so painted chrome can be
// inspected as plain text.
func stripANSI(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == 0x1b {
			i++
			if i < len(s) && s[i] == '[' {
				for i < len(s) && !unicode.IsLetter(rune(s[i])) {
					i++
				}
				i++
				continue
			}
			if i < len(s) && s[i] == ']' {
				for i < len(s) && s[i] != 0x07 {
					i++
				}
				i++
				continue
			}
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func hasPaintedContent(output string) bool {
	for _, r := range stripANSI(output) {
		if unicode.IsPrint(r) && !unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func TestInteractiveModePaintsAndQuits(t *testing.T) {
	t.Setenv(codingagent.EnvAgentDirName(), t.TempDir())

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdinW.Close()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinR, stdoutW
	defer func() { os.Stdin, os.Stdout = origStdin, origStdout }()

	host, err := codingagent.SetupPrintSession(codingagent.PrintSessionOptions{
		Cwd:  t.TempDir(),
		Mode: "text",
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	mode := codingagent.NewInteractiveMode(codingagent.InteractiveModeOptions{
		Session:  host.Session(),
		Terminal: tui.NewProcessTerminal(),
		Version:  "test",
	})

	runDone := make(chan error, 1)
	go func() { runDone <- mode.Run() }()

	// The first frame must paint visible chrome.
	var painted strings.Builder
	deadline := time.Now().Add(3 * time.Second)
	for !hasPaintedContent(painted.String()) && time.Now().Before(deadline) {
		chunk := make([]byte, 4096)
		_ = stdoutR.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		if n, err := stdoutR.Read(chunk); err == nil && n > 0 {
			painted.Write(chunk[:n])
		}
	}
	if !hasPaintedContent(painted.String()) {
		t.Fatalf("no painted chrome within 3s; raw output so far:\n%q", painted.String())
	}

	// /quit must end the run through the real input pipeline.
	if _, err := stdinW.Write([]byte("/quit\r")); err != nil {
		t.Fatalf("write /quit: %v", err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		t.Fatalf("Run did not exit after /quit; goroutine dump:\n%s", buf[:n])
	}
}

// TestInteractiveModeChatsThroughProvider drives the TUI end to end: a
// typed message must reach the provider with the CLI API key and the reply
// must be painted, then /quit must end the run.
func TestInteractiveModeChatsThroughProvider(t *testing.T) {
	host, captured := newFakeProviderSession(t)

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdinW.Close()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinR, stdoutW
	defer func() { os.Stdin, os.Stdout = origStdin, origStdout }()

	mode := codingagent.NewInteractiveMode(codingagent.InteractiveModeOptions{
		Session:  host.Session(),
		Terminal: tui.NewProcessTerminal(),
		Version:  "test",
	})
	runDone := make(chan error, 1)
	go func() { runDone <- mode.Run() }()

	if _, err := stdinW.Write([]byte("hello\r")); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	// The reply must be painted.
	var painted strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(stripANSI(painted.String()), "alpha item") && time.Now().Before(deadline) {
		chunk := make([]byte, 4096)
		_ = stdoutR.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		if n, err := stdoutR.Read(chunk); err == nil && n > 0 {
			painted.Write(chunk[:n])
		}
	}
	// The reply contains a markdown list, the block form whose nil theme
	// function crashed the live TUI.
	if !strings.Contains(stripANSI(painted.String()), "alpha item") {
		requested, auth, path := captured.snapshot()
		var transcript strings.Builder
		for _, line := range mode.Transcript().Render(80) {
			transcript.WriteString(line + "\n")
		}
		var agentState strings.Builder
		fmt.Fprintf(&agentState, "agentMessages=%d blocks=%d\n", len(host.Session().Agent.Messages()), len(mode.Transcript().Blocks()))
		for _, message := range host.Session().Agent.Messages() {
			assistant, ok := message.(*ai.AssistantMessage)
			if !ok {
				fmt.Fprintf(&agentState, "  %T\n", message)
				continue
			}
			errText := ""
			if assistant.ErrorMessage != nil {
				errText = *assistant.ErrorMessage
			}
			fmt.Fprintf(&agentState, "  assistant stop=%q err=%q blocks=%d\n", assistant.StopReason, errText, len(assistant.Content))
		}
		if message, hasError := host.Session().Agent.ErrorMessage(); hasError {
			fmt.Fprintf(&agentState, "agent error: %s\n", message)
		}
		t.Fatalf("reply not painted within 5s; requested=%v auth=%q path=%q runActive=%v\n%s\ntranscript:\n%s\noutput:\n%q",
			requested, auth, path, mode.IsRunActive(), agentState.String(), transcript.String(), stripANSI(painted.String()))
	}

	requested, authHeader, _ := captured.snapshot()
	if !requested {
		t.Fatal("no request reached the fake provider")
	}
	if authHeader != "Bearer secret-key" {
		t.Fatalf("Authorization = %q, want Bearer secret-key", authHeader)
	}

	if _, err := stdinW.Write([]byte("/quit\r")); err != nil {
		t.Fatalf("write /quit: %v", err)
	}
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit after /quit")
	}
}

// TestInteractiveModeShowsTypedInput pins the regression where the editor
// was never mounted: typing must paint the input box and the typed text
// before any submit.
func TestInteractiveModeShowsTypedInput(t *testing.T) {
	t.Setenv(codingagent.EnvAgentDirName(), t.TempDir())
	host, err := codingagent.SetupPrintSession(codingagent.PrintSessionOptions{Cwd: t.TempDir(), Mode: "text"})
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdinW.Close()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinR, stdoutW
	defer func() { os.Stdin, os.Stdout = origStdin, origStdout }()

	mode := codingagent.NewInteractiveMode(codingagent.InteractiveModeOptions{
		Session:  host.Session(),
		Terminal: tui.NewProcessTerminal(),
		Version:  "test",
	})
	runDone := make(chan error, 1)
	go func() { runDone <- mode.Run() }()

	// Type without submitting, then the typed text must be painted along
	// with the editor's border.
	if _, err := stdinW.Write([]byte("hello")); err != nil {
		t.Fatalf("write input: %v", err)
	}
	var painted strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		chunk := make([]byte, 4096)
		_ = stdoutR.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		if n, err := stdoutR.Read(chunk); err == nil && n > 0 {
			painted.Write(chunk[:n])
		}
		stripped := stripANSI(painted.String())
		if strings.Contains(stripped, "hello") && strings.Contains(stripped, "─") {
			break
		}
	}
	stripped := stripANSI(painted.String())
	if !strings.Contains(stripped, "hello") {
		t.Fatalf("typed text not painted; output:\n%q", stripped)
	}
	if !strings.Contains(stripped, "─") {
		t.Fatalf("editor border not painted; output:\n%q", stripped)
	}

	// Clear the unsubmitted "hello" first: the editor would otherwise
	// submit "hello/quit" as a prompt.
	if _, err := stdinW.Write([]byte("\x7f\x7f\x7f\x7f\x7f/quit\r")); err != nil {
		t.Fatalf("write /quit: %v", err)
	}
	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit after /quit")
	}
}

// TestInteractiveCtrlCExitsThroughInputPipeline proves the app-level key
// listener is registered on the live TUI: typing paints, and a double
// ctrl+c (clear + exit) ends the run through the real stdin path.
func TestInteractiveCtrlCExitsThroughInputPipeline(t *testing.T) {
	t.Setenv(codingagent.EnvAgentDirName(), t.TempDir())
	host, err := codingagent.SetupPrintSession(codingagent.PrintSessionOptions{Cwd: t.TempDir(), Mode: "text"})
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdinW.Close()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	origStdin, origStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinR, stdoutW
	defer func() { os.Stdin, os.Stdout = origStdin, origStdout }()

	mode := codingagent.NewInteractiveMode(codingagent.InteractiveModeOptions{
		Session:  host.Session(),
		Terminal: tui.NewProcessTerminal(),
		Version:  "test",
	})
	runDone := make(chan error, 1)
	go func() { runDone <- mode.Run() }()

	// Wait for the startup frame, then type without submitting.
	var painted strings.Builder
	waitFor := func(want string) {
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(stripANSI(painted.String()), want) && time.Now().Before(deadline) {
			chunk := make([]byte, 4096)
			_ = stdoutR.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
			if n, err := stdoutR.Read(chunk); err == nil && n > 0 {
				painted.Write(chunk[:n])
			}
		}
		if !strings.Contains(stripANSI(painted.String()), want) {
			t.Fatalf("%q not painted; output:\n%q", want, stripANSI(painted.String()))
		}
	}
	waitFor("─")
	if _, err := stdinW.Write([]byte("abc")); err != nil {
		t.Fatalf("write input: %v", err)
	}
	waitFor("abc")

	// Double ctrl+c: the first clears the editor, the second exits.
	if _, err := stdinW.Write([]byte("\x03")); err != nil {
		t.Fatalf("ctrl+c: %v", err)
	}
	if _, err := stdinW.Write([]byte("\x03")); err != nil {
		t.Fatalf("ctrl+c: %v", err)
	}
	select {
	case <-runDone:
	case <-time.After(3 * time.Second):
		t.Fatal("double ctrl+c did not exit through the input pipeline")
	}
}
