package pico3

// bounded.go ports harness/pico3/bounded.ts: a bounded byte collector
// retaining a prefix or suffix constrained by both a byte budget and a
// newline budget, accounting for every discarded byte/newline.

// Bounded is the collector.
type Bounded struct {
	bytes        []byte
	maxBytes     int
	maxLines     int
	retain       string // "head" | "tail"
	DroppedBytes int
	DroppedLines int
	Total        int
}

// NewBounded builds a collector; budgets clamp at zero.
func NewBounded(maxBytes, maxLines int, retain string) *Bounded {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if maxLines < 0 {
		maxLines = 0
	}
	return &Bounded{maxBytes: maxBytes, maxLines: maxLines, retain: retain}
}

// Push feeds one chunk.
func (b *Bounded) Push(chunk []byte) {
	b.Total += len(chunk)
	if len(chunk) == 0 {
		return
	}
	if b.maxBytes == 0 || b.maxLines == 0 {
		b.drop(chunk)
		return
	}
	if b.retain == "head" {
		b.pushHead(chunk)
		return
	}
	b.pushTail(chunk)
}

func (b *Bounded) pushHead(chunk []byte) {
	remainingBytes := b.maxBytes - len(b.bytes)
	remainingLines := b.maxLines - countNewlines(b.bytes)
	if remainingBytes <= 0 || remainingLines <= 0 {
		b.drop(chunk)
		return
	}
	take := len(chunk)
	if take > remainingBytes {
		take = remainingBytes
	}
	lines := 0
	for i := 0; i < take; i++ {
		if chunk[i] != 0x0a {
			continue
		}
		lines++
		if lines == remainingLines {
			take = i + 1
			break
		}
	}
	b.bytes = concatBytes(b.bytes, chunk[:take])
	b.drop(chunk[take:])
}

func (b *Bounded) pushTail(chunk []byte) {
	incomingStart := tailStart(chunk, b.maxBytes, b.maxLines)
	b.drop(chunk[:incomingStart])
	combined := concatBytes(b.bytes, chunk[incomingStart:])
	start := tailStart(combined, b.maxBytes, b.maxLines)
	b.drop(combined[:start])
	b.bytes = append([]byte{}, combined[start:]...)
}

func (b *Bounded) drop(bytes []byte) {
	b.DroppedBytes += len(bytes)
	b.DroppedLines += countNewlines(bytes)
}

// Dropped reports the dropped byte count.
func (b *Bounded) Dropped() int { return b.DroppedBytes }

// Text decodes the retained bytes.
func (b *Bounded) Text() string { return string(b.bytes) }

func tailStart(bytes []byte, maxBytes, maxLines int) int {
	start := len(bytes) - maxBytes
	if start < 0 {
		start = 0
	}
	excessLines := countNewlines(bytes[start:]) - maxLines
	for ; start < len(bytes) && excessLines > 0; start++ {
		if bytes[start] == 0x0a {
			excessLines--
		}
	}
	return start
}

func concatBytes(a, b []byte) []byte {
	if len(a) == 0 {
		return append([]byte{}, b...)
	}
	if len(b) == 0 {
		return a
	}
	out := make([]byte, len(a)+len(b))
	copy(out, a)
	copy(out[len(a):], b)
	return out
}

func countNewlines(bytes []byte) int {
	count := 0
	for _, b := range bytes {
		if b == 0x0a {
			count++
		}
	}
	return count
}
