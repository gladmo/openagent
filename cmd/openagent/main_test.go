package main

// Tests for the CLI entry dispatch: app-mode resolution (the ported
// resolveAppMode), the print-message split, and the regressions where a
// bare headless invocation or --list-models panicked instead of exiting.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/codingagent"
)

func TestResolveAppMode(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		stdinTTY  bool
		stdoutTTY bool
		want      codingagent.Mode
	}{
		{"explicit rpc", []string{"--mode", "rpc"}, true, true, codingagent.ModeRPC},
		{"explicit json", []string{"--mode", "json"}, true, true, codingagent.ModeJSON},
		{"explicit json headless", []string{"--mode", "json"}, false, false, codingagent.ModeJSON},
		{"explicit interactive", []string{"--mode", "interactive"}, true, true, codingagent.ModeInteractive},
		{"explicit text on TTY stays interactive", []string{"--mode", "text"}, true, true, codingagent.ModeInteractive},
		{"print flag forces text", []string{"-p", "hi"}, true, true, codingagent.ModeText},
		{"piped stdin forces text", nil, false, true, codingagent.ModeText},
		{"piped stdout forces text", nil, true, false, codingagent.ModeText},
		{"default on TTY is interactive", nil, true, true, codingagent.ModeInteractive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			parsed := codingagent.ParseArgs(c.args)
			if got := resolveAppMode(parsed, c.stdinTTY, c.stdoutTTY); got != c.want {
				t.Errorf("resolveAppMode(%v) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}

func TestSplitInitialMessage(t *testing.T) {
	if initial, rest := splitInitialMessage(nil); initial != "" || len(rest) != 0 {
		t.Errorf("empty split = (%q, %v)", initial, rest)
	}
	if initial, rest := splitInitialMessage([]string{"only"}); initial != "only" || len(rest) != 0 {
		t.Errorf("single split = (%q, %v)", initial, rest)
	}
	if initial, rest := splitInitialMessage([]string{"a", "b", "c"}); initial != "a" || len(rest) != 2 || rest[0] != "b" || rest[1] != "c" {
		t.Errorf("multi split = (%q, %v)", initial, rest)
	}
}

func TestRunListModelsExitsZero(t *testing.T) {
	t.Setenv(codingagent.EnvAgentDirName(), t.TempDir())
	if code := run([]string{"--list-models"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
}

// TestRunHeadlessWithoutMessages pins the regression where a bare
// headless invocation panicked on Messages[1:].
func TestRunHeadlessWithoutMessagesExitsZero(t *testing.T) {
	t.Setenv(codingagent.EnvAgentDirName(), t.TempDir())
	if code := run(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if code := run([]string{"-p"}); code != 0 {
		t.Fatalf("-p exit = %d, want 0", code)
	}
}

func TestApplyCliModelSelection(t *testing.T) {
	t.Run("unauthenticated provider fails loud", func(t *testing.T) {
		t.Setenv(codingagent.EnvAgentDirName(), t.TempDir())
		host, err := codingagent.SetupPrintSession(codingagent.PrintSessionOptions{Cwd: t.TempDir(), Mode: "text"})
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		parsed := codingagent.ParseArgs([]string{"--provider", "zai", "--model", "glm-5.3"})
		message, fatal := applyCliModelSelection(host.Session(), parsed)
		if !fatal {
			t.Fatalf("expected fatal diagnostic, got (%q, %v)", message, fatal)
		}
		if !strings.Contains(message, "No API key for zai") {
			t.Fatalf("message = %q", message)
		}
	})

	t.Run("authenticated provider sets the session model", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv(codingagent.EnvAgentDirName(), dir)
		modelsJSON := `{
			"providers": {
				"my-gateway": {
					"name": "My Gateway",
					"baseUrl": "https://gw.example.com/v1",
					"apiKey": "static-key",
					"api": "openai-completions",
					"models": [{"id": "custom-1", "contextWindow": 64000}]
				}
			}
		}`
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsJSON), 0o644); err != nil {
			t.Fatalf("models.json: %v", err)
		}
		host, err := codingagent.SetupPrintSession(codingagent.PrintSessionOptions{Cwd: t.TempDir(), Mode: "text"})
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		parsed := codingagent.ParseArgs([]string{"--provider", "my-gateway", "--model", "custom-1", "--thinking", "high"})
		message, fatal := applyCliModelSelection(host.Session(), parsed)
		if fatal || message != "" {
			t.Fatalf("diagnostic = (%q, %v)", message, fatal)
		}
		model := host.Session().Model()
		if model == nil || model.Provider != "my-gateway" || model.ID != "custom-1" {
			t.Fatalf("session model = %+v", model)
		}
		if got := host.Session().ThinkingLevel(); got != "high" {
			t.Fatalf("thinking level = %q, want high", got)
		}
	})

	t.Run("empty model leaves the session untouched", func(t *testing.T) {
		t.Setenv(codingagent.EnvAgentDirName(), t.TempDir())
		host, err := codingagent.SetupPrintSession(codingagent.PrintSessionOptions{Cwd: t.TempDir(), Mode: "text"})
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		message, fatal := applyCliModelSelection(host.Session(), codingagent.ParseArgs(nil))
		if message != "" || fatal {
			t.Fatalf("diagnostic = (%q, %v)", message, fatal)
		}
	})
}

func TestApplyCliModelSelectionAPIKeyOverride(t *testing.T) {
	t.Run("api-key authenticates the resolved provider", func(t *testing.T) {
		t.Setenv(codingagent.EnvAgentDirName(), t.TempDir())
		host, err := codingagent.SetupPrintSession(codingagent.PrintSessionOptions{Cwd: t.TempDir(), Mode: "text"})
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		parsed := codingagent.ParseArgs([]string{
			"--provider", "zai", "--model", "glm-5.3", "--api-key", "test-key",
		})
		message, fatal := applyCliModelSelection(host.Session(), parsed)
		if fatal || message != "" {
			t.Fatalf("diagnostic = (%q, %v)", message, fatal)
		}
		model := host.Session().Model()
		if model == nil || model.Provider != "zai" || model.ID != "glm-5.3" {
			t.Fatalf("session model = %+v", model)
		}
		if !host.Session().ModelRuntime.HasConfiguredAuth("zai") {
			t.Fatal("runtime key did not mark zai configured")
		}
		auth, ok := host.Session().ModelRuntime.GetAuth("zai")
		if !ok || auth == nil || auth.Auth.APIKey == nil || *auth.Auth.APIKey != "test-key" {
			t.Fatalf("request-side auth = %+v ok=%v", auth, ok)
		}
	})

	t.Run("api-key without a model is rejected", func(t *testing.T) {
		t.Setenv(codingagent.EnvAgentDirName(), t.TempDir())
		host, err := codingagent.SetupPrintSession(codingagent.PrintSessionOptions{Cwd: t.TempDir(), Mode: "text"})
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		message, fatal := applyCliModelSelection(host.Session(), codingagent.ParseArgs([]string{"--api-key", "test-key"}))
		if !fatal || !strings.Contains(message, "--api-key requires a model") {
			t.Fatalf("diagnostic = (%q, %v)", message, fatal)
		}
	})
}
