package codingagent

// Tests for the login dialog component (adapted from login-dialog.test.ts).

import (
	"errors"
	"strings"
	"testing"
)

func TestLoginDialogTitle(t *testing.T) {
	component := NewLoginDialogComponent("anthropic", nil, "", "")
	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "Login to anthropic") {
		t.Fatalf("title = %v", lines)
	}

	named := NewLoginDialogComponent("zai", nil, "ZAI", "Sign in to ZAI")
	lines = named.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Sign in to ZAI") {
		t.Fatalf("override = %q", joined)
	}
	if strings.Contains(joined, "Login to") {
		t.Fatalf("default leaked = %q", joined)
	}
}

func TestLoginDialogShowAuth(t *testing.T) {
	component := NewLoginDialogComponent("p", nil, "", "")
	component.ShowAuth("https://auth.example.com/start", "Visit the page")

	lines := component.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "https://auth.example.com/start") {
		t.Fatalf("url = %q", joined)
	}
	if !strings.Contains(joined, "\x1b]8;;https://auth.example.com/start\x07") {
		t.Fatalf("hyperlink = %q", joined)
	}
	if !strings.Contains(joined, "click to open") {
		t.Fatalf("hint = %q", joined)
	}
	if !strings.Contains(joined, "Visit the page") {
		t.Fatalf("instructions = %q", joined)
	}
	// The URL dispatched to the (recording) opener, never a real browser.
	resetOpenedBrowserTargets()
	component.ShowAuth("https://auth-flow.test/start", "")
	targets := openedBrowserTargets()
	if len(targets) != 1 || targets[0] != "https://auth-flow.test/start" {
		t.Fatalf("browser targets = %v", targets)
	}
}

func TestLoginDialogShowDeviceCode(t *testing.T) {
	component := NewLoginDialogComponent("github", nil, "", "")
	component.ShowDeviceCode("https://github.com/login/device", "ABCD-1234")

	lines := component.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "github.com/login/device") {
		t.Fatalf("url = %q", joined)
	}
	if !strings.Contains(joined, "Enter code: ABCD-1234") {
		t.Fatalf("code = %q", joined)
	}
}

func TestLoginDialogManualInputFlow(t *testing.T) {
	component := NewLoginDialogComponent("p", nil, "", "")
	var submitted string
	var submitErr error
	component.ShowManualInput("Paste the code:", func(value string, err error) {
		submitted = value
		submitErr = err
	})

	component.HandleInput("a")
	component.HandleInput("b")
	component.HandleInput("enter")
	if submitted != "ab" {
		t.Fatalf("submitted = %q", submitted)
	}
	if submitErr != nil {
		t.Fatalf("err = %v", submitErr)
	}
	// Submitted input renders as "> ab".
	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "> ab") {
		t.Fatalf("echo = %v", lines)
	}
}

func TestLoginDialogCancel(t *testing.T) {
	completed := false
	var message string
	component := NewLoginDialogComponent("p", func(success bool, msg string) {
		completed = true
		message = msg
	}, "", "")
	component.ShowManualInput("Paste:", func(value string, err error) {
		if err == nil {
			t.Fatal("cancel resolved without error")
		}
	})

	component.HandleInput("escape")
	if !component.Cancelled() {
		t.Fatal("not cancelled")
	}
	if !completed || message != "Login cancelled" {
		t.Fatalf("complete = %v %q", completed, message)
	}
}

func TestLoginDialogShowInfo(t *testing.T) {
	component := NewLoginDialogComponent("p", nil, "", "")
	component.ShowInfo("Configure at:", []AuthInfoLink{
		{Label: "Docs", URL: "https://docs.example.com"},
		{URL: "https://plain.example.com"},
	}, true)

	lines := component.Render(80)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Docs: https://docs.example.com") {
		t.Fatalf("labeled = %q", joined)
	}
	if !strings.Contains(joined, "https://plain.example.com") {
		t.Fatalf("plain = %q", joined)
	}
	if !strings.Contains(joined, "esc to close") {
		t.Fatalf("close hint = %q", joined)
	}
}

func TestLoginDialogWaitingAndProgress(t *testing.T) {
	component := NewLoginDialogComponent("p", nil, "", "")
	component.ShowWaiting("Waiting for authorization...")
	component.ShowProgress("Still polling...")

	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "Waiting for authorization...") {
		t.Fatalf("waiting = %q", joined)
	}
	if !strings.Contains(joined, "Still polling...") {
		t.Fatalf("progress = %q", joined)
	}
}

func TestLoginDialogComplete(t *testing.T) {
	success := false
	component := NewLoginDialogComponent("p", func(ok bool, msg string) { success = ok }, "", "")
	component.Complete(true, "")
	if !success {
		t.Fatal("completion lost")
	}
}

func TestLoginDialogBackspace(t *testing.T) {
	component := NewLoginDialogComponent("p", nil, "", "")
	var submitted string
	component.ShowManualInput("Paste:", func(value string, err error) { submitted = value })
	component.HandleInput("x")
	component.HandleInput("y")
	component.HandleInput("backspace")
	component.HandleInput("enter")
	if submitted != "x" {
		t.Fatalf("submitted = %q", submitted)
	}
	_ = errors.New
}
