package codingagent

// Tests for list_models.go, ported from the reference cli/list-models.ts
// behavior: token formatting, table shape, fuzzy filtering, and the
// no-models guidance path.

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatTokenCount(t *testing.T) {
	cases := []struct {
		count float64
		want  string
	}{
		{999, "999"},
		{1000, "1K"},
		{1500, "1.5K"},
		{200000, "200K"},
		{1000000, "1M"},
		{1500000, "1.5M"},
		{0, "0"},
	}
	for _, c := range cases {
		if got := FormatTokenCount(c.count); got != c.want {
			t.Errorf("FormatTokenCount(%v) = %q, want %q", c.count, got, c.want)
		}
	}
}

// availableTestRuntime returns a runtime whose only configured provider is
// my-gateway with two chat models.
func availableTestRuntime(t *testing.T) *ModelRuntime {
	t.Helper()
	runtime := newTestRuntime(t, `{
		"providers": {
			"my-gateway": {
				"name": "My Gateway",
				"baseUrl": "https://gw.example.com/v1",
				"apiKey": "static-key",
				"api": "openai-completions",
				"models": [
					{"id": "custom-2", "contextWindow": 128000, "maxTokens": 16384, "reasoning": true, "input": ["text", "image"]},
					{"id": "custom-1", "contextWindow": 64000}
				]
			}
		}
	}`)
	if err := runtime.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return runtime
}

func TestListModelsTable(t *testing.T) {
	var out, errOut bytes.Buffer
	ListModels(&out, &errOut, availableTestRuntime(t), "")

	if errOut.Len() != 0 {
		t.Errorf("stderr = %q", errOut.String())
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("lines = %d, want at least 3 (header + 2 models):\n%s", len(lines), out.String())
	}

	header := lines[0]
	want := []string{"provider", "model", "context", "max-out", "thinking", "images"}
	fields := strings.Fields(header)
	if len(fields) != len(want) {
		t.Fatalf("header columns = %v, want %v", fields, want)
	}
	for i, column := range want {
		if fields[i] != column {
			t.Errorf("header column %d = %q, want %q", i, fields[i], column)
		}
	}
	// Columns are padded and joined by exactly two spaces; ambient
	// OAuth-capable providers may add rows, so alignment is checked
	// across every line rather than against a fixed row count.
	for i, line := range lines {
		if len(line) != len(header) {
			t.Errorf("line %d not aligned to header width: %q", i, line)
		}
	}

	// The my-gateway rows stay adjacent and sorted by model id.
	var gateway []int
	for i, line := range lines {
		if strings.Contains(line, "my-gateway") {
			gateway = append(gateway, i)
		}
	}
	if len(gateway) != 2 || gateway[1] != gateway[0]+1 {
		t.Fatalf("my-gateway rows not adjacent: %v", gateway)
	}
	first, second := lines[gateway[0]], lines[gateway[1]]
	if !strings.Contains(first, "custom-1") || !strings.Contains(second, "custom-2") {
		t.Fatalf("rows not sorted by id:\n%s\n%s", first, second)
	}
	if !strings.Contains(second, "128K") || !strings.Contains(first, "64K") {
		t.Errorf("context column missing compact counts:\n%s\n%s", first, second)
	}
	if !strings.Contains(second, "yes") {
		t.Errorf("reasoning model should report thinking=yes: %q", second)
	}
	if !strings.Contains(first, "no") {
		t.Errorf("non-reasoning model should report thinking=no: %q", first)
	}
}

func TestListModelsFuzzyFilter(t *testing.T) {
	var matchOut, missOut bytes.Buffer
	ListModels(&matchOut, &bytes.Buffer{}, availableTestRuntime(t), "custom-1")
	if !strings.Contains(matchOut.String(), "custom-1") || strings.Contains(matchOut.String(), "custom-2") {
		t.Errorf("filter should keep only custom-1:\n%s", matchOut.String())
	}

	ListModels(&missOut, &bytes.Buffer{}, availableTestRuntime(t), "zzz")
	if got := missOut.String(); got != "No models matching \"zzz\"\n" {
		t.Errorf("miss output = %q", got)
	}
}

func TestListModelsNoneAvailable(t *testing.T) {
	// No configured credentials: the availability snapshot is empty and the
	// auth-guidance message replaces the table.
	runtime := newTestRuntime(t, "")
	var out bytes.Buffer
	ListModels(&out, &bytes.Buffer{}, runtime, "")
	if got := out.String(); !strings.Contains(got, "No models available") {
		t.Errorf("output = %q", got)
	}
}
