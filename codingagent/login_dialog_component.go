package codingagent

// Port of modes/interactive/components/login-dialog.ts: the OAuth login
// dialog replacing the editor during auth flows — URL/device-code
// display with hyperlinks, manual-input and prompt flows with pending
// resolution, informational/waiting/progress states, and cancel.

import (
	"fmt"
	"runtime"

	"github.com/gladmo/openagent/tui"
)

// LoginDialogComponent renders the login flow.
type LoginDialogComponent struct {
	*tui.VStack

	providerID    string
	onComplete    func(success bool, message string)
	cancelled     bool
	inputValue    string
	inputActive   bool
	inputPrompt   string
	contentLines  []string
	pendingSubmit func(value string, err error)
}

// NewLoginDialogComponent builds the dialog.
func NewLoginDialogComponent(providerID string, onComplete func(success bool, message string), providerNameOverride, titleOverride string) *LoginDialogComponent {
	providerName := providerID
	if providerNameOverride != "" {
		providerName = providerNameOverride
	}
	title := titleOverride
	if title == "" {
		title = "Login to " + providerName
	}
	component := &LoginDialogComponent{
		VStack:     tui.NewVStack(0),
		providerID: providerID,
		onComplete: onComplete,
	}
	component.VStack.AddChild(tui.NewText(title, 1, 0, nil), nil)
	return component
}

// Cancelled reports the cancellation state (tests).
func (c *LoginDialogComponent) Cancelled() bool { return c.cancelled }

// hyperlink wraps text in an OSC 8 link.
func hyperlink(url, text string) string {
	return "\x1b]8;;" + url + "\x07" + text + "\x1b]8;;\x07"
}

// clickHint renders the platform Cmd/Ctrl+click hint.
func clickHint(url string) string {
	hint := "Ctrl+click to open"
	if runtime.GOOS == "darwin" {
		hint = "Cmd+click to open"
	}
	return hyperlink(url, hint)
}

// rebuild renders the current dialog state.
func (c *LoginDialogComponent) rebuild() {
	c.VStack = tui.NewVStack(0)
	for _, line := range c.contentLines {
		c.VStack.AddChild(tui.NewText(line, 1, 0, nil), nil)
	}
	if c.inputActive {
		if c.inputValue != "" {
			c.VStack.AddChild(tui.NewText("> "+c.inputValue, 0, 0, nil), nil)
		} else if c.inputPrompt != "" {
			c.VStack.AddChild(tui.NewText(c.inputPrompt, 0, 0, nil), nil)
			c.VStack.AddChild(tui.NewText("(esc to cancel)", 1, 0, nil), nil)
		}
	}
}

// ShowAuth displays the auth URL and opens the browser.
func (c *LoginDialogComponent) ShowAuth(url string, instructions string) {
	c.contentLines = []string{"", hyperlink(url, url), clickHint(url)}
	if instructions != "" {
		c.contentLines = append(c.contentLines, "", instructions)
	}
	OpenBrowser(url)
	c.inputActive = false
	c.rebuild()
}

// ShowDeviceCode displays the verification URL + user code.
func (c *LoginDialogComponent) ShowDeviceCode(verificationURI, userCode string) {
	c.contentLines = []string{
		"",
		hyperlink(verificationURI, verificationURI),
		clickHint(verificationURI),
		"",
		"Enter code: " + userCode,
	}
	c.inputActive = false
	c.rebuild()
}

// ShowManualInput prompts for a code/URL, resolving on submit.
func (c *LoginDialogComponent) ShowManualInput(prompt string, submit func(value string, err error)) {
	c.contentLines = append(c.contentLines, "", prompt)
	c.inputActive = true
	c.inputPrompt = prompt
	c.inputValue = ""
	c.pendingSubmit = submit
	c.rebuild()
}

// ShowPrompt appends a labeled input (preserving prior URL content).
func (c *LoginDialogComponent) ShowPrompt(message, placeholder string, submit func(value string, err error)) {
	c.contentLines = append(c.contentLines, "", message)
	if placeholder != "" {
		c.contentLines = append(c.contentLines, "e.g., "+placeholder)
	}
	c.inputActive = true
	c.inputPrompt = message
	c.inputValue = ""
	c.pendingSubmit = submit
	c.rebuild()
}

// ShowDetails renders provider-owned informational lines.
func (c *LoginDialogComponent) ShowDetails(lines []string) {
	c.contentLines = append([]string{""}, lines...)
	c.inputActive = false
	c.rebuild()
}

// AuthInfoLink is one provider info link.
type AuthInfoLink struct {
	Label string
	URL   string
}

// ShowInfo renders a message with links and an optional close hint.
func (c *LoginDialogComponent) ShowInfo(message string, links []AuthInfoLink, showCloseHint bool) {
	c.contentLines = append(c.contentLines, "", message)
	for _, link := range links {
		text := link.URL
		if link.Label != "" {
			text = link.Label + ": " + link.URL
		}
		c.contentLines = append(c.contentLines, hyperlink(link.URL, text))
	}
	if showCloseHint {
		c.contentLines = append(c.contentLines, "", "(esc to close)")
	}
	c.rebuild()
}

// ShowWaiting renders a polling message.
func (c *LoginDialogComponent) ShowWaiting(message string) {
	c.contentLines = append(c.contentLines, "", message, "(esc to cancel)")
	c.rebuild()
}

// ShowProgress appends one progress line.
func (c *LoginDialogComponent) ShowProgress(message string) {
	c.contentLines = append(c.contentLines, message)
	c.rebuild()
}

// Cancel aborts the flow.
func (c *LoginDialogComponent) Cancel() {
	c.cancelled = true
	if c.pendingSubmit != nil {
		c.pendingSubmit("", fmt.Errorf("Login cancelled"))
		c.pendingSubmit = nil
	}
	if c.onComplete != nil {
		c.onComplete(false, "Login cancelled")
	}
}

// Complete finishes the flow.
func (c *LoginDialogComponent) Complete(success bool, message string) {
	if c.onComplete != nil {
		c.onComplete(success, message)
	}
}

// HandleInput drives cancellation and typing.
func (c *LoginDialogComponent) HandleInput(key string) {
	if key == "escape" || key == "ctrl+c" {
		c.Cancel()
		return
	}
	if c.inputActive {
		if key == "enter" {
			value := c.inputValue
			if c.pendingSubmit != nil {
				c.pendingSubmit(value, nil)
				c.pendingSubmit = nil
			}
			return
		}
		if key == "backspace" {
			if len(c.inputValue) > 0 {
				c.inputValue = c.inputValue[:len(c.inputValue)-1]
				c.rebuild()
			}
			return
		}
		if len(key) == 1 {
			c.inputValue += key
			c.rebuild()
		}
	}
}
