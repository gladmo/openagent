package codingagent

// Port of modes/interactive/components/countdown-timer.ts,
// bordered-loader.ts, and dynamic-border.ts: dialog countdown, the
// bordered loading indicator, and the active/inactive border swap.

import (
	"sync"
	"time"

	"github.com/gladmo/openagent/tui"
)

// CountdownTimer drives dialog countdowns (1-second ticks).
type CountdownTimer struct {
	mu               sync.Mutex
	remainingSeconds int
	ticker           *time.Ticker
	stopped          bool
	onTick           func(seconds int)
	onExpire         func()
	tui              tui.TUI
}

// NewCountdownTimer starts ticking immediately (first tick synchronous).
func NewCountdownTimer(timeoutMs int, ui tui.TUI, onTick func(seconds int), onExpire func()) *CountdownTimer {
	timer := &CountdownTimer{
		remainingSeconds: (timeoutMs + 999) / 1000,
		onTick:           onTick,
		onExpire:         onExpire,
		tui:              ui,
	}
	if onTick != nil {
		onTick(timer.remainingSeconds)
	}

	timer.ticker = time.NewTicker(time.Second)
	go func() {
		for range timer.ticker.C {
			timer.mu.Lock()
			if timer.stopped {
				timer.mu.Unlock()
				return
			}
			timer.remainingSeconds--
			expired := timer.remainingSeconds <= 0
			remaining := timer.remainingSeconds
			timer.mu.Unlock()

			if onTick != nil {
				onTick(remaining)
			}
			if ui != nil {
				ui.RequestRender(false)
			}
			if expired {
				timer.Dispose()
				if onExpire != nil {
					onExpire()
				}
				return
			}
		}
	}()
	return timer
}

// Dispose stops the ticker (idempotent).
func (c *CountdownTimer) Dispose() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return
	}
	c.stopped = true
	if c.ticker != nil {
		c.ticker.Stop()
	}
}

// ============================================================================
// bordered-loader.ts
// ============================================================================

// BorderedLoader renders a bordered loading message (the tui Loader
// supplies the spinner animation; the border wraps it).
type BorderedLoader struct {
	mu     sync.Mutex
	loader *tui.Loader
}

// NewBorderedLoader builds the loader (unanimated frame for tests; Start
// animates through the TUI).
func NewBorderedLoader(ui tui.TUI, title string) *BorderedLoader {
	return &BorderedLoader{
		loader: tui.NewLoader(ui, nil, nil, title, nil),
	}
}

// SetTitle replaces the message.
func (l *BorderedLoader) SetTitle(title string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loader.SetMessage(title)
}

// Start begins the animation.
func (l *BorderedLoader) Start() { l.loader.Start() }

// Stop halts the animation.
func (l *BorderedLoader) Stop() { l.loader.Stop() }

// Render produces the loader rows.
func (l *BorderedLoader) Render(width int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loader.Render(width)
}

// ============================================================================
// dynamic-border.ts
// ============================================================================

// DynamicBorder swaps border styling by active state.
type DynamicBorder struct {
	active     bool
	activeFn   func(string) string
	inactiveFn func(string) string
}

// NewDynamicBorder composes the two styles.
func NewDynamicBorder(inactiveStyle func(string) string, activeStyle func(string) string) *DynamicBorder {
	return &DynamicBorder{activeFn: activeStyle, inactiveFn: inactiveStyle}
}

// SetActive toggles the style source.
func (d *DynamicBorder) SetActive(active bool) { d.active = active }

// Apply styles one string.
func (d *DynamicBorder) Apply(str string) string {
	if d.active && d.activeFn != nil {
		return d.activeFn(str)
	}
	if d.inactiveFn != nil {
		return d.inactiveFn(str)
	}
	return str
}
