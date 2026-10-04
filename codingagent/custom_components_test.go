package codingagent

// Tests for the custom components and markdown transform (adapted from
// their reference tests).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/tui"
)

func TestCreateMarkdownTransform(t *testing.T) {
	transformerA := func(markdown string, context MarkdownTransformContext) string {
		return strings.ToUpper(markdown)
	}
	transformerB := func(markdown string, context MarkdownTransformContext) string {
		return markdown + "!"
	}
	panicking := func(markdown string, context MarkdownTransformContext) string {
		panic("boom")
	}

	transform := CreateMarkdownTransform("assistant", true, []MarkdownTransformer{transformerA, transformerB})
	if got := transform("hello", 60); got != "HELLO!" {
		t.Fatalf("transformed = %q", got)
	}

	// A panicking transformer keeps the current markdown.
	safe := CreateMarkdownTransform("assistant", false, []MarkdownTransformer{panicking, transformerB})
	if got := safe("hello", 60); got != "hello!" {
		t.Fatalf("panic contained = %q", got)
	}

	// Empty return keeps current.
	empty := func(markdown string, context MarkdownTransformContext) string { return "" }
	chain := CreateMarkdownTransform("user", false, []MarkdownTransformer{transformerA, empty})
	if got := chain("hello", 60); got != "HELLO" {
		t.Fatalf("empty kept = %q", got)
	}

	// Context passes through.
	var seen MarkdownTransformContext
	capture := func(markdown string, context MarkdownTransformContext) string {
		seen = context
		return markdown
	}
	CreateMarkdownTransform("custom", true, []MarkdownTransformer{capture})("x", 80)
	if seen.MessageType != "custom" || !seen.IsStreaming || seen.AvailableWidth != 80 {
		t.Fatalf("context = %+v", seen)
	}
}

func TestCustomMessageComponentDefault(t *testing.T) {
	message := &CustomMessageView{CustomType: "my-ext", Content: "custom body"}
	component := NewCustomMessageComponent(message, nil, 1)

	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[my-ext]") {
		t.Fatalf("label = %q", joined)
	}
	if !strings.Contains(joined, "custom body") {
		t.Fatalf("content = %q", joined)
	}
}

func TestCustomMessageComponentCustomRenderer(t *testing.T) {
	message := &CustomMessageView{CustomType: "ext", Content: "x"}
	calls := 0
	lastExpanded := false
	renderer := func(message *CustomMessageView, options CustomMessageRenderOptions) tui.Component {
		calls++
		lastExpanded = options.Expanded
		return tui.NewText("renderer output", 0, 0, nil)
	}
	component := NewCustomMessageComponent(message, renderer, 1)
	component.SetExpanded(true)
	if !lastExpanded {
		t.Fatal("expansion not passed to renderer")
	}

	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "renderer output") {
		t.Fatalf("rendered = %v", lines)
	}
	if calls == 0 {
		t.Fatal("renderer not called")
	}

	// Expansion change re-renders.
	component.SetExpanded(false)
	if calls < 2 {
		t.Fatal("no re-render on toggle")
	}
}

func TestCustomMessageComponentRendererPanic(t *testing.T) {
	message := &CustomMessageView{CustomType: "boom-ext", Content: "fallback"}
	renderer := func(message *CustomMessageView, options CustomMessageRenderOptions) tui.Component {
		panic("renderer exploded")
	}
	component := NewCustomMessageComponent(message, renderer, 1)

	// Falls through to the default rendering.
	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "[boom-ext]") || !strings.Contains(joined, "fallback") {
		t.Fatalf("fallback = %q", joined)
	}
}

func TestCustomMessageComponentOutputPad(t *testing.T) {
	message := &CustomMessageView{CustomType: "ext", Content: "padded"}
	component := NewCustomMessageComponent(message, nil, 1)
	component.SetOutputPad(2)
	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "padded") {
		t.Fatalf("rendered = %v", lines)
	}
}

func TestCustomEntryComponent(t *testing.T) {
	entry := &CustomEntryView{CustomType: "note", Data: "entry data"}
	renderer := func(entry *CustomEntryView, options CustomEntryRenderOptions) tui.Component {
		return tui.NewText("entry rendered", 0, 0, nil)
	}
	component := NewCustomEntryComponent(entry, renderer)

	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "entry rendered") {
		t.Fatalf("rendered = %v", lines)
	}

	// Expansion toggles re-render.
	component.SetExpanded(true)
}

func TestCustomEntryComponentRendererError(t *testing.T) {
	entry := &CustomEntryView{CustomType: "broken", Data: ""}
	renderer := func(entry *CustomEntryView, options CustomEntryRenderOptions) tui.Component {
		panic("entry renderer failed")
	}
	component := NewCustomEntryComponent(entry, renderer)

	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "renderer failed") {
		t.Fatalf("error = %q", joined)
	}
	if !strings.Contains(joined, "[broken]") {
		t.Fatalf("type = %q", joined)
	}
}

func TestCustomEntryComponentNilRenderer(t *testing.T) {
	entry := &CustomEntryView{CustomType: "nil", Data: ""}
	renderer := func(entry *CustomEntryView, options CustomEntryRenderOptions) tui.Component {
		return nil
	}
	component := NewCustomEntryComponent(entry, renderer)
	lines := component.Render(60)
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			t.Fatalf("non-empty = %q", line)
		}
	}
}
