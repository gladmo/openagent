package harness

// ExecCapture tests over a non-local env: the bounded view plus spill
// semantics are reusable framework surface, not local-env plumbing.

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// failingTempEnv wraps fakeEnv with a CreateTempFile that always fails.
type failingTempEnv struct {
	*fakeEnv
}

func (e *failingTempEnv) CreateTempFile(*CreateTempFileOptions, Context) Result[string, *FileError] {
	return Err[string, *FileError](NewFileError(FileErrUnknown, "temp creation refused", ""))
}

// pushChunks feeds content through a reused buffer, as a real read loop
// would, then finishes and returns the capture.
func pushChunks(t *testing.T, capture *ExecCapture, content string, chunkSize int) {
	t.Helper()
	reader := strings.NewReader(content)
	buffer := make([]byte, chunkSize)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			capture.Push(buffer[:n])
		}
		if err != nil {
			break
		}
	}
	capture.Finish()
}

func spillOptions() *ShellOutputCaptureOptions {
	return &ShellOutputCaptureOptions{
		Limits: ShellOutputLimits{MaxBytes: DefaultMaxBytes, MaxLines: DefaultMaxLines, Retain: RetainTail},
		Spill:  true,
	}
}

func TestExecCaptureSpillHoldsFullOutput(t *testing.T) {
	env := newFakeEnv(t)
	capture := NewExecCapture(env, spillOptions(), nil, BackgroundContext)

	var lines []string
	for i := 1; i <= 3000; i++ {
		lines = append(lines, strings.Repeat("x", 16)+strconv.Itoa(i))
	}
	content := strings.Join(lines, "\n") + "\n"
	pushChunks(t, capture, content, 8*1024)

	if err := capture.SpillError(); err != nil {
		t.Fatalf("spill error: %+v", err)
	}
	view := capture.Snapshot()
	if !view.Truncation.Truncated {
		t.Fatal("expected truncated view")
	}
	if view.SpillPath == "" {
		t.Fatal("spill path missing")
	}
	spilled, err := os.ReadFile(view.SpillPath)
	if err != nil {
		t.Fatalf("read spill: %v", err)
	}
	if string(spilled) != content {
		t.Fatalf("spill holds %d bytes, want %d (full output)", len(spilled), len(content))
	}
	if !strings.HasPrefix(string(spilled), lines[0]) {
		t.Fatal("spill missing buffered prefix")
	}
}

func TestExecCaptureNoSpillUnderLimit(t *testing.T) {
	env := newFakeEnv(t)
	capture := NewExecCapture(env, spillOptions(), nil, BackgroundContext)
	pushChunks(t, capture, "small output\n", 4*1024)

	if err := capture.SpillError(); err != nil {
		t.Fatalf("spill error: %+v", err)
	}
	view := capture.Snapshot()
	if view.Truncation.Truncated {
		t.Fatalf("unexpected truncation: %+v", view.Truncation)
	}
	if view.SpillPath != "" {
		t.Fatalf("spill created under limit: %q", view.SpillPath)
	}
}

func TestExecCaptureSpillDisabled(t *testing.T) {
	env := newFakeEnv(t)
	options := spillOptions()
	options.Spill = false
	capture := NewExecCapture(env, options, nil, BackgroundContext)
	pushChunks(t, capture, strings.Repeat("line\n", 3000), 8*1024)

	view := capture.Snapshot()
	if !view.Truncation.Truncated {
		t.Fatal("expected truncated view")
	}
	if view.SpillPath != "" {
		t.Fatalf("spill created while disabled: %q", view.SpillPath)
	}
}

func TestExecCaptureSpillCreateFailureFailsLoud(t *testing.T) {
	env := &failingTempEnv{fakeEnv: newFakeEnv(t)}
	capture := NewExecCapture(env, spillOptions(), nil, BackgroundContext)
	pushChunks(t, capture, strings.Repeat("line\n", 3000), 8*1024)

	err := capture.SpillError()
	if err == nil || err.Code != ExecErrSpillError {
		t.Fatalf("spill error = %+v, want spill_error", err)
	}
}

func TestExecCaptureSnapshotPublishesUpdate(t *testing.T) {
	env := newFakeEnv(t)
	var published *ShellOutputView
	capture := NewExecCapture(env, spillOptions(), func(update ShellOutputUpdate, _ Context) {
		if replace, ok := update.(*ShellOutputReplace); ok {
			published = &replace.Output
		}
	}, BackgroundContext)
	pushChunks(t, capture, "done\n", 4*1024)

	view := capture.Snapshot()
	if published == nil || published.Text != view.Text {
		t.Fatalf("published = %+v, view = %+v", published, view)
	}
}
