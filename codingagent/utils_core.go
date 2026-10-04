package codingagent

// Port of utils/frontmatter.ts, utils/deprecation.ts, utils/sleep.ts,
// utils/abort.ts, and utils/mime.ts.

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gladmo/openagent/abort"
	"gopkg.in/yaml.v3"
)

// =============================================================================
// utils/frontmatter.ts
// =============================================================================

// FrontmatterResult carries parsed frontmatter and body.
type FrontmatterResult struct {
	Frontmatter map[string]any
	Body        string
}

var frontmatterNewlines = strings.NewReplacer("\r\n", "\n", "\r", "\n")

func extractFrontmatter(content string) (yamlString string, body string) {
	normalized := frontmatterNewlines.Replace(StripBom(content))

	if !strings.HasPrefix(normalized, "---") {
		return "", normalized
	}

	endIndex := strings.Index(normalized[3:], "\n---")
	if endIndex == -1 {
		return "", normalized
	}
	endIndex += 3
	if endIndex < 4 {
		// Empty frontmatter ("---\n---\nbody"): JS slice(4, 3) returns
		// the empty string; Go must guard explicitly.
		return "", strings.TrimSpace(normalized[endIndex+4:])
	}

	return normalized[4:endIndex], strings.TrimSpace(normalized[endIndex+4:])
}

// ParseFrontmatter parses YAML frontmatter delimited by --- lines.
func ParseFrontmatter(content string) FrontmatterResult {
	yamlString, body := extractFrontmatter(content)
	if yamlString == "" {
		return FrontmatterResult{Frontmatter: map[string]any{}, Body: body}
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(yamlString), &parsed); err != nil || parsed == nil {
		parsed = map[string]any{}
	}
	return FrontmatterResult{Frontmatter: parsed, Body: body}
}

// StripFrontmatter returns only the body.
func StripFrontmatter(content string) string {
	return ParseFrontmatter(content).Body
}

// =============================================================================
// utils/deprecation.ts
// =============================================================================

var (
	deprecationMu      sync.Mutex
	deprecationEmitted = map[string]bool{}
)

// WarnDeprecation prints a deprecation warning once per message.
func WarnDeprecation(message string) {
	deprecationMu.Lock()
	defer deprecationMu.Unlock()
	if deprecationEmitted[message] {
		return
	}
	deprecationEmitted[message] = true
	fmt.Fprintf(os.Stderr, "\x1b[33mDeprecation warning: %s\x1b[39m\n", message)
}

// ClearDeprecationWarningsForTests resets warning state.
func ClearDeprecationWarningsForTests() {
	deprecationMu.Lock()
	defer deprecationMu.Unlock()
	deprecationEmitted = map[string]bool{}
}

// =============================================================================
// utils/sleep.ts
// =============================================================================

// Sleep waits for ms, aborting with an error when the signal fires.
func Sleep(ms int, signal *abort.Signal) error {
	if signal != nil && signal.Aborted() {
		return fmt.Errorf("Aborted")
	}
	if signal == nil {
		time.Sleep(time.Duration(ms) * time.Millisecond)
		return nil
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-signal.Done():
		return fmt.Errorf("Aborted")
	}
}

// =============================================================================
// utils/abort.ts
// =============================================================================

// OperationSignal normalizes an optional signal without imposing a
// deadline.
func OperationSignal(signal *abort.Signal) *abort.Signal {
	if signal != nil {
		return signal
	}
	return abort.NewController().Signal()
}

// RaceWithAbortSignal runs operation, rejecting on abort while letting the
// abandoned operation settle.
func RaceWithAbortSignal(operation func() (any, error), signal *abort.Signal) (any, error) {
	if signal == nil {
		return operation()
	}
	if signal.Aborted() {
		go func() {
			defer func() { recover() }()
			_, _ = operation()
		}()
		return nil, fmt.Errorf("The operation was aborted")
	}

	type outcome struct {
		value any
		err   error
	}
	result := make(chan outcome, 1)
	aborted := make(chan struct{})
	remove := signal.OnAbort(func() { close(aborted) })
	defer remove()

	go func() {
		value, err := operation()
		select {
		case result <- outcome{value, err}:
		default:
		}
	}()

	select {
	case out := <-result:
		return out.value, out.err
	case <-aborted:
		return nil, fmt.Errorf("The operation was aborted")
	}
}

// =============================================================================
// utils/mime.ts
// =============================================================================

const imageTypeSniffBytes = 4100

var pngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// DetectSupportedImageMimeType sniffs the buffer for a supported image type.
func DetectSupportedImageMimeType(buffer []byte) string {
	if bytesHasPrefix(buffer, []byte{0xff, 0xd8, 0xff}) {
		if len(buffer) > 3 && buffer[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	}
	if bytesHasPrefix(buffer, pngSignature) {
		if isPng(buffer) && !isAnimatedPng(buffer) {
			return "image/png"
		}
		return ""
	}
	if bytesHasPrefix(buffer, []byte("GIF87a")) || bytesHasPrefix(buffer, []byte("GIF89a")) {
		return "image/gif"
	}
	if bytesHasPrefix(buffer, []byte("RIFF")) && bytesHasPrefixAt(buffer, 8, "WEBP") {
		return "image/webp"
	}
	if bytesHasPrefix(buffer, []byte("BM")) && isBmp(buffer) {
		return "image/bmp"
	}
	return ""
}

// DetectSupportedImageMimeTypeFromFile sniffs the first bytes of a file.
func DetectSupportedImageMimeTypeFromFile(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		return ""
	}
	defer file.Close()
	buffer := make([]byte, imageTypeSniffBytes)
	n, _ := file.Read(buffer)
	return DetectSupportedImageMimeType(buffer[:n])
}

func bytesHasPrefix(buffer, prefix []byte) bool {
	return len(buffer) >= len(prefix) && string(buffer[:len(prefix)]) == string(prefix)
}

func bytesHasPrefixAt(buffer []byte, offset int, prefix string) bool {
	return len(buffer) >= offset+len(prefix) && string(buffer[offset:offset+len(prefix)]) == prefix
}

func isPng(buffer []byte) bool {
	return len(buffer) >= 16 && binary.BigEndian.Uint32(buffer[len(pngSignature):]) == 13 &&
		bytesHasPrefixAt(buffer, 12, "IHDR")
}

func isAnimatedPng(buffer []byte) bool {
	offset := len(pngSignature)
	for offset+8 <= len(buffer) {
		chunkLength := binary.BigEndian.Uint32(buffer[offset:])
		chunkTypeOffset := offset + 4
		if bytesHasPrefixAt(buffer, chunkTypeOffset, "acTL") {
			return true
		}
		if bytesHasPrefixAt(buffer, chunkTypeOffset, "IDAT") {
			return false
		}

		nextOffset := offset + 8 + int(chunkLength) + 4
		if nextOffset <= offset || nextOffset > len(buffer) {
			return false
		}
		offset = nextOffset
	}
	return false
}

func isBmp(buffer []byte) bool {
	if len(buffer) < 26 {
		return false
	}

	declaredFileSize := binary.LittleEndian.Uint32(buffer[2:])
	pixelDataOffset := binary.LittleEndian.Uint32(buffer[10:])
	dibHeaderSize := binary.LittleEndian.Uint32(buffer[14:])
	if declaredFileSize != 0 && declaredFileSize < 26 {
		return false
	}
	if pixelDataOffset < 14+dibHeaderSize {
		return false
	}
	if declaredFileSize != 0 && pixelDataOffset >= declaredFileSize {
		return false
	}

	// Only uncompressed RGB(A) and BITMAPINFOHEADER-family DIB headers are
	// accepted (the reference's supported subset).
	switch dibHeaderSize {
	case 12, 40, 52, 56, 64, 108, 124:
		compression := uint32(0)
		if dibHeaderSize >= 40 && len(buffer) >= 34 {
			compression = binary.LittleEndian.Uint32(buffer[30:])
		}
		return compression == 0 || compression == 3
	}
	return false
}
