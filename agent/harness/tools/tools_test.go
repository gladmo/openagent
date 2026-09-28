package tools

// Ports of tools tests: image sniffing, base64, path-utils, edit-diff,
// read/write tools via a fake env.

import (
	"os"

	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"

	"github.com/gladmo/openagent/agent/harness"
	"github.com/gladmo/openagent/jsonx"
)

func toolTestEnv(t *testing.T) *fakeToolsEnv {
	t.Helper()
	return &fakeToolsEnv{root: t.TempDir()}
}

// fakeToolsEnv reuses the harness test fake via embedding semantics (the
// harness test fake is in the harness package's test files, so this port
// carries a minimal copy).
type fakeToolsEnv struct{ root string }

func (e *fakeToolsEnv) abs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(e.root, p)
}

func (e *fakeToolsEnv) Cwd() string { return e.root }
func (e *fakeToolsEnv) AbsolutePath(path string, _ harness.Context) harness.Result[string, *harness.FileError] {
	return harness.Ok[string, *harness.FileError](e.abs(path))
}
func (e *fakeToolsEnv) JoinPath(parts []string, _ harness.Context) harness.Result[string, *harness.FileError] {
	return harness.Ok[string, *harness.FileError](filepath.Join(parts...))
}
func (e *fakeToolsEnv) ReadTextFile(path string, _ harness.Context) harness.Result[string, *harness.FileError] {
	data, err := os.ReadFile(e.abs(path))
	if err != nil {
		return harness.Err[string, *harness.FileError](harness.NewFileError(harness.FileErrNotFound, err.Error(), path))
	}
	return harness.Ok[string, *harness.FileError](string(data))
}
func (e *fakeToolsEnv) OpenTextLineReader(string, harness.Context) harness.Result[harness.TextLineReader, *harness.FileError] {
	return harness.Err[harness.TextLineReader, *harness.FileError](harness.NewFileError(harness.FileErrNotSupported, "unsupported", ""))
}
func (e *fakeToolsEnv) ReadTextLines(string, *harness.ReadTextLinesOptions, harness.Context) harness.Result[[]string, *harness.FileError] {
	return harness.Ok[[]string, *harness.FileError](nil)
}
func (e *fakeToolsEnv) ReadBinaryFile(path string, _ harness.Context) harness.Result[[]byte, *harness.FileError] {
	data, err := os.ReadFile(e.abs(path))
	if err != nil {
		return harness.Err[[]byte, *harness.FileError](harness.NewFileError(harness.FileErrNotFound, err.Error(), path))
	}
	return harness.Ok[[]byte, *harness.FileError](data)
}
func (e *fakeToolsEnv) WriteFile(path string, content []byte, _ harness.Context) harness.Result[struct{}, *harness.FileError] {
	if err := os.MkdirAll(filepath.Dir(e.abs(path)), 0o755); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	if err := os.WriteFile(e.abs(path), content, 0o644); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}
func (e *fakeToolsEnv) AppendFile(path string, content []byte, _ harness.Context) harness.Result[struct{}, *harness.FileError] {
	f, err := os.OpenFile(e.abs(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}
func (e *fakeToolsEnv) RenameFile(source, destination string, _ harness.Context) harness.Result[struct{}, *harness.FileError] {
	if err := os.Rename(e.abs(source), e.abs(destination)); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), source))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}
func (e *fakeToolsEnv) FileInfo(path string, _ harness.Context) harness.Result[harness.FileInfo, *harness.FileError] {
	info, err := os.Lstat(e.abs(path))
	if err != nil {
		return harness.Err[harness.FileInfo, *harness.FileError](harness.NewFileError(harness.FileErrNotFound, err.Error(), path))
	}
	kind := harness.FileKindFile
	if info.IsDir() {
		kind = harness.FileKindDirectory
	} else if info.Mode()&os.ModeSymlink != 0 {
		kind = harness.FileKindSymlink
	}
	return harness.Ok[harness.FileInfo, *harness.FileError](harness.FileInfo{
		Name: filepath.Base(path), Path: e.abs(path), Kind: kind, Size: info.Size(), MtimeMs: float64(info.ModTime().UnixMilli()),
	})
}
func (e *fakeToolsEnv) ListDir(path string, _ harness.Context) harness.Result[[]harness.FileInfo, *harness.FileError] {
	entries, err := os.ReadDir(e.abs(path))
	if err != nil {
		return harness.Err[[]harness.FileInfo, *harness.FileError](harness.NewFileError(harness.FileErrNotFound, err.Error(), path))
	}
	out := []harness.FileInfo{}
	for _, entry := range entries {
		kind := harness.FileKindFile
		if entry.IsDir() {
			kind = harness.FileKindDirectory
		}
		out = append(out, harness.FileInfo{Name: entry.Name(), Path: filepath.Join(e.abs(path), entry.Name()), Kind: kind})
	}
	return harness.Ok[[]harness.FileInfo, *harness.FileError](out)
}
func (e *fakeToolsEnv) CanonicalPath(path string, _ harness.Context) harness.Result[string, *harness.FileError] {
	resolved, err := filepath.EvalSymlinks(e.abs(path))
	if err != nil {
		return harness.Err[string, *harness.FileError](harness.NewFileError(harness.FileErrNotFound, err.Error(), path))
	}
	return harness.Ok[string, *harness.FileError](resolved)
}
func (e *fakeToolsEnv) Exists(path string, _ harness.Context) harness.Result[bool, *harness.FileError] {
	_, err := os.Lstat(e.abs(path))
	return harness.Ok[bool, *harness.FileError](err == nil)
}
func (e *fakeToolsEnv) CreateDir(path string, _ *harness.CreateDirOptions, _ harness.Context) harness.Result[struct{}, *harness.FileError] {
	if err := os.MkdirAll(e.abs(path), 0o755); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}
func (e *fakeToolsEnv) Remove(path string, options *harness.RemoveOptions, _ harness.Context) harness.Result[struct{}, *harness.FileError] {
	if options != nil && options.Recursive != nil && *options.Recursive {
		if err := os.RemoveAll(e.abs(path)); err != nil {
			return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
		}
		return harness.Ok[struct{}, *harness.FileError](struct{}{})
	}
	if err := os.Remove(e.abs(path)); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}
func (e *fakeToolsEnv) CreateTempDir(prefix string, _ harness.Context) harness.Result[string, *harness.FileError] {
	dir, err := os.MkdirTemp(e.root, prefix)
	if err != nil {
		return harness.Err[string, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), ""))
	}
	return harness.Ok[string, *harness.FileError](dir)
}
func (e *fakeToolsEnv) CreateTempFile(*harness.CreateTempFileOptions, harness.Context) harness.Result[string, *harness.FileError] {
	f, err := os.CreateTemp(e.root, "tmp-*")
	if err != nil {
		return harness.Err[string, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), ""))
	}
	defer f.Close()
	return harness.Ok[string, *harness.FileError](f.Name())
}
func (e *fakeToolsEnv) Cleanup(harness.Context) error { return nil }
func (e *fakeToolsEnv) Exec(string, *harness.ShellExecOptions, harness.Context) harness.Result[harness.ShellExecResult, *harness.ExecutionError] {
	return harness.Err[harness.ShellExecResult, *harness.ExecutionError](harness.NewExecutionError("not_supported", "unsupported"))
}
func (e *fakeToolsEnv) CleanupShell(harness.Context) error { return nil }

func TestDetectImageMimeTypes(t *testing.T) {
	png := append([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a},
		0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1)
	if got := DetectSupportedImageMimeType(png); got != "image/png" {
		t.Fatalf("png = %q", got)
	}
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0}
	if got := DetectSupportedImageMimeType(jpeg); got != "image/jpeg" {
		t.Fatalf("jpeg = %q", got)
	}
	jpegLs := []byte{0xff, 0xd8, 0xff, 0xf7}
	if got := DetectSupportedImageMimeType(jpegLs); got != "" {
		t.Fatalf("jpeg-ls = %q", got)
	}
	gif := []byte("GIF89a......")
	if got := DetectSupportedImageMimeType(gif); got != "image/gif" {
		t.Fatalf("gif = %q", got)
	}
	webp := []byte("RIFF____WEBPVP8 ")
	if got := DetectSupportedImageMimeType(webp); got != "image/webp" {
		t.Fatalf("webp = %q", got)
	}
	unknown := []byte("plain text here")
	if got := DetectSupportedImageMimeType(unknown); got != "" {
		t.Fatalf("unknown = %q", got)
	}
}

func TestEncodeBase64(t *testing.T) {
	if got := EncodeBase64([]byte("hello world")); got != "aGVsbG8gd29ybGQ=" {
		t.Fatalf("got %s", got)
	}
	if got := EncodeBase64([]byte{}); got != "" {
		t.Fatalf("empty = %s", got)
	}
	if got := EncodeBase64([]byte("a")); got != "YQ==" {
		t.Fatalf("a = %s", got)
	}
}

func TestNormalizeToolPath(t *testing.T) {
	if got := NormalizeToolPath("@src/main.go"); got != "src/main.go" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeToolPath("a\u00A0b"); got != "a b" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeToolPath("plain/path"); got != "plain/path" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveReadToolPathVariants(t *testing.T) {
	env := toolTestEnv(t)
	// File saved with a narrow no-break space before PM.
	name := "log 2 PM. txt"
	if err := os.WriteFile(filepath.Join(env.root, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveReadToolPath(env, "log 2 PM. txt", harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(resolved) != name {
		t.Fatalf("resolved = %q", resolved)
	}
	// Missing file falls back to the exact resolution.
	missing, _ := ResolveReadToolPath(env, "no such file", harness.BackgroundContext)
	if filepath.Base(missing) != "no such file" {
		t.Fatalf("missing = %q", missing)
	}
}

func TestEditDiffLineEndingDetection(t *testing.T) {
	if got := DetectLineEnding("a\r\nb\r\n"); got != "\r\n" {
		t.Fatalf("got %q", got)
	}
	if got := DetectLineEnding("a\nb\n"); got != "\n" {
		t.Fatalf("got %q", got)
	}
	if got := DetectLineEnding("a\nb\r\nc"); got != "\n" {
		t.Fatalf("got %q", got)
	}
}

func TestFuzzyFindText(t *testing.T) {
	content := "line one ‘quoted’\nline two — dash\nline three  "
	// Identical text matches exactly (no fuzzy).
	if match := FuzzyFindText(content, "line one ‘quoted’"); !match.Found || match.UsedFuzzyMatch {
		t.Fatalf("exact match = %+v", match)
	}
	// ASCII query against curly content goes fuzzy.
	if match := FuzzyFindText(content, "line one 'quoted'"); !match.Found || !match.UsedFuzzyMatch {
		t.Fatalf("fuzzy match = %+v", match)
	}
	_ = content
	normalized := NormalizeForFuzzyMatch(content)
	if !strings.Contains(normalized, "line one 'quoted'") {
		t.Fatalf("normalized = %q", normalized)
	}
	if !strings.Contains(normalized, "line two - dash") {
		t.Fatalf("normalized = %q", normalized)
	}
	if !strings.HasSuffix(normalized, "line three") {
		t.Fatalf("trailing whitespace not stripped: %q", normalized)
	}
}

func TestApplyEditsExact(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	base, next, err := ApplyEditsToNormalizedContent(content, []Edit{{OldText: "beta", NewText: "BETA"}}, "file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if base != content {
		t.Fatalf("base = %q", base)
	}
	if next != "alpha\nBETA\ngamma\n" {
		t.Fatalf("next = %q", next)
	}
}

func TestApplyEditsErrors(t *testing.T) {
	content := "aaa aaa\n"
	if _, _, err := ApplyEditsToNormalizedContent(content, []Edit{{OldText: "aaa", NewText: "x"}}, "f"); err == nil || !strings.Contains(err.Error(), "2 occurrences") {
		t.Fatalf("duplicate err = %v", err)
	}
	if _, _, err := ApplyEditsToNormalizedContent(content, []Edit{{OldText: "", NewText: "x"}}, "f"); err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("empty err = %v", err)
	}
	if _, _, err := ApplyEditsToNormalizedContent(content, []Edit{{OldText: "zzz", NewText: "x"}}, "f"); err == nil || !strings.Contains(err.Error(), "Could not find") {
		t.Fatalf("missing err = %v", err)
	}
	if _, _, err := ApplyEditsToNormalizedContent("unique text\n", []Edit{{OldText: "unique text", NewText: "unique text"}}, "f"); err == nil || !strings.Contains(err.Error(), "No changes") {
		t.Fatalf("no-change err = %v", err)
	}
	// Overlap.
	two := "one two three four five\n"
	if _, _, err := ApplyEditsToNormalizedContent(two, []Edit{
		{OldText: "two three", NewText: "X"},
		{OldText: "three four", NewText: "Y"},
	}, "f"); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlap err = %v", err)
	}
}

func TestApplyEditsFuzzyPreservesLines(t *testing.T) {
	// The oldText uses a curly apostrophe and trailing space; the content
	// has ASCII and no trailing space. Fuzzy path must edit only the
	// matched line, preserving other lines byte-for-byte.
	content := "first line\nit’s fine here  \nlast line\n"
	base, next, err := ApplyEditsToNormalizedContent(content, []Edit{{OldText: "it's fine here", NewText: "IT IS FINE"}}, "file")
	if err != nil {
		t.Fatal(err)
	}
	if base != content {
		t.Fatalf("base = %q", base)
	}
	if !strings.HasPrefix(next, "first line\n") || !strings.HasSuffix(next, "\nlast line\n") {
		t.Fatalf("next = %q", next)
	}
	if !strings.Contains(next, "IT IS FINE") {
		t.Fatalf("next = %q", next)
	}
}

func TestGenerateUnifiedPatchFromEdit(t *testing.T) {
	patch := GenerateUnifiedPatch("f.txt", "a\nb\nc\n", "a\nX\nc\n", 4)
	if !strings.HasPrefix(patch, "--- f.txt\n+++ f.txt\n@@ -1,3 +1,3 @@\n a\n-b\n+X\n c\n") {
		t.Fatalf("patch = %q", patch)
	}
}

func TestGenerateDiffStringNumbered(t *testing.T) {
	diffText, firstChanged := GenerateDiffString("one\ntwo\nthree\n", "one\nTWO\nthree\n", 4)
	if firstChanged != 2 {
		t.Fatalf("firstChanged = %d", firstChanged)
	}
	lines := strings.Split(diffText, "\n")
	if len(lines) != 4 {
		t.Fatalf("diff = %q", diffText)
	}
	if lines[0] != " 1 one" || lines[1] != "-2 two" || lines[2] != "+2 TWO" || lines[3] != " 3 three" {
		t.Fatalf("diff lines = %v", lines)
	}
}

func TestWriteToolCreatesParentsAndWrites(t *testing.T) {
	env := toolTestEnv(t)
	tool := CreateWriteTool()
	result, err := tool.Execute("c1", jsonx.ObjFrom("path", "nested/dir/file.txt", "content", "hello"), nil,
		ExecutionToolContext{Env: env}, nil, harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if text := toolText(result); text != "Successfully wrote to nested/dir/file.txt" {
		t.Fatalf("text = %q", text)
	}
	data, err := os.ReadFile(filepath.Join(env.root, "nested/dir/file.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("file = %q err = %v", data, err)
	}
}

func TestReadToolTextWithPagination(t *testing.T) {
	env := toolTestEnv(t)
	var content strings.Builder
	for i := 1; i <= 10; i++ {
		content.WriteString("line-")
		content.WriteString(jsonx.FormatNumber(float64(i)))
		content.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(env.root, "ten.txt"), []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := CreateReadTool()

	// Full read: no truncation footer.
	result, err := tool.Execute("c1", jsonx.ObjFrom("path", "ten.txt"), nil, ExecutionToolContext{Env: env}, nil, harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if text := toolText(result); !strings.Contains(text, "line-10") || strings.Contains(text, "[Showing lines") {
		t.Fatalf("text = %q", text)
	}

	// Limited read: "more lines" footer.
	result, err = tool.Execute("c2", jsonx.ObjFrom("path", "ten.txt", "limit", float64(3)), nil, ExecutionToolContext{Env: env}, nil, harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if text := toolText(result); !strings.Contains(text, "[8 more lines in file. Use offset=4 to continue.]") {
		t.Fatalf("text = %q", text)
	}

	// Offset beyond end errors.
	if _, err := tool.Execute("c3", jsonx.ObjFrom("path", "ten.txt", "offset", float64(99)), nil, ExecutionToolContext{Env: env}, nil, harness.BackgroundContext); err == nil || !strings.Contains(err.Error(), "beyond end of file") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadToolImagePassthrough(t *testing.T) {
	env := toolTestEnv(t)
	png := append([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a},
		0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1)
	if err := os.WriteFile(filepath.Join(env.root, "img.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := CreateReadTool()
	result, err := tool.Execute("c1", jsonx.ObjFrom("path", "img.png"), nil, ExecutionToolContext{Env: env}, nil, harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Content) != 2 {
		t.Fatalf("content = %+v", result.Content)
	}
	if text := toolText(result); text != "Read image file [image/png]" {
		t.Fatalf("text = %q", text)
	}
}

func TestEditToolEndToEnd(t *testing.T) {
	env := toolTestEnv(t)
	if err := os.WriteFile(filepath.Join(env.root, "code.go"), []byte("package main\n\nfunc old() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := CreateEditTool()
	result, err := tool.Execute("c1", jsonx.ObjFrom("path", "code.go",
		"edits", []any{jsonx.ObjFrom("oldText", "func old() {}", "newText", "func new() {}")}),
		nil, ExecutionToolContext{Env: env}, nil, harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if text := toolText(result); text != "Successfully replaced 1 block(s) in code.go." {
		t.Fatalf("text = %q", text)
	}
	data, _ := os.ReadFile(filepath.Join(env.root, "code.go"))
	if string(data) != "package main\n\nfunc new() {}\n" {
		t.Fatalf("file = %q", data)
	}
	details := result.Details.(EditToolDetails)
	if !strings.Contains(details.Diff, "-3 func old() {}") || !strings.Contains(details.Diff, "+3 func new() {}") {
		t.Fatalf("diff = %q", details.Diff)
	}
	if !strings.HasPrefix(details.Patch, "--- code.go\n+++ code.go\n") {
		t.Fatalf("patch = %q", details.Patch)
	}
	if details.FirstChangedLine == nil || *details.FirstChangedLine != 3 {
		t.Fatalf("firstChangedLine = %v", details.FirstChangedLine)
	}

	// Legacy prepareArguments: top-level oldText/newText merged into edits.
	prepared := PrepareEditArguments(jsonx.ObjFrom("path", "code.go", "oldText", "func new() {}", "newText", "func newer() {}"))
	if preparedObj, ok := prepared.(*jsonx.Obj); ok {
		if edits, ok := preparedObj.MustGet("edits").([]any); !ok || len(edits) != 1 {
			t.Fatalf("prepared edits = %v", preparedObj.MustGet("edits"))
		}
		if _, has := preparedObj.Get("oldText"); has {
			t.Fatal("oldText not stripped")
		}
	}
}

func toolText(result *agent.AgentToolResult) string {
	return ai.ContentText(ai.BlocksContent(result.Content...), "\n")
}
