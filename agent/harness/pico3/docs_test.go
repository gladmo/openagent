package pico3

// Ports of the doc-state shapes + defaults routing.

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func TestNewDocumentStates(t *testing.T) {
	rewindable := NewRewindableState()
	if rewindable.MustGet("thinkingLevel") != "off" {
		t.Fatalf("rewindable = %v", rewindable)
	}
	if _, ok := rewindable.Get("model"); ok {
		t.Fatal("model should be optional-absent")
	}
	plugins := rewindable.MustGet("plugins").(*jsonx.Obj)
	if plugins == nil {
		t.Fatal("plugins missing")
	}

	sticky := NewStickyState()
	if sticky.MustGet("steeringMode") != "all" || sticky.MustGet("followUpMode") != "all" {
		t.Fatalf("sticky = %v", sticky)
	}
	turn := sticky.MustGet("turn").(*jsonx.Obj)
	if turn == nil || turn.MustGet("tools") == nil {
		t.Fatal("turn.tools missing")
	}
	if sticky.MustGet("tasks") == nil {
		t.Fatal("tasks missing")
	}

	session := NewSessionState()
	if session.MustGet("plugins") == nil {
		t.Fatal("session plugins missing")
	}
}

func TestCollectDefaultsRouting(t *testing.T) {
	declarations := append(CoreConfigDeclarations(),
		KindConfigDeclaration{Key: "app.mode", Doc: "sticky", Default: "fast"},
	)
	defaults := CollectDefaults(declarations)
	if defaults.Route["thinkingLevel"] != "rewindable" {
		t.Fatalf("route = %v", defaults.Route["thinkingLevel"])
	}
	if defaults.Route["app.mode"] != "sticky" {
		t.Fatalf("route = %v", defaults.Route["app.mode"])
	}
	if defaults.Sticky.MustGet("app.mode") != "fast" {
		t.Fatal("sticky default lost")
	}
	// model routes rewindable with NO default (provider-set only).
	if v, ok := defaults.Rewindable.Get("model"); ok && v != nil {
		t.Fatal("model default leaked")
	}
}

func TestProjectConfigLayering(t *testing.T) {
	defaults := CollectDefaults(CoreConfigDeclarations())
	rewindable := NewRewindableState()
	rewindable.Set("thinkingLevel", "high")
	sticky := NewStickyState()
	sticky.Set("steeringMode", "one-at-a-time")

	config := ProjectConfig(defaults, rewindable, sticky)
	if config.MustGet("thinkingLevel") != "high" {
		t.Fatalf("doc value lost: %v", config.MustGet("thinkingLevel"))
	}
	if config.MustGet("steeringMode") != "one-at-a-time" {
		t.Fatalf("sticky doc value lost: %v", config.MustGet("steeringMode"))
	}
	// Absent in both -> registered default.
	if config.MustGet("followUpMode") != "all" {
		t.Fatalf("default lost: %v", config.MustGet("followUpMode"))
	}
	// Absent everywhere with no default -> omitted.
	if _, ok := config.Get("model"); ok {
		t.Fatal("model projected without value")
	}
}

func TestMergePluginsOrder(t *testing.T) {
	// Registration defaults then document slices; later wins.
	regRewindable := jsonx.ObjFrom("theme", "dark")
	regSticky := jsonx.ObjFrom("level", float64(1))
	docRewindable := jsonx.ObjFrom("theme", "light", "extra", true)
	docSticky := jsonx.ObjFrom("level", float64(2))
	projected := MergePlugins(
		[]*jsonx.Obj{regRewindable, regSticky},
		[]*jsonx.Obj{docRewindable, docSticky},
		nil,
	)
	if projected.MustGet("theme") != "light" {
		t.Fatalf("theme = %v", projected.MustGet("theme"))
	}
	if projected.MustGet("level") != float64(2) {
		t.Fatalf("level = %v", projected.MustGet("level"))
	}
	if projected.MustGet("extra") != true {
		t.Fatal("extra lost")
	}
	// The projection function transforms the merged view.
	upper := MergePlugins([]*jsonx.Obj{jsonx.ObjFrom("k", "v")}, nil, func(merged *jsonx.Obj) *jsonx.Obj {
		out := jsonx.NewObj()
		out.Set("projected", merged.MustGet("k"))
		return out
	})
	if upper.MustGet("projected") != "v" {
		t.Fatalf("projected = %v", upper)
	}
}
