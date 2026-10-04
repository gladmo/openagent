package codingagent

// Lanczos3-equivalent resampling and EXIF orientation for the photon
// replacement (documented deviation: Go image stack instead of Rust/WASM).

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
)

// resampleImage scales img to width x height with a Catmull-Rom filter
// (a close, cheap approximation of Lanczos3 for downscaling).
func resampleImage(img image.Image, width, height int) image.Image {
	srcBounds := img.Bounds()
	src := img
	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	xRatio := float64(srcBounds.Dx()) / float64(width)
	yRatio := float64(srcBounds.Dy()) / float64(height)

	for y := 0; y < height; y++ {
		srcY := (float64(y) + 0.5) * yRatio
		y0 := int(srcY-1.5) - srcBounds.Min.Y
		for x := 0; x < width; x++ {
			srcX := (float64(x) + 0.5) * xRatio
			x0 := int(srcX-1.5) - srcBounds.Min.X

			var rSum, gSum, bSum, aSum float64
			var weightSum float64
			for dy := -1; dy <= 2; dy++ {
				sy := clampInt(y0+dy+srcBounds.Min.Y, srcBounds.Min.Y, srcBounds.Max.Y-1)
				wy := catmullRom(float64(sy) - srcY)
				for dx := -1; dx <= 2; dx++ {
					sx := clampInt(x0+dx+srcBounds.Min.X, srcBounds.Min.X, srcBounds.Max.X-1)
					wx := catmullRom(float64(sx) - srcX)
					r, g, b, a := src.At(sx, sy).RGBA()
					w := wx * wy
					rSum += float64(r) * w
					gSum += float64(g) * w
					bSum += float64(b) * w
					aSum += float64(a) * w
					weightSum += w
				}
			}
			if weightSum == 0 {
				weightSum = 1
			}
			dst.SetRGBA(x, y, colorFromFloats(rSum/weightSum, gSum/weightSum, bSum/weightSum, aSum/weightSum))
		}
	}
	return dst
}

func catmullRom(t float64) float64 {
	tAbs := t
	if tAbs < 0 {
		tAbs = -tAbs
	}
	switch {
	case tAbs <= 1:
		return 1.5*tAbs*tAbs*tAbs - 2.5*tAbs*tAbs + 1
	case tAbs < 2:
		return -0.5*tAbs*tAbs*tAbs + 2.5*tAbs*tAbs - 4*tAbs + 2
	}
	return 0
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func colorFromFloats(r, g, b, a float64) color.RGBA {
	return color.RGBA{
		R: uint8(clampInt(int(r/257), 0, 255)),
		G: uint8(clampInt(int(g/257), 0, 255)),
		B: uint8(clampInt(int(b/257), 0, 255)),
		A: uint8(clampInt(int(a/257), 0, 255)),
	}
}

// exifOrientation extracts the EXIF orientation tag (1-8) from a JPEG.
func exifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	index := 2
	for index+4 <= len(data) {
		if data[index] != 0xFF {
			index++
			continue
		}
		marker := data[index+1]
		if marker == 0xE1 { // APP1 (EXIF)
			return parseExifOrientation(data[index+4:])
		}
		if marker == 0xDA || marker == 0xD9 { // SOS or EOI: no EXIF ahead
			return 1
		}
		if index+4 > len(data) {
			return 1
		}
		segmentLength := int(data[index+2])<<8 | int(data[index+3])
		index += 2 + segmentLength
	}
	return 1
}

func parseExifOrientation(exif []byte) int {
	// TIFF header: II*\0 or MM\0*
	if len(exif) < 8 {
		return 1
	}
	littleEndian := exif[0] == 'I'
	readU16 := func(b []byte) int {
		if littleEndian {
			return int(b[0]) | int(b[1])<<8
		}
		return int(b[0])<<8 | int(b[1])
	}
	readU32 := func(b []byte) int {
		if littleEndian {
			return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 | int(b[3])<<24
		}
		return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	}
	ifdOffset := readU32(exif[4:8])
	if ifdOffset+2 > len(exif) {
		return 1
	}
	count := readU16(exif[ifdOffset : ifdOffset+2])
	for i := 0; i < count; i++ {
		entry := ifdOffset + 2 + i*12
		if entry+12 > len(exif) {
			return 1
		}
		tag := readU16(exif[entry : entry+2])
		if tag == 0x0112 { // Orientation
			value := readU16(exif[entry+8 : entry+10])
			if value >= 1 && value <= 8 {
				return value
			}
			return 1
		}
	}
	return 1
}

// applyExifOrientation rotates/flips img per the EXIF orientation tag.
func applyExifOrientation(img image.Image, source []byte) image.Image {
	orientation := exifOrientation(source)
	if orientation == 1 {
		return img
	}

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	swapDims := orientation >= 5
	outW, outH := width, height
	if swapDims {
		outW, outH = height, width
	}
	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			var dx, dy int
			switch orientation {
			case 2: // mirror horizontal
				dx, dy = width-1-x, y
			case 3: // rotate 180
				dx, dy = width-1-x, height-1-y
			case 4: // mirror vertical
				dx, dy = x, height-1-y
			case 5: // mirror horizontal + rotate 270
				dx, dy = width-1-y, x
			case 6: // rotate 90
				dx, dy = width-1-y, x
			case 7: // mirror horizontal + rotate 90
				dx, dy = y, height-1-x
			case 8: // rotate 270
				dx, dy = y, height-1-x
			default:
				dx, dy = x, y
			}
			dst.Set(dx, dy, img.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}
	return dst
}

// encodeJpegForTest re-encodes via the jpeg path (probe helper).
func encodeJpegForTest(img image.Image) []byte {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	return buf.Bytes()
}
