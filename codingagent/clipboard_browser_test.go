package codingagent

// Tests for clipboard routing, browser opening, and key display
// (adapted from clipboard.test.ts surfaces).

import (
	"os"
	"strings"
	"testing"

	"runtime"
)

func TestIsRemoteSession(t *testing.T) {
	if !isRemoteSession(map[string]string{"SSH_CONNECTION": "x"}) {
		t.Fatal("SSH_CONNECTION not detected")
	}
	if !isRemoteSession(map[string]string{"SSH_CLIENT": "x"}) {
		t.Fatal("SSH_CLIENT not detected")
	}
	if !isRemoteSession(map[string]string{"MOSH_CONNECTION": "x"}) {
		t.Fatal("MOSH_CONNECTION not detected")
	}
	if isRemoteSession(map[string]string{}) {
		t.Fatal("local detected as remote")
	}
}

func TestIsWSL(t *testing.T) {
	if !isWSL(map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}) {
		t.Fatal("WSL_DISTRO_NAME not detected")
	}
	if !isWSL(map[string]string{"WSL_INTEROP": "1"}) {
		t.Fatal("WSL_INTEROP not detected")
	}
	if isWSL(map[string]string{}) {
		t.Fatal("non-WSL detected")
	}
}

func TestReadClipboardText(t *testing.T) {
	// On test hosts no clipboard helpers are guaranteed; the contract is
	// "" rather than an error.
	got := ReadClipboardText()
	_ = got
}

func TestCopyToClipboardError(t *testing.T) {
	// Copying with no helpers available yields an actionable error (the
	// exact message depends on the host's display env).
	err := CopyToClipboard("hello")
	if err == nil {
		// A clipboard helper exists on this host (e.g., pbcopy on macOS).
		return
	}
	message := err.Error()
	if !strings.Contains(message, "Clipboard unavailable") {
		t.Fatalf("error = %q", message)
	}
}

func TestOsc52Emit(t *testing.T) {
	// Capture stdout.
	saved := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	if !emitOsc52("hello") {
		t.Fatal("small payload rejected")
	}
	w.Close()
	os.Stdout = saved
	var buf strings.Builder
	temp := make([]byte, 256)
	n, _ := r.Read(temp)
	buf.Write(temp[:n])
	output := buf.String()
	if !strings.HasPrefix(output, "\x1b]52;c;") || !strings.HasSuffix(output, "\x07") {
		t.Fatalf("osc52 = %q", output)
	}

	// Oversized payload rejected.
	big := strings.Repeat("x", maxOsc52EncodedLength)
	if emitOsc52(big) {
		t.Fatal("oversized accepted")
	}
}

func TestOpenBrowser(t *testing.T) {
	// The opener is injected suite-wide (TestMain); assert dispatch and
	// that restoring the seam still records.
	resetOpenedBrowserTargets()
	OpenBrowser("https://browser-seam.test/path")
	targets := openedBrowserTargets()
	if len(targets) != 1 || targets[0] != "https://browser-seam.test/path" {
		t.Fatalf("targets = %v", targets)
	}

	// An explicit swap wins and restores cleanly.
	var captured string
	restore := SetBrowserOpener(func(target string) { captured = target })
	OpenBrowser("https://swapped.test")
	if captured != "https://swapped.test" {
		t.Fatalf("captured = %q", captured)
	}
	restore()
	OpenBrowser("https://restored.test")
	targets = openedBrowserTargets()
	if len(targets) != 2 || targets[1] != "https://restored.test" {
		t.Fatalf("restored = %v", targets)
	}
}

func TestKeyDisplayText(t *testing.T) {
	if got := KeyDisplayText("app.interrupt"); got != "escape" {
		t.Fatalf("interrupt = %q", got)
	}
	if got := KeyDisplayText("app.model.cycleForward"); got != "ctrl+p" {
		t.Fatalf("model = %q", got)
	}
	// No defaults: empty.
	if got := KeyDisplayText("app.session.new"); got != "" {
		t.Fatalf("no-default = %q", got)
	}
	// Unknown binding: empty.
	if got := KeyDisplayText("not.a.binding"); got != "" {
		t.Fatalf("unknown = %q", got)
	}
}

// Ports of the clipboard fixes: PI_CLIPBOARD_OSC52 (3e477eab5) and Termux
// on Android (592fb57b7).
func TestIsRemoteSessionHonorsOsc52Override(t *testing.T) {
	if isRemoteSession(map[string]string{}) {
		t.Fatal("plain session counted remote")
	}
	if !isRemoteSession(map[string]string{"PI_CLIPBOARD_OSC52": "1"}) {
		t.Fatal("OSC 52 override ignored")
	}
	if !isRemoteSession(map[string]string{"SSH_CONNECTION": "1"}) {
		t.Fatal("SSH session ignored")
	}
}

func TestReadClipboardTermuxRunsOffLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux uses the full command set")
	}
	// On android/darwin, Termux must still be consulted: the helper is
	// absent here, so the read stays empty but must not panic or skip the
	// branch. TERMUX_VERSION being present selects the termux command.
	t.Setenv("TERMUX_VERSION", "0.118")
	if text := ReadClipboardText(); text != "" {
		t.Fatalf("text = %q", text)
	}
}
