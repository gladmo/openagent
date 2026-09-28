package harness

// Ports of skills.test.ts, system-prompt.test.ts, prompt-templates tests
// (via a fake ExecutionEnv), and resource-formatting.test.ts.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// fakeEnv is a real-filesystem-backed ExecutionEnv for tests (cwd = temp
// dir); Shell operations are not needed by these tests.
type fakeEnv struct {
	root string
}

func newFakeEnv(t *testing.T) *fakeEnv {
	t.Helper()
	root := t.TempDir()
	return &fakeEnv{root: root}
}

func (e *fakeEnv) abs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(e.root, p)
}

func (e *fakeEnv) Cwd() string { return e.root }
func (e *fakeEnv) AbsolutePath(path string, _ Context) Result[string, *FileError] {
	return Ok[string, *FileError](e.abs(path))
}
func (e *fakeEnv) JoinPath(parts []string, _ Context) Result[string, *FileError] {
	return Ok[string, *FileError](filepath.Join(parts...))
}
func (e *fakeEnv) ReadTextFile(path string, _ Context) Result[string, *FileError] {
	data, err := os.ReadFile(e.abs(path))
	if err != nil {
		return Err[string, *FileError](NewFileError(FileErrNotFound, err.Error(), path))
	}
	return Ok[string, *FileError](string(data))
}
func (e *fakeEnv) OpenTextLineReader(string, Context) Result[TextLineReader, *FileError] {
	return Err[TextLineReader, *FileError](NewFileError(FileErrNotSupported, "not supported", ""))
}
func (e *fakeEnv) ReadTextLines(string, *ReadTextLinesOptions, Context) Result[[]string, *FileError] {
	return Ok[[]string, *FileError](nil)
}
func (e *fakeEnv) ReadBinaryFile(path string, _ Context) Result[[]byte, *FileError] {
	data, err := os.ReadFile(e.abs(path))
	if err != nil {
		return Err[[]byte, *FileError](NewFileError(FileErrNotFound, err.Error(), path))
	}
	return Ok[[]byte, *FileError](data)
}
func (e *fakeEnv) WriteFile(path string, content []byte, _ Context) Result[struct{}, *FileError] {
	if err := os.MkdirAll(filepath.Dir(e.abs(path)), 0o755); err != nil {
		return Err[struct{}, *FileError](NewFileError(FileErrUnknown, err.Error(), path))
	}
	if err := os.WriteFile(e.abs(path), content, 0o644); err != nil {
		return Err[struct{}, *FileError](NewFileError(FileErrUnknown, err.Error(), path))
	}
	return Ok[struct{}, *FileError](struct{}{})
}
func (e *fakeEnv) AppendFile(path string, content []byte, _ Context) Result[struct{}, *FileError] {
	if err := os.MkdirAll(filepath.Dir(e.abs(path)), 0o755); err != nil {
		return Err[struct{}, *FileError](NewFileError(FileErrUnknown, err.Error(), path))
	}
	f, err := os.OpenFile(e.abs(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Err[struct{}, *FileError](NewFileError(FileErrUnknown, err.Error(), path))
	}
	defer f.Close()
	if _, err := f.Write(content); err != nil {
		return Err[struct{}, *FileError](NewFileError(FileErrUnknown, err.Error(), path))
	}
	return Ok[struct{}, *FileError](struct{}{})
}
func (e *fakeEnv) RenameFile(source, destination string, _ Context) Result[struct{}, *FileError] {
	if err := os.Rename(e.abs(source), e.abs(destination)); err != nil {
		return Err[struct{}, *FileError](NewFileError(FileErrUnknown, err.Error(), source))
	}
	return Ok[struct{}, *FileError](struct{}{})
}
func (e *fakeEnv) FileInfo(path string, _ Context) Result[FileInfo, *FileError] {
	info, err := os.Lstat(e.abs(path))
	if err != nil {
		return Err[FileInfo, *FileError](NewFileError(FileErrNotFound, err.Error(), path))
	}
	kind := FileKindFile
	if info.IsDir() {
		kind = FileKindDirectory
	} else if info.Mode()&os.ModeSymlink != 0 {
		kind = FileKindSymlink
	}
	return Ok[FileInfo, *FileError](FileInfo{
		Name: filepath.Base(path), Path: e.abs(path), Kind: kind,
		Size: info.Size(), MtimeMs: float64(info.ModTime().UnixMilli()),
	})
}
func (e *fakeEnv) ListDir(path string, _ Context) Result[[]FileInfo, *FileError] {
	entries, err := os.ReadDir(e.abs(path))
	if err != nil {
		return Err[[]FileInfo, *FileError](NewFileError(FileErrNotFound, err.Error(), path))
	}
	out := []FileInfo{}
	for _, entry := range entries {
		kind := FileKindFile
		if entry.IsDir() {
			kind = FileKindDirectory
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			kind = FileKindSymlink
		}
		out = append(out, FileInfo{
			Name: entry.Name(), Path: filepath.Join(e.abs(path), entry.Name()), Kind: kind,
			Size: info.Size(), MtimeMs: float64(info.ModTime().UnixMilli()),
		})
	}
	return Ok[[]FileInfo, *FileError](out)
}
func (e *fakeEnv) CanonicalPath(path string, _ Context) Result[string, *FileError] {
	resolved, err := filepath.EvalSymlinks(e.abs(path))
	if err != nil {
		return Err[string, *FileError](NewFileError(FileErrNotFound, err.Error(), path))
	}
	return Ok[string, *FileError](resolved)
}
func (e *fakeEnv) Exists(path string, _ Context) Result[bool, *FileError] {
	_, err := os.Lstat(e.abs(path))
	return Ok[bool, *FileError](err == nil)
}
func (e *fakeEnv) CreateDir(path string, _ *CreateDirOptions, _ Context) Result[struct{}, *FileError] {
	if err := os.MkdirAll(e.abs(path), 0o755); err != nil {
		return Err[struct{}, *FileError](NewFileError(FileErrUnknown, err.Error(), path))
	}
	return Ok[struct{}, *FileError](struct{}{})
}
func (e *fakeEnv) Remove(path string, options *RemoveOptions, _ Context) Result[struct{}, *FileError] {
	if options != nil && options.Recursive != nil && *options.Recursive {
		if err := os.RemoveAll(e.abs(path)); err != nil {
			return Err[struct{}, *FileError](NewFileError(FileErrUnknown, err.Error(), path))
		}
		return Ok[struct{}, *FileError](struct{}{})
	}
	if err := os.Remove(e.abs(path)); err != nil {
		return Err[struct{}, *FileError](NewFileError(FileErrUnknown, err.Error(), path))
	}
	return Ok[struct{}, *FileError](struct{}{})
}
func (e *fakeEnv) CreateTempDir(prefix string, _ Context) Result[string, *FileError] {
	dir, err := os.MkdirTemp(e.root, prefix)
	if err != nil {
		return Err[string, *FileError](NewFileError(FileErrUnknown, err.Error(), ""))
	}
	return Ok[string, *FileError](dir)
}
func (e *fakeEnv) CreateTempFile(options *CreateTempFileOptions, _ Context) Result[string, *FileError] {
	prefix, suffix := "", ""
	if options != nil {
		if options.Prefix != nil {
			prefix = *options.Prefix
		}
		if options.Suffix != nil {
			suffix = *options.Suffix
		}
	}
	f, err := os.CreateTemp(e.root, prefix+"*"+suffix)
	if err != nil {
		return Err[string, *FileError](NewFileError(FileErrUnknown, err.Error(), ""))
	}
	defer f.Close()
	return Ok[string, *FileError](f.Name())
}
func (e *fakeEnv) Cleanup(Context) error { return nil }
func (e *fakeEnv) Exec(string, *ShellExecOptions, Context) Result[ShellExecResult, *ExecutionError] {
	return Err[ShellExecResult, *ExecutionError](NewExecutionError(execNotSupportedAlias2, "not supported"))
}
func (e *fakeEnv) CleanupShell(Context) error { return nil }

const execNotSupportedAlias2 = "not_supported"

func TestLoadSkillsFromSKILLMd(t *testing.T) {
	env := newFakeEnv(t)
	skillDir := filepath.Join(env.root, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: my-skill\ndescription: Does things\n---\n\nInstructions here."
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	result := LoadSkills(env, []string{env.root}, BackgroundContext)
	if len(result.Skills) != 1 {
		t.Fatalf("skills = %d diagnostics = %+v", len(result.Skills), result.Diagnostics)
	}
	skill := result.Skills[0]
	if skill.Name != "my-skill" || skill.Description != "Does things" || skill.Content != "Instructions here." {
		t.Fatalf("skill = %+v", skill)
	}
}

func TestLoadSkillsNameValidation(t *testing.T) {
	env := newFakeEnv(t)
	// Directory name mismatch -> invalid_metadata warning, skill still loads.
	skillDir := filepath.Join(env.root, "correct-name")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: wrong-name\ndescription: d\n---\nbody"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	result := LoadSkills(env, []string{env.root}, BackgroundContext)
	if len(result.Skills) != 1 {
		t.Fatalf("skills = %d", len(result.Skills))
	}
	found := false
	for _, d := range result.Diagnostics {
		if d.Code == SkillDiagInvalidMetadata && strings.Contains(d.Message, "does not match parent directory") {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
}

func TestLoadSkillsIgnoresFiles(t *testing.T) {
	env := newFakeEnv(t)
	// ignored-skill/ has SKILL.md but is listed in .gitignore.
	if err := os.WriteFile(filepath.Join(env.root, ".gitignore"), []byte("ignored-skill/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ignored-skill", "kept-skill"} {
		dir := filepath.Join(env.root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: d\n---\nbody"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result := LoadSkills(env, []string{env.root}, BackgroundContext)
	if len(result.Skills) != 1 || result.Skills[0].Name != "kept-skill" {
		names := []string{}
		for _, s := range result.Skills {
			names = append(names, s.Name)
		}
		t.Fatalf("skills = %v", names)
	}
}

func TestLoadSkillsRootMdRequiresFrontmatter(t *testing.T) {
	env := newFakeEnv(t)
	// Root .md without description frontmatter is skipped.
	if err := os.WriteFile(filepath.Join(env.root, "plain.md"), []byte("no frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.root, "described.md"), []byte("---\ndescription: has one\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	result := LoadSkills(env, []string{env.root}, BackgroundContext)
	if len(result.Skills) != 1 {
		t.Fatalf("skills = %d", len(result.Skills))
	}
	skill := result.Skills[0]
	if skill.Name != filepath.Base(env.root) || skill.Description != "has one" {
		t.Fatalf("skill = %+v", skill)
	}
}

func TestFormatSkillsForSystemPrompt(t *testing.T) {
	skills := []Skill{
		{Name: "a", Description: "A <skill>", FilePath: "/x/a/SKILL.md"},
		{Name: "hidden", Description: "h", FilePath: "/x/h/SKILL.md", DisableModelInvocation: true},
		{Name: "b", Description: "B & B", FilePath: "/x/b/SKILL.md"},
	}
	out := FormatSkillsForSystemPrompt(skills)
	if !strings.Contains(out, "<available_skills>") || !strings.Contains(out, "<name>a</name>") {
		t.Fatalf("out = %q", out)
	}
	if !strings.Contains(out, "<description>A &lt;skill&gt;</description>") {
		t.Fatalf("escape: %q", out)
	}
	if !strings.Contains(out, "B &amp; B") {
		t.Fatalf("escape &: %q", out)
	}
	if strings.Contains(out, "hidden") {
		t.Fatal("hidden skill visible")
	}
	// Empty input.
	if FormatSkillsForSystemPrompt(nil) != "" {
		t.Fatal("empty input")
	}
}

func TestFormatSkillInvocation(t *testing.T) {
	skill := Skill{Name: "deploy", FilePath: "/skills/deploy/SKILL.md", Content: "Steps..."}
	out := FormatSkillInvocation(skill, "extra instructions")
	if !strings.HasPrefix(out, "<skill name=\"deploy\" location=\"/skills/deploy/SKILL.md\">") {
		t.Fatalf("out = %q", out)
	}
	if !strings.Contains(out, "References are relative to /skills/deploy") {
		t.Fatalf("references = %q", out)
	}
	if !strings.HasSuffix(out, "Steps...\n</skill>\n\nextra instructions") {
		t.Fatalf("tail = %q", out)
	}
}

func TestBashExecutionToText(t *testing.T) {
	exit := int64(1)
	msg := CreateBashExecutionMessage("ls -la", "file1\nfile2", &exit, false, true, "/tmp/spill.log", 1)
	text := BashExecutionToText(msg)
	want := "Ran `ls -la`\n```\nfile1\nfile2\n```\n\nCommand exited with code 1\n\n[Output truncated. Full output: /tmp/spill.log]"
	if text != want {
		t.Fatalf("text = %q", text)
	}
	cancelled := CreateBashExecutionMessage("sleep 100", "", nil, true, false, "", 1)
	if got := BashExecutionToText(cancelled); !strings.Contains(got, "(command cancelled)") || !strings.Contains(got, "(no output)") {
		t.Fatalf("cancelled = %q", got)
	}
}

func TestConvertToLlmCustomRoles(t *testing.T) {
	agentMessages := []agent.AgentMessage{
		CreateCompactionSummaryMessage("compacted", 5000, float64(1)),
		CreateBranchSummaryMessage("branched", nil, float64(2)),
		CreateBashExecutionMessage("ls", "out", nil, false, false, "", float64(3)),
	}
	llm := ConvertToLlm(agentMessages)
	if len(llm) != 3 {
		t.Fatalf("llm = %d", len(llm))
	}
	for _, m := range llm {
		if m.Role() != "user" {
			t.Fatalf("role = %s", m.Role())
		}
	}
	first := llm[0].(*ai.UserMessage)
	if text := ai.ContentText(first.Content, "\n"); !strings.Contains(text, "compacted") || !strings.Contains(text, "<summary>") {
		t.Fatalf("first = %q", text)
	}
	second := llm[1].(*ai.UserMessage)
	if text := ai.ContentText(second.Content, "\n"); !strings.Contains(text, "branched") {
		t.Fatalf("second = %q", text)
	}
	third := llm[2].(*ai.UserMessage)
	if text := ai.ContentText(third.Content, "\n"); !strings.Contains(text, "Ran `ls`") {
		t.Fatalf("third = %q", text)
	}
	// excludeFromContext filters bashExecution messages.
	excluded := CreateBashExecutionMessage("secret", "s", nil, false, false, "", float64(4))
	excluded.ExcludeFromContext = true
	filtered := ConvertToLlm([]agent.AgentMessage{excluded})
	if len(filtered) != 0 {
		t.Fatalf("filtered = %d", len(filtered))
	}
}
