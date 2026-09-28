package pico3

// Ports of system.ts section behaviors.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

// fakeSectionSource serves managed entries newest-first with pagination.
type fakeSectionSource struct {
	entries []*Entry
}

func (f *fakeSectionSource) ScanEntries(scan EntryScan) ([]*Entry, error) {
	var filtered []*Entry
	for _, entry := range f.entries {
		if entry.ConversationID != scan.ConversationID || entry.Kind != scan.Kind {
			continue
		}
		if scan.Before != nil && entry.ID >= *scan.Before {
			continue
		}
		filtered = append(filtered, entry)
	}
	// The source is already newest-first (descending).
	if len(filtered) > scan.Limit {
		filtered = filtered[:scan.Limit]
	}
	return filtered, nil
}

func systemEntry(id Id, conversation Id, sections []SectionRecord, baseline bool) *Entry {
	data := jsonx.NewObj()
	records := make([]any, 0, len(sections))
	for _, r := range sections {
		obj := jsonx.NewObj()
		obj.Set("key", r.Key)
		if r.Action == SectionSeedRemove {
			obj.Set("action", "remove")
		} else {
			obj.Set("action", "set")
			obj.Set("value", r.Value)
			obj.Set("rendered", r.Rendered)
		}
		records = append(records, obj)
	}
	data.Set("sections", records)
	if baseline {
		data.Set("baseline", true)
	}
	return &Entry{ID: id, ConversationID: conversation, Kind: "pi.system", Data: data}
}

func TestFoldCanonicalBaselines(t *testing.T) {
	source := &fakeSectionSource{entries: []*Entry{
		systemEntry(5, 1, []SectionRecord{{Action: SectionSeedSet, Key: "skills", Value: "s2", Rendered: "r-s2"}}, false),
		systemEntry(4, 1, []SectionRecord{{Action: SectionSeedRemove, Key: "environment"}}, false),
		systemEntry(3, 1, []SectionRecord{{Action: SectionSeedSet, Key: "environment", Value: "e1", Rendered: "r-e1"}}, true),
		systemEntry(2, 1, []SectionRecord{{Action: SectionSeedSet, Key: "stale", Value: "x", Rendered: "r-x"}}, false),
	}}
	result, err := FoldCanonical(source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.NewestBaseline == nil || *result.NewestBaseline != 3 {
		t.Fatalf("baseline = %v", result.NewestBaseline)
	}
	if result.NewestManaged == nil || *result.NewestManaged != 5 {
		t.Fatalf("newestManaged = %v", result.NewestManaged)
	}
	// Pre-baseline entry (2) is excluded; remove of environment wins over
	// the baseline's set.
	canonical := result.Canonical
	if _, exists := canonical.Get("stale"); exists {
		t.Fatal("pre-baseline leaked")
	}
	if _, exists := canonical.Get("environment"); exists {
		t.Fatal("remove not applied")
	}
	if state, ok := canonical.Get("skills"); !ok || state.Rendered != "r-s2" {
		t.Fatalf("skills = %+v", state)
	}
}

func TestFoldCanonicalNoBaseline(t *testing.T) {
	source := &fakeSectionSource{entries: []*Entry{
		systemEntry(2, 1, []SectionRecord{{Action: SectionSeedSet, Key: "a", Value: float64(1), Rendered: "ra"}}, false),
	}}
	result, err := FoldCanonical(source, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.NewestBaseline != nil {
		t.Fatalf("baseline = %v", result.NewestBaseline)
	}
	if _, ok := result.Canonical.Get("a"); !ok {
		t.Fatal("entry without baseline dropped")
	}
}

func TestBuiltinSectionRenderers(t *testing.T) {
	if got := SectionIdentity.Render("you are pi"); got != "you are pi" {
		t.Fatalf("identity = %q", got)
	}
	env := jsonx.ObjFrom("cwd", "/work/project")
	if got := SectionEnvironment.Render(env); got != "Working directory: /work/project" {
		t.Fatalf("environment = %q", got)
	}
	skills := []any{
		jsonx.ObjFrom("name", "read", "description", "reads files"),
		jsonx.ObjFrom("name", "write", "description", "writes files"),
	}
	want := "- read: reads files\n- write: writes files"
	if got := SectionSkills.Render(skills); got != want {
		t.Fatalf("skills = %q", got)
	}
}

func TestDraftEditAndFreeze(t *testing.T) {
	registry := map[string]SystemSection{
		"identity": SectionIdentity,
		"skills":   SectionSkills,
	}
	canonical := NewCanonical()
	canonical.Set("identity", SectionState{Value: "old", Rendered: "old"})
	canonical.Set("skills", SectionState{Value: []any{}, Rendered: "- none"})

	draft := NewDraft(canonical)
	// Untouched section keeps stored text.
	draft.Set(SectionIdentity, "new identity")
	draft.Set(SectionSkills, []any{
		jsonx.ObjFrom("name", "bash", "description", "runs commands"),
	})
	draft.Wrap(SectionSkills, func(rendered string) string {
		return rendered + "\n(custom suffix)"
	})

	frozen := Freeze(draft, canonical, registry)
	if state, ok := frozen.Get("identity"); !ok || state.Rendered != "new identity" {
		t.Fatalf("identity = %+v", state)
	}
	skillsState, _ := frozen.Get("skills")
	if !strings.HasSuffix(skillsState.Rendered, "(custom suffix)") {
		t.Fatalf("skills = %q", skillsState.Rendered)
	}
	if !strings.Contains(skillsState.Rendered, "- bash: runs commands") {
		t.Fatalf("skills = %q", skillsState.Rendered)
	}
	// Canonical untouched by freeze.
	if state, _ := canonical.Get("identity"); state.Rendered != "old" {
		t.Fatal("canonical mutated")
	}
}

func TestDraftDeleteAndUnregistered(t *testing.T) {
	registry := map[string]SystemSection{"identity": SectionIdentity}
	canonical := NewCanonical()
	canonical.Set("identity", SectionState{Value: "v", Rendered: "r"})
	canonical.Set("ghost", SectionState{Value: "g", Rendered: "rg"})

	draft := NewDraft(canonical)
	draft.Delete("identity")
	// Touch the unregistered key.
	draft.Set(SystemSection{Key: "ghost"}, "touched")

	frozen := Freeze(draft, canonical, registry)
	if _, exists := frozen.Get("identity"); exists {
		t.Fatal("deleted section survived")
	}
	// Unregistered touched key treated as removed.
	if _, exists := frozen.Get("ghost"); exists {
		t.Fatal("unregistered touched key survived")
	}
}

func TestDraftSnapshotRestore(t *testing.T) {
	canonical := NewCanonical()
	canonical.Set("identity", SectionState{Value: "base", Rendered: "base"})
	draft := NewDraft(canonical)
	snap := draft.Snapshot()
	draft.Set(SectionIdentity, "experiment")
	if draft.Get(SectionIdentity) != "experiment" {
		t.Fatal("set failed")
	}
	draft.Restore(snap)
	if draft.Get(SectionIdentity) != "base" {
		t.Fatalf("restored = %v", draft.Get(SectionIdentity))
	}
}

func TestDraftPositionSemantics(t *testing.T) {
	canonical := NewCanonical()
	canonical.Set("a", SectionState{Value: "1", Rendered: "1"})
	canonical.Set("b", SectionState{Value: "2", Rendered: "2"})
	draft := NewDraft(canonical)
	// Existing key keeps position on set.
	draft.Set(SystemSection{Key: "a"}, "1-new")
	draft.Set(SystemSection{Key: "c"}, "3")
	order := append([]string{}, draft.order...)
	if order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Fatalf("order = %v", order)
	}
	// Delete removes from order.
	draft.Delete("b")
	if len(draft.order) != 2 || draft.order[1] != "c" {
		t.Fatalf("order after delete = %v", draft.order)
	}
}

func TestSameSnapshot(t *testing.T) {
	managed := Id(5)
	baseline := Id(3)
	a := &PreparationSnapshot{NewestManaged: &managed, NewestBaseline: &baseline, ThinkingLevel: "low", SelectedTools: []string{"read"}, SectionsRev: 2, ToolsRev: 1}
	b := &PreparationSnapshot{NewestManaged: &managed, NewestBaseline: &baseline, ThinkingLevel: "low", SelectedTools: []string{"read"}, SectionsRev: 2, ToolsRev: 1}
	if !SameSnapshot(a, b) {
		t.Fatal("equal snapshots differ")
	}
	c := &PreparationSnapshot{NewestManaged: &managed, NewestBaseline: &baseline, ThinkingLevel: "high", SelectedTools: []string{"read"}, SectionsRev: 2, ToolsRev: 1}
	if SameSnapshot(a, c) {
		t.Fatal("different snapshots equal")
	}
}
