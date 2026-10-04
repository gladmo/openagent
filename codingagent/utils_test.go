package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ported from config.ts paths and utils/paths.ts + text.ts + json.ts tests
// (config.test.ts and paths-related assertions).

func TestStripBom(t *testing.T) {
	if got := StripBom("\uFEFFhello"); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if got := StripBom("hello"); got != "hello" {
		t.Fatalf("got %q", got)
	}
	bom, text := SplitBom("\uFEFFa")
	if bom != "\uFEFF" || text != "a" {
		t.Fatalf("split %q %q", bom, text)
	}
}

func TestStripJsonComments(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"a": 1}`, `{"a": 1}`},
		{`{"a": 1,}`, `{"a": 1}`},
		{"{\"a\": 1, // comment\n}", "{\"a\": 1 \n}"},
		{`{"a": "http://x"}`, `{"a": "http://x"}`},
		{`{"a": [1, 2,]}`, `{"a": [1, 2]}`},
	}
	for _, tc := range cases {
		if got := StripJsonComments(tc.in); got != tc.want {
			t.Errorf("StripJsonComments(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	home, _ := os.UserHomeDir()

	if got := NormalizePathDefault("~/dev"); got != filepath.Join(home, "dev") {
		t.Fatalf("tilde = %q", got)
	}
	if got := NormalizePathDefault("~"); got != home {
		t.Fatalf("bare tilde = %q", got)
	}

	strip := true
	if got := NormalizePath("@file", PathInputOptions{StripAtPrefix: strip}); got != "file" {
		t.Fatalf("strip at = %q", got)
	}

	noExpand := false
	if got := NormalizePath("~/x", PathInputOptions{ExpandTilde: &noExpand}); got != "~/x" {
		t.Fatalf("no expand = %q", got)
	}

	trim := true
	if got := NormalizePath("  x  ", PathInputOptions{Trim: trim}); got != "x" {
		t.Fatalf("trim = %q", got)
	}
}

func TestNormalizeUnicodeSpaces(t *testing.T) {
	if got := NormalizePath("a\u3000b", PathInputOptions{NormalizeUnicodeSpaces: true}); got != "a b" {
		t.Fatalf("got %q", got)
	}
}

func TestIsLocalPath(t *testing.T) {
	for _, local := range []string{"./x", "../y", "name", "file:///tmp"} {
		if !IsLocalPath(local) {
			t.Errorf("%q should be local", local)
		}
	}
	for _, remote := range []string{"npm:x", "git:x", "github:a/b", "http://x", "https://x", "ssh://x"} {
		if IsLocalPath(remote) {
			t.Errorf("%q should not be local", remote)
		}
	}
}

func TestResolvePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := ResolvePath("~/x", "/base", PathInputOptions{}); got != filepath.Join(home, "x") {
		t.Fatalf("tilde = %q", got)
	}
	if got := ResolvePath("rel", "/base", PathInputOptions{}); got != "/base/rel" {
		t.Fatalf("rel = %q", got)
	}
	if got := ResolvePath("/abs", "/base", PathInputOptions{}); got != "/abs" {
		t.Fatalf("abs = %q", got)
	}
}

func TestGetCwdRelativePath(t *testing.T) {
	dir := t.TempDir()
	if got, ok := GetCwdRelativePath(filepath.Join(dir, "sub"), dir); !ok || got != "sub" {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	if _, ok := GetCwdRelativePath(filepath.Dir(dir), dir); ok {
		t.Fatal("outside cwd should not be relative")
	}
	if got, ok := GetCwdRelativePath(dir, dir); !ok || got != "." {
		t.Fatalf("same dir: %q %v", got, ok)
	}
}

func TestParseFrontmatter(t *testing.T) {
	content := "---\nname: test\nenabled: true\n---\n\nbody text"
	result := ParseFrontmatter(content)
	if result.Body != "body text" {
		t.Fatalf("body = %q", result.Body)
	}
	if result.Frontmatter["name"] != "test" {
		t.Fatalf("frontmatter = %+v", result.Frontmatter)
	}
	if enabled, ok := result.Frontmatter["enabled"].(bool); !ok || !enabled {
		t.Fatalf("enabled = %v", result.Frontmatter["enabled"])
	}

	if got := StripFrontmatter("no frontmatter"); got != "no frontmatter" {
		t.Fatalf("no frontmatter = %q", got)
	}
	// CRLF normalization.
	if got := StripFrontmatter("---\r\na: 1\r\n---\r\nbody"); got != "body" {
		t.Fatalf("crlf = %q", got)
	}
	// Unterminated frontmatter: whole content is body.
	if got := StripFrontmatter("---\na: 1\nbody"); got != "---\na: 1\nbody" {
		t.Fatalf("unterminated = %q", got)
	}
}

func TestWarnDeprecation(t *testing.T) {
	ClearDeprecationWarningsForTests()
	WarnDeprecation("test message")
	WarnDeprecation("test message")
	// Second call is a no-op (cannot easily capture stderr; smoke only).
	ClearDeprecationWarningsForTests()
}

func TestDetectImageMime(t *testing.T) {
	// JPEG magic.
	if got := DetectSupportedImageMimeType([]byte{0xff, 0xd8, 0xff, 0xe0}); got != "image/jpeg" {
		t.Fatalf("jpeg = %q", got)
	}
	// JPEG-XR (0xf7 fourth byte) rejected.
	if got := DetectSupportedImageMimeType([]byte{0xff, 0xd8, 0xff, 0xf7}); got != "" {
		t.Fatalf("jpegxr = %q", got)
	}
	// PNG (signature + IHDR).
	png := append([]byte{}, pngSignature...)
	png = append(png, 0, 0, 0, 13) // IHDR length
	png = append(png, []byte("IHDR")...)
	png = append(png, make([]byte, 13)...)
	if got := DetectSupportedImageMimeType(png); got != "image/png" {
		t.Fatalf("png = %q", got)
	}
	// GIF.
	if got := DetectSupportedImageMimeType([]byte("GIF89a....")); got != "image/gif" {
		t.Fatalf("gif = %q", got)
	}
	// WEBP.
	webp := append([]byte("RIFF"), 0, 0, 0, 0)
	webp = append(webp, []byte("WEBP")...)
	if got := DetectSupportedImageMimeType(webp); got != "image/webp" {
		t.Fatalf("webp = %q", got)
	}
	// Unknown.
	if got := DetectSupportedImageMimeType([]byte("xxxx")); got != "" {
		t.Fatalf("unknown = %q", got)
	}
}

func TestIsAnimatedPng(t *testing.T) {
	// acTL chunk after signature => animated.
	apng := append([]byte{}, pngSignature...)
	apng = append(apng, 0, 0, 0, 8) // chunk length
	apng = append(apng, []byte("acTL")...)
	apng = append(apng, make([]byte, 8)...)
	if !isAnimatedPng(apng) {
		t.Fatal("acTL should be animated")
	}
	// IDAT first => static.
	png := append([]byte{}, pngSignature...)
	png = append(png, 0, 0, 0, 13)
	png = append(png, []byte("IHDR")...)
	png = append(png, make([]byte, 13)...)
	png = append(png, 0, 0, 0, 10)
	png = append(png, []byte("IDAT")...)
	png = append(png, make([]byte, 10)...)
	if isAnimatedPng(png) {
		t.Fatal("IDAT should be static")
	}
}

func TestIsBmp(t *testing.T) {
	// BITMAPINFOHEADER (40), uncompressed, valid offsets.
	bmp := make([]byte, 54)
	bmp[0], bmp[1] = 'B', 'M'
	putLE32 := func(offset int, v uint32) {
		bmp[offset] = byte(v)
		bmp[offset+1] = byte(v >> 8)
		bmp[offset+2] = byte(v >> 16)
		bmp[offset+3] = byte(v >> 24)
	}
	putLE32(2, 0)   // file size (0 = unspecified)
	putLE32(10, 54) // pixel data offset
	putLE32(14, 40) // DIB header size
	putLE32(30, 0)  // BI_RGB
	if !isBmp(bmp) {
		t.Fatal("valid bmp rejected")
	}
	putLE32(14, 20) // unsupported DIB size
	if isBmp(bmp) {
		t.Fatal("unsupported DIB accepted")
	}
}

func TestGetPiUserAgent(t *testing.T) {
	agent := GetPiUserAgent("1.2.3")
	if !strings.HasPrefix(agent, "pi/1.2.3 (") {
		t.Fatalf("agent = %q", agent)
	}
	if !strings.Contains(agent, "; go/") {
		t.Fatalf("agent = %q", agent)
	}
}

func TestMakeSelfUpdateCommandStep(t *testing.T) {
	step := makeSelfUpdateCommandStep("npm", []string{"install", "-g", "pkg@latest"})
	if step.Display != "npm install -g pkg@latest" {
		t.Fatalf("display = %q", step.Display)
	}
	step = makeSelfUpdateCommandStep("cmd", []string{"arg with space"})
	if step.Display != `cmd "arg with space"` {
		t.Fatalf("quoted = %q", step.Display)
	}
}

func TestMakeSelfUpdateCommand(t *testing.T) {
	install := makeSelfUpdateCommandStep("npm", []string{"install", "-g", "x"})
	uninstall := makeSelfUpdateCommandStep("npm", []string{"uninstall", "-g", "y"})
	combined := makeSelfUpdateCommand(install, &uninstall)
	if combined.Display != "npm uninstall -g y && npm install -g x" {
		t.Fatalf("display = %q", combined.Display)
	}
	if len(combined.Steps) != 2 || combined.Steps[0].Command != "npm" {
		t.Fatalf("steps = %+v", combined.Steps)
	}
	single := makeSelfUpdateCommand(install, nil)
	if single.Display != "npm install -g x" || single.Steps != nil {
		t.Fatalf("single = %+v", single)
	}
}

func TestGetAgentDirEnvOverride(t *testing.T) {
	t.Setenv(strings.ToUpper("pi")+"_CODING_AGENT_DIR", "/custom/dir")
	if got := GetAgentDir(); got != "/custom/dir" {
		t.Fatalf("agent dir = %q", got)
	}
}
