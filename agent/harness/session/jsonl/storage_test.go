package jsonl

// Ports of jsonl-storage / jsonl-io test behaviors over a fake FileSystem.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

type fakeFS struct{ root string }

func newFakeFS(t *testing.T) *fakeFS {
	t.Helper()
	return &fakeFS{root: t.TempDir()}
}

func (f *fakeFS) abs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(f.root, p)
}
func (f *fakeFS) Cwd() string { return f.root }
func (f *fakeFS) AbsolutePath(path string, _ harness.Context) harness.Result[string, *harness.FileError] {
	return harness.Ok[string, *harness.FileError](f.abs(path))
}
func (f *fakeFS) JoinPath(parts []string, _ harness.Context) harness.Result[string, *harness.FileError] {
	return harness.Ok[string, *harness.FileError](filepath.Join(parts...))
}
func (f *fakeFS) ReadTextFile(path string, _ harness.Context) harness.Result[string, *harness.FileError] {
	data, err := os.ReadFile(f.abs(path))
	if err != nil {
		return harness.Err[string, *harness.FileError](harness.NewFileError(harness.FileErrNotFound, err.Error(), path))
	}
	return harness.Ok[string, *harness.FileError](string(data))
}
func (f *fakeFS) OpenTextLineReader(path string, ctx harness.Context) harness.Result[harness.TextLineReader, *harness.FileError] {
	content := f.ReadTextFile(path, ctx)
	if !content.Ok {
		return harness.Err[harness.TextLineReader, *harness.FileError](content.Error)
	}
	lines := strings.Split(content.Value, "\n")
	index := 0
	return harness.Ok[harness.TextLineReader, *harness.FileError](&sliceLineReader{lines: lines, index: &index})
}
func (f *fakeFS) ReadTextLines(string, *harness.ReadTextLinesOptions, harness.Context) harness.Result[[]string, *harness.FileError] {
	return harness.Ok[[]string, *harness.FileError](nil)
}
func (f *fakeFS) ReadBinaryFile(path string, _ harness.Context) harness.Result[[]byte, *harness.FileError] {
	data, err := os.ReadFile(f.abs(path))
	if err != nil {
		return harness.Err[[]byte, *harness.FileError](harness.NewFileError(harness.FileErrNotFound, err.Error(), path))
	}
	return harness.Ok[[]byte, *harness.FileError](data)
}
func (f *fakeFS) WriteFile(path string, content []byte, _ harness.Context) harness.Result[struct{}, *harness.FileError] {
	if err := os.MkdirAll(filepath.Dir(f.abs(path)), 0o755); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	if err := os.WriteFile(f.abs(path), content, 0o644); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}
func (f *fakeFS) AppendFile(path string, content []byte, _ harness.Context) harness.Result[struct{}, *harness.FileError] {
	if err := os.MkdirAll(filepath.Dir(f.abs(path)), 0o755); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	fh, err := os.OpenFile(f.abs(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	defer fh.Close()
	if _, err := fh.Write(content); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}
func (f *fakeFS) RenameFile(source, destination string, _ harness.Context) harness.Result[struct{}, *harness.FileError] {
	if err := os.MkdirAll(filepath.Dir(f.abs(destination)), 0o755); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), source))
	}
	if err := os.Rename(f.abs(source), f.abs(destination)); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), source))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}
func (f *fakeFS) FileInfo(path string, _ harness.Context) harness.Result[harness.FileInfo, *harness.FileError] {
	info, err := os.Lstat(f.abs(path))
	if err != nil {
		return harness.Err[harness.FileInfo, *harness.FileError](harness.NewFileError(harness.FileErrNotFound, err.Error(), path))
	}
	kind := harness.FileKindFile
	if info.IsDir() {
		kind = harness.FileKindDirectory
	}
	return harness.Ok[harness.FileInfo, *harness.FileError](harness.FileInfo{Name: filepath.Base(path), Path: f.abs(path), Kind: kind})
}
func (f *fakeFS) ListDir(path string, _ harness.Context) harness.Result[[]harness.FileInfo, *harness.FileError] {
	entries, err := os.ReadDir(f.abs(path))
	if err != nil {
		return harness.Err[[]harness.FileInfo, *harness.FileError](harness.NewFileError(harness.FileErrNotFound, err.Error(), path))
	}
	out := []harness.FileInfo{}
	for _, entry := range entries {
		kind := harness.FileKindFile
		if entry.IsDir() {
			kind = harness.FileKindDirectory
		}
		out = append(out, harness.FileInfo{Name: entry.Name(), Path: filepath.Join(f.abs(path), entry.Name()), Kind: kind})
	}
	return harness.Ok[[]harness.FileInfo, *harness.FileError](out)
}
func (f *fakeFS) CanonicalPath(path string, _ harness.Context) harness.Result[string, *harness.FileError] {
	resolved, err := filepath.EvalSymlinks(f.abs(path))
	if err != nil {
		return harness.Err[string, *harness.FileError](harness.NewFileError(harness.FileErrNotFound, err.Error(), path))
	}
	return harness.Ok[string, *harness.FileError](resolved)
}
func (f *fakeFS) Exists(path string, _ harness.Context) harness.Result[bool, *harness.FileError] {
	_, err := os.Lstat(f.abs(path))
	return harness.Ok[bool, *harness.FileError](err == nil)
}
func (f *fakeFS) CreateDir(path string, _ *harness.CreateDirOptions, _ harness.Context) harness.Result[struct{}, *harness.FileError] {
	if err := os.MkdirAll(f.abs(path), 0o755); err != nil {
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}
func (f *fakeFS) Remove(path string, options *harness.RemoveOptions, _ harness.Context) harness.Result[struct{}, *harness.FileError] {
	force := options != nil && options.Force != nil && *options.Force
	if err := os.RemoveAll(f.abs(path)); err != nil {
		if os.IsNotExist(err) && force {
			return harness.Ok[struct{}, *harness.FileError](struct{}{})
		}
		return harness.Err[struct{}, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), path))
	}
	return harness.Ok[struct{}, *harness.FileError](struct{}{})
}
func (f *fakeFS) CreateTempDir(string, harness.Context) harness.Result[string, *harness.FileError] {
	dir, err := os.MkdirTemp("", "tmp-")
	if err != nil {
		return harness.Err[string, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), ""))
	}
	return harness.Ok[string, *harness.FileError](dir)
}
func (f *fakeFS) CreateTempFile(*harness.CreateTempFileOptions, harness.Context) harness.Result[string, *harness.FileError] {
	fh, err := os.CreateTemp("", "tmp-*")
	if err != nil {
		return harness.Err[string, *harness.FileError](harness.NewFileError(harness.FileErrUnknown, err.Error(), ""))
	}
	fh.Close()
	return harness.Ok[string, *harness.FileError](fh.Name())
}
func (f *fakeFS) Cleanup(harness.Context) error { return nil }
func (f *fakeFS) Exec(string, *harness.ShellExecOptions, harness.Context) harness.Result[harness.ShellExecResult, *harness.ExecutionError] {
	return harness.Err[harness.ShellExecResult, *harness.ExecutionError](harness.NewExecutionError("not_supported", "unsupported"))
}
func (f *fakeFS) CleanupShell(harness.Context) error { return nil }

type sliceLineReader struct {
	lines []string
	index *int
}

func (r *sliceLineReader) ReadLine(harness.Context) (harness.Result[*harness.TextLine, *harness.FileError], error) {
	if *r.index >= len(r.lines) {
		return harness.Ok[*harness.TextLine, *harness.FileError](nil), nil
	}
	line := &harness.TextLine{Text: r.lines[*r.index], Terminated: true}
	*r.index++
	return harness.Ok[*harness.TextLine, *harness.FileError](line), nil
}
func (r *sliceLineReader) Close(harness.Context) error { return nil }

func TestJsonlStorageCreateAndReopen(t *testing.T) {
	fs := newFakeFS(t)
	header := StorageHeader{V: 4, Kind: "header", ID: "s1", StorageVersion: 1, CreatedAt: 1000, Cwd: fs.root}
	storage, err := CreateJsonlStorage(fs, "session.jsonl", header, nil, harness.BackgroundContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Commit one value write.
	write := session.WriteFromValue(session.SetValue(session.SessionName, "hello"))
	if _, err := storage.Commit([]session.Write{write}, harness.BackgroundContext); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(harness.BackgroundContext); err != nil {
		t.Fatal(err)
	}

	// Reopen: header + transaction replay.
	reopened, err := OpenJsonlStorage(fs, "session.jsonl", harness.BackgroundContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(harness.BackgroundContext)
	name, err := reopened.GetValue(session.SessionName, harness.BackgroundContext)
	if err != nil || name == nil || name.Value != "hello" {
		t.Fatalf("name = %+v err = %v", name, err)
	}
	if reopened.IsLegacyV3() {
		t.Fatal("v4 reopened as legacy")
	}
}

func TestJsonlStorageTornTailRewritten(t *testing.T) {
	fs := newFakeFS(t)
	headerLine := `{"v":4,"kind":"header","id":"s1","storageVersion":1,"createdAt":0,"cwd":""}`
	valueLine := `{"kind":"value","op":"set","seq":1,"namespace":"pi.session.name","key":"","value":"kept"}`
	// Write a file whose last line is torn (no trailing newline).
	torn := headerLine + "\n" + valueLine + "\n" + `{"kind":"value","op":"set","seq":2,"namespace":"torn","key":"x","value":"par`
	if w := fs.WriteFile("torn.jsonl", []byte(torn), harness.BackgroundContext); !w.Ok {
		t.Fatal(w.Error)
	}
	storage, err := OpenJsonlStorage(fs, "torn.jsonl", harness.BackgroundContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close(harness.BackgroundContext)
	// The complete prefix survived; the torn write is gone.
	stored, _ := storage.GetValue(session.NewValue("pi.session.name"), harness.BackgroundContext)
	if stored == nil || stored.Value != "kept" {
		t.Fatalf("stored = %+v", stored)
	}
	tornValue, _ := storage.GetValue(session.NewValue("torn", "x"), harness.BackgroundContext)
	if tornValue != nil {
		t.Fatalf("torn write replayed: %+v", tornValue)
	}
	// The file was rewritten atomically: torn bytes gone, content ends with newline.
	content, _ := os.ReadFile(filepath.Join(fs.root, "torn.jsonl"))
	if !strings.HasSuffix(string(content), "\n") || strings.Contains(string(content), `"torn"`) {
		t.Fatalf("content = %q", content)
	}
}

func TestJsonlStorageEmptyCommitWritesNothing(t *testing.T) {
	fs := newFakeFS(t)
	header := StorageHeader{V: 4, Kind: "header", ID: "s", StorageVersion: 1, Cwd: ""}
	storage, _ := CreateJsonlStorage(fs, "s.jsonl", header, nil, harness.BackgroundContext, nil)
	before, _ := os.ReadFile(filepath.Join(fs.root, "s.jsonl"))
	if _, err := storage.Commit(nil, harness.BackgroundContext); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(fs.root, "s.jsonl"))
	if len(before) != len(after) {
		t.Fatal("empty commit appended bytes")
	}
}

func TestJsonlLegacyV3OpenAndUpgrade(t *testing.T) {
	fs := newFakeFS(t)
	// A minimal v3 file: header + one message entry.
	header := `{"type":"session","version":3,"id":"legacy-1","timestamp":"2024-01-01T00:00:00.000Z","cwd":"/old"}`
	entry := `{"type":"message","id":"m1","parentId":null,"timestamp":100,"message":{"role":"user","content":"hi","timestamp":100}}`
	content := header + "\n" + entry + "\n"
	if w := fs.WriteFile("legacy.jsonl", []byte(content), harness.BackgroundContext); !w.Ok {
		t.Fatal(w.Error)
	}
	storage, err := OpenJsonlStorage(fs, "legacy.jsonl", harness.BackgroundContext, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !storage.IsLegacyV3() {
		t.Fatal("expected legacy v3 backing")
	}
	// Stats report the imported usage while legacy-backed.
	stats, _ := storage.GetStats(harness.BackgroundContext)
	if stats.MessageCount != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	// First non-empty commit upgrades to v4 atomically.
	write := session.WriteFromValue(session.SetValue(session.SessionName, "upgraded"))
	result, err := storage.Commit([]session.Write{write}, harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if storage.IsLegacyV3() {
		t.Fatal("still legacy after commit")
	}
	// The returned sequences skip the internal usage-adjustment row.
	if len(result.Seqs) != 1 {
		t.Fatalf("seqs = %v", result.Seqs)
	}
	// The file now starts with a v4 header.
	newContent, _ := os.ReadFile(filepath.Join(fs.root, "legacy.jsonl"))
	if !strings.HasPrefix(string(newContent), `{"v":4,"kind":"header"`) {
		t.Fatalf("header = %q", string(newContent)[:60])
	}
	// The message entry survived (minted id) and name is set.
	scan, _ := storage.ScanEntries(session.EntryScan{Type: session.EntryTypeMessage}, harness.BackgroundContext)
	if len(scan) != 1 {
		t.Fatalf("messages = %d", len(scan))
	}
	name, _ := storage.GetValue(session.SessionName, harness.BackgroundContext)
	if name == nil || name.Value != "upgraded" {
		t.Fatalf("name = %+v", name)
	}
	_ = jsonx.NewObj
}
