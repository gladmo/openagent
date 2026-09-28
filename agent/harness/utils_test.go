package harness

// Ports of harness tests: types.test.ts (Result), truncate.test.ts,
// output-capture.test.ts (representative cases).

import (
	"strconv"
	"strings"
	"testing"
)

func TestResult(t *testing.T) {
	okResult := Ok[string, error]("value")
	if !okResult.Ok || okResult.Value != "value" {
		t.Fatalf("ok = %+v", okResult)
	}
	errResult := Err[string, error](toErrorHarness("boom"))
	if errResult.Ok || errResult.Error == nil {
		t.Fatalf("err = %+v", errResult)
	}
	if v := GetOrUndefined(okResult); v != "value" {
		t.Fatalf("getOrUndefined = %q", v)
	}
	if err := ToError("plain"); err.Error() != "plain" {
		t.Fatalf("toError = %v", err)
	}
}

func TestTruncateHeadNoTruncation(t *testing.T) {
	result := TruncateHead("one\ntwo\n", nil)
	if result.Truncated || result.OutputLines != 2 || result.OutputBytes != 8 {
		t.Fatalf("result = %+v", result)
	}
}

func TestTruncateHeadLineLimit(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("x\n")
	}
	result := TruncateHead(sb.String(), &TruncationOptions{MaxLines: intPtr(10)})
	if !result.Truncated || result.TruncatedBy != "lines" || result.OutputLines != 10 {
		t.Fatalf("result = %+v", result)
	}
	if lines := strings.Count(result.Content, "\n"); lines != 9 { // 10 lines joined by \n
		t.Fatalf("lines = %d", lines)
	}
}

func TestTruncateHeadByteLimit(t *testing.T) {
	result := TruncateHead(strings.Repeat("abcdefghij", 100), &TruncationOptions{MaxBytes: int64Ptr(50)})
	if !result.Truncated || result.TruncatedBy != "bytes" {
		t.Fatalf("result = %+v", result)
	}
	if result.OutputBytes > 50 {
		t.Fatalf("outputBytes = %d", result.OutputBytes)
	}
}

func TestTruncateHeadFirstLineExceeds(t *testing.T) {
	result := TruncateHead(strings.Repeat("x", 1000), &TruncationOptions{MaxBytes: int64Ptr(50)})
	if !result.Truncated || !result.FirstLineExceedsLimit || result.Content != "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestTruncateTailPartialLastLine(t *testing.T) {
	// A single line longer than maxBytes: keep its end (partial).
	result := TruncateTail(strings.Repeat("y", 1000), &TruncationOptions{MaxBytes: int64Ptr(50)})
	if !result.Truncated || !result.LastLinePartial {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Content) > 50 {
		t.Fatalf("content len = %d", len(result.Content))
	}
}

func TestTruncateTailKeepsEnd(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("line-")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString("\n")
	}
	result := TruncateTail(sb.String(), &TruncationOptions{MaxLines: intPtr(3)})
	if !result.Truncated || result.TruncatedBy != "lines" || result.OutputLines != 3 {
		t.Fatalf("result = %+v", result)
	}
	if !strings.HasSuffix(strings.TrimSpace(result.Content), "line-99") {
		t.Fatalf("content tail = %q", result.Content)
	}
}

func TestFormatSize(t *testing.T) {
	if got := FormatSize(512); got != "512B" {
		t.Fatalf("got %s", got)
	}
	if got := FormatSize(2048); got != "2.0KB" {
		t.Fatalf("got %s", got)
	}
	if got := FormatSize(3 * 1024 * 1024); got != "3.0MB" {
		t.Fatalf("got %s", got)
	}
}

func TestTruncateLine(t *testing.T) {
	if r := TruncateLine("short"); r.WasTruncated {
		t.Fatal("short truncated")
	}
	long := strings.Repeat("a", 600)
	r := TruncateLine(long)
	if !r.WasTruncated || !strings.HasSuffix(r.Text, "... [truncated]") {
		t.Fatalf("r = %+v", r)
	}
	if runic := len([]rune(r.Text)); runic != 500+len("... [truncated]") {
		t.Fatalf("len = %d", runic)
	}
}

func TestSanitizeShellOutput(t *testing.T) {
	got := SanitizeShellOutput("a\x00b\x01c\td\ne\rf\x0bg￹g￻h")
	// Controls stripped except \t \n \r; interstitials removed.
	if strings.ContainsAny(got, "\x00\x01\x0b￹￻") {
		t.Fatalf("got %q", got)
	}
	// TS strips \x0b-\x1f (which includes \r) but keeps \t (0x09) and
	// \n (0x0a).
	if !strings.Contains(got, "\t") || !strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Fatalf("got %q", got)
	}
}

func TestOutputCaptureAppendUpdate(t *testing.T) {
	var updates []ShellOutputUpdate
	capture := NewOutputCapture(&ShellOutputCaptureOptions{
		Limits: ShellOutputLimits{MaxBytes: 10000, MaxLines: 100},
	}, BackgroundContext, func(update ShellOutputUpdate, _ Context) {
		updates = append(updates, update)
	}, func(error) {})
	previous := capture.Snapshot()
	_ = previous
	capture.Push("hello\nworld\n")
	view := capture.Snapshot()
	update := updateFrom(nil, view)
	if _, ok := update.(*ShellOutputReplace); !ok {
		t.Fatalf("first update = %T", update)
	}
	// Second snapshot extends: append detected.
	capture.Push("more\n")
	next := capture.Snapshot()
	delta := updateFrom(&view, next)
	appendUpdate, ok := delta.(*ShellOutputAppend)
	if !ok {
		t.Fatalf("second update = %T", delta)
	}
	if appendUpdate.Text != "more\n" {
		t.Fatalf("append text = %q", appendUpdate.Text)
	}
	_ = updates
}

func TestOutputCaptureTailRetention(t *testing.T) {
	capture := NewOutputCapture(&ShellOutputCaptureOptions{
		Limits: ShellOutputLimits{MaxBytes: 20, MaxLines: 3, Retain: RetainTail},
	}, BackgroundContext, nil, nil)
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("0123456789")
	}
	capture.Push(sb.String())
	view := capture.Snapshot()
	if !view.Truncation.Truncated {
		t.Fatal("not truncated")
	}
	if UTF8ByteLength(view.Text) > 20 {
		t.Fatalf("view bytes = %d", UTF8ByteLength(view.Text))
	}
	// Retained text is the TAIL of the input.
	if !strings.HasSuffix(sb.String(), strings.TrimRight(view.Text, "\n")) {
		// LastLinePartial can cut mid-line; just verify the tail overlap by
		// checking the final bytes are present.
		if !strings.HasSuffix(sb.String(), view.Text[len(view.Text)-minIntUtil(10, len(view.Text)):]) {
			t.Fatalf("view is not the tail: %q", view.Text)
		}
	}
}

func TestApplyShellOutputUpdate(t *testing.T) {
	meta := ShellOutputMetadata{Truncation: TruncationResultData{}}
	current := ShellOutputView{Text: "hello", ShellOutputMetadata: meta}
	after := ApplyShellOutputUpdate(&current, &ShellOutputAppend{Text: " world", Metadata: meta})
	if after.Text != "hello world" {
		t.Fatalf("append = %q", after.Text)
	}
	slided := ApplyShellOutputUpdate(&after, &ShellOutputSlide{Drop: 6, Text: "WORLD", Metadata: meta})
	if slided.Text != "worldWORLD" {
		t.Fatalf("slide = %q", slided.Text)
	}
	meta2 := ApplyShellOutputUpdate(&slided, &ShellOutputMetadataUpdate{Metadata: meta})
	if meta2.Text != "worldWORLD" {
		t.Fatalf("metadata = %q", meta2.Text)
	}
}

func toErrorHarness(msg string) error { return ToError(msg) }
