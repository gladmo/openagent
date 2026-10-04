package codingagent

// Tests for CLI argument parsing (ported from args.test.ts surfaces).

import (
	"strings"
	"testing"
)

func TestParseArgsBasics(t *testing.T) {
	args := ParseArgs([]string{"hello", "world"})
	if len(args.Messages) != 2 || args.Messages[0] != "hello" {
		t.Fatalf("messages = %v", args.Messages)
	}
	if len(args.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", args.Diagnostics)
	}
}

func TestParseArgsFlags(t *testing.T) {
	args := ParseArgs([]string{
		"--provider", "anthropic",
		"--model", "claude-opus-4-8",
		"--api-key", "sk-test",
		"--thinking", "high",
		"--continue",
	})
	if args.Provider != "anthropic" || args.Model != "claude-opus-4-8" || args.APIKey != "sk-test" {
		t.Fatalf("flags = %+v", args)
	}
	if args.Thinking != "high" {
		t.Fatalf("thinking = %q", args.Thinking)
	}
	if !args.Continue {
		t.Fatal("continue not set")
	}
}

func TestParseArgsMode(t *testing.T) {
	args := ParseArgs([]string{"--mode", "json"})
	if args.Mode != ModeJSON {
		t.Fatalf("mode = %q", args.Mode)
	}

	args = ParseArgs([]string{"--mode", "bogus"})
	if len(args.Diagnostics) != 1 || args.Diagnostics[0].Type != "error" {
		t.Fatalf("diagnostics = %v", args.Diagnostics)
	}

	args = ParseArgs([]string{"--mode"})
	if len(args.Diagnostics) != 1 || !strings.Contains(args.Diagnostics[0].Message, "requires") {
		t.Fatalf("diagnostics = %v", args.Diagnostics)
	}
}

func TestParseArgsPrint(t *testing.T) {
	args := ParseArgs([]string{"-p", "do the thing"})
	if !args.Print {
		t.Fatal("print not set")
	}
	if len(args.Messages) != 1 || args.Messages[0] != "do the thing" {
		t.Fatalf("messages = %v", args.Messages)
	}

	// -p without a following message.
	args = ParseArgs([]string{"-p", "--other"})
	if !args.Print || len(args.Messages) != 0 {
		t.Fatalf("print = %v messages = %v", args.Print, args.Messages)
	}
}

func TestParseArgsFileArgs(t *testing.T) {
	args := ParseArgs([]string{"@file1.txt", "message", "@file2.md"})
	if len(args.FileArgs) != 2 || args.FileArgs[0] != "file1.txt" {
		t.Fatalf("files = %v", args.FileArgs)
	}
	if len(args.Messages) != 1 {
		t.Fatalf("messages = %v", args.Messages)
	}

	// After --, @ still routes to files.
	args = ParseArgs([]string{"--", "@f", "plain"})
	if len(args.FileArgs) != 1 || len(args.Messages) != 1 {
		t.Fatalf("after -- = %v %v", args.FileArgs, args.Messages)
	}
}

func TestParseArgsCommaLists(t *testing.T) {
	args := ParseArgs([]string{"--models", "a, b ,c"})
	if len(args.Models) != 3 || args.Models[0] != "a" || args.Models[2] != "c" {
		t.Fatalf("models = %v", args.Models)
	}

	args = ParseArgs([]string{"--tools", "read,write"})
	if len(args.Tools) != 2 || args.Tools[0] != "read" {
		t.Fatalf("tools = %v", args.Tools)
	}
}

func TestParseArgsThinkingValidation(t *testing.T) {
	args := ParseArgs([]string{"--thinking", "ultra"})
	if args.Thinking != "" {
		t.Fatalf("thinking = %q", args.Thinking)
	}
	if len(args.Diagnostics) != 1 || args.Diagnostics[0].Type != "warning" {
		t.Fatalf("diagnostics = %v", args.Diagnostics)
	}
	if !strings.Contains(args.Diagnostics[0].Message, "Valid values") {
		t.Fatalf("message = %q", args.Diagnostics[0].Message)
	}
}

func TestParseArgsUnknownFlags(t *testing.T) {
	args := ParseArgs([]string{"--ext-flag", "value", "--bool-flag"})
	if args.UnknownFlags["ext-flag"] != "value" {
		t.Fatalf("ext-flag = %v", args.UnknownFlags["ext-flag"])
	}
	if args.UnknownFlags["bool-flag"] != true {
		t.Fatalf("bool-flag = %v", args.UnknownFlags["bool-flag"])
	}

	// = form.
	args = ParseArgs([]string{"--key=value"})
	if args.UnknownFlags["key"] != "value" {
		t.Fatalf("key = %v", args.UnknownFlags["key"])
	}

	// Single-dash unknown errors.
	args = ParseArgs([]string{"-x"})
	if len(args.Diagnostics) != 1 || !strings.Contains(args.Diagnostics[0].Message, "Unknown option") {
		t.Fatalf("diagnostics = %v", args.Diagnostics)
	}
}

func TestParseArgsListModels(t *testing.T) {
	args := ParseArgs([]string{"--list-models"})
	if args.ListModels != true {
		t.Fatalf("list = %v", args.ListModels)
	}

	args = ParseArgs([]string{"--list-models", "claude"})
	if args.ListModels != "claude" {
		t.Fatalf("list = %v", args.ListModels)
	}

	args = ParseArgs([]string{"--list-models", "@file"})
	if args.ListModels != true {
		t.Fatalf("list = %v", args.ListModels)
	}
}

func TestParseArgsTuiMode(t *testing.T) {
	args := ParseArgs([]string{"--tui-mode", "fullscreen"})
	if args.TuiMode != "fullscreen" {
		t.Fatalf("tui = %q", args.TuiMode)
	}

	args = ParseArgs([]string{"--tui-mode", "bogus"})
	if len(args.Diagnostics) != 1 || !strings.Contains(args.Diagnostics[0].Message, "Invalid TUI mode") {
		t.Fatalf("diagnostics = %v", args.Diagnostics)
	}
}

func TestParseArgsProjectTrust(t *testing.T) {
	args := ParseArgs([]string{"--approve"})
	if args.ProjectTrustOverride == nil || *args.ProjectTrustOverride != true {
		t.Fatalf("approve = %v", args.ProjectTrustOverride)
	}

	args = ParseArgs([]string{"--no-approve"})
	if args.ProjectTrustOverride == nil || *args.ProjectTrustOverride != false {
		t.Fatalf("no-approve = %v", args.ProjectTrustOverride)
	}

	args = ParseArgs([]string{})
	if args.ProjectTrustOverride != nil {
		t.Fatalf("default = %v", args.ProjectTrustOverride)
	}
}

func TestPrintHelpContainsCore(t *testing.T) {
	help := PrintHelp()
	for _, expected := range []string{
		"AI coding assistant",
		"--provider",
		"--mode",
		"--print, -p",
		"@file",
	} {
		if !strings.Contains(help, expected) {
			t.Errorf("help missing %q", expected)
		}
	}
}

func TestIsValidThinkingLevel(t *testing.T) {
	for _, valid := range ThinkingLevelOptions {
		if !IsValidThinkingLevel(valid) {
			t.Errorf("%q should be valid", valid)
		}
	}
	for _, invalid := range []string{"", "ultra", "HIGH"} {
		if IsValidThinkingLevel(invalid) {
			t.Errorf("%q should be invalid", invalid)
		}
	}
}
