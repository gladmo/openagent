package tui

import (
	"reflect"
	"testing"
)

// Ported from pi/packages/tui/test/fuzzy.test.ts.

func TestFuzzyMatchBasics(t *testing.T) {
	if got := FuzzyMatchQuery("", "anything"); !got.Matches || got.Score != 0 {
		t.Fatalf("empty query: %+v", got)
	}
	if got := FuzzyMatchQuery("longquery", "short"); got.Matches {
		t.Fatalf("longer query matched: %+v", got)
	}
	exact := FuzzyMatchQuery("test", "test")
	if !exact.Matches || exact.Score >= 0 {
		t.Fatalf("exact match: %+v", exact)
	}
	if got := FuzzyMatchQuery("abc", "aXbXc"); !got.Matches {
		t.Fatalf("in-order failed")
	}
	if got := FuzzyMatchQuery("abc", "cba"); got.Matches {
		t.Fatalf("out-of-order matched")
	}
	if !FuzzyMatchQuery("ABC", "abc").Matches || !FuzzyMatchQuery("abc", "ABC").Matches {
		t.Fatal("case sensitivity broken")
	}
}

func TestFuzzyMatchScoring(t *testing.T) {
	consecutive := FuzzyMatchQuery("foo", "foobar")
	scattered := FuzzyMatchQuery("foo", "f_o_o_bar")
	if !consecutive.Matches || !scattered.Matches {
		t.Fatal("both should match")
	}
	if consecutive.Score >= scattered.Score {
		t.Fatalf("consecutive %v should beat scattered %v", consecutive.Score, scattered.Score)
	}

	atBoundary := FuzzyMatchQuery("fb", "foo-bar")
	notAtBoundary := FuzzyMatchQuery("fb", "afbx")
	if !atBoundary.Matches || !notAtBoundary.Matches {
		t.Fatal("both should match")
	}
	if atBoundary.Score >= notAtBoundary.Score {
		t.Fatalf("boundary %v should beat non-boundary %v", atBoundary.Score, notAtBoundary.Score)
	}

	if got := FuzzyMatchQuery("codex52", "gpt-5.2-codex"); !got.Matches {
		t.Fatalf("swapped tokens did not match: %+v", got)
	}
}

func TestFuzzyFilterBasics(t *testing.T) {
	items := []string{"apple", "banana", "cherry"}
	if got := FuzzyFilter(items, "", func(x string) string { return x }); !reflect.DeepEqual(got, items) {
		t.Fatalf("empty query: %v", got)
	}

	got := FuzzyFilter(items, "an", func(x string) string { return x })
	if len(got) != 1 || got[0] != "banana" {
		t.Fatalf("filtered: %v", got)
	}

	got = FuzzyFilter([]string{"a_p_p", "app", "application"}, "app", func(x string) string { return x })
	if got[0] != "app" {
		t.Fatalf("sorted by quality: %v", got)
	}

	got = FuzzyFilter([]string{"clone", "cl"}, "cl", func(x string) string { return x })
	if !reflect.DeepEqual(got, []string{"cl", "clone"}) {
		t.Fatalf("exact over prefix: %v", got)
	}
}

type fuzzyItem struct {
	name string
	id   int
}

func TestFuzzyFilterCustomGetText(t *testing.T) {
	items := []fuzzyItem{{"foo", 1}, {"bar", 2}, {"foobar", 3}}
	got := FuzzyFilter(items, "foo", func(item fuzzyItem) string { return item.name })
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	names := []string{got[0].name, got[1].name}
	if !contains(names, "foo") || !contains(names, "foobar") {
		t.Fatalf("got %v", names)
	}
}

func contains(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

type modelItem struct {
	id       string
	provider string
}

func TestFuzzyFilterSlashSeparatedReordered(t *testing.T) {
	item := modelItem{id: "gpt-5.5", provider: "openai-codex"}
	got := FuzzyFilter([]modelItem{item}, "openai-codex/gpt-5.5", func(m modelItem) string {
		return m.id + " " + m.provider
	})
	if len(got) != 1 || got[0] != item {
		t.Fatalf("got %v", got)
	}
}
