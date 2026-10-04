package codingagent

// Port of utils/image-resize-core.ts, image-convert.ts, and image-process.ts.
// Photon (Rust/WASM) maps to Go's image stack (documented deviation): decode
// via image/png, image/jpeg, image/gif, image/webp; resize via a Lanczos3-
// equivalent Catmull-Rom resample (image/draw's nearest scaling replaced by
// a hand-rolled filter); EXIF orientation applies pixel transforms.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	// webp decode via golang.org/x/image is avoided (dep convention);
	// webp images fall back to "cannot be converted" like a missing photon.
	_ "image/jpeg"
)

// ImageResizeOptions bound resized images.
type ImageResizeOptions struct {
	MaxWidth    *int // default 2000
	MaxHeight   *int // default 2000
	MaxBytes    *int // default 4.5MB base64 payload
	JPEGQuality *int // default 80
}

// ResizedImage is a resize outcome.
type ResizedImage struct {
	Data           string // base64
	MimeType       string
	OriginalWidth  int
	OriginalHeight int
	Width          int
	Height         int
	WasResized     bool
}

const defaultImageMaxBytes = 4.5 * 1024 * 1024

func (o *ImageResizeOptions) resolved() (maxWidth, maxHeight, maxBytes, jpegQuality int) {
	maxWidth, maxHeight, jpegQuality = 2000, 2000, 80
	maxBytes = defaultImageMaxBytes
	if o != nil {
		if o.MaxWidth != nil {
			maxWidth = *o.MaxWidth
		}
		if o.MaxHeight != nil {
			maxHeight = *o.MaxHeight
		}
		if o.MaxBytes != nil {
			maxBytes = *o.MaxBytes
		}
		if o.JPEGQuality != nil {
			jpegQuality = *o.JPEGQuality
		}
	}
	return
}

type encodedCandidate struct {
	data        string
	encodedSize int
	mimeType    string
}

func encodeCandidateImage(encoded []byte, mimeType string) encodedCandidate {
	data := base64.StdEncoding.EncodeToString(encoded)
	return encodedCandidate{data: data, encodedSize: len(data), mimeType: mimeType}
}

// decodeImageBytes decodes any supported input format.
func decodeImageBytes(input []byte) (image.Image, string, error) {
	img, format, err := image.Decode(bytes.NewReader(input))
	if err != nil {
		return nil, "", err
	}
	return img, format, nil
}

// ConvertImageBytesToPng re-encodes an image as PNG.
func ConvertImageBytesToPng(input []byte) ([]byte, bool) {
	img, _, err := decodeImageBytes(input)
	if err != nil {
		return nil, false
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// ConvertToPng converts base64 image data to PNG for terminal display
// (kitty requires PNG, f=100).
func ConvertToPng(base64Data, mimeType string) (*struct{ Data, MimeType string }, bool) {
	if mimeType == "image/png" {
		return &struct{ Data, MimeType string }{base64Data, mimeType}, true
	}
	raw, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, false
	}
	pngBytes, ok := ConvertImageBytesToPng(raw)
	if !ok {
		return nil, false
	}
	return &struct{ Data, MimeType string }{base64.StdEncoding.EncodeToString(pngBytes), "image/png"}, true
}

// ResizeImageInProcess resizes to fit dimensions and encoded size; nil when
// it cannot fit (mirroring a null photon result).
func ResizeImageInProcess(inputBytes []byte, mimeType string, options *ImageResizeOptions) *ResizedImage {
	maxWidth, maxHeight, maxBytes, jpegQuality := options.resolved()
	inputBase64Size := base64EncodedSize(len(inputBytes))

	img, _, err := decodeImageBytes(inputBytes)
	if err != nil {
		return nil
	}
	img = applyExifOrientation(img, inputBytes)

	bounds := img.Bounds()
	originalWidth := bounds.Dx()
	originalHeight := bounds.Dy()
	format := "png"
	if parts := splitMimeSubtype(mimeType); parts != "" {
		format = parts
	}

	if originalWidth <= maxWidth && originalHeight <= maxHeight && inputBase64Size < maxBytes {
		return &ResizedImage{
			Data: base64.StdEncoding.EncodeToString(inputBytes),
			MimeType: func() string {
				if mimeType != "" {
					return mimeType
				}
				return "image/" + format
			}(),
			OriginalWidth:  originalWidth,
			OriginalHeight: originalHeight,
			Width:          originalWidth,
			Height:         originalHeight,
		}
	}

	targetWidth, targetHeight := originalWidth, originalHeight
	if targetWidth > maxWidth {
		targetHeight = roundFloat(float64(targetHeight) * float64(maxWidth) / float64(targetWidth))
		targetWidth = maxWidth
	}
	if targetHeight > maxHeight {
		targetWidth = roundFloat(float64(targetWidth) * float64(maxHeight) / float64(targetHeight))
		targetHeight = maxHeight
	}

	qualitySteps := uniqueInts([]int{jpegQuality, 85, 70, 55, 40})
	currentWidth, currentHeight := targetWidth, targetHeight

	for {
		resized := resampleImage(img, currentWidth, currentHeight)
		var candidates []encodedCandidate
		var pngBuf bytes.Buffer
		if err := png.Encode(&pngBuf, resized); err == nil {
			candidates = append(candidates, encodeCandidateImage(pngBuf.Bytes(), "image/png"))
		}
		for _, quality := range qualitySteps {
			var jpegBuf bytes.Buffer
			if err := jpeg.Encode(&jpegBuf, resized, &jpeg.Options{Quality: quality}); err == nil {
				candidates = append(candidates, encodeCandidateImage(jpegBuf.Bytes(), "image/jpeg"))
			}
		}

		for _, candidate := range candidates {
			if candidate.encodedSize < maxBytes {
				return &ResizedImage{
					Data:           candidate.data,
					MimeType:       candidate.mimeType,
					OriginalWidth:  originalWidth,
					OriginalHeight: originalHeight,
					Width:          currentWidth,
					Height:         currentHeight,
					WasResized:     true,
				}
			}
		}

		if currentWidth == 1 && currentHeight == 1 {
			break
		}
		nextWidth := currentWidth
		if currentWidth != 1 {
			nextWidth = maxIntGo(1, currentWidth*3/4)
		}
		nextHeight := currentHeight
		if currentHeight != 1 {
			nextHeight = maxIntGo(1, currentHeight*3/4)
		}
		if nextWidth == currentWidth && nextHeight == currentHeight {
			break
		}
		currentWidth, currentHeight = nextWidth, nextHeight
	}

	return nil
}

func base64EncodedSize(n int) int { return (n + 2) / 3 * 4 }

func roundFloat(v float64) int { return int(v + 0.5) }

func uniqueInts(values []int) []int {
	seen := map[int]bool{}
	var out []int
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func splitMimeSubtype(mimeType string) string {
	for i := 0; i < len(mimeType); i++ {
		if mimeType[i] == '/' {
			return mimeType[i+1:]
		}
	}
	return ""
}

// ProcessImageOptions configure ProcessImage.
type ProcessImageOptions struct {
	AutoResizeImages *bool
	ResizeOptions    *ImageResizeOptions
}

// ProcessImageResult carries success data or an omission message.
type ProcessImageResult struct {
	OK       bool
	Data     string
	MimeType string
	Hints    []string
	Message  string
}

func baseMimeType(mimeType string) string {
	for i := 0; i < len(mimeType); i++ {
		if mimeType[i] == ';' {
			return lowerTrim(mimeType[:i])
		}
	}
	return lowerTrim(mimeType)
}

func lowerTrim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	out := []byte(s)
	for i := range out {
		if out[i] >= 'A' && out[i] <= 'Z' {
			out[i] += 'a' - 'A'
		}
	}
	return string(out)
}

func normalizeSupportedImageMimeType(mimeType string) string {
	switch baseMimeType(mimeType) {
	case "image/png":
		return "image/png"
	case "image/jpeg", "image/jpg":
		return "image/jpeg"
	case "image/gif":
		return "image/gif"
	case "image/webp":
		return "image/webp"
	}
	return ""
}

type normalizedImage struct {
	bytes         image.Image
	mimeType      string
	convertedFrom string
}

func normalizeImageBytes(raw []byte, mimeType string) *normalizedImage {
	if normalized := normalizeSupportedImageMimeType(mimeType); normalized != "" {
		img, _, err := image.Decode(bytes.NewReader(raw))
		if err != nil {
			return nil
		}
		return &normalizedImage{bytes: img, mimeType: normalized}
	}

	pngBytes, ok := ConvertImageBytesToPng(raw)
	if !ok {
		return nil
	}
	img, _, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil
	}
	return &normalizedImage{bytes: img, mimeType: "image/png", convertedFrom: baseMimeType(mimeType)}
}

func conversionHint(from, to string) string {
	if from == "" || from == to {
		return ""
	}
	return fmt.Sprintf("[Image converted from %s to %s.]", from, to)
}

// FormatDimensionNote renders the resize summary hint.
func FormatDimensionNote(resized *ResizedImage) string {
	if !resized.WasResized {
		return ""
	}
	return fmt.Sprintf("[Image resized from %dx%d to %dx%d.]", resized.OriginalWidth, resized.OriginalHeight, resized.Width, resized.Height)
}

// ProcessImage normalizes, optionally resizes, and base64-encodes an image.
func ProcessImage(raw []byte, mimeType string, options *ProcessImageOptions) ProcessImageResult {
	autoResize := true
	if options != nil && options.AutoResizeImages != nil {
		autoResize = *options.AutoResizeImages
	}

	normalized := normalizeImageBytes(raw, mimeType)
	if normalized == nil {
		return ProcessImageResult{OK: false, Message: "[Image omitted: could not be converted to a supported inline image format.]"}
	}

	if autoResize {
		inputBytes := reencodeImage(normalized.bytes, normalized.mimeType)
		resized := ResizeImageInProcess(inputBytes, normalized.mimeType, nil)
		if options != nil {
			resized = ResizeImageInProcess(inputBytes, normalized.mimeType, options.ResizeOptions)
		}
		if resized == nil {
			return ProcessImageResult{OK: false, Message: "[Image omitted: could not be resized below the inline image size limit.]"}
		}

		var hints []string
		if hint := conversionHint(normalized.convertedFrom, resized.MimeType); hint != "" {
			hints = append(hints, hint)
		}
		if note := FormatDimensionNote(resized); note != "" {
			hints = append(hints, note)
		}
		return ProcessImageResult{OK: true, Data: resized.Data, MimeType: resized.MimeType, Hints: hints}
	}

	var hints []string
	if hint := conversionHint(normalized.convertedFrom, normalized.mimeType); hint != "" {
		hints = append(hints, hint)
	}

	var encoded bytes.Buffer
	switch normalized.mimeType {
	case "image/jpeg":
		_ = jpeg.Encode(&encoded, normalized.bytes, nil)
	case "image/gif":
		_ = gif.Encode(&encoded, normalized.bytes, nil)
	default:
		_ = png.Encode(&encoded, normalized.bytes)
	}
	return ProcessImageResult{OK: true, Data: base64.StdEncoding.EncodeToString(encoded.Bytes()), MimeType: normalized.mimeType, Hints: hints}
}

// reencodeImage serializes a decoded image back to bytes for size checks.
func reencodeImage(img image.Image, mimeType string) []byte {
	var buf bytes.Buffer
	switch mimeType {
	case "image/jpeg":
		_ = jpeg.Encode(&buf, img, nil)
	case "image/gif":
		_ = gif.Encode(&buf, img, nil)
	default:
		_ = png.Encode(&buf, img)
	}
	return buf.Bytes()
}

func maxIntGo(a, b int) int {
	if a > b {
		return a
	}
	return b
}
