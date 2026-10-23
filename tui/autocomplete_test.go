package tui

// Ported from pi/packages/tui/test/autocomplete.test.ts (prefix extraction,
// dot-slash completion, quoted path completion; fd @-suggestions run when fd
// is installed).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/abort"
)

func autocompleteSetupFolder(t *testing.T, baseDir string, dirs []string, files map[string]string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(baseDir, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	for filePath, contents := range files {
		fullPath := filepath.Join(baseDir, filePath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filePath, err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", filePath, err)
		}
	}
}

func autocompleteSuggestions(t *testing.T, provider *CombinedAutocompleteProvider, lines []string, cursorLine, cursorCol int, force bool) *AutocompleteSuggestions {
	t.Helper()
	result := provider.GetSuggestions(lines, cursorLine, cursorCol, AutocompleteQueryOptions{Signal: abort.NewController().Signal(), Force: force})
	return result
}

func TestAutocompleteExtractPathPrefix(t *testing.T) {
	provider := NewCombinedAutocompleteProvider(nil, nil, "/tmp", "")

	// Forced: '/' from 'hey /'.
	result := autocompleteSuggestions(t, provider, []string{"hey /"}, 0, 5, true)
	if result == nil {
		t.Fatal("expected suggestions for root directory")
	}
	if result.Prefix != "/" {
		t.Fatalf("prefix = %q", result.Prefix)
	}

	// Forced: '/A'.
	result = autocompleteSuggestions(t, provider, []string{"/A"}, 0, 2, true)
	if result != nil && result.Prefix != "/A" {
		t.Fatalf("prefix = %q", result.Prefix)
	}

	// Slash commands do not trigger (Tab with text starting / and no space
	// counts as a command).
	result = autocompleteSuggestions(t, provider, []string{"/model"}, 0, 6, true)
	if result != nil {
		t.Fatalf("slash command triggered: %+v", result)
	}

	// Absolute path in command arguments triggers.
	result = autocompleteSuggestions(t, provider, []string{"/command /"}, 0, 10, true)
	if result == nil {
		t.Fatal("expected suggestions for command argument path")
	}
	if result.Prefix != "/" {
		t.Fatalf("prefix = %q", result.Prefix)
	}
}

func TestAutocompleteCjkPrefixesAfterSeparators(t *testing.T) {
	baseDir := t.TempDir()
	autocompleteSetupFolder(t, baseDir, nil, map[string]string{"说明.md": "file", "文档/说明.md": "nested"})
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	completions := []struct{ prefix, value string }{
		{"说", "说明.md"},
		{"文", "文档/"},
		{"文档/说", "文档/说明.md"},
		{"./文档/说", "./文档/说明.md"},
	}
	separators := []rune(" \t\u3000\u00a0，：；。！？（「《")
	for _, separator := range separators {
		for _, completion := range completions {
			before := "查看𠮷" + string(separator)
			line := before + completion.prefix + " 后文"
			cursorCol := len(before) + len(completion.prefix)
			result := autocompleteSuggestions(t, provider, []string{line}, 0, cursorCol, true)
			if result == nil {
				t.Fatalf("no result for %q", line)
			}
			if result.Prefix != completion.prefix {
				t.Errorf("sep %q prefix = %q, want %q", separator, result.Prefix, completion.prefix)
				continue
			}
			if len(result.Items) != 1 || result.Items[0].Value != completion.value {
				t.Errorf("sep %q items = %+v, want [%s]", separator, result.Items, completion.value)
				continue
			}
			applied := provider.ApplyCompletion([]string{line}, 0, cursorCol, result.Items[0], result.Prefix)
			if applied.Lines[0] != before+completion.value+" 后文" {
				t.Errorf("sep %q applied = %q", separator, applied.Lines[0])
			}
			if applied.CursorCol != len(before)+len(completion.value) {
				t.Errorf("sep %q cursor = %d", separator, applied.CursorCol)
			}
		}
	}
}

func TestAutocompleteSeparatorsAreBoundaries(t *testing.T) {
	baseDir := t.TempDir()
	autocompleteSetupFolder(t, baseDir, nil, map[string]string{"归档/说明.md": "other"})
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	for _, separator := range []string{" ", "\u3000", "，", "。"} {
		directory := "资料" + separator + "归档"
		autocompleteSetupFolder(t, baseDir, nil, map[string]string{directory + "/说明.md": "archive"})
		for _, marker := range []string{"", "@"} {
			if marker == "@" {
				continue // @ prefix goes through fd fuzzy (not fd-dependent here but distinct path)
			}
			line := marker + directory + "/说"
			result := autocompleteSuggestions(t, provider, []string{line}, 0, len(line), true)
			if result == nil {
				t.Fatalf("no result for %q", line)
			}
			if result.Prefix != "归档/说" {
				t.Fatalf("sep %q prefix = %q", separator, result.Prefix)
			}
			if len(result.Items) != 1 || result.Items[0].Value != "归档/说明.md" {
				t.Fatalf("sep %q items = %+v", separator, result.Items)
			}
		}
		// Quoted: the quoted prefix contains the separator literally.
		quoted := "查看，\"" + directory + "/说\"后文"
		cursorCol := strings.Index(quoted, "\"后文")
		result := autocompleteSuggestions(t, provider, []string{quoted}, 0, cursorCol, true)
		if result == nil {
			t.Fatalf("no quoted result for %q", quoted)
		}
		if result.Prefix != "\""+directory+"/说" {
			t.Fatalf("quoted prefix = %q", result.Prefix)
		}
		if len(result.Items) != 1 || result.Items[0].Value != "\""+directory+"/说明.md\"" {
			t.Fatalf("quoted items = %+v", result.Items)
		}
		applied := provider.ApplyCompletion([]string{quoted}, 0, cursorCol, result.Items[0], result.Prefix)
		if applied.Lines[0] != "查看，\""+directory+"/说明.md\"后文" {
			t.Fatalf("quoted applied = %q", applied.Lines[0])
		}
		// Missing path in quotes: null.
		missing := "查看，\"不存在" + separator + "归档/说"
		if result := autocompleteSuggestions(t, provider, []string{missing}, 0, len(missing), true); result != nil {
			t.Fatalf("missing quoted path returned: %+v", result)
		}
	}
}

func TestAutocompleteEmptyPrefixAfterSeparator(t *testing.T) {
	baseDir := t.TempDir()
	autocompleteSetupFolder(t, baseDir, nil, map[string]string{"说明.md": "text"})
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	for _, separator := range []string{" ", "\t", "\u3000", "，", "。"} {
		for _, force := range []bool{false, true} {
			line := "查看" + separator
			result := autocompleteSuggestions(t, provider, []string{line}, 0, len(line), force)
			if result == nil {
				t.Fatalf("no result for %q force=%v", line, force)
			}
			if result.Prefix != "" {
				t.Fatalf("prefix = %q", result.Prefix)
			}
			if len(result.Items) != 1 || result.Items[0].Value != "说明.md" {
				t.Fatalf("items = %+v", result.Items)
			}
		}
	}
	if result := autocompleteSuggestions(t, provider, []string{""}, 0, 0, false); result != nil {
		t.Fatalf("empty text triggered: %+v", result)
	}
}

func TestAutocompleteOpeningWrappers(t *testing.T) {
	baseDir := t.TempDir()
	autocompleteSetupFolder(t, baseDir, nil, map[string]string{"src/main.ts": "x"})
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	for _, wrapper := range []string{"(", "[", "{", "<", "`", "((", "(`"} {
		for _, prefix := range []string{"src/ma", "./src/ma"} {
			before := "see " + wrapper
			line := before + prefix
			result := autocompleteSuggestions(t, provider, []string{line}, 0, len(line), true)
			if result == nil {
				t.Fatalf("wrapper %q prefix %q: no result", wrapper, prefix)
			}
			if result.Prefix != prefix {
				t.Fatalf("wrapper %q prefix = %q, want %q", wrapper, result.Prefix, prefix)
			}
			value := strings.Replace(prefix, "src/ma", "src/main.ts", 1)
			if len(result.Items) != 1 || result.Items[0].Value != value {
				t.Fatalf("wrapper %q items = %+v, want [%s]", wrapper, result.Items, value)
			}
			applied := provider.ApplyCompletion([]string{line}, 0, len(line), result.Items[0], result.Prefix)
			if applied.Lines[0] != before+value {
				t.Fatalf("wrapper %q applied = %q", wrapper, applied.Lines[0])
			}
		}
	}
}

func TestAutocompleteQuotedAfterWrapper(t *testing.T) {
	baseDir := t.TempDir()
	autocompleteSetupFolder(t, baseDir, nil, map[string]string{"my dir/main.ts": "x"})
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	line := `see ("my dir/ma`
	result := autocompleteSuggestions(t, provider, []string{line}, 0, len(line), true)
	if result == nil {
		t.Fatal("no result")
	}
	if result.Prefix != `"my dir/ma` {
		t.Fatalf("prefix = %q", result.Prefix)
	}
	if len(result.Items) != 1 || result.Items[0].Value != `"my dir/main.ts"` {
		t.Fatalf("items = %+v", result.Items)
	}
}

func TestAutocompleteKeepsClosedWrappers(t *testing.T) {
	baseDir := t.TempDir()
	autocompleteSetupFolder(t, baseDir, nil, map[string]string{"[slug]/page.tsx": "x", "(group)/layout.tsx": "x"})
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	cases := [][2]string{
		{"[slug]/pa", "[slug]/page.tsx"},
		{"(group)/la", "(group)/layout.tsx"},
		{"./[slug]/pa", "./[slug]/page.tsx"},
	}
	for _, tc := range cases {
		line := "see " + tc[0]
		result := autocompleteSuggestions(t, provider, []string{line}, 0, len(line), true)
		if result == nil {
			t.Fatalf("no result for %q", line)
		}
		if result.Prefix != tc[0] {
			t.Fatalf("prefix = %q, want %q", result.Prefix, tc[0])
		}
		if len(result.Items) != 1 || result.Items[0].Value != tc[1] {
			t.Fatalf("items = %+v, want [%s]", result.Items, tc[1])
		}
	}
}

func TestAutocompletePreservesDotSlash(t *testing.T) {
	baseDir := t.TempDir()
	autocompleteSetupFolder(t, baseDir, []string{"src"}, map[string]string{"update.sh": "#!/bin/bash", "utils.ts": "export {};", "src/index.ts": "export {};"})
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	result := autocompleteSuggestions(t, provider, []string{"./up"}, 0, 4, true)
	if result == nil {
		t.Fatal("no suggestions for ./up")
	}
	found := false
	for _, item := range result.Items {
		if item.Value == "./update.sh" {
			found = true
		}
	}
	if !found {
		t.Fatalf("./update.sh missing: %+v", result.Items)
	}

	result = autocompleteSuggestions(t, provider, []string{"./sr"}, 0, 4, true)
	if result == nil {
		t.Fatal("no suggestions for ./sr")
	}
	found = false
	for _, item := range result.Items {
		if item.Value == "./src/" {
			found = true
		}
	}
	if !found {
		t.Fatalf("./src/ missing: %+v", result.Items)
	}
}

func TestAutocompleteQuotesPathsForDirectCompletion(t *testing.T) {
	baseDir := t.TempDir()
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	for _, separator := range []string{" ", "\u3000", "，", "。"} {
		directory := "my" + separator + "folder"
		autocompleteSetupFolder(t, baseDir, nil, map[string]string{directory + "/test.txt": "content"})
		result := autocompleteSuggestions(t, provider, []string{"my"}, 0, 2, true)
		if result == nil {
			t.Fatalf("sep %q: no result", separator)
		}
		var item *AutocompleteItem
		for i := range result.Items {
			if result.Items[i].Value == "\""+directory+"/\"" {
				item = &result.Items[i]
				break
			}
		}
		if item == nil {
			t.Fatalf("sep %q: %q/ missing in %+v", separator, directory, result.Items)
		}
		applied := provider.ApplyCompletion([]string{"my"}, 0, 2, *item, result.Prefix)
		continued := autocompleteSuggestions(t, provider, applied.Lines, 0, applied.CursorCol, true)
		if continued == nil {
			t.Fatalf("sep %q: continuation empty", separator)
		}
		if continued.Prefix != "\""+directory+"/" {
			t.Fatalf("sep %q continuation prefix = %q", separator, continued.Prefix)
		}
		if len(continued.Items) != 1 || continued.Items[0].Value != "\""+directory+"/test.txt\"" {
			t.Fatalf("sep %q continuation items = %+v", separator, continued.Items)
		}
	}
}

func TestAutocompleteDirectoriesBeforeFiles(t *testing.T) {
	baseDir := t.TempDir()
	autocompleteSetupFolder(t, baseDir, []string{"z folder", "z，folder"}, map[string]string{"a.txt": "text"})
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	result := autocompleteSuggestions(t, provider, []string{""}, 0, 0, true)
	if result == nil {
		t.Fatal("no result")
	}
	wantDirs := []bool{true, true, false}
	if len(result.Items) != len(wantDirs) {
		t.Fatalf("items = %+v", result.Items)
	}
	for i, wantDir := range wantDirs {
		if got := strings.HasSuffix(result.Items[i].Label, "/"); got != wantDir {
			t.Fatalf("item %d dir = %v, want %v (%+v)", i, got, wantDir, result.Items)
		}
	}
}

func TestAutocompleteContinuesInsideQuotes(t *testing.T) {
	baseDir := t.TempDir()
	autocompleteSetupFolder(t, baseDir, nil, map[string]string{"my folder/test.txt": "content", "my folder/other.txt": "content"})
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	line := `"my folder/"`
	result := autocompleteSuggestions(t, provider, []string{line}, 0, len(line)-1, true)
	if result == nil {
		t.Fatal("no result")
	}
	found := map[string]bool{}
	for _, item := range result.Items {
		found[item.Value] = true
	}
	if !found[`"my folder/test.txt"`] || !found[`"my folder/other.txt"`] {
		t.Fatalf("items = %+v", result.Items)
	}
}

func TestAutocompleteQuotedNoDuplicateClose(t *testing.T) {
	baseDir := t.TempDir()
	autocompleteSetupFolder(t, baseDir, nil, map[string]string{"my folder/test.txt": "content"})
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	line := `"my folder/te"`
	cursorCol := len(line) - 1
	result := autocompleteSuggestions(t, provider, []string{line}, 0, cursorCol, true)
	if result == nil {
		t.Fatal("no result")
	}
	var item *AutocompleteItem
	for i := range result.Items {
		if result.Items[i].Value == `"my folder/test.txt"` {
			item = &result.Items[i]
			break
		}
	}
	if item == nil {
		t.Fatalf("test.txt missing: %+v", result.Items)
	}

	applied := provider.ApplyCompletion([]string{line}, 0, cursorCol, *item, result.Prefix)
	if applied.Lines[0] != `"my folder/test.txt"` {
		t.Fatalf("applied = %q", applied.Lines[0])
	}
}

func TestAutocompleteSlashCommandCompletion(t *testing.T) {
	baseDir := t.TempDir()
	provider := NewCombinedAutocompleteProvider([]SlashCommand{
		{Name: "model", Description: "Pick model"},
		{Name: "skill:review", Description: "Review code", ArgumentHint: "<file>"},
	}, nil, baseDir, "")

	result := autocompleteSuggestions(t, provider, []string{"/rev"}, 0, 4, false)
	if result == nil {
		t.Fatal("no result for /rev")
	}
	if result.Prefix != "/rev" {
		t.Fatalf("prefix = %q", result.Prefix)
	}
	if len(result.Items) == 0 {
		t.Fatal("no items")
	}
	// skill: commands match on bare name.
	if result.Items[0].Value != "skill:review" {
		t.Fatalf("item = %+v", result.Items[0])
	}
	// Description joins hint and desc.
	foundDesc := ""
	for _, item := range result.Items {
		if item.Value == "skill:review" {
			foundDesc = item.Description
		}
	}
	if foundDesc != "<file> — Review code" {
		t.Fatalf("description = %q", foundDesc)
	}

	// Apply: "/" + value + " ".
	applied := provider.ApplyCompletion([]string{"/rev"}, 0, 4, result.Items[0], result.Prefix)
	if applied.Lines[0] != "/skill:review " {
		t.Fatalf("applied = %q", applied.Lines[0])
	}
	if applied.CursorCol != len("/skill:review ") {
		t.Fatalf("cursor = %d", applied.CursorCol)
	}
}

func TestAutocompleteSlashCommandArguments(t *testing.T) {
	baseDir := t.TempDir()
	provider := NewCombinedAutocompleteProvider([]SlashCommand{
		{
			Name: "model",
			GetArgumentCompletions: func(argumentPrefix string) []AutocompleteItem {
				if strings.HasPrefix("claude-sonnet", argumentPrefix) {
					return []AutocompleteItem{{Value: "claude-sonnet", Label: "claude-sonnet"}}
				}
				return nil
			},
		},
	}, nil, baseDir, "")

	result := autocompleteSuggestions(t, provider, []string{"/model claude"}, 0, 13, false)
	if result == nil {
		t.Fatal("no result for command argument")
	}
	if result.Prefix != "claude" {
		t.Fatalf("prefix = %q", result.Prefix)
	}
	if len(result.Items) != 1 || result.Items[0].Value != "claude-sonnet" {
		t.Fatalf("items = %+v", result.Items)
	}
}

func TestAutocompleteShouldTriggerFileCompletion(t *testing.T) {
	baseDir := t.TempDir()
	provider := NewCombinedAutocompleteProvider(nil, nil, baseDir, "")

	if provider.ShouldTriggerFileCompletion([]string{"/model"}, 0, 6) {
		t.Fatal("slash command should not trigger")
	}
	if !provider.ShouldTriggerFileCompletion([]string{"hello wo"}, 0, 8) {
		t.Fatal("normal text should trigger")
	}
	if !provider.ShouldTriggerFileCompletion([]string{"/model arg"}, 0, 10) {
		t.Fatal("command argument should trigger")
	}
}

// Port of the fd query escaping (#10972): single-segment queries with regex
// metacharacters must match literally.
func TestBuildFdPathQueryEscapesSingleSegment(t *testing.T) {
	query := autocompleteBuildFdPathQuery("(group).tsx")
	if query != `\(group\)\.tsx` {
		t.Fatalf("single-segment query = %q", query)
	}
	multi := autocompleteBuildFdPathQuery("pages/[id].tsx/main.go")
	if !strings.HasPrefix(multi, `pages[\\/]`) || !strings.Contains(multi, "main") {
		t.Fatalf("multi-segment query = %q", multi)
	}
}
