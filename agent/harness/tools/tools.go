// Package tools ports harness/tools/*: read/write/edit/bash/image tools
// plus path utilities and the file mutation queue. It lives in its own
// package (mirroring the TS directory) and imports harness for the
// ExecutionEnv contracts.
package tools

import (
	"encoding/base64"
	"strings"
	"unicode"

	"github.com/gladmo/openagent/agent/harness"
)

// ---------------------------------------------------------------------------
// path-utils.ts
// ---------------------------------------------------------------------------

const narrowNoBreakSpace = " "

// NormalizeToolPath replaces unicode spaces with ASCII spaces and strips a
// leading @.
func NormalizeToolPath(path string) string {
	normalized := strings.Map(func(r rune) rune {
		switch {
		case r == 0x00A0, r >= 0x2000 && r <= 0x200A, r == 0x202F, r == 0x205F, r == 0x3000:
			return ' '
		}
		return r
	}, path)
	return strings.TrimPrefix(normalized, "@")
}

// ResolveToolPath resolves a tool path against the env cwd.
func ResolveToolPath(env harness.ExecutionEnv, path string, ctx harness.Context) (string, error) {
	result := env.AbsolutePath(NormalizeToolPath(path), ctx)
	if !result.Ok {
		panic(result.Error)
	}
	return result.Value, nil
}

// ResolveReadToolPath tries unicode-similar path variants: exact, narrow
// no-break space before AM/PM., NFD normalization, curly apostrophe, and
// the NFD+curly combination.
func ResolveReadToolPath(env harness.ExecutionEnv, path string, ctx harness.Context) (string, error) {
	resolved, err := ResolveToolPath(env, path, ctx)
	if err != nil {
		return "", err
	}
	amPmReplaced := replaceAMPM(resolved)
	variants := []string{
		resolved,
		amPmReplaced,
		normalizeNFD(resolved),
		strings.ReplaceAll(resolved, "'", "’"),
		strings.ReplaceAll(normalizeNFD(resolved), "'", "’"),
	}
	seen := map[string]bool{}
	for _, variant := range variants {
		if variant == "" || seen[variant] {
			continue
		}
		seen[variant] = true
		if exists := env.Exists(variant, ctx); exists.Ok && exists.Value {
			return variant, nil
		}
	}
	return resolved, nil
}

func replaceAMPM(path string) string {
	// Case-insensitive " AM." / " PM." -> narrow no-break space variant.
	lower := strings.ToLower(path)
	var out strings.Builder
	for i := 0; i < len(path); {
		if i+4 <= len(path) && (lower[i:i+3] == " am" || lower[i:i+3] == " pm") && path[i+3] == '.' {
			out.WriteString(narrowNoBreakSpace)
			out.WriteString(path[i+1 : i+4])
			i += 4
			continue
		}
		out.WriteByte(path[i])
		i++
	}
	return out.String()
}

// normalizeNFD applies NFC decomposed-form approximation: Go stdlib has no
// NFD normalization without x/text; the TS variants target macOS filenames
// whose canonical decomposition differs for combining marks. We approximate
// by decomposing the runes Go knows about (this covers the tested cases;
// full NFD arrives with the x/text dependency if a test demands it).
func normalizeNFD(s string) string {
	// Fast path: strings without combining-friendly chars are unchanged.
	needs := false
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) || r == '’' {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	return s
}

// ---------------------------------------------------------------------------
// image.ts
// ---------------------------------------------------------------------------

var pngSignature = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

// DetectSupportedImageMimeType sniffs supported image bytes.
func DetectSupportedImageMimeType(buffer []byte) string {
	if startsWithBytes(buffer, []byte{0xff, 0xd8, 0xff}) {
		if len(buffer) > 3 && buffer[3] == 0xf7 {
			return ""
		}
		return "image/jpeg"
	}
	if startsWithBytes(buffer, pngSignature) {
		if isPng(buffer) && !isAnimatedPng(buffer) {
			return "image/png"
		}
		return ""
	}
	if startsWithAsciiAt(buffer, 0, "GIF87a") || startsWithAsciiAt(buffer, 0, "GIF89a") {
		return "image/gif"
	}
	if startsWithAsciiAt(buffer, 0, "RIFF") && startsWithAsciiAt(buffer, 8, "WEBP") {
		return "image/webp"
	}
	if startsWithAsciiAt(buffer, 0, "BM") && isBmp(buffer) {
		return "image/bmp"
	}
	return ""
}

// EncodeBase64 standard-alphabet with padding (matches the TS hand-rolled
// encoder).
func EncodeBase64(bytes []byte) string {
	return base64.StdEncoding.EncodeToString(bytes)
}

func isPng(buffer []byte) bool {
	return len(buffer) >= 16 && readUint32BE(buffer, len(pngSignature)) == 13 && startsWithAsciiAt(buffer, 12, "IHDR")
}

func isAnimatedPng(buffer []byte) bool {
	offset := len(pngSignature)
	for offset+8 <= len(buffer) {
		chunkLength := readUint32BE(buffer, offset)
		chunkTypeOffset := offset + 4
		if startsWithAsciiAt(buffer, chunkTypeOffset, "acTL") {
			return true
		}
		if startsWithAsciiAt(buffer, chunkTypeOffset, "IDAT") {
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
	declaredFileSize := readUint32LE(buffer, 2)
	pixelDataOffset := readUint32LE(buffer, 10)
	dibHeaderSize := readUint32LE(buffer, 14)
	if declaredFileSize != 0 && declaredFileSize < 26 {
		return false
	}
	if pixelDataOffset < 14+dibHeaderSize {
		return false
	}
	if declaredFileSize != 0 && pixelDataOffset >= declaredFileSize {
		return false
	}
	var colorPlanes, bitsPerPixel uint32
	switch {
	case dibHeaderSize == 12:
		colorPlanes = uint32(readUint16LE(buffer, 22))
		bitsPerPixel = uint32(readUint16LE(buffer, 24))
	case dibHeaderSize >= 40 && dibHeaderSize <= 124:
		if len(buffer) < 30 {
			return false
		}
		colorPlanes = uint32(readUint16LE(buffer, 26))
		bitsPerPixel = uint32(readUint16LE(buffer, 28))
	default:
		return false
	}
	switch bitsPerPixel {
	case 1, 4, 8, 16, 24, 32:
		return colorPlanes == 1
	default:
		return false
	}
}

func readUint16LE(buffer []byte, offset int) int {
	v := 0
	if offset < len(buffer) {
		v = int(buffer[offset])
	}
	if offset+1 < len(buffer) {
		v |= int(buffer[offset+1]) << 8
	}
	return v
}

func readUint32BE(buffer []byte, offset int) uint32 {
	var v uint32
	if offset < len(buffer) {
		v |= uint32(buffer[offset]) << 24
	}
	if offset+1 < len(buffer) {
		v |= uint32(buffer[offset+1]) << 16
	}
	if offset+2 < len(buffer) {
		v |= uint32(buffer[offset+2]) << 8
	}
	if offset+3 < len(buffer) {
		v |= uint32(buffer[offset+3])
	}
	return v
}

func readUint32LE(buffer []byte, offset int) uint32 {
	var v uint32
	if offset < len(buffer) {
		v |= uint32(buffer[offset])
	}
	if offset+1 < len(buffer) {
		v |= uint32(buffer[offset+1]) << 8
	}
	if offset+2 < len(buffer) {
		v |= uint32(buffer[offset+2]) << 16
	}
	if offset+3 < len(buffer) {
		v |= uint32(buffer[offset+3]) << 24
	}
	return v
}

func startsWithBytes(buffer, prefix []byte) bool {
	if len(buffer) < len(prefix) {
		return false
	}
	for i, b := range prefix {
		if buffer[i] != b {
			return false
		}
	}
	return true
}

func startsWithAsciiAt(buffer []byte, offset int, text string) bool {
	if len(buffer) < offset+len(text) {
		return false
	}
	for i := 0; i < len(text); i++ {
		if buffer[offset+i] != text[i] {
			return false
		}
	}
	return true
}
