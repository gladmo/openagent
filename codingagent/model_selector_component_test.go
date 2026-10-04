package codingagent

// Tests for the model selector component (adapted from
// model-selector.test.ts).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
)

func selectorModel(provider, id string) *ai.Model {
	return &ai.Model{Provider: provider, ID: id}
}

func newSelectorForTest(t *testing.T, scoped ...*ai.Model) *ModelSelectorComponent {
	t.Helper()
	var scopedRefs []ScopedModelRef
	for _, model := range scoped {
		scopedRefs = append(scopedRefs, ScopedModelRef{Model: model})
	}
	return NewModelSelectorComponent(ModelSelectorOptions{
		ScopedModels: scopedRefs,
	})
}

func TestModelSelectorInitialList(t *testing.T) {
	component := newSelectorForTest(t,
		selectorModel("anthropic", "claude-sonnet"),
		selectorModel("openai", "gpt-5"),
	)
	if component.FilteredCount() != 2 {
		t.Fatalf("count = %d", component.FilteredCount())
	}
	lines := component.Render(60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "claude-sonnet") || !strings.Contains(joined, "gpt-5") {
		t.Fatalf("list = %q", joined)
	}
	if !strings.Contains(joined, "[anthropic]") || !strings.Contains(joined, "[openai]") {
		t.Fatalf("providers = %q", joined)
	}
}

func TestModelSelectorScopeToggle(t *testing.T) {
	component := newSelectorForTest(t,
		selectorModel("anthropic", "claude"),
		selectorModel("openai", "gpt"),
	)
	if component.Scope() != "scoped" {
		t.Fatalf("scope = %q", component.Scope())
	}
	component.HandleInput("tab")
	if component.Scope() != "all" {
		t.Fatalf("after tab = %q", component.Scope())
	}
	component.HandleInput("tab")
	if component.Scope() != "scoped" {
		t.Fatal("toggle did not wrap")
	}
}

func TestModelSelectorSearch(t *testing.T) {
	component := newSelectorForTest(t,
		selectorModel("anthropic", "claude-sonnet"),
		selectorModel("openai", "gpt-5"),
		selectorModel("openai", "o3"),
	)
	component.HandleInput("g")
	component.HandleInput("p")
	component.HandleInput("t")
	if component.SearchValue() != "gpt" {
		t.Fatalf("query = %q", component.SearchValue())
	}
	if component.FilteredCount() != 1 {
		t.Fatalf("filtered = %d", component.FilteredCount())
	}
	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "gpt-5") {
		t.Fatalf("result = %v", lines)
	}
	if strings.Contains(strings.Join(lines, "\n"), "claude") {
		t.Fatalf("leaked = %v", lines)
	}

	// Backspace shrinks.
	component.HandleInput("backspace")
	component.HandleInput("backspace")
	component.HandleInput("backspace")
	if component.SearchValue() != "" {
		t.Fatalf("query = %q", component.SearchValue())
	}
	if component.FilteredCount() != 3 {
		t.Fatalf("restored = %d", component.FilteredCount())
	}
}

func TestModelSelectorNoResults(t *testing.T) {
	component := newSelectorForTest(t, selectorModel("p", "m"))
	component.HandleInput("z")
	component.HandleInput("z")
	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "No matching models") {
		t.Fatalf("empty = %v", lines)
	}
}

func TestModelSelectorNavigationWraps(t *testing.T) {
	component := newSelectorForTest(t,
		selectorModel("p", "one"),
		selectorModel("p", "two"),
		selectorModel("p", "three"),
	)
	if component.SelectedIndex() != 0 {
		t.Fatal("start not at 0")
	}
	component.HandleInput("up")
	if component.SelectedIndex() != 2 {
		t.Fatalf("up-wrap = %d", component.SelectedIndex())
	}
	component.HandleInput("down")
	if component.SelectedIndex() != 0 {
		t.Fatalf("down-wrap = %d", component.SelectedIndex())
	}
}

func TestModelSelectorSelects(t *testing.T) {
	var selected *ai.Model
	component := NewModelSelectorComponent(ModelSelectorOptions{
		ScopedModels: []ScopedModelRef{{Model: selectorModel("p", "chosen")}},
		OnSelect:     func(model *ai.Model) { selected = model },
	})
	component.HandleInput("enter")
	if selected == nil || selected.ID != "chosen" {
		t.Fatalf("selected = %v", selected)
	}
}

func TestModelSelectorCancel(t *testing.T) {
	cancelled := false
	component := NewModelSelectorComponent(ModelSelectorOptions{
		ScopedModels: []ScopedModelRef{{Model: selectorModel("p", "m")}},
		OnCancel:     func() { cancelled = true },
	})
	component.HandleInput("escape")
	if !cancelled {
		t.Fatal("cancel lost")
	}
}

func TestModelSelectorSaveDefault(t *testing.T) {
	var saved *ai.Model
	component := NewModelSelectorComponent(ModelSelectorOptions{
		ScopedModels:      []ScopedModelRef{{Model: selectorModel("p", "saved")}},
		OnSelectAsDefault: func(model *ai.Model) { saved = model },
	})
	component.HandleInput("ctrl+s")
	if saved == nil || saved.ID != "saved" {
		t.Fatalf("saved = %v", saved)
	}
}

func TestModelSelectorDefaultBadgeAndPin(t *testing.T) {
	defaults := &DefaultModelRef{Provider: "p", ID: "defaulted"}
	component := NewModelSelectorComponent(ModelSelectorOptions{
		ScopedModels: []ScopedModelRef{
			{Model: selectorModel("p", "alpha")},
			{Model: selectorModel("p", "defaulted")},
			{Model: selectorModel("q", "zeta")},
		},
		DefaultModel: defaults,
	})
	// "default" search pins the default first.
	component.filterModels("default")
	if component.FilteredCount() < 1 {
		t.Fatal("no results")
	}
	if component.filteredModels[0].ID != "defaulted" {
		t.Fatalf("pin = %q", component.filteredModels[0].ID)
	}
	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "· default") {
		t.Fatalf("badge = %v", lines)
	}
}

func TestModelSelectorCurrentMarker(t *testing.T) {
	current := selectorModel("p", "current")
	component := NewModelSelectorComponent(ModelSelectorOptions{
		CurrentModel: current,
		ScopedModels: []ScopedModelRef{
			{Model: current},
			{Model: selectorModel("p", "other")},
		},
	})
	lines := component.Render(60)
	if !strings.Contains(strings.Join(lines, "\n"), "✓") {
		t.Fatalf("marker = %v", lines)
	}
}
