package codingagent

// Tests for the write/read tool definitions and image processing (ported
// from tools/write.test.ts, tools/read.test.ts, and image-process surfaces).

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gladmo/openagent/abort"
)

func TestWithFileMutationQueueSerializes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.txt")

	// Pre-create the target so every mutation resolves the same queue key:
	// a missing file keys by path while an existing one keys by real path
	// (macOS /tmp -> /private/tmp), which would legitimately split queues.
	os.WriteFile(target, []byte("0"), 0o644)

	const workers = 8
	const iterations = 20
	counter := make(chan int, workers*iterations)

	done := make(chan struct{})
	for w := 0; w < workers; w++ {
		go func() {
			for i := 0; i < iterations; i++ {
				_ = WithFileMutationQueue(target, func() error {
					// Read-modify-write must not interleave.
					current := 0
					if data, err := os.ReadFile(target); err == nil {
						fmt.Sscanf(string(data), "%d", &current)
					}
					counter <- current
					return os.WriteFile(target, []byte(fmt.Sprintf("%d", current+1)), 0o644)
				})
			}
			done <- struct{}{}
		}()
	}
	for w := 0; w < workers; w++ {
		<-done
	}
	close(counter)

	// No two workers observed the same counter value.
	seen := map[int]bool{}
	for value := range counter {
		if seen[value] {
			t.Fatalf("observed duplicate counter value %d", value)
		}
		seen[value] = true
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	final := 0
	fmt.Sscanf(string(data), "%d", &final)
	if final != workers*iterations {
		t.Fatalf("final = %d, want %d", final, workers*iterations)
	}
}

func timeAfterMs(ms int) <-chan time.Time { return time.After(time.Duration(ms) * time.Millisecond) }

func TestWithFileMutationQueueDifferentFilesParallel(t *testing.T) {
	// Different files use different queues: both callbacks can hold locks.
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")

	holdA := make(chan struct{})
	releaseA := make(chan struct{})
	go func() {
		_ = WithFileMutationQueue(a, func() error {
			close(holdA)
			<-releaseA
			return nil
		})
	}()
	<-holdA

	bDone := make(chan error)
	go func() {
		bDone <- WithFileMutationQueue(b, func() error { return nil })
	}()
	select {
	case <-bDone:
		// B completed while A still holds its lock: queues are independent.
	case <-timeAfterMs(500):
		t.Fatal("different-file queue blocked on same-file lock")
	}
	close(releaseA)
}

func TestWriteTool(t *testing.T) {
	dir := t.TempDir()
	definition := CreateWriteToolDefinition(dir, nil)

	result, err := definition.Execute("call-1", WriteToolInput{Path: "sub/dir/file.txt", Content: "hello"}, nil, nil, &ExtensionContext{Cwd: dir})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	text, ok := result.(*ToolResultText)
	if !ok || text.Text != "Successfully wrote to sub/dir/file.txt" {
		t.Fatalf("result = %+v", result)
	}

	data, err := os.ReadFile(filepath.Join(dir, "sub/dir/file.txt"))
	if err != nil || string(data) != "hello" {
		t.Fatalf("file = %q err=%v", data, err)
	}

	// Overwrite works.
	if _, err := definition.Execute("call-2", WriteToolInput{Path: "sub/dir/file.txt", Content: "world"}, nil, nil, nil); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	data, _ = os.ReadFile(filepath.Join(dir, "sub/dir/file.txt"))
	if string(data) != "world" {
		t.Fatalf("overwrite content = %q", data)
	}
}

func TestWriteToolAbort(t *testing.T) {
	dir := t.TempDir()
	definition := CreateWriteToolDefinition(dir, nil)

	controller := abort.NewController()
	controller.Abort()
	_, err := definition.Execute("call-1", WriteToolInput{Path: "x.txt", Content: "y"}, controller.Signal(), nil, nil)
	if err == nil || err.Error() != "Operation aborted" {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "x.txt")); statErr == nil {
		t.Fatal("aborted write should not create the file")
	}
}

func TestWriteToolCustomOperations(t *testing.T) {
	dir := t.TempDir()
	ops := &recordingWriteOps{dir: dir}
	definition := CreateWriteToolDefinition(dir, &WriteToolOptions{Operations: ops})

	if _, err := definition.Execute("c", WriteToolInput{Path: "z.txt", Content: "data"}, nil, nil, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !ops.writeCalled || !ops.mkdirCalled {
		t.Fatalf("ops = %+v", ops)
	}
}

type recordingWriteOps struct {
	dir         string
	writeCalled bool
	mkdirCalled bool
}

func (o *recordingWriteOps) WriteFile(path, content string) error {
	o.writeCalled = true
	return os.WriteFile(path, []byte(content), 0o644)
}

func (o *recordingWriteOps) Mkdir(dir string) error {
	o.mkdirCalled = true
	return os.MkdirAll(dir, 0o755)
}

func TestReadToolText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3"), 0o644)

	definition := CreateReadToolDefinition(dir, nil)
	result, err := definition.Execute("c", ReadToolInput{Path: "a.txt"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output, ok := result.(*ReadToolOutput)
	if !ok {
		t.Fatalf("result = %T", result)
	}
	if output.Text != "line1\nline2\nline3" {
		t.Fatalf("text = %q", output.Text)
	}
	if output.Details != nil {
		t.Fatalf("details = %+v", output.Details)
	}
}

func TestReadToolOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("l1\nl2\nl3\nl4\nl5"), 0o644)

	definition := CreateReadToolDefinition(dir, nil)

	// Offset 2 + limit 2 => l2 l3, with a remaining-lines notice.
	result, err := definition.Execute("c", ReadToolInput{Path: "a.txt", Offset: 2, Limit: 2}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*ReadToolOutput)
	if !strings.HasPrefix(output.Text, "l2\nl3") {
		t.Fatalf("text = %q", output.Text)
	}
	if !strings.Contains(output.Text, "2 more lines in file. Use offset=4 to continue.") {
		t.Fatalf("notice = %q", output.Text)
	}

	// Offset beyond EOF errors.
	if _, err := definition.Execute("c", ReadToolInput{Path: "a.txt", Offset: 10}, nil, nil, nil); err == nil {
		t.Fatal("offset beyond EOF should error")
	}
}

func TestReadToolLineTruncation(t *testing.T) {
	dir := t.TempDir()
	var lines []string
	for i := 1; i <= DefaultMaxLines+10; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Join(lines, "\n")), 0o644)

	definition := CreateReadToolDefinition(dir, nil)
	result, err := definition.Execute("c", ReadToolInput{Path: "big.txt"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*ReadToolOutput)
	if !strings.Contains(output.Text, fmt.Sprintf("Use offset=%d to continue.", DefaultMaxLines+1)) {
		t.Fatalf("continuation notice missing: %q", output.Text[len(output.Text)-200:])
	}
	if output.Details == nil || output.Details.Truncation == nil || !output.Details.Truncation.Truncated {
		t.Fatalf("details = %+v", output.Details)
	}
}

func TestReadToolFirstLineTooBig(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("x", DefaultMaxBytes+100)
	os.WriteFile(filepath.Join(dir, "huge.txt"), []byte(huge), 0o644)

	definition := CreateReadToolDefinition(dir, nil)
	result, err := definition.Execute("c", ReadToolInput{Path: "huge.txt"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*ReadToolOutput)
	if !strings.Contains(output.Text, "exceeds") || !strings.Contains(output.Text, "sed -n") {
		t.Fatalf("fallback notice = %q", output.Text)
	}
	if output.Details == nil || !output.Details.Truncation.FirstLineExceedsLimit {
		t.Fatalf("details = %+v", output.Details)
	}
}

func TestReadToolMissing(t *testing.T) {
	dir := t.TempDir()
	definition := CreateReadToolDefinition(dir, nil)
	if _, err := definition.Execute("c", ReadToolInput{Path: "nope.txt"}, nil, nil, nil); err == nil {
		t.Fatal("missing file should error")
	}
}

func testPNGBytes(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x80, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestReadToolImage(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "img.png"), testPNGBytes(t, 64, 48), 0o644)

	definition := CreateReadToolDefinition(dir, nil)
	result, err := definition.Execute("c", ReadToolInput{Path: "img.png"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output := result.(*ReadToolOutput)
	if !strings.HasPrefix(output.Text, "Read image file [image/png]") {
		t.Fatalf("text = %q", output.Text)
	}
	if _, err := base64.StdEncoding.DecodeString(output.ImageData); err != nil {
		t.Fatalf("image data not base64: %v", err)
	}
}

func TestProcessImagePassthrough(t *testing.T) {
	pngBytes := testPNGBytes(t, 100, 80)
	result := ProcessImage(pngBytes, "image/png", nil)
	if !result.OK {
		t.Fatalf("result = %+v", result)
	}
	if result.MimeType != "image/png" {
		t.Fatalf("mime = %q", result.MimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil || len(decoded) == 0 {
		t.Fatalf("data = %d bytes err=%v", len(decoded), err)
	}
}

func TestProcessImageResize(t *testing.T) {
	pngBytes := testPNGBytes(t, 3000, 2000)
	noResize := false
	result := ProcessImage(pngBytes, "image/png", &ProcessImageOptions{AutoResizeImages: &noResize})
	if !result.OK {
		t.Fatalf("result = %+v", result)
	}
	if result.Data == "" {
		t.Fatal("no data")
	}

	// Auto-resize path fits a small limit.
	smallLimit := 50 * 1024
	options := &ImageResizeOptions{MaxBytes: &smallLimit, MaxWidth: intPtr(800), MaxHeight: intPtr(800)}
	resized := ResizeImageInProcess(pngBytes, "image/png", options)
	if resized == nil {
		t.Fatal("resize returned nil")
	}
	if resized.Width > 800 || resized.Height > 800 {
		t.Fatalf("dims = %dx%d", resized.Width, resized.Height)
	}
	if !resized.WasResized {
		t.Fatal("wasResized = false")
	}
	if base64EncodedSize(len(decodeBase64(t, resized.Data))) >= smallLimit {
		t.Fatal("encoded size over limit")
	}
}

func decodeBase64(t *testing.T, data string) []byte {
	t.Helper()
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return decoded
}

func intPtr(v int) *int { return &v }

func TestResizeImageWithinLimits(t *testing.T) {
	pngBytes := testPNGBytes(t, 100, 100)
	// Already within limits: passthrough.
	resized := ResizeImageInProcess(pngBytes, "image/png", nil)
	if resized == nil {
		t.Fatal("resize nil")
	}
	if resized.WasResized {
		t.Fatalf("unexpected resize: %+v", resized)
	}
	if resized.Width != 100 || resized.Height != 100 {
		t.Fatalf("dims = %dx%d", resized.Width, resized.Height)
	}
}

func TestResizeImageImpossible(t *testing.T) {
	pngBytes := testPNGBytes(t, 50, 50)
	// One-byte limit cannot be met: nil.
	one := 1
	options := &ImageResizeOptions{MaxBytes: &one, MaxWidth: intPtr(2000), MaxHeight: intPtr(2000)}
	if resized := ResizeImageInProcess(pngBytes, "image/png", options); resized != nil {
		t.Fatalf("impossible resize returned %+v", resized)
	}
}

func TestConvertToPng(t *testing.T) {
	pngBytes := testPNGBytes(t, 20, 20)
	pngB64 := base64.StdEncoding.EncodeToString(pngBytes)

	// PNG input passes through.
	if result, ok := ConvertToPng(pngB64, "image/png"); !ok || result.MimeType != "image/png" {
		t.Fatalf("png passthrough = %+v ok=%v", result, ok)
	}

	// Non-PNG decodable input converts.
	jpegBytes := encodeJpegForTest(decodeImage(t, pngBytes))
	jpegB64 := base64.StdEncoding.EncodeToString(jpegBytes)
	result, ok := ConvertToPng(jpegB64, "image/jpeg")
	if !ok || result.MimeType != "image/png" {
		t.Fatalf("jpeg conversion = %+v ok=%v", result, ok)
	}
	decoded, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, format, err := image.Decode(bytes.NewReader(decoded)); err != nil || format != "png" {
		t.Fatalf("format = %q err=%v", format, err)
	}
}

func decodeImage(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return img
}

func TestExifOrientationParsing(t *testing.T) {
	if got := exifOrientation([]byte{0xFF, 0xD8, 0xFF, 0xE0}); got != 1 {
		t.Fatalf("plain jpeg = %d", got)
	}
	// Hand-built APP1 EXIF with orientation 6.
	exif := buildExifOrientation(6)
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE1, byte(len(exif) >> 8), byte(len(exif))}, exif...)
	if got := exifOrientation(jpeg); got != 6 {
		t.Fatalf("orientation jpeg = %d", got)
	}

	// applyExifOrientation rotates dims for orientation 6.
	img := image.NewRGBA(image.Rect(0, 0, 10, 4))
	oriented := applyExifOrientation(img, jpeg)
	if oriented.Bounds().Dx() != 4 || oriented.Bounds().Dy() != 10 {
		t.Fatalf("oriented dims = %dx%d", oriented.Bounds().Dx(), oriented.Bounds().Dy())
	}
}

func buildExifOrientation(orientation int) []byte {
	// "II*\0" + IFD offset 8 + one entry with tag 0x0112.
	exif := []byte{
		'I', 'I', 0x2A, 0x00,
		0x08, 0x00, 0x00, 0x00,
		0x01, 0x00, // entry count
		0x12, 0x01, // tag 0x0112
		0x03, 0x00, // type SHORT
		0x01, 0x00, 0x00, 0x00, // count
		byte(orientation), 0x00, 0x00, 0x00, // value
		0x00, 0x00, 0x00, 0x00, // next IFD
	}
	return exif
}

func TestWrapToolDefinition(t *testing.T) {
	dir := t.TempDir()
	tool := WrapToolDefinition(CreateWriteToolDefinition(dir, nil), nil)
	if tool.Name != "write" || tool.Label != "write" {
		t.Fatalf("tool = %+v", tool)
	}
	if tool.Parameters == nil {
		t.Fatal("parameters nil")
	}

	tools := WrapToolDefinitions([]*ToolDefinition{CreateWriteToolDefinition(dir, nil)}, nil)
	if len(tools) != 1 || tools[0].Name != "write" {
		t.Fatalf("tools = %+v", tools)
	}

	// Round-trip through CreateToolDefinitionFromAgentTool.
	definition := CreateToolDefinitionFromAgentTool(tool)
	if definition.Name != "write" || definition.Description == "" {
		t.Fatalf("definition = %+v", definition)
	}
}

func TestMapToolInputs(t *testing.T) {
	if input, ok := mapWriteToolInput(map[string]any{"path": "a", "content": "b"}); !ok || input.Path != "a" || input.Content != "b" {
		t.Fatalf("write map = %+v ok=%v", input, ok)
	}
	if input, ok := mapReadToolInput(map[string]any{"path": "x", "offset": float64(3)}); !ok || input.Path != "x" || input.Offset != 3 {
		t.Fatalf("read map = %+v ok=%v", input, ok)
	}
}
