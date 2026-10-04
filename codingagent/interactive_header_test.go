package codingagent

// Tests for the startup header (interactive-mode.ts builtInHeader) and the
// ExpandableText helper it uses.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/tui"
)

func TestStartupHeaderText(t *testing.T) {
	compact := KeyHint("app.interrupt", "interrupt") + " · " + RawKeyHint("/", "commands")
	expandedHints := strings.Join([]string{
		KeyHint("app.interrupt", "to interrupt"),
		KeyHint("app.clear", "to clear"),
		KeyHint("app.exit", "to exit (empty)"),
		RawKeyHint("/", "for commands"),
	}, "\n")
	collapsed, expanded := StartupHeaderText("1.2.3", compact, expandedHints)

	if !strings.Contains(collapsed, AppName()+" v1.2.3") {
		t.Fatalf("collapsed header missing logo: %q", collapsed)
	}
	if !strings.Contains(collapsed, "commands") {
		t.Fatalf("collapsed header missing compact hints: %q", collapsed)
	}
	if !strings.Contains(collapsed, "Ask it how to use or extend") {
		t.Fatalf("collapsed header missing onboarding: %q", collapsed)
	}
	if strings.Contains(collapsed, "to interrupt") {
		t.Fatalf("collapsed header must not carry the expanded hints: %q", collapsed)
	}

	if !strings.Contains(expanded, AppName()+" v1.2.3") {
		t.Fatalf("expanded header missing logo: %q", expanded)
	}
	if !strings.Contains(expanded, "to interrupt") || !strings.Contains(expanded, "for commands") {
		t.Fatalf("expanded header missing instructions: %q", expanded)
	}
	if strings.Count(expanded, "\n") <= strings.Count(collapsed, "\n") {
		t.Fatal("expanded header must be taller than the collapsed one")
	}
}

func TestExpandableTextToggles(t *testing.T) {
	component := NewExpandableText(
		func() string { return "collapsed" },
		func() string { return "expanded" },
		false, 0, 0,
	)
	if got := strings.Join(component.Render(40), "\n"); !strings.Contains(got, "collapsed") {
		t.Fatalf("initial render = %q", got)
	}
	component.SetExpanded(true)
	if got := strings.Join(component.Render(40), "\n"); !strings.Contains(got, "expanded") {
		t.Fatalf("expanded render = %q", got)
	}
	component.SetExpanded(false)
	if got := strings.Join(component.Render(40), "\n"); !strings.Contains(got, "collapsed") {
		t.Fatalf("collapsed render = %q", got)
	}
}

func TestInteractiveModeMountsStartupHeader(t *testing.T) {
	mode, _ := newInteractiveForTest(t)

	if mode.header == nil {
		t.Fatal("no startup header")
	}
	if len(mode.headerStack.Children) != 3 {
		t.Fatalf("header stack children = %d, want spacer/header/spacer", len(mode.headerStack.Children))
	}
	if mode.headerStack.Children[1] != tui.Component(mode.header) {
		t.Fatal("header not mounted between the spacers")
	}
	rendered := strings.Join(mode.header.Render(100), "\n")
	if !strings.Contains(rendered, AppName()+" vtest") {
		t.Fatalf("header render = %q", rendered)
	}
}

func TestInteractiveModeQuietStartupSilencesHeader(t *testing.T) {
	session := newTestSession(t)
	session.SettingsManager.SetQuietStartup(true)

	mode := NewInteractiveMode(InteractiveModeOptions{
		Session: session,
		TuiMode: "regular",
		Version: "test",
	})
	if len(mode.headerStack.Children) != 1 {
		t.Fatalf("quiet header stack children = %d, want 1", len(mode.headerStack.Children))
	}
	if rendered := mode.header.Render(100); len(rendered) != 0 {
		t.Fatalf("quiet header render = %q", rendered)
	}
}

func TestQuietStartupSettingRoundTrip(t *testing.T) {
	settings := SettingsManagerInMemory(Settings{}, nil)
	if settings.GetQuietStartup() {
		t.Fatal("quietStartup must default to false")
	}
	settings.SetQuietStartup(true)
	if !settings.GetQuietStartup() {
		t.Fatal("quietStartup not persisted in memory")
	}
}
