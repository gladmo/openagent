package codingagent

// Port of utils/clipboard.ts, utils/open-browser.ts, and modes/
// interactive/tui-renderer.ts: clipboard (pbcopy/clip/wl-copy/xclip/
// xsel/termux + OSC 52 with remote/headless/WSL routing), the
// no-shell browser opener, and the interactive TUI composition root.

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// maxOsc52EncodedLength bounds OSC 52 payloads.
const maxOsc52EncodedLength = 100_000

// isRemoteSession reports SSH/Mosh clients (whose clipboard lives on the
// client side). PI_CLIPBOARD_OSC52=1 covers remote clients that leave no
// SSH markers, such as multiplexer attaches or IDE daemons.
func isRemoteSession(env map[string]string) bool {
	return env["SSH_CONNECTION"] != "" || env["SSH_CLIENT"] != "" || env["MOSH_CONNECTION"] != "" || env["PI_CLIPBOARD_OSC52"] == "1"
}

// emitOsc52 writes the OSC 52 clipboard sequence; false when oversized.
func emitOsc52(text string) bool {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	if len(encoded) > maxOsc52EncodedLength {
		return false
	}
	_, _ = os.Stdout.WriteString("\x1b]52;c;" + encoded + "\x07")
	return true
}

// runClipboardCommand executes one clipboard helper with a timeout;
// nil bytes = failure.
func runClipboardCommand(command string, args []string, input string, timeoutMs int) ([]byte, bool) {
	cmd := exec.Command(command, args...)
	// Detach on POSIX so the helper leaves pi's process group and Ctrl+Z
	// suspend (SIGTSTP to the group) does not stop the clipboard owner.
	cmd.SysProcAttr = detachedSysProcAttr()
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	} else {
		cmd.Stdin = nil
	}
	var output strings.Builder
	cmd.Stdout = &output
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		waitErr = <-done
		return nil, false
	}
	// A non-zero exit is a failure even without a timeout: clipboard
	// helpers report missing X access / empty clipboards this way, and
	// treating it as success skips the OSC 52 fallback.
	if waitErr != nil {
		return nil, false
	}
	return []byte(output.String()), true
}

// isWSL detects the Windows Subsystem for Linux.
func isWSL(env map[string]string) bool {
	return env["WSL_DISTRO_NAME"] != "" || env["WSL_INTEROP"] != ""
}

// ReadClipboardText reads plain text from the system clipboard ("" when
// unavailable).
func ReadClipboardText() string {
	env := envMap()
	type command struct {
		name string
		args []string
	}
	var commands []command
	// Termux reports platform "android", not "linux".
	if env["TERMUX_VERSION"] != "" {
		commands = append(commands, command{"termux-clipboard-get", nil})
	}
	if runtime.GOOS == "linux" {
		var linuxCommands []command
		if env["WAYLAND_DISPLAY"] != "" {
			commands = append(commands, command{"wl-paste", []string{"--no-newline", "--type", "text"}})
		}
		if env["DISPLAY"] != "" {
			linuxCommands = append(linuxCommands, command{"xclip", []string{"-selection", "clipboard", "-out"}})
			linuxCommands = append(linuxCommands, command{"xsel", []string{"--clipboard", "--output"}})
		}
		commands = append(commands, linuxCommands...)
		for _, entry := range commands {
			if bytes, ok := runClipboardCommand(entry.name, entry.args, "", 5000); ok {
				return string(bytes)
			}
		}
	}
	return ""
}

func envMap() map[string]string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	return env
}

// CopyToClipboard writes text through platform commands then OSC 52
// routing; errors match the reference's actionable messages.
func CopyToClipboard(text string) error {
	platform := runtime.GOOS
	env := envMap()
	copied := false

	type command struct {
		name string
		args []string
	}
	var commands []command
	switch platform {
	case "darwin":
		commands = append(commands, command{"pbcopy", nil})
	case "windows":
		commands = append(commands, command{"clip", nil})
	case "linux":
		if env["TERMUX_VERSION"] != "" {
			commands = append(commands, command{"termux-clipboard-set", nil})
		}
		if env["WAYLAND_DISPLAY"] != "" {
			commands = append(commands, command{"wl-copy", nil})
		}
		if env["DISPLAY"] != "" {
			commands = append(commands, command{"xclip", []string{"-selection", "clipboard"}})
			commands = append(commands, command{"xsel", []string{"--clipboard", "--input"}})
		}
	}
	for _, entry := range commands {
		if _, ok := runClipboardCommand(entry.name, entry.args, text, 5000); ok {
			copied = true
			break
		}
	}

	osc52Emitted := false
	if !copied && platform == "linux" && isWSL(env) {
		if env["WT_SESSION"] != "" {
			osc52Emitted = emitOsc52(text)
		}
		copied = osc52Emitted
	}

	headless := platform == "linux" && env["DISPLAY"] == "" && env["WAYLAND_DISPLAY"] == "" && env["TERMUX_VERSION"] == ""
	oversized := false
	if !osc52Emitted && (isRemoteSession(env) || (!copied && headless)) {
		if emitOsc52(text) {
			copied = true
		} else {
			oversized = true
		}
	}

	if copied {
		return nil
	}
	if oversized {
		return fmt.Errorf("Clipboard unavailable: text exceeds the OSC 52 size limit")
	}
	if platform == "linux" {
		if env["TERMUX_VERSION"] != "" {
			return fmt.Errorf("Clipboard unavailable: install the Termux:API app and `termux-api` package")
		}
		if env["WAYLAND_DISPLAY"] != "" {
			return fmt.Errorf("Clipboard unavailable: install `wl-clipboard` (`wl-copy`) or check Wayland access")
		}
		if env["DISPLAY"] != "" {
			return fmt.Errorf("Clipboard unavailable: install `xclip` or `xsel`, or check X11 access")
		}
	}
	return fmt.Errorf("Clipboard unavailable")
}

// ============================================================================
// open-browser.ts
// ============================================================================

// browserOpener is the injection seam: tests install a recorder so no
// suite ever launches a real browser.
var browserOpener = launchPlatformBrowser

// SetBrowserOpener replaces the opener and returns the restore func.
func SetBrowserOpener(opener func(target string)) func() {
	previous := browserOpener
	browserOpener = opener
	return func() { browserOpener = previous }
}

// OpenBrowser opens a URL or file in the platform handler (honors the
// injection seam). Never invokes a shell (Windows rundll32 avoids
// cmd.exe metacharacter re-parsing); launcher failures are swallowed
// (best-effort).
func OpenBrowser(target string) {
	browserOpener(target)
}

// launchPlatformBrowser is the real platform launcher.
func launchPlatformBrowser(target string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}

// KeyDisplayText resolves a binding's display key (first default).
func KeyDisplayText(binding string) string {
	definitions := AppKeybindings()
	if definition, ok := definitions[Keybinding(binding)]; ok && len(definition.DefaultKeys) > 0 {
		return string(definition.DefaultKeys[0])
	}
	return ""
}
