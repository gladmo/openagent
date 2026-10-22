package telemetry

import (
	"strings"
	"testing"
)

// Port of pi-telemetry test/render.test.go. Attribute rendering iterates
// keys in sorted order (Go maps are unordered; the TS golden's insertion
// order becomes sorted order here).

func renderSpan(id string, overrides func(*StoredSpan)) StoredSpan {
	span := StoredSpan{
		ID:        id,
		TraceID:   "trace",
		Name:      id,
		StartedMs: 0,
		EndedMs:   10,
		Events:    []StoredSpanEvent{},
		Links:     []StoredSpanLink{},
		Status:    StatusOK(),
	}
	if overrides != nil {
		overrides(&span)
	}
	return span
}

func TestRenderTraceUnorderedTrace(t *testing.T) {
	root := renderSpan("root", func(s *StoredSpan) {
		s.StartedMs = 1000
		s.EndedMs = 1020
		s.Attributes = SpanAttributes{
			"request": "a b",
			"attempt": float64(2),
			"cached":  false,
			"tags":    []string{"a", "b"},
			"sizes":   []float64{1, 2},
			"flags":   []bool{true, false},
		}
		s.Events = []StoredSpanEvent{
			{Name: "checkpoint", TimeMs: 2, Attributes: SpanAttributes{"phase": "tools"}},
			{Name: "tick", TimeMs: 3.5},
		}
		s.Links = []StoredSpanLink{
			{TraceID: "remote-trace", SpanID: "remote", Attributes: SpanAttributes{"reason": "steer"}},
			{TraceID: "other-trace", SpanID: "other"},
		}
	})
	first := renderSpan("first", func(s *StoredSpan) {
		s.ParentID = "root"
		s.StartedMs = 1005
		s.EndedMs = 1009
		s.Status = StatusError("Error", "failed call")
	})
	nested := renderSpan("nested", func(s *StoredSpan) {
		s.ParentID = "first"
		s.StartedMs = 1006
		s.EndedMs = 1007
	})
	second := renderSpan("second", func(s *StoredSpan) {
		s.ParentID = "root"
		s.StartedMs = 1010
		s.EndedMs = 1012
		s.Status = StatusErrorWithoutDetails()
	})
	input := []StoredSpan{second, nested, first, root}
	want := strings.Join([]string{
		`root attempt=2 cached=false flags=[true,false] request="a b" sizes=[1,2] tags=["a","b"] (20ms)`,
		`  · checkpoint phase="tools" (+2ms)`,
		`  · tick (+3.5ms)`,
		`  → link trace remote-trace span remote reason="steer"`,
		`  → link trace other-trace span other`,
		`  first (4ms) error Error: failed call`,
		`    nested (1ms)`,
		`  second (2ms) error`,
	}, "\n")
	if got := RenderTrace(input); got != want {
		t.Fatalf("render:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderTraceMissingParentsBecomeRoots(t *testing.T) {
	got := RenderTrace([]StoredSpan{
		renderSpan("later", func(s *StoredSpan) { s.ParentID = "external"; s.StartedMs = 20; s.EndedMs = 30 }),
		renderSpan("child", func(s *StoredSpan) { s.ParentID = "later"; s.StartedMs = 25; s.EndedMs = 30 }),
		renderSpan("earlier", func(s *StoredSpan) {
			s.StartedMs = 10
			s.EndedMs = 15
			s.Links = []StoredSpanLink{{TraceID: "trace", SpanID: "later"}}
		}),
	})
	want := strings.Join([]string{
		"earlier (5ms)",
		"  → link trace trace span later",
		"later (10ms)",
		"  child (5ms)",
	}, "\n")
	if got != want {
		t.Fatalf("render:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderTraceCrossTraceLinksWithoutFollowing(t *testing.T) {
	got := RenderTrace([]StoredSpan{
		renderSpan("server:1", func(s *StoredSpan) {
			s.Name = "server"
			s.TraceID = "server-trace"
			s.Links = []StoredSpanLink{{TraceID: "worker-trace", SpanID: "worker:1"}}
		}),
		renderSpan("worker:1", func(s *StoredSpan) {
			s.Name = "worker"
			s.TraceID = "worker-trace"
			s.Links = []StoredSpanLink{{TraceID: "server-trace", SpanID: "server:1"}}
		}),
	})
	want := strings.Join([]string{
		"server (10ms)",
		"  → link trace worker-trace span worker:1",
		"worker (10ms)",
		"  → link trace server-trace span server:1",
	}, "\n")
	if got != want {
		t.Fatalf("render:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderTraceDeduplicatesMergedSpans(t *testing.T) {
	got := RenderTrace([]StoredSpan{
		renderSpan("worker:1", func(s *StoredSpan) {
			s.Name = "child"
			s.ParentID = "server:1"
			s.Events = []StoredSpanEvent{{Name: "done", TimeMs: 3}}
		}),
		renderSpan("server:1", func(s *StoredSpan) { s.Name = "root" }),
		renderSpan("worker:2", func(s *StoredSpan) { s.Name = "grandchild"; s.ParentID = "worker:1" }),
		renderSpan("server:1", func(s *StoredSpan) { s.Name = "root" }),
		renderSpan("worker:1", func(s *StoredSpan) {
			s.Name = "child"
			s.ParentID = "server:1"
			s.Events = []StoredSpanEvent{{Name: "done", TimeMs: 3}}
		}),
	})
	want := "root (10ms)\n  child (10ms)\n    · done (+3ms)\n    grandchild (10ms)"
	if got != want {
		t.Fatalf("render:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderTracePreservesInputOrderForEqualStarts(t *testing.T) {
	got := RenderTrace([]StoredSpan{
		renderSpan("root", nil),
		renderSpan("z", func(s *StoredSpan) { s.ParentID = "root" }),
		renderSpan("a", func(s *StoredSpan) { s.ParentID = "root" }),
		renderSpan("other", nil),
	})
	want := "root (10ms)\n  z (10ms)\n  a (10ms)\nother (10ms)"
	if got != want {
		t.Fatalf("render:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderTraceEscapesStringValues(t *testing.T) {
	got := RenderTrace([]StoredSpan{
		renderSpan("root", func(s *StoredSpan) {
			s.Attributes = SpanAttributes{"text": "line one\n\"line two\""}
		}),
	})
	want := `root text="line one\n\"line two\"" (10ms)`
	if got != want {
		t.Fatalf("render:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderTraceDeepTreesWithoutRecursion(t *testing.T) {
	spans := make([]StoredSpan, 2000)
	for index := range spans {
		index := index
		spans[index] = renderSpan(itoa(index), func(s *StoredSpan) {
			if index > 0 {
				s.ParentID = itoa(index - 1)
			}
		})
	}
	rendered := RenderTrace(spans)
	lines := strings.Split(rendered, "\n")
	if len(lines) != len(spans) {
		t.Fatalf("lines = %d, want %d", len(lines), len(spans))
	}
	indent := ""
	for i := 0; i < 1999; i++ {
		indent += "  "
	}
	if !strings.HasSuffix(rendered, indent+"1999 (10ms)") {
		t.Fatalf("tail = %q", rendered[len(rendered)-40:])
	}
}

func TestRenderTraceEmptyInput(t *testing.T) {
	if got := RenderTrace(nil); got != "" {
		t.Fatalf("render = %q", got)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}
