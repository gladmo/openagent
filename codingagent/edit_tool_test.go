package codingagent

// Ported from test/edit-tool-legacy-input.test.ts plus edit-diff fuzzy
// matching and diff rendering surfaces.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gladmo/openagent/abort"
)

func TestPrepareEditArgumentsFoldsLegacy(t *testing.T) {
	definition := CreateEditToolDefinition(t.TempDir(), nil)

	// Top-level oldText/newText fold into edits.
	prepared := definition.PrepareArguments(map[string]any{
		"path":    "file.txt",
		"oldText": "before",
		"newText": "after",
	}).(map[string]any)
	want := map[string]any{
		"path":  "file.txt",
		"edits": []any{map[string]any{"oldText": "before", "newText": "after"}},
	}
	if !reflect.DeepEqual(prepared, want) {
		t.Fatalf("prepared = %+v, want %+v", prepared, want)
	}

	// Legacy replacement appends to existing edits.
	prepared = definition.PrepareArguments(map[string]any{
		"path": "file.txt",
		"edits": []any{
			map[string]any{"oldText": "a", "newText": "b"},
		},
		"oldText": "c",
		"newText": "d",
	}).(map[string]any)
	edits := prepared["edits"].([]any)
	if len(edits) != 2 {
		t.Fatalf("edits = %+v", edits)
	}
	last := edits[1].(map[string]any)
	if last["oldText"] != "c" || last["newText"] != "d" {
		t.Fatalf("last = %+v", last)
	}
	if _, has := prepared["oldText"]; has {
		t.Fatal("legacy oldText not removed")
	}
}

func TestPrepareEditArgumentsPassthrough(t *testing.T) {
	definition := CreateEditToolDefinition(t.TempDir(), nil)

	input := map[string]any{
		"path":  "file.txt",
		"edits": []any{map[string]any{"oldText": "a", "newText": "b"}},
	}
	prepared := definition.PrepareArguments(input)
	preparedMap, ok := prepared.(map[string]any)
	if !ok || !reflect.DeepEqual(preparedMap, input) {
		t.Fatalf("valid input should pass through unchanged: %+v", preparedMap)
	}

	if got := definition.PrepareArguments("garbage"); got != any("garbage") {
		t.Fatalf("non-object = %v", got)
	}
	if got := definition.PrepareArguments(nil); got != nil {
		t.Fatalf("nil = %v", got)
	}
}

func TestPrepareEditArgumentsStringified(t *testing.T) {
	definition := CreateEditToolDefinition(t.TempDir(), nil)

	// JSON string parses into an edits array.
	prepared := definition.PrepareArguments(map[string]any{
		"path":  "file.txt",
		"edits": `[{"oldText":"a","newText":"b"}]`,
	}).(map[string]any)
	edits, ok := prepared["edits"].([]any)
	if !ok || len(edits) != 1 {
		t.Fatalf("edits = %+v", prepared["edits"])
	}

	// Invalid JSON leaves the string alone.
	prepared = definition.PrepareArguments(map[string]any{
		"path":  "file.txt",
		"edits": "not json",
	}).(map[string]any)
	if prepared["edits"] != any("not json") {
		t.Fatalf("invalid json edits = %+v", prepared["edits"])
	}

	// Single edit object wraps into an array.
	prepared = definition.PrepareArguments(map[string]any{
		"path":  "file.txt",
		"edits": map[string]any{"oldText": "a", "newText": "b"},
	}).(map[string]any)
	edits, ok = prepared["edits"].([]any)
	if !ok || len(edits) != 1 {
		t.Fatalf("single edit = %+v", prepared["edits"])
	}
}

func TestEditToolLegacyExecute(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "legacy.txt")
	os.WriteFile(filePath, []byte("before\n"), 0o644)

	definition := CreateEditToolDefinition(dir, nil)
	prepared := definition.PrepareArguments(map[string]any{
		"path":    "legacy.txt",
		"oldText": "before",
		"newText": "after",
	})

	result, err := definition.Execute("tool-1", prepared, nil, nil, &ExtensionContext{Cwd: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*EditToolOutput)
	if output.Text != "Successfully replaced 1 block(s) in legacy.txt." {
		t.Fatalf("text = %q", output.Text)
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != "after\n" {
		t.Fatalf("file = %q", data)
	}
}

func TestEditToolBasic(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "a.txt")
	os.WriteFile(filePath, []byte("alpha\nbeta\ngamma\n"), 0o644)

	definition := CreateEditToolDefinition(dir, nil)
	result, err := definition.Execute("t", EditToolInput{
		Path:  "a.txt",
		Edits: []Edit{{OldText: "beta", NewText: "BETA"}},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("file = %q", data)
	}
	output := result.(*EditToolOutput)
	if output.Details == nil || output.Details.Diff == "" || output.Details.Patch == "" {
		t.Fatalf("details = %+v", output.Details)
	}
	if !strings.Contains(output.Details.Patch, "-beta") || !strings.Contains(output.Details.Patch, "+BETA") {
		t.Fatalf("patch = %q", output.Details.Patch)
	}
	if !output.Details.HasFirstChanged || output.Details.FirstChangedLine != 2 {
		t.Fatalf("first changed = %+v", output.Details.FirstChangedLine)
	}
}

func TestEditToolMultipleEdits(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "a.txt")
	os.WriteFile(filePath, []byte("one\ntwo\nthree\nfour\n"), 0o644)

	definition := CreateEditToolDefinition(dir, nil)
	_, err := definition.Execute("t", EditToolInput{
		Path: "a.txt",
		Edits: []Edit{
			{OldText: "one", NewText: "ONE"},
			{OldText: "three", NewText: "THREE"},
		},
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != "ONE\ntwo\nTHREE\nfour\n" {
		t.Fatalf("file = %q", data)
	}
}

func TestEditToolErrors(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "a.txt")
	os.WriteFile(filePath, []byte("dup\ndup\nend\n"), 0o644)
	missing := filepath.Join(dir, "missing.txt")
	os.WriteFile(missing, nil, 0o644)
	os.Remove(missing)

	definition := CreateEditToolDefinition(dir, nil)

	// Not found.
	_, err := definition.Execute("t", EditToolInput{
		Path:  "a.txt",
		Edits: []Edit{{OldText: "nope", NewText: "x"}},
	}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Could not find the exact text in a.txt") {
		t.Fatalf("not found err = %v", err)
	}

	// Duplicate occurrences.
	_, err = definition.Execute("t", EditToolInput{
		Path:  "a.txt",
		Edits: []Edit{{OldText: "dup", NewText: "x"}},
	}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Found 2 occurrences") {
		t.Fatalf("duplicate err = %v", err)
	}

	// Empty oldText.
	_, err = definition.Execute("t", EditToolInput{
		Path:  "a.txt",
		Edits: []Edit{{OldText: "", NewText: "x"}},
	}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "oldText must not be empty") {
		t.Fatalf("empty err = %v", err)
	}

	// Overlapping edits.
	_, err = definition.Execute("t", EditToolInput{
		Path: "a.txt",
		Edits: []Edit{
			{OldText: "dup\ndup", NewText: "x"},
			{OldText: "dup\nend", NewText: "y"},
		},
	}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlap err = %v", err)
	}

	// No change.
	os.WriteFile(filePath, []byte("same\n"), 0o644)
	_, err = definition.Execute("t", EditToolInput{
		Path:  "a.txt",
		Edits: []Edit{{OldText: "same", NewText: "same"}},
	}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "No changes made") {
		t.Fatalf("no change err = %v", err)
	}

	// Empty edits.
	_, err = definition.Execute("t", EditToolInput{Path: "a.txt"}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "at least one replacement") {
		t.Fatalf("empty edits err = %v", err)
	}

	// Missing file.
	_, err = definition.Execute("t", EditToolInput{
		Path:  "missing.txt",
		Edits: []Edit{{OldText: "x", NewText: "y"}},
	}, nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Could not edit file: missing.txt") {
		t.Fatalf("missing err = %v", err)
	}
}

func TestEditToolFuzzyMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "f.txt")

	// Trailing whitespace stays when the old text matches exactly as a
	// substring (exact match wins over fuzzy).
	os.WriteFile(filePath, []byte("line one   \nline two\n"), 0o644)
	definition := CreateEditToolDefinition(dir, nil)
	if _, err := definition.Execute("t", EditToolInput{
		Path:  "f.txt",
		Edits: []Edit{{OldText: "line one", NewText: "first"}},
	}, nil, nil, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != "first   \nline two\n" {
		t.Fatalf("file = %q", data)
	}

	// Smart quotes normalize.
	os.WriteFile(filePath, []byte("say “hello” now\n"), 0o644)
	if _, err := definition.Execute("t", EditToolInput{
		Path:  "f.txt",
		Edits: []Edit{{OldText: `say "hello" now`, NewText: "say 'hi' now"}},
	}, nil, nil, nil); err != nil {
		t.Fatalf("smart quote execute: %v", err)
	}
	data, _ = os.ReadFile(filePath)
	if string(data) != "say 'hi' now\n" {
		t.Fatalf("smart quote file = %q", data)
	}
}

func TestEditToolLineEndingsPreserved(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "crlf.txt")
	os.WriteFile(filePath, []byte("one\r\ntwo\r\nthree\r\n"), 0o644)

	definition := CreateEditToolDefinition(dir, nil)
	if _, err := definition.Execute("t", EditToolInput{
		Path:  "crlf.txt",
		Edits: []Edit{{OldText: "two", NewText: "TWO"}},
	}, nil, nil, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != "one\r\nTWO\r\nthree\r\n" {
		t.Fatalf("crlf file = %q", data)
	}
}

func TestEditToolBomPreserved(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "bom.txt")
	os.WriteFile(filePath, []byte("\uFEFFcontent here\n"), 0o644)

	definition := CreateEditToolDefinition(dir, nil)
	if _, err := definition.Execute("t", EditToolInput{
		Path:  "bom.txt",
		Edits: []Edit{{OldText: "content", NewText: "text"}},
	}, nil, nil, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, _ := os.ReadFile(filePath)
	if string(data) != "\uFEFFtext here\n" {
		t.Fatalf("bom file = %q", data)
	}
}

func TestDetectLineEnding(t *testing.T) {
	if got := DetectLineEnding("a\nb"); got != "\n" {
		t.Fatalf("lf = %q", got)
	}
	if got := DetectLineEnding("a\r\nb"); got != "\r\n" {
		t.Fatalf("crlf = %q", got)
	}
	if got := DetectLineEnding("a\rb"); got != "\n" {
		t.Fatalf("cr only = %q", got)
	}
	if got := DetectLineEnding("no newlines"); got != "\n" {
		t.Fatalf("none = %q", got)
	}
}

func TestNormalizeForFuzzyMatch(t *testing.T) {
	// Split/join preserves the empty final element, so the trailing
	// newline survives.
	if got := NormalizeForFuzzyMatch("line  \nnext\t\n"); got != "line\nnext\n" {
		t.Fatalf("trailing ws = %q", got)
	}
	if got := NormalizeForFuzzyMatch("“quoted”"); got != `"quoted"` {
		t.Fatalf("quotes = %q", got)
	}
	if got := NormalizeForFuzzyMatch("it’s"); got != "it's" {
		t.Fatalf("apostrophe = %q", got)
	}
	if got := NormalizeForFuzzyMatch("a—b–c"); got != "a-b-c" {
		t.Fatalf("dashes = %q", got)
	}
	if got := NormalizeForFuzzyMatch("a\u3000b"); got != "a b" {
		t.Fatalf("ideographic space = %q", got)
	}
}

func TestFuzzyFindText(t *testing.T) {
	content := "exact here\nother line\n"
	result := FuzzyFindText(content, "other line")
	if !result.Found || result.UsedFuzzyMatch || result.Index != len("exact here\n") {
		t.Fatalf("exact = %+v", result)
	}
	if result.ContentForReplacement != content {
		t.Fatal("exact should use original content")
	}

	// Fuzzy fallback fires only when the exact substring is absent.
	fuzzyContent := "say “hi” now\n"
	result = FuzzyFindText(fuzzyContent, `say "hi" now`)
	if !result.Found || !result.UsedFuzzyMatch {
		t.Fatalf("fuzzy = %+v", result)
	}
	if result.ContentForReplacement != "say \"hi\" now\n" {
		t.Fatalf("replacement content = %q", result.ContentForReplacement)
	}

	// Not found.
	result = FuzzyFindText("abc", "xyz")
	if result.Found {
		t.Fatalf("missing = %+v", result)
	}
}

func TestApplyEditsPreservesUnchangedLines(t *testing.T) {
	// Fuzzy path: the touched line is rewritten from normalized content
	// while untouched lines keep their original bytes.
	content := "keep me   \nsay “hi” now\nkeep too  \n"
	normalized := NormalizeToLF(content)
	applied, err := ApplyEditsToNormalizedContent(normalized, []Edit{
		{OldText: `say "hi" now`, NewText: "greetings"},
	}, "f.txt")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if applied.NewContent != "keep me   \ngreetings\nkeep too  \n" {
		t.Fatalf("preserved = %q", applied.NewContent)
	}
}

func TestGenerateUnifiedPatch(t *testing.T) {
	patch := GenerateUnifiedPatch("a.txt", "one\ntwo\n", "one\nTWO\n", 4)
	if !strings.Contains(patch, "--- a.txt") || !strings.Contains(patch, "+++ a.txt") {
		t.Fatalf("headers = %q", patch)
	}
	if !strings.Contains(patch, "-two") || !strings.Contains(patch, "+TWO") {
		t.Fatalf("body = %q", patch)
	}
	if strings.Contains(patch, "@@") && strings.Contains(patch, "one") && !strings.Contains(patch, "-one") {
		// Context lines included.
		t.Log("context present")
	}
}

func TestGenerateDiffString(t *testing.T) {
	old := ""
	for i := 1; i <= 10; i++ {
		old += strings.Repeat("x", 20) + "\n"
	}
	new_ := strings.Replace(old, "x", "y", 1)
	result := GenerateDiffString(old, new_, 2)

	lines := strings.Split(result.Diff, "\n")
	if len(lines) < 2 {
		t.Fatalf("diff = %q", result.Diff)
	}
	hasRemoved, hasAdded := false, false
	for _, line := range lines {
		if strings.HasPrefix(line, "-") {
			hasRemoved = true
		}
		if strings.HasPrefix(line, "+") {
			hasAdded = true
		}
	}
	if !hasRemoved || !hasAdded {
		t.Fatalf("diff lacks -/+ lines: %q", result.Diff)
	}
	if !result.HasFirstChanged || result.FirstChangedLine != 1 {
		t.Fatalf("first changed = %d", result.FirstChangedLine)
	}

	// Elision markers appear for long context runs.
	long := strings.Repeat("ctx\n", 50)
	oldLong := "head\n" + long + "tail\n"
	newLong := "HEAD\n" + long + "tail\n"
	result = GenerateDiffString(oldLong, newLong, 2)
	if !strings.Contains(result.Diff, "...") {
		t.Fatalf("elision missing: %q", result.Diff)
	}
}

func TestComputeEditDiffPreview(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "p.txt")
	os.WriteFile(filePath, []byte("alpha\nbeta\n"), 0o644)

	result, errMsg := ComputeEditDiff("p.txt", "beta", "BETA", dir)
	if errMsg != "" {
		t.Fatalf("error = %q", errMsg)
	}
	if result == nil || !strings.Contains(result.Diff, "+") {
		t.Fatalf("result = %+v", result)
	}

	// Missing file reports an error string.
	_, errMsg = ComputeEditDiff("missing.txt", "a", "b", dir)
	if errMsg == "" || !strings.Contains(errMsg, "Could not edit file: missing.txt") {
		t.Fatalf("missing = %q", errMsg)
	}

	// File unchanged by the preview.
	data, _ := os.ReadFile(filePath)
	if string(data) != "alpha\nbeta\n" {
		t.Fatalf("preview mutated file: %q", data)
	}
}

func TestEditToolCustomOperations(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("aaa\n"), 0o644)
	ops := &recordingEditOps{}
	definition := CreateEditToolDefinition(dir, &EditToolOptions{Operations: ops})

	if _, err := definition.Execute("t", EditToolInput{
		Path:  "x.txt",
		Edits: []Edit{{OldText: "aaa", NewText: "bbb"}},
	}, nil, nil, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !ops.readCalled || !ops.writeCalled || !ops.accessCalled {
		t.Fatalf("ops = %+v", ops)
	}
}

type recordingEditOps struct {
	readCalled   bool
	writeCalled  bool
	accessCalled bool
}

func (o *recordingEditOps) ReadFile(path string) ([]byte, error) {
	o.readCalled = true
	return os.ReadFile(path)
}

func (o *recordingEditOps) WriteFile(path, content string) error {
	o.writeCalled = true
	return os.WriteFile(path, []byte(content), 0o644)
}

func (o *recordingEditOps) Access(path string) error {
	o.accessCalled = true
	_, err := os.Stat(path)
	return err
}

func TestEditToolAbort(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("aaa\n"), 0o644)

	definition := CreateEditToolDefinition(dir, nil)
	controller := abort.NewController()
	controller.Abort()
	_, err := definition.Execute("t", EditToolInput{
		Path:  "a.txt",
		Edits: []Edit{{OldText: "aaa", NewText: "bbb"}},
	}, controller.Signal(), nil, nil)
	if err == nil || err.Error() != "Operation aborted" {
		t.Fatalf("err = %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(data) != "aaa\n" {
		t.Fatalf("aborted edit mutated file: %q", data)
	}
}
