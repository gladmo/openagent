package tui

// KillRing is a ring buffer for Emacs-style kill/yank operations: killed
// text entries accumulate (consecutive kills merge), yank pastes the most
// recent, yank-pop cycles through older entries. Port of kill-ring.ts.

// KillRingPushOptions controls accumulation.
type KillRingPushOptions struct {
	// Prepend accumulates backward deletion (prepend) instead of forward
	// deletion (append).
	Prepend bool
	// Accumulate merges with the most recent entry instead of pushing.
	Accumulate bool
}

type KillRing struct {
	ring []string
}

// Push adds killed text; empty text is ignored.
func (k *KillRing) Push(text string, opts KillRingPushOptions) {
	if text == "" {
		return
	}

	if opts.Accumulate && len(k.ring) > 0 {
		last := k.ring[len(k.ring)-1]
		k.ring = k.ring[:len(k.ring)-1]
		if opts.Prepend {
			k.ring = append(k.ring, text+last)
		} else {
			k.ring = append(k.ring, last+text)
		}
	} else {
		k.ring = append(k.ring, text)
	}
}

// Peek returns the most recent entry without modifying the ring.
func (k *KillRing) Peek() (string, bool) {
	if len(k.ring) == 0 {
		return "", false
	}
	return k.ring[len(k.ring)-1], true
}

// Rotate moves the last entry to front (for yank-pop cycling).
func (k *KillRing) Rotate() {
	if len(k.ring) > 1 {
		last := k.ring[len(k.ring)-1]
		k.ring = k.ring[:len(k.ring)-1]
		k.ring = append([]string{last}, k.ring...)
	}
}

// Len returns the number of entries.
func (k *KillRing) Len() int {
	return len(k.ring)
}
