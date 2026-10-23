package tui

// TuiBase behavior: focus management with overlay restore, the overlay stack,
// render scheduling (16ms throttle, immediate input path), input listener
// chain, and OSC color/scheme queries. Continuation of the tui.ts port.

import (
	"regexp"
	"strconv"
	"time"

	"strings"

	"os"
	"os/signal"
	"syscall"
)

var cellSizeResponsePattern = regexp.MustCompile(`^\x1b\[6;(\d+);(\d+)t$`)

// GetShowHardwareCursor reports hardware cursor display.
func (t *TuiBase) GetShowHardwareCursor() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.showHardwareCursor
}

// SetShowHardwareCursor toggles hardware cursor display.
func (t *TuiBase) SetShowHardwareCursor(enabled bool) {
	t.mu.Lock()
	if t.showHardwareCursor == enabled {
		t.mu.Unlock()
		return
	}
	t.showHardwareCursor = enabled
	if !enabled {
		t.hideTerminalCursorLocked()
	}
	t.mu.Unlock()
	t.RequestRender(false)
}

// GetClearOnShrink reports whether shrinking content triggers a full
// re-render clearing empty rows.
func (t *TuiBase) GetClearOnShrink() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.clearOnShrink
}

// SetClearOnShrink toggles full re-render on content shrink: true clears
// empty rows; false (default) leaves them (fewer redraws on slow terminals).
func (t *TuiBase) SetClearOnShrink(enabled bool) {
	t.mu.Lock()
	t.clearOnShrink = enabled
	t.mu.Unlock()
}

// GetFocusedComponent returns the focused component (nil when none).
func (t *TuiBase) GetFocusedComponent() Component {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.focusedComponent
}

// SetFocus focuses a component, clearing overlay restore state.
func (t *TuiBase) SetFocus(component Component) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.setFocusInternal(component, false)
}

// setFocusInternal applies focus with an overlay-restore policy. Caller
// holds t.mu.
func (t *TuiBase) setFocusInternal(component Component, preserveOverlayRestore bool) {
	previousFocus := t.focusedComponent
	nextFocus := component
	var previousFocusedOverlay *overlayStackEntry
	if previousFocus != nil {
		previousFocusedOverlay = t.findVisibleOverlayByComponent(previousFocus)
	}
	nextFocusIsOverlay := false
	if nextFocus != nil {
		for _, entry := range t.overlayStack {
			if entry.component == nextFocus {
				nextFocusIsOverlay = true
				break
			}
		}
	}
	restoreState := t.getVisibleOverlayFocusRestoreLocked()
	if nextFocus != nil && !nextFocusIsOverlay {
		if restoreState.status == "blocked" && sameComponent(restoreState.blockedBy, previousFocus) {
			if restoreState.resume.status == "focus-target" || !t.isComponentMountedLocked(restoreState.blockedBy) {
				nextFocus = t.resolveBlockedOverlayFocusResumeLocked(restoreState)
			} else {
				t.overlayFocusRestore = overlayFocusRestoreState{
					status:    "blocked",
					overlay:   restoreState.overlay,
					blockedBy: nextFocus,
					resume:    restoreState.resume,
				}
			}
		} else if previousFocusedOverlay != nil &&
			restoreState.status != "inactive" &&
			restoreState.overlay == previousFocusedOverlay &&
			!t.isOverlayFocusAncestorLocked(previousFocusedOverlay, nextFocus) {
			t.overlayFocusRestore = overlayFocusRestoreState{
				status:    "blocked",
				overlay:   previousFocusedOverlay,
				blockedBy: nextFocus,
				resume:    &overlayBlockedFocusResume{status: "restore-overlay"},
			}
		}
	} else if nextFocus == nil {
		if restoreState.status == "blocked" && sameComponent(restoreState.blockedBy, previousFocus) {
			nextFocus = t.resolveBlockedOverlayFocusResumeLocked(restoreState)
		} else if !preserveOverlayRestore {
			t.clearOverlayFocusRestoreLocked()
		}
	}

	if focused, ok := t.focusedComponent.(Focusable); ok {
		focused.SetFocused(false)
	}

	t.focusedComponent = nextFocus

	if focused, ok := nextFocus.(Focusable); ok {
		focused.SetFocused(true)
	}

	if nextFocus != nil {
		if focusedOverlay := t.findVisibleOverlayByComponent(nextFocus); focusedOverlay != nil {
			t.overlayFocusRestore = overlayFocusRestoreState{status: "eligible", overlay: focusedOverlay}
		}
	}
}

func sameComponent(a, b Component) bool {
	return a == nil && b == nil || a != nil && b != nil && a == b
}

func (t *TuiBase) clearOverlayFocusRestoreLocked() {
	t.overlayFocusRestore = overlayFocusRestoreState{status: "inactive"}
}

func (t *TuiBase) clearOverlayFocusRestoreForLocked(overlay *overlayStackEntry) {
	if t.overlayFocusRestore.status != "inactive" && t.overlayFocusRestore.overlay == overlay {
		t.clearOverlayFocusRestoreLocked()
	}
}

func (t *TuiBase) resolveBlockedOverlayFocusResumeLocked(restoreState overlayFocusRestoreState) Component {
	if restoreState.resume.status == "restore-overlay" {
		return restoreState.overlay.component
	}
	t.clearOverlayFocusRestoreLocked()
	return restoreState.resume.target
}

func (t *TuiBase) getVisibleOverlayFocusRestoreLocked() overlayFocusRestoreState {
	restoreState := t.overlayFocusRestore
	if restoreState.status == "inactive" {
		return restoreState
	}
	if !t.overlayStackContains(restoreState.overlay) || !t.isOverlayVisibleLocked(restoreState.overlay) {
		return overlayFocusRestoreState{status: "inactive"}
	}
	return restoreState
}

func (t *TuiBase) overlayStackContains(entry *overlayStackEntry) bool {
	for _, candidate := range t.overlayStack {
		if candidate == entry {
			return true
		}
	}
	return false
}

func (t *TuiBase) isOverlayFocusAncestorLocked(entry *overlayStackEntry, component Component) bool {
	visited := map[Component]bool{}
	current := entry.preFocus
	for current != nil && !visited[current] {
		visited[current] = true
		if current == component {
			return true
		}
		if overlay := t.findOverlayEntryByComponent(current); overlay != nil {
			current = overlay.preFocus
		} else {
			current = nil
		}
	}
	return false
}

func (t *TuiBase) findOverlayEntryByComponent(component Component) *overlayStackEntry {
	for _, overlay := range t.overlayStack {
		if overlay.component == component {
			return overlay
		}
	}
	return nil
}

func (t *TuiBase) findVisibleOverlayByComponent(component Component) *overlayStackEntry {
	for _, entry := range t.overlayStack {
		if entry.component == component && t.isOverlayVisibleLocked(entry) {
			return entry
		}
	}
	return nil
}

func (t *TuiBase) retargetOverlayPreFocusLocked(removed *overlayStackEntry) {
	for _, overlay := range t.overlayStack {
		if overlay != removed && overlay.preFocus == removed.component {
			overlay.preFocus = removed.preFocus
		}
	}
}

// getMountedRoots returns the component roots visible for focus checks.
func (t *TuiBase) getMountedRoots() []Component {
	return t.Children
}

func (t *TuiBase) isComponentMountedLocked(component Component) bool {
	for _, child := range t.getMountedRoots() {
		if containsComponent(child, component) {
			return true
		}
	}
	return false
}

func containsComponent(root, target Component) bool {
	if root == target {
		return true
	}
	container, ok := root.(*Container)
	if !ok {
		if embedded, ok := root.(interface{ ContainerChildren() []Component }); ok {
			for _, child := range embedded.ContainerChildren() {
				if containsComponent(child, target) {
					return true
				}
			}
		}
		return false
	}
	for _, child := range container.Children {
		if containsComponent(child, target) {
			return true
		}
	}
	return false
}

// ShowOverlay shows an overlay component with configurable positioning and
// sizing; the returned handle controls its visibility.
func (t *TuiBase) ShowOverlay(component Component, options *OverlayOptions) OverlayHandle {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.focusOrderCounter++
	entry := &overlayStackEntry{
		component:  component,
		options:    options,
		preFocus:   t.focusedComponent,
		hidden:     false,
		focusOrder: t.focusOrderCounter,
	}
	t.overlayStack = append(t.overlayStack, entry)
	// Only focus if the overlay is actually visible.
	nonCapturing := options != nil && options.NonCapturing
	if !nonCapturing && t.isOverlayVisibleLocked(entry) {
		t.setFocusInternal(component, false)
	}
	t.hideTerminalCursorLocked()
	t.requestRenderLocked(false)

	return &overlayHandle{base: t, entry: entry, component: component, options: options}
}

type overlayHandle struct {
	base      *TuiBase
	entry     *overlayStackEntry
	component Component
	options   *OverlayOptions
}

func (h *overlayHandle) Hide() {
	t := h.base
	t.mu.Lock()
	defer t.mu.Unlock()
	index := -1
	for i, candidate := range t.overlayStack {
		if candidate == h.entry {
			index = i
			break
		}
	}
	if index == -1 {
		return
	}
	t.clearOverlayFocusRestoreForLocked(h.entry)
	t.retargetOverlayPreFocusLocked(h.entry)
	t.overlayStack = append(t.overlayStack[:index], t.overlayStack[index+1:]...)
	if t.focusedComponent == h.component {
		topVisible := t.getTopmostVisibleOverlayLocked()
		if topVisible != nil {
			t.setFocusInternal(topVisible.component, false)
		} else {
			t.setFocusInternal(h.entry.preFocus, false)
		}
	}
	if len(t.overlayStack) == 0 {
		t.hideTerminalCursorLocked()
	}
	t.requestRenderLocked(false)
}

func (h *overlayHandle) SetHidden(hidden bool) {
	t := h.base
	t.mu.Lock()
	defer t.mu.Unlock()
	if h.entry.hidden == hidden {
		return
	}
	h.entry.hidden = hidden
	if hidden {
		t.clearOverlayFocusRestoreForLocked(h.entry)
		if t.focusedComponent == h.component {
			topVisible := t.getTopmostVisibleOverlayLocked()
			if topVisible != nil {
				t.setFocusInternal(topVisible.component, false)
			} else {
				t.setFocusInternal(h.entry.preFocus, false)
			}
		}
	} else {
		nonCapturing := h.options != nil && h.options.NonCapturing
		if !nonCapturing && t.isOverlayVisibleLocked(h.entry) {
			t.focusOrderCounter++
			h.entry.focusOrder = t.focusOrderCounter
			t.setFocusInternal(h.component, false)
		}
	}
	t.requestRenderLocked(false)
}

func (h *overlayHandle) IsHidden() bool {
	t := h.base
	t.mu.Lock()
	defer t.mu.Unlock()
	return h.entry.hidden
}

func (h *overlayHandle) Focus() {
	t := h.base
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.overlayStackContains(h.entry) || !t.isOverlayVisibleLocked(h.entry) {
		return
	}
	t.focusOrderCounter++
	h.entry.focusOrder = t.focusOrderCounter
	t.setFocusInternal(h.component, false)
	t.requestRenderLocked(false)
}

func (h *overlayHandle) Unfocus(unfocusOptions *OverlayUnfocusOptions) {
	t := h.base
	t.mu.Lock()
	defer t.mu.Unlock()
	isFocused := t.focusedComponent == h.component
	restoreState := t.overlayFocusRestore
	hasPendingRestore := restoreState.status != "inactive" && restoreState.overlay == h.entry
	if !isFocused && !hasPendingRestore {
		return
	}
	if restoreState.status == "blocked" &&
		restoreState.overlay == h.entry &&
		t.focusedComponent == restoreState.blockedBy {
		if unfocusOptions != nil {
			t.overlayFocusRestore = overlayFocusRestoreState{
				status:    "blocked",
				overlay:   h.entry,
				blockedBy: restoreState.blockedBy,
				resume:    &overlayBlockedFocusResume{status: "focus-target", target: unfocusOptions.Target},
			}
		} else {
			t.clearOverlayFocusRestoreLocked()
		}
		t.requestRenderLocked(false)
		return
	}
	t.clearOverlayFocusRestoreForLocked(h.entry)
	if isFocused || unfocusOptions != nil {
		topVisible := t.getTopmostVisibleOverlayLocked()
		var fallbackTarget Component
		if topVisible != nil && topVisible != h.entry {
			fallbackTarget = topVisible.component
		} else {
			fallbackTarget = h.entry.preFocus
		}
		if unfocusOptions != nil {
			t.setFocusInternal(unfocusOptions.Target, false)
		} else {
			t.setFocusInternal(fallbackTarget, false)
		}
	}
	t.requestRenderLocked(false)
}

func (h *overlayHandle) IsFocused() bool {
	t := h.base
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.focusedComponent == h.component
}

func (h *overlayHandle) GetBounds() *OverlayBounds {
	t := h.base
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.overlayStackContains(h.entry) || !t.isOverlayVisibleLocked(h.entry) || h.entry.bounds == nil {
		return nil
	}
	bounds := *h.entry.bounds
	return &bounds
}

// HideOverlay hides the topmost overlay and restores previous focus.
func (t *TuiBase) HideOverlay() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.overlayStack) == 0 {
		return
	}
	overlay := t.overlayStack[len(t.overlayStack)-1]
	t.clearOverlayFocusRestoreForLocked(overlay)
	t.retargetOverlayPreFocusLocked(overlay)
	t.overlayStack = t.overlayStack[:len(t.overlayStack)-1]
	if t.focusedComponent == overlay.component {
		topVisible := t.getTopmostVisibleOverlayLocked()
		if topVisible != nil {
			t.setFocusInternal(topVisible.component, false)
		} else {
			t.setFocusInternal(overlay.preFocus, false)
		}
	}
	if len(t.overlayStack) == 0 {
		t.hideTerminalCursorLocked()
	}
	t.requestRenderLocked(false)
}

// hideTerminalCursorLocked hides the cursor while running; after Stop the
// shell owns the cursor and it must stay visible. Caller holds t.mu.
func (t *TuiBase) hideTerminalCursorLocked() {
	if !t.stoppedFlag {
		t.terminalRef.HideCursor()
	}
}

// HasOverlay reports whether any overlay is visible.
func (t *TuiBase) HasOverlay() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, overlay := range t.overlayStack {
		if t.isOverlayVisibleLocked(overlay) {
			return true
		}
	}
	return false
}

// hasOverlayEntries reports whether any overlay entries exist.
func (t *TuiBase) hasOverlayEntries() bool {
	return len(t.overlayStack) > 0
}

// isOverlayFocusedLocked reports whether focus is a visible overlay.
func (t *TuiBase) isOverlayFocusedLocked() bool {
	for _, entry := range t.overlayStack {
		if entry.component == t.focusedComponent && t.isOverlayVisibleLocked(entry) {
			return true
		}
	}
	return false
}

// resolveMouseFocusTarget keeps overlay containers as keyboard focus owners
// when a nested control is clicked.
func (t *TuiBase) resolveMouseFocusTargetLocked(component Component) Component {
	for index := len(t.overlayStack) - 1; index >= 0; index-- {
		overlay := t.overlayStack[index]
		if t.isOverlayVisibleLocked(overlay) && containsComponent(overlay.component, component) {
			return overlay.component
		}
	}
	return component
}

// dispatchMouseToOverlayLocked dispatches to the visually topmost overlay
// under the pointer.
func (t *TuiBase) dispatchMouseToOverlayLocked(event TuiMouseEvent) (bool, *TuiMouseDispatchResult) {
	for index := len(t.renderedOverlayLayouts) - 1; index >= 0; index-- {
		layout := t.renderedOverlayLayouts[index]
		if event.ScreenX < layout.col ||
			event.ScreenX >= layout.col+layout.width ||
			event.ScreenY < layout.row ||
			event.ScreenY >= layout.row+layout.height {
			continue
		}
		local := event
		local.X = event.ScreenX - layout.col
		local.Y = event.ScreenY - layout.row
		local.Width = layout.width
		local.Height = layout.height
		result := DispatchMouseEvent(layout.entry.component, local)
		if result != nil && result.Focus {
			result.FocusTarget = layout.entry.component
		}
		return true, result
	}
	return false, nil
}

// isOverlayVisibleLocked checks hidden state and the visible() callback.
func (t *TuiBase) isOverlayVisibleLocked(entry *overlayStackEntry) bool {
	if entry.hidden {
		return false
	}
	if entry.options != nil && entry.options.Visible != nil {
		return entry.options.Visible(t.terminalRef.Columns(), t.terminalRef.Rows())
	}
	return true
}

// getTopmostVisibleOverlayLocked finds the visual-frontmost visible capturing
// overlay.
func (t *TuiBase) getTopmostVisibleOverlayLocked() *overlayStackEntry {
	var topmost *overlayStackEntry
	for _, overlay := range t.overlayStack {
		if overlay.options != nil && overlay.options.NonCapturing || !t.isOverlayVisibleLocked(overlay) {
			continue
		}
		if topmost == nil || overlay.focusOrder > topmost.focusOrder {
			topmost = overlay
		}
	}
	return topmost
}

// Invalidate invalidates all mounted roots and overlays.
func (t *TuiBase) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, root := range t.getMountedRoots() {
		root.Invalidate()
	}
	for _, overlay := range t.overlayStack {
		overlay.component.Invalidate()
	}
}

// ExitRestorePrefix returns the mode resets written before the terminal's
// own resets when the process exits without Stop.
func (t *TuiBase) ExitRestorePrefix() string {
	prefix := "\x1b[?25h"
	if t.colorSchemeNotifications {
		prefix = "\x1b[?2031l" + prefix
	}
	return prefix
}

// ExitRestoreSuffix returns the mode resets written after the terminal's
// own resets when the process exits without Stop.
func (t *TuiBase) ExitRestoreSuffix() string {
	return ""
}

// installExitRestore arms a last-resort restore for exit paths that skip
// Stop, e.g. os.Exit or a fatal signal (#11170). Hard kills (SIGKILL) and
// panics on other goroutines cannot be covered; Go has no exit hook, so the
// terminal modes are restored from a signal handler before re-raising.
func (t *TuiBase) installExitRestore() {
	if t.exitRestoreSignals != nil {
		return
	}
	channel := make(chan os.Signal, 1)
	signal.Notify(channel, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	t.exitRestoreSignals = channel
	go func() {
		received, ok := <-channel
		if !ok {
			return
		}
		t.restoreTerminalForExit()
		// Re-raise with the default disposition so the exit status the
		// parent observes matches the signal.
		signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		if signaled, isSignal := received.(syscall.Signal); isSignal {
			_ = syscall.Kill(syscall.Getpid(), signaled)
		}
		os.Exit(128)
	}()
}

func (t *TuiBase) disposeExitRestore() {
	if t.exitRestoreSignals != nil {
		signal.Stop(t.exitRestoreSignals)
		close(t.exitRestoreSignals)
		t.exitRestoreSignals = nil
	}
}

func (t *TuiBase) restoreTerminalForExit() {
	t.mu.Lock()
	stopped := t.stoppedFlag
	prefix, suffix := t.ExitRestorePrefix(), t.ExitRestoreSuffix()
	t.mu.Unlock()
	if stopped {
		return
	}
	if restorer, ok := t.terminalRef.(interface {
		RestoreOnExit(before, after string)
	}); ok {
		restorer.RestoreOnExit(prefix, suffix)
	}
}

// Start begins terminal input and the first render.
func (t *TuiBase) Start() {
	t.mu.Lock()
	t.installExitRestore()
	t.stoppedFlag = false
	if t.BeforeTerminalStart != nil {
		t.BeforeTerminalStart()
	}
	t.terminalRef.Start(t.handleTerminalInput, func() { t.RequestRender(false) })
	if t.AfterTerminalStart != nil {
		t.AfterTerminalStart()
	}
	t.terminalRef.HideCursor()
	if t.colorSchemeNotifications {
		t.terminalRef.Write("\x1b[?2031h")
	}
	t.queryCellSizeLocked()
	t.requestRenderLocked(false)
	t.mu.Unlock()
}

// AddInputListener registers a listener; the returned func unregisters it.
func (t *TuiBase) AddInputListener(listener TuiInputListener) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.inputListeners = append(t.inputListeners, listener)
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		for i, candidate := range t.inputListeners {
			if &candidate == &listener {
				t.inputListeners = append(t.inputListeners[:i], t.inputListeners[i+1:]...)
				return
			}
		}
	}
}

// RemoveInputListener is intentionally absent: func values are not
// comparable in Go, and the reference's removeInputListener has no callers —
// AddInputListener's disposer is the single removal path (repo registration
// convention).

// schemeListenerEntry registers scheme listeners by id: func values are not
// comparable in Go, so removal uses the registration id.
type schemeListenerEntry struct {
	id int
	fn func(TerminalColorScheme)
}

var schemeListenerCounter int

// OnTerminalColorSchemeChange registers a scheme listener; the returned func
// unregisters it.
func (t *TuiBase) OnTerminalColorSchemeChange(listener func(TerminalColorScheme)) func() {
	t.mu.Lock()
	defer t.mu.Unlock()
	schemeListenerCounter++
	entry := schemeListenerEntry{id: schemeListenerCounter, fn: listener}
	t.terminalColorSchemeListeners = append(t.terminalColorSchemeListeners, entry)
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		for i, candidate := range t.terminalColorSchemeListeners {
			if candidate.id == entry.id {
				t.terminalColorSchemeListeners = append(t.terminalColorSchemeListeners[:i], t.terminalColorSchemeListeners[i+1:]...)
				return
			}
		}
	}
}

// SetTerminalColorSchemeNotifications toggles ?2031 notifications.
func (t *TuiBase) SetTerminalColorSchemeNotifications(enabled bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.colorSchemeNotifications == enabled {
		return
	}
	t.colorSchemeNotifications = enabled
	if !t.stoppedFlag {
		if enabled {
			t.terminalRef.Write("\x1b[?2031h")
		} else {
			t.terminalRef.Write("\x1b[?2031l")
		}
	}
}

// queryCellSizeLocked queries the terminal cell size (CSI 16 t) when images
// are supported. Caller holds t.mu.
func (t *TuiBase) queryCellSizeLocked() {
	if GetCapabilities().Images == ImageProtocolNone {
		return
	}
	t.terminalRef.Write("\x1b[16t")
}

// Stop restores the terminal.
func (t *TuiBase) Stop(options *TuiStopOptions) {
	if options == nil {
		options = &TuiStopOptions{}
	}
	t.mu.Lock()
	t.stoppedFlag = true
	t.cancelRenderTimerLocked()
	if t.colorSchemeNotifications {
		t.terminalRef.Write("\x1b[?2031l")
	}
	if t.BeforeTerminalStop != nil {
		t.BeforeTerminalStop(options)
	}
	t.terminalRef.ShowCursor()
	t.terminalRef.Stop()
	if t.AfterTerminalStop != nil {
		t.AfterTerminalStop(options)
	}
	t.disposeExitRestore()
	t.mu.Unlock()
}

// RenderNow renders synchronously, optionally resetting render state.
func (t *TuiBase) RenderNow(force bool) {
	t.mu.Lock()
	if force && t.ResetRenderState != nil {
		t.ResetRenderState()
	}
	t.renderRequested = false
	t.cancelRenderTimerLocked()
	t.lastRenderAt = t.Now()
	doRender := t.DoRender
	t.mu.Unlock()
	if doRender != nil {
		doRender()
	}
}

// RequestRender schedules a render; force resets render state and renders
// immediately.
func (t *TuiBase) RequestRender(force bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requestRenderLocked(force)
}

func (t *TuiBase) requestRenderLocked(force bool) {
	if force {
		if t.ResetRenderState != nil {
			t.ResetRenderState()
		}
		t.requestImmediateRenderLocked()
		return
	}
	if t.renderRequested {
		return
	}
	t.renderRequested = true
	t.scheduleNextTick(func() { t.mu.Lock(); defer t.mu.Unlock(); t.scheduleRenderLocked() })
}

func (t *TuiBase) requestImmediateRenderLocked() {
	t.cancelRenderTimerLocked()
	t.renderRequested = true
	if t.immediateRenderScheduled {
		return
	}
	t.immediateRenderScheduled = true
	t.scheduleNextTick(func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.immediateRenderScheduled = false
		if t.stoppedFlag || !t.renderRequested {
			return
		}
		// A previously queued scheduleRender() can create a timer before this
		// callback runs; user input must preempt that throttled frame.
		t.cancelRenderTimerLocked()
		t.renderRequested = false
		t.lastRenderAt = t.Now()
		if t.DoRender != nil {
			doRender := t.DoRender
			t.mu.Unlock()
			// Re-lock through a defer: a panicking render must leave the
			// mutex balanced for the outer Unlock instead of turning the
			// original panic into "unlock of unlocked mutex".
			func() {
				defer t.mu.Lock()
				doRender()
			}()
		}
	})
}

// scheduleNextTick runs fn asynchronously (Node's process.nextTick).
func (t *TuiBase) scheduleNextTick(fn func()) {
	if t.After != nil {
		t.After(0, fn)
	}
}

func (t *TuiBase) cancelRenderTimerLocked() {
	if t.renderTimer != nil {
		t.renderTimer.Stop()
		t.renderTimer = nil
	}
}

func (t *TuiBase) scheduleRenderLocked() {
	if t.stoppedFlag || t.renderTimer != nil || !t.renderRequested {
		return
	}
	elapsed := t.Now() - t.lastRenderAt
	delayMs := minRenderIntervalMs - int(elapsed/int64(time.Millisecond))
	if delayMs < 0 {
		delayMs = 0
	}
	t.renderTimer = t.After(time.Duration(delayMs)*time.Millisecond, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.renderTimer = nil
		if t.stoppedFlag || !t.renderRequested {
			return
		}
		t.renderRequested = false
		t.lastRenderAt = t.Now()
		if t.DoRender != nil {
			doRender := t.DoRender
			t.mu.Unlock()
			// Re-lock through a defer (see requestImmediateRenderLocked).
			func() {
				defer t.mu.Lock()
				doRender()
			}()
		}
		if t.renderRequested {
			t.scheduleRenderLocked()
		}
	})
}

// handleTerminalInput processes one decoded input chunk: OSC replies, scheme
// reports, listener chain, cell size, debug key, overlay focus restore, then
// focused-component dispatch.
func (t *TuiBase) handleTerminalInput(data string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.consumeOscColorResponseLocked(data) {
		return
	}
	if t.consumeTerminalColorSchemeReportLocked(data) {
		return
	}

	if len(t.inputListeners) > 0 {
		// Listeners run without the base lock: the alt screen's viewport
		// listener re-enters TuiBase APIs that acquire it.
		listeners := append([]TuiInputListener(nil), t.inputListeners...)
		current := data
		t.mu.Unlock()
		consumed := false
		for _, listener := range listeners {
			result := listener(current)
			if result != nil && result.Consume {
				consumed = true
				break
			}
			if result != nil && result.Data != nil {
				current = *result.Data
			}
		}
		t.mu.Lock()
		if consumed {
			return
		}
		if len(current) == 0 {
			return
		}
		data = current
	}

	// Consume cell size replies without blocking unrelated input.
	if t.consumeCellSizeResponseLocked(data) {
		return
	}

	// Global debug key (Shift+Ctrl+D).
	if MatchesKey(data, "shift+ctrl+d") && t.OnDebug != nil {
		debug := t.OnDebug
		t.mu.Unlock()
		debug()
		t.mu.Lock()
		return
	}

	// A focused overlay that became invisible redirects focus.
	focusedOverlay := t.findOverlayEntryByComponent(t.focusedComponent)
	if focusedOverlay != nil && !t.isOverlayVisibleLocked(focusedOverlay) {
		topVisible := t.getTopmostVisibleOverlayLocked()
		if topVisible != nil {
			t.setFocusInternal(topVisible.component, false)
		} else {
			t.setFocusInternal(focusedOverlay.preFocus, true)
		}
	}

	focusIsOverlay := t.findOverlayEntryByComponent(t.focusedComponent) != nil
	if !focusIsOverlay {
		restoreState := t.getVisibleOverlayFocusRestoreLocked()
		if restoreState.status == "eligible" {
			t.setFocusInternal(restoreState.overlay.component, false)
		} else if restoreState.status == "blocked" && !sameComponent(restoreState.blockedBy, t.focusedComponent) {
			if restoreState.resume.status == "restore-overlay" {
				t.setFocusInternal(restoreState.overlay.component, false)
			} else {
				t.clearOverlayFocusRestoreLocked()
				t.setFocusInternal(restoreState.resume.target, false)
			}
		}
	}

	// Pass input to the focused component (including Ctrl+C; the component
	// decides). Keyboard input is latency-sensitive: bypass the throttled
	// timer path.
	if handler, ok := t.focusedComponent.(InputHandler); ok {
		if IsKeyRelease(data) {
			if wants, ok := t.focusedComponent.(WantsKeyReleaseComponent); !ok || !wants.WantsKeyRelease() {
				return
			}
		}
		t.mu.Unlock()
		handler.HandleInput(data)
		t.mu.Lock()
		t.requestImmediateRenderLocked()
	}
}

func (t *TuiBase) consumeOscColorResponseLocked(data string) bool {
	response := ParseOscColorResponse(data)
	if response == nil || t.pendingOscColorReplies[int(response.Slot)] <= 0 {
		return false
	}

	slot := int(response.Slot)
	t.pendingOscColorReplies[slot]--
	if len(t.pendingOscColorQueries[slot]) > 0 {
		query := t.pendingOscColorQueries[slot][0]
		t.pendingOscColorQueries[slot] = t.pendingOscColorQueries[slot][1:]
		if query != nil && !query.settled {
			query.settled = true
			if query.timer != nil {
				query.timer.Stop()
				query.timer = nil
			}
			if query.resolve != nil {
				query.resolve(response.Rgb)
				query.resolve = nil
			}
		}
	}
	return true
}

func (t *TuiBase) consumeTerminalColorSchemeReportLocked(data string) bool {
	scheme := ParseTerminalColorSchemeReport(data)
	if scheme == "" {
		return false
	}
	listeners := make([]func(TerminalColorScheme), 0, len(t.terminalColorSchemeListeners))
	for _, entry := range t.terminalColorSchemeListeners {
		listeners = append(listeners, entry.fn)
	}
	t.mu.Unlock()
	for _, listener := range listeners {
		listener(scheme)
	}
	t.mu.Lock()
	return true
}

func (t *TuiBase) consumeCellSizeResponseLocked(data string) bool {
	// Response format: ESC [ 6 ; height ; width t
	match := cellSizeResponsePattern.FindStringSubmatch(data)
	if match == nil {
		return false
	}
	heightPx, _ := strconv.Atoi(match[1])
	widthPx, _ := strconv.Atoi(match[2])
	if heightPx <= 0 || widthPx <= 0 {
		return true
	}

	SetCellDimensions(CellDimensions{WidthPx: widthPx, HeightPx: heightPx})
	// Invalidate all components so images re-render with correct dimensions.
	for _, root := range t.getMountedRoots() {
		root.Invalidate()
	}
	for _, overlay := range t.overlayStack {
		overlay.component.Invalidate()
	}
	t.requestRenderLocked(false)
	return true
}

// overlayLayout is the resolved overlay geometry.
type overlayLayout struct {
	width        int
	row          int
	col          int
	maxHeight    int
	hasMaxHeight bool
}

// resolveOverlayLayoutLocked resolves overlay layout from options. Caller
// holds t.mu (only reads terminalRef via Columns/Rows).
func (t *TuiBase) resolveOverlayLayoutLocked(options *OverlayOptions, overlayHeight, termWidth, termHeight int) overlayLayout {
	opt := options
	if opt == nil {
		opt = &OverlayOptions{}
	}

	// Parse margin (clamp to non-negative)
	marginTop, marginRight, marginBottom, marginLeft := 0, 0, 0, 0
	switch m := opt.Margin.(type) {
	case int:
		marginTop, marginRight, marginBottom, marginLeft = m, m, m, m
	case OverlayMargin:
		if m.Top != nil {
			marginTop = maxInt(0, *m.Top)
		}
		if m.Right != nil {
			marginRight = maxInt(0, *m.Right)
		}
		if m.Bottom != nil {
			marginBottom = maxInt(0, *m.Bottom)
		}
		if m.Left != nil {
			marginLeft = maxInt(0, *m.Left)
		}
	}

	// Available space after margins
	availWidth := maxInt(1, termWidth-marginLeft-marginRight)
	availHeight := maxInt(1, termHeight-marginTop-marginBottom)

	// Resolve width
	width, ok := parseSizeValue(opt.Width, termWidth)
	if !ok {
		width = minInt(80, availWidth)
	}
	if opt.MinWidth != nil {
		width = maxInt(width, *opt.MinWidth)
	}
	width = maxInt(1, minInt(width, availWidth))

	// Resolve maxHeight
	layout := overlayLayout{}
	if maxHeight, ok := parseSizeValue(opt.MaxHeight, termHeight); ok {
		layout.maxHeight = maxInt(1, minInt(maxHeight, availHeight))
		layout.hasMaxHeight = true
	}

	// Effective height (may be clamped by maxHeight)
	effectiveHeight := overlayHeight
	if layout.hasMaxHeight && overlayHeight > layout.maxHeight {
		effectiveHeight = layout.maxHeight
	}

	// Resolve position
	var row int
	var col int

	if opt.Row != nil {
		if rowStr, isString := opt.Row.(string); isString {
			if match := sizeValuePattern.FindStringSubmatch(rowStr); match != nil {
				maxRow := maxInt(0, availHeight-effectiveHeight)
				percent := parseFloatJS(match[1]) / 100
				row = marginTop + int(jsFloor(float64(maxRow)*percent))
			} else {
				row = t.resolveAnchorRowLocked(AnchorCenter, effectiveHeight, availHeight, marginTop)
			}
		} else if rowValue, ok := parseSizeValue(opt.Row, termHeight); ok {
			row = rowValue
		}
	} else {
		anchor := opt.Anchor
		if anchor == "" {
			anchor = AnchorCenter
		}
		row = t.resolveAnchorRowLocked(anchor, effectiveHeight, availHeight, marginTop)
	}

	if opt.Col != nil {
		if colStr, isString := opt.Col.(string); isString {
			if match := sizeValuePattern.FindStringSubmatch(colStr); match != nil {
				maxCol := maxInt(0, availWidth-width)
				percent := parseFloatJS(match[1]) / 100
				col = marginLeft + int(jsFloor(float64(maxCol)*percent))
			} else {
				col = t.resolveAnchorColLocked(AnchorCenter, width, availWidth, marginLeft)
			}
		} else if colValue, ok := parseSizeValue(opt.Col, termWidth); ok {
			col = colValue
		}
	} else {
		anchor := opt.Anchor
		if anchor == "" {
			anchor = AnchorCenter
		}
		col = t.resolveAnchorColLocked(anchor, width, availWidth, marginLeft)
	}

	// Apply offsets
	if opt.OffsetY != nil {
		row += *opt.OffsetY
	}
	if opt.OffsetX != nil {
		col += *opt.OffsetX
	}

	// Clamp to terminal bounds (respecting margins)
	row = maxInt(marginTop, minInt(row, termHeight-marginBottom-effectiveHeight))
	col = maxInt(marginLeft, minInt(col, termWidth-marginRight-width))

	layout.width = width
	layout.row = row
	layout.col = col
	return layout
}

func (t *TuiBase) resolveAnchorRowLocked(anchor OverlayAnchor, height, availHeight, marginTop int) int {
	switch anchor {
	case AnchorTopLeft, AnchorTopCenter, AnchorTopRight:
		return marginTop
	case AnchorBottomLeft, AnchorBottomCenter, AnchorBottomRight:
		return marginTop + availHeight - height
	default: // left-center, center, right-center
		return marginTop + (availHeight-height)/2
	}
}

func (t *TuiBase) resolveAnchorColLocked(anchor OverlayAnchor, width, availWidth, marginLeft int) int {
	switch anchor {
	case AnchorTopLeft, AnchorLeftCenter, AnchorBottomLeft:
		return marginLeft
	case AnchorTopRight, AnchorRightCenter, AnchorBottomRight:
		return marginLeft + availWidth - width
	default: // top-center, center, bottom-center
		return marginLeft + (availWidth-width)/2
	}
}

// CompositeOverlays composites all overlays into content lines, sorted by
// focusOrder (higher = on top). Caller holds t.mu; doRender invokes it.
func (t *TuiBase) CompositeOverlays(lines []string, termWidth, termHeight int) []string {
	if len(t.overlayStack) == 0 {
		t.renderedOverlayLayouts = nil
		return lines
	}
	result := append([]string(nil), lines...)

	for _, entry := range t.overlayStack {
		entry.bounds = nil
	}

	type renderedEntry struct {
		entry        *overlayStackEntry
		overlayLines []string
		row          int
		col          int
		w            int
	}
	var rendered []renderedEntry
	minLinesNeeded := len(result)

	var visibleEntries []*overlayStackEntry
	for _, entry := range t.overlayStack {
		if t.isOverlayVisibleLocked(entry) {
			visibleEntries = append(visibleEntries, entry)
		}
	}
	// Stable sort by focusOrder.
	for i := 1; i < len(visibleEntries); i++ {
		for j := i; j > 0 && visibleEntries[j].focusOrder < visibleEntries[j-1].focusOrder; j-- {
			visibleEntries[j], visibleEntries[j-1] = visibleEntries[j-1], visibleEntries[j]
		}
	}
	for _, entry := range visibleEntries {
		// Layout with height=0 first: width and maxHeight don't depend on
		// overlay height.
		sizing := t.resolveOverlayLayoutLocked(entry.options, 0, termWidth, termHeight)

		overlayLines := t.resolveFakeCursors(entry.component.Render(sizing.width))

		if sizing.hasMaxHeight && len(overlayLines) > sizing.maxHeight {
			overlayLines = overlayLines[:sizing.maxHeight]
		}

		// Final row/col with actual height.
		final := t.resolveOverlayLayoutLocked(entry.options, len(overlayLines), termWidth, termHeight)
		entry.bounds = &OverlayBounds{Row: final.row, Col: final.col, Width: sizing.width, Height: len(overlayLines)}

		rendered = append(rendered, renderedEntry{entry: entry, overlayLines: overlayLines, row: final.row, col: final.col, w: sizing.width})
		if minLinesNeeded < final.row+len(overlayLines) {
			minLinesNeeded = final.row + len(overlayLines)
		}
	}
	t.renderedOverlayLayouts = nil
	for _, r := range rendered {
		t.renderedOverlayLayouts = append(t.renderedOverlayLayouts, renderedOverlayLayout{
			entry: r.entry, row: r.row, col: r.col, width: r.w, height: len(r.overlayLines),
		})
	}

	// Pad to at least terminal height so overlays have screen-relative
	// positions. Excludes the historical high-water mark: it caused
	// self-reinforcing inflation pushing content into scrollback on widen.
	workingHeight := maxInt(len(result), maxInt(termHeight, minLinesNeeded))

	for len(result) < workingHeight {
		result = append(result, "")
	}

	viewportStart := maxInt(0, workingHeight-termHeight)

	for _, r := range rendered {
		for i := 0; i < len(r.overlayLines); i++ {
			idx := viewportStart + r.row + i
			if idx >= 0 && idx < len(result) {
				// Defensive: truncate overlay lines to declared width.
				overlayLine := r.overlayLines[i]
				if VisibleWidth(overlayLine) > r.w {
					overlayLine = SliceByColumn(overlayLine, 0, r.w, true)
				}
				result[idx] = CompositeTuiLine(result[idx], overlayLine, r.col, r.w, termWidth)
			}
		}
	}

	return result
}

// ApplyLineResets normalizes each non-image line and appends the segment
// reset.
func (t *TuiBase) ApplyLineResets(lines []string) []string {
	for i := range lines {
		if !IsImageLine(lines[i]) {
			lines[i] = NormalizeTerminalOutput(lines[i]) + segmentReset
		}
	}
	return lines
}

// ExtractCursorPosition finds CURSOR_MARKER, computes its column, and strips
// it; only the bottom `height` lines are scanned.
func (t *TuiBase) ExtractCursorPosition(lines []string, height int) (row, col int, ok bool) {
	viewportTop := maxInt(0, len(lines)-height)
	for r := len(lines) - 1; r >= viewportTop; r-- {
		line := lines[r]
		markerIndex := indexString(line, CURSOR_MARKER)
		if markerIndex == -1 {
			continue
		}
		beforeMarker := line[:markerIndex]
		column := VisibleWidth(beforeMarker)
		lines[r] = line[:markerIndex] + line[markerIndex+len(CURSOR_MARKER):]
		return r, column, true
	}
	return 0, 0, false
}

func indexString(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// QueryTerminalBackgroundColor queries the default background via OSC 11;
// the channel receives one value (nil on timeout/parse failure).
func (t *TuiBase) QueryTerminalBackgroundColor(timeoutMs int) chan *RgbColor {
	return t.queryTerminalColor(11, timeoutMs)
}

// QueryTerminalForegroundColor queries the default foreground via OSC 10.
func (t *TuiBase) QueryTerminalForegroundColor(timeoutMs int) chan *RgbColor {
	return t.queryTerminalColor(10, timeoutMs)
}

func (t *TuiBase) queryTerminalColor(slot, timeoutMs int) chan *RgbColor {
	result := make(chan *RgbColor, 1)
	t.mu.Lock()
	query := &pendingOscColorQuery{}
	query.resolve = func(rgb *RgbColor) { result <- rgb }
	query.timer = t.After(time.Duration(timeoutMs)*time.Millisecond, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if query.settled {
			return
		}
		query.settled = true
		query.timer = nil
		if query.resolve != nil {
			query.resolve(nil)
			query.resolve = nil
		}
	})
	t.pendingOscColorQueries[slot] = append(t.pendingOscColorQueries[slot], query)
	t.pendingOscColorReplies[slot]++
	t.terminalRef.Write("\x1b]" + strconv.Itoa(slot) + ";?\x07")
	t.mu.Unlock()
	return result
}

// QueryTerminalColorScheme queries the color-scheme preference via DSR
// ?996n; replies arrive as ?997;1n (dark) or ?997;2n (light).
func (t *TuiBase) QueryTerminalColorScheme(timeoutMs int) chan TerminalColorScheme {
	result := make(chan TerminalColorScheme, 1)
	t.mu.Lock()
	settled := false
	var timer *time.Timer
	var unsubscribe func()
	unsubscribe = t.OnTerminalColorSchemeChange(func(scheme TerminalColorScheme) {
		t.mu.Lock()
		defer t.mu.Unlock()
		if settled {
			return
		}
		settled = true
		if timer != nil {
			timer.Stop()
			timer = nil
		}
		unsubscribe()
		result <- scheme
	})
	timer = t.After(time.Duration(timeoutMs)*time.Millisecond, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if settled {
			return
		}
		settled = true
		unsubscribe()
		result <- ""
	})
	t.terminalRef.Write("\x1b[?996n")
	t.mu.Unlock()
	return result
}

// resolveFakeCursors resolves fake cursor markers to reverse video. When
// the hardware cursor is shown, a fake cursor directly after
// CURSOR_MARKER is dropped because the terminal cursor is drawn there.
// Fake cursors of components without focus have no CURSOR_MARKER and stay
// visible.
func (t *TuiBase) resolveFakeCursors(lines []string) []string {
	out := make([]string, len(lines))
	for index, line := range lines {
		if !strings.Contains(line, FAKE_CURSOR_START) {
			out[index] = line
			continue
		}
		resolved := line
		if t.showHardwareCursor {
			resolved = focusedFakeCursorPattern.ReplaceAllString(resolved, "$1$2")
		}
		resolved = strings.ReplaceAll(resolved, FAKE_CURSOR_START, "\x1b[7m")
		resolved = strings.ReplaceAll(resolved, FAKE_CURSOR_END, "\x1b[27m")
		out[index] = resolved
	}
	return out
}
