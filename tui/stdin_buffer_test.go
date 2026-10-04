package tui

import (
	"bytes"
	"testing"
	"time"
)

// manualTimer captures scheduled callbacks so tests fire them
// deterministically instead of sleeping against real timers.
type manualTimer struct {
	fired bool
	fn    func()
}

type manualClock struct {
	timers []*manualTimer
}

func (m *manualClock) after(d time.Duration, fn func()) *time.Timer {
	entry := &manualTimer{fn: fn}
	m.timers = append(m.timers, entry)
	// A stopped time.Timer stub; Stop is a no-op like an expired timer.
	return time.NewTimer(0)
}

// fireFirst fires the earliest unscheduled callback.
func (m *manualClock) fireFirst() bool {
	for _, entry := range m.timers {
		if !entry.fired {
			entry.fired = true
			entry.fn()
			return true
		}
	}
	return false
}

func newManualBuffer(timeoutMs, escapeMs int) (*StdinBuffer, *manualClock, *[]string) {
	clock := &manualClock{}
	sequences := &[]string{}
	buf := NewStdinBuffer(func(seq string) { *sequences = append(*sequences, seq) }, nil, StdinBufferOptions{TimeoutMs: timeoutMs, EscapeTimeoutMs: escapeMs})
	buf.After = clock.after
	return buf, clock, sequences
}

// Ported from pi/packages/tui/test/stdin-buffer.test.ts.

func newTestBuffer(timeoutMs int) (*StdinBuffer, *[]string) {
	sequences := &[]string{}
	buf := NewStdinBuffer(func(seq string) { *sequences = append(*sequences, seq) }, nil, StdinBufferOptions{TimeoutMs: timeoutMs})
	return buf, sequences
}

func assertSequences(t *testing.T, got *[]string, want ...string) {
	t.Helper()
	if len(*got) != len(want) {
		t.Fatalf("sequences = %q, want %q", *got, want)
	}
	for i := range want {
		if (*got)[i] != want[i] {
			t.Fatalf("sequences = %q, want %q", *got, want)
		}
	}
}

func TestStdinBufferRegularCharacters(t *testing.T) {
	buf, seqs := newTestBuffer(10)
	buf.Process([]byte("a"))
	assertSequences(t, seqs, "a")

	buf2, seqs2 := newTestBuffer(10)
	buf2.Process([]byte("abc"))
	assertSequences(t, seqs2, "a", "b", "c")

	buf3, seqs3 := newTestBuffer(10)
	buf3.Process([]byte("hello 世界"))
	assertSequences(t, seqs3, "h", "e", "l", "l", "o", " ", "世", "界")
}

func TestStdinBufferCompleteSequences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"mouse SGR", "\x1b[<35;20;5m", []string{"\x1b[<35;20;5m"}},
		{"arrow", "\x1b[A", []string{"\x1b[A"}},
		{"function key", "\x1b[11~", []string{"\x1b[11~"}},
		{"meta", "\x1ba", []string{"\x1ba"}},
		{"SS3", "\x1bOA", []string{"\x1bOA"}},
		{"chars then escape", "abc\x1b[A", []string{"a", "b", "c", "\x1b[A"}},
		{"escape then chars", "\x1b[Aabc", []string{"\x1b[A", "a", "b", "c"}},
		{"multiple sequences", "\x1b[A\x1b[B\x1b[C", []string{"\x1b[A", "\x1b[B", "\x1b[C"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, seqs := newTestBuffer(10)
			buf.Process([]byte(tc.in))
			assertSequences(t, seqs, tc.want...)
		})
	}
}

func TestStdinBufferDelayedMouseChunks(t *testing.T) {
	buf, seqs := newTestBuffer(0) // default 50ms sequence timeout
	buf.Process([]byte("\x1b["))
	time.Sleep(20 * time.Millisecond)
	assertSequences(t, seqs)
	buf.Process([]byte("<65;48;39M"))
	assertSequences(t, seqs, "\x1b[<65;48;39M")
	buf.Destroy()
}

func TestStdinBufferIncompleteSequences(t *testing.T) {
	buf, seqs := newTestBuffer(10)
	buf.Process([]byte("abc\x1b[<35"))
	assertSequences(t, seqs, "a", "b", "c")
	if got := buf.GetBuffer(); got != "\x1b[<35" {
		t.Fatalf("buffer = %q", got)
	}
	buf.Process([]byte(";20;5m"))
	assertSequences(t, seqs, "a", "b", "c", "\x1b[<35;20;5m")
}

func TestStdinBufferKittyProtocol(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"CSI u press", "\x1b[97u", []string{"\x1b[97u"}},
		{"CSI u release", "\x1b[97;1:3u", []string{"\x1b[97;1:3u"}},
		{"batched press+release", "\x1b[97u\x1b[97;1:3u", []string{"\x1b[97u", "\x1b[97;1:3u"}},
		{"multiple batched", "\x1b[97u\x1b[97;1:3u\x1b[98u\x1b[98;1:3u", []string{"\x1b[97u", "\x1b[97;1:3u", "\x1b[98u", "\x1b[98;1:3u"}},
		{"arrow with event", "\x1b[1;1:1A", []string{"\x1b[1;1:1A"}},
		{"functional with event", "\x1b[3;1:3~", []string{"\x1b[3;1:3~"}},
		{"WezTerm ESC+ESC+CSI", "\x1b\x1b[27;129:3u", []string{"\x1b", "\x1b[27;129:3u"}},
		{"ESC+ESC+CSI no modifier", "\x1b\x1b[27;1:3u", []string{"\x1b", "\x1b[27;1:3u"}},
		{"ESC+ESC stays single", "\x1b\x1b", []string{"\x1b\x1b"}},
		{"plain + kitty release", "a\x1b[97;1:3u", []string{"a", "\x1b[97;1:3u"}},
		{"drop duplicate after printable", "\x1b[224uà", []string{"\x1b[224u"}},
		{"keep non-matching after printable", "\x1b[97ub", []string{"\x1b[97u", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, seqs := newTestBuffer(10)
			buf.Process([]byte(tc.in))
			assertSequences(t, seqs, tc.want...)
		})
	}

	buf, seqs := newTestBuffer(10)
	buf.Process([]byte("\x1b[64u"))
	buf.Process([]byte("@"))
	assertSequences(t, seqs, "\x1b[64u")
}

func TestStdinBufferTimeoutFlush(t *testing.T) {
	// Incomplete sequence flushed after the sequence timeout
	buf, clock, seqs := newManualBuffer(10, 10)
	buf.Process([]byte("\x1b[<35"))
	assertSequences(t, seqs)
	if !clock.fireFirst() {
		t.Fatal("no timeout scheduled")
	}
	assertSequences(t, seqs, "\x1b[<35")

	// Lone ESC flushed alone: host sees Escape instead of Alt+Enter when the
	// transport splits bytes further apart than the timeout.
	buf2, clock2, seqs2 := newManualBuffer(10, 10)
	buf2.Process([]byte("\x1b"))
	assertSequences(t, seqs2)
	if !clock2.fireFirst() {
		t.Fatal("no escape timeout scheduled")
	}
	buf2.Process([]byte("\r"))
	assertSequences(t, seqs2, "\x1b", "\r")
	if !MatchesKey((*seqs2)[0], "escape") {
		t.Fatal("first flushed sequence is not Escape")
	}

	// Larger escapeTimeout merges split ESC + CR into Alt+Enter: the CR
	// arrives (at 20ms) before the 100ms escape timeout elapses, so the
	// timer must NOT fire first.
	buf3, _, seqs3 := newManualBuffer(10, 100)
	buf3.Process([]byte("\x1b"))
	buf3.Process([]byte("\r"))
	assertSequences(t, seqs3, "\x1b\r")
	if !MatchesKey((*seqs3)[0], "alt+enter") {
		t.Fatal("merged sequence is not alt+enter")
	}

	// Sequence timeout does not apply to a lone ESC.
	buf4, clock4, seqs4 := newManualBuffer(100, 10)
	buf4.Process([]byte("\x1b"))
	if !clock4.fireFirst() {
		t.Fatal("no escape timeout scheduled")
	}
	buf4.Process([]byte("\r"))
	assertSequences(t, seqs4, "\x1b", "\r")
}

func TestStdinBufferMouse(t *testing.T) {
	buf, seqs := newTestBuffer(10)
	buf.Process([]byte("\x1b[<0;10;20M"))
	assertSequences(t, seqs, "\x1b[<0;10;20M")

	buf2, seqs2 := newTestBuffer(10)
	buf2.Process([]byte("\x1b[<0;10;20m"))
	assertSequences(t, seqs2, "\x1b[<0;10;20m")

	buf3, seqs3 := newTestBuffer(10)
	buf3.Process([]byte("\x1b[<35;20;5M\x1b[<35;20;6m"))
	assertSequences(t, seqs3, "\x1b[<35;20;5M", "\x1b[<35;20;6m")

	buf4, seqs4 := newTestBuffer(10)
	buf4.Process([]byte("\x1b[<35"))
	buf4.Process([]byte(";20;5M"))
	assertSequences(t, seqs4, "\x1b[<35;20;5M")

	// Old-style mouse: ESC[M + 3 bytes
	buf5, seqs5 := newTestBuffer(10)
	buf5.Process([]byte("\x1b[M abc"))
	assertSequences(t, seqs5, "\x1b[M ab", "c")

	buf6, seqs6 := newTestBuffer(10)
	buf6.Process([]byte("\x1b[M"))
	if got := buf6.GetBuffer(); got != "\x1b[M" {
		t.Fatalf("buffer = %q", got)
	}
	buf6.Process([]byte(" a"))
	if got := buf6.GetBuffer(); got != "\x1b[M a" {
		t.Fatalf("buffer = %q", got)
	}
	buf6.Process([]byte("b"))
	assertSequences(t, seqs6, "\x1b[M ab")
}

func TestStdinBufferEdgeCases(t *testing.T) {
	buf, seqs := newTestBuffer(10)
	buf.Process([]byte(""))
	assertSequences(t, seqs, "")

	buf2, clock2, seqs2 := newManualBuffer(10, 10)
	buf2.Process([]byte("\x1b"))
	assertSequences(t, seqs2)
	if !clock2.fireFirst() {
		t.Fatal("no timeout scheduled")
	}
	assertSequences(t, seqs2, "\x1b")

	// Default (50ms) sequence timeout still flushes a lone ESC via the 10ms
	// escape timeout.
	buf3, clock3, seqs3 := newManualBuffer(0, 0)
	buf3.Process([]byte("\x1b"))
	if !clock3.fireFirst() {
		t.Fatal("no timeout scheduled")
	}
	assertSequences(t, seqs3, "\x1b")
	buf3.Destroy()

	buf4, seqs4 := newTestBuffer(10)
	buf4.Process([]byte("\x1b"))
	assertSequences(t, seqs4)
	flushed := buf4.Flush()
	assertSequences(t, &flushed, "\x1b")

	buf5, seqs5 := newTestBuffer(10)
	buf5.Process([]byte("\x1b[A"))
	assertSequences(t, seqs5, "\x1b[A")

	var longSeq bytes.Buffer
	longSeq.WriteString("\x1b[")
	for i := 0; i < 50; i++ {
		longSeq.WriteString("1;")
	}
	longSeq.WriteString("H")
	buf6, seqs6 := newTestBuffer(10)
	buf6.Process(longSeq.Bytes())
	assertSequences(t, seqs6, longSeq.String())
}

func TestStdinBufferFlush(t *testing.T) {
	buf, seqs := newTestBuffer(10)
	buf.Process([]byte("\x1b[<35"))
	flushed := buf.Flush()
	assertSequences(t, &flushed, "\x1b[<35")
	if got := buf.GetBuffer(); got != "" {
		t.Fatalf("buffer = %q", got)
	}
	_ = seqs
}

func TestStdinBufferPaste(t *testing.T) {
	var pastes []string
	seqs := &[]string{}
	buf := NewStdinBuffer(func(seq string) { *seqs = append(*seqs, seq) }, func(content string) { pastes = append(pastes, content) }, StdinBufferOptions{TimeoutMs: 10})

	buf.Process([]byte("\x1b[200~pasted content\x1b[201~"))
	if len(pastes) != 1 || pastes[0] != "pasted content" {
		t.Fatalf("pastes = %q", pastes)
	}
	assertSequences(t, seqs)

	// Paste arriving in chunks
	pastes = nil
	*seqs = nil
	buf.Process([]byte("\x1b[200~chunk "))
	time.Sleep(5 * time.Millisecond)
	buf.Process([]byte("two\x1b[201~"))
	if len(pastes) != 1 || pastes[0] != "chunk two" {
		t.Fatalf("chunked pastes = %q", pastes)
	}
	assertSequences(t, seqs)

	// Input before and after paste
	pastes = nil
	*seqs = nil
	buf.Process([]byte("before\x1b[200~pasted\x1b[201~after"))
	if len(pastes) != 1 || pastes[0] != "pasted" {
		t.Fatalf("pastes = %q", pastes)
	}
	assertSequences(t, seqs, "b", "e", "f", "o", "r", "e", "a", "f", "t", "e", "r")
}
