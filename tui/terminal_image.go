package tui

// Port of terminal-image.ts: capability detection, cell/image dimensions,
// Kitty/iTerm2 graphics encoding, image-line parsing/cropping, and the text
// fallback for terminals without inline images.

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ImageProtocol selects the inline-image protocol, or none.
type ImageProtocol string

const (
	ImageProtocolKitty  ImageProtocol = "kitty"
	ImageProtocolITerm2 ImageProtocol = "iterm2"
	ImageProtocolNone   ImageProtocol = ""
)

// TerminalCapabilities reports what the terminal supports.
type TerminalCapabilities struct {
	Images     ImageProtocol
	TrueColor  bool
	Hyperlinks bool
}

// CellDimensions is the terminal cell size in pixels, updated by TUI after a
// CSI 16 t query.
type CellDimensions struct {
	WidthPx  int
	HeightPx int
}

// ImageDimensions is an image's pixel size.
type ImageDimensions struct {
	WidthPx  int
	HeightPx int
}

// ImageRenderOptions controls inline image rendering.
type ImageRenderOptions struct {
	MaxWidthCells       *int
	MaxHeightCells      *int
	PreserveAspectRatio bool
	// ImageID reuses/replaces an existing Kitty image.
	ImageID *int
	// MoveCursor lets Kitty apply its default cursor movement (default true).
	MoveCursor *bool
}

var capabilitiesMu sync.Mutex
var cachedCapabilities *TerminalCapabilities
var capabilityOverrides TerminalCapabilitiesOverrides

// TerminalCapabilitiesOverrides overrides selected auto-detected
// capabilities; nil fields keep detection results.
type TerminalCapabilitiesOverrides struct {
	Images     *ImageProtocol
	TrueColor  *bool
	Hyperlinks *bool
}

// Default cell dimensions - updated by TUI when the terminal responds to a query.
var cellDims = CellDimensions{WidthPx: 9, HeightPx: 18}

// GetCellDimensions returns the current cell size.
func GetCellDimensions() CellDimensions {
	capabilitiesMu.Lock()
	defer capabilitiesMu.Unlock()
	return cellDims
}

// SetCellDimensions updates the cell size.
func SetCellDimensions(dims CellDimensions) {
	capabilitiesMu.Lock()
	cellDims = dims
	capabilitiesMu.Unlock()
}

// probeTmuxHyperlinks checks whether the attached tmux client forwards OSC 8
// hyperlinks (client_termfeatures lists "hyperlinks"); false on any error.
func probeTmuxHyperlinks() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{client_termfeatures}")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	for _, feature := range strings.Split(string(output), ",") {
		if strings.TrimSpace(feature) == "hyperlinks" {
			return true
		}
	}
	return false
}

func detectCapabilitiesFromEnvironment(tmuxForwardsHyperlink func() bool) TerminalCapabilities {
	termProgram := strings.ToLower(os.Getenv("TERM_PROGRAM"))
	terminalEmulator := strings.ToLower(os.Getenv("TERMINAL_EMULATOR"))
	term := strings.ToLower(os.Getenv("TERM"))
	colorTerm := strings.ToLower(os.Getenv("COLORTERM"))
	hasTrueColorHint := colorTerm == "truecolor" || colorTerm == "24bit" || strings.HasSuffix(term, "-direct")
	isWindowsConsole := runtimeGOOS() == "windows"

	// OSC 8 hyperlinks only when tmux confirms forwarding; image protocols
	// are unreliable under tmux.
	if os.Getenv("TMUX") != "" || strings.HasPrefix(term, "tmux") {
		return TerminalCapabilities{Images: ImageProtocolNone, TrueColor: hasTrueColorHint, Hyperlinks: tmuxForwardsHyperlink()}
	}

	// screen does not forward OSC 8 hyperlinks.
	if strings.HasPrefix(term, "screen") {
		return TerminalCapabilities{Images: ImageProtocolNone, TrueColor: hasTrueColorHint, Hyperlinks: false}
	}

	if os.Getenv("KITTY_WINDOW_ID") != "" || termProgram == "kitty" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}

	if termProgram == "ghostty" || strings.Contains(term, "ghostty") || os.Getenv("GHOSTTY_RESOURCES_DIR") != "" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}

	if os.Getenv("WEZTERM_PANE") != "" || termProgram == "wezterm" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}

	// Warp supports the Kitty graphics protocol and OSC 8 hyperlinks.
	if termProgram == "warpterminal" || os.Getenv("WARP_SESSION_ID") != "" || os.Getenv("WARP_TERMINAL_SESSION_UUID") != "" {
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	}

	if os.Getenv("ITERM_SESSION_ID") != "" || termProgram == "iterm.app" {
		return TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true}
	}

	if os.Getenv("WT_SESSION") != "" {
		return TerminalCapabilities{Images: ImageProtocolNone, TrueColor: true, Hyperlinks: true}
	}

	if termProgram == "alacritty" || termProgram == "vscode" || termProgram == "zed" {
		return TerminalCapabilities{Images: ImageProtocolNone, TrueColor: true, Hyperlinks: true}
	}

	if terminalEmulator == "jetbrains-jediterm" {
		return TerminalCapabilities{Images: ImageProtocolNone, TrueColor: true, Hyperlinks: false}
	}

	// Windows consoles support truecolor; keep hyperlinks off unless detected.
	if isWindowsConsole {
		return TerminalCapabilities{Images: ImageProtocolNone, TrueColor: true, Hyperlinks: false}
	}

	// Unknown terminal: conservative. OSC 8 rendered invisibly makes the URL
	// disappear, so default to the legacy `text (url)` behavior.
	return TerminalCapabilities{Images: ImageProtocolNone, TrueColor: hasTrueColorHint, Hyperlinks: false}
}

func parseBooleanCapabilityOverride(value string) *bool {
	switch value {
	case "1":
		tr := true
		return &tr
	case "0":
		f := false
		return &f
	}
	return nil
}

// DetectCapabilities detects terminal capabilities from the environment.
func DetectCapabilities(tmuxForwardsHyperlink func() bool) TerminalCapabilities {
	if tmuxForwardsHyperlink == nil {
		tmuxForwardsHyperlink = probeTmuxHyperlinks
	}
	hyperlinks := parseBooleanCapabilityOverride(os.Getenv("PI_HYPERLINKS"))
	probe := tmuxForwardsHyperlink
	if hyperlinks != nil {
		probe = func() bool { return *hyperlinks }
	}
	detected := detectCapabilitiesFromEnvironment(probe)

	imageProtocol := strings.ToLower(os.Getenv("PI_IMAGE_PROTOCOL"))
	if imageProtocol == "kitty" || imageProtocol == "iterm2" {
		detected.Images = ImageProtocol(imageProtocol)
	} else if imageProtocol == "none" || imageProtocol == "0" {
		detected.Images = ImageProtocolNone
	}
	if trueColor := parseBooleanCapabilityOverride(os.Getenv("PI_TRUE_COLOR")); trueColor != nil {
		detected.TrueColor = *trueColor
	}
	if hyperlinks != nil {
		detected.Hyperlinks = *hyperlinks
	}
	return detected
}

// GetCapabilities returns the cached capabilities, detecting on first use.
func GetCapabilities() TerminalCapabilities {
	capabilitiesMu.Lock()
	defer capabilitiesMu.Unlock()
	if cachedCapabilities == nil {
		var probe func() bool
		if capabilityOverrides.Hyperlinks != nil {
			h := *capabilityOverrides.Hyperlinks
			probe = func() bool { return h }
		}
		caps := DetectCapabilities(probe)
		if capabilityOverrides.Images != nil {
			caps.Images = *capabilityOverrides.Images
		}
		if capabilityOverrides.TrueColor != nil {
			caps.TrueColor = *capabilityOverrides.TrueColor
		}
		if capabilityOverrides.Hyperlinks != nil {
			caps.Hyperlinks = *capabilityOverrides.Hyperlinks
		}
		cachedCapabilities = &caps
	}
	return *cachedCapabilities
}

// GetTerminalColorMode picks the ANSI color mode from capabilities.
func GetTerminalColorMode(capabilities TerminalCapabilities) TerminalColorMode {
	if capabilities.TrueColor {
		return TerminalColorModeTrueColor
	}
	return TerminalColorMode256
}

// ResetCapabilitiesCache clears the cached detection.
func ResetCapabilitiesCache() {
	capabilitiesMu.Lock()
	cachedCapabilities = nil
	capabilitiesMu.Unlock()
}

// SetCapabilityOverrides overrides selected auto-detected capabilities.
func SetCapabilityOverrides(overrides TerminalCapabilitiesOverrides) {
	capabilitiesMu.Lock()
	defer capabilitiesMu.Unlock()
	same := overridesEqual(capabilityOverrides, overrides)
	if same {
		return
	}
	capabilityOverrides = overrides
	cachedCapabilities = nil
}

func overridesEqual(a, b TerminalCapabilitiesOverrides) bool {
	samePtr := func(x, y *bool) bool {
		if (x == nil) != (y == nil) {
			return false
		}
		return x == nil || *x == *y
	}
	sameImages := (a.Images == nil) == (b.Images == nil) && (a.Images == nil || *a.Images == *b.Images)
	return sameImages && samePtr(a.TrueColor, b.TrueColor) && samePtr(a.Hyperlinks, b.Hyperlinks)
}

// SetCapabilities replaces the cached capabilities (tests use this to
// exercise both code paths).
func SetCapabilities(caps TerminalCapabilities) {
	capabilitiesMu.Lock()
	cachedCapabilities = &caps
	capabilitiesMu.Unlock()
}

const kittyPrefix = "\x1b_G"
const iterm2Prefix = "\x1b]1337;File="

// IsImageLine reports whether the line carries an inline image sequence.
func IsImageLine(line string) bool {
	// Fast path: sequence at line start (single-row images); slow path:
	// multi-row images have a cursor-up prefix before the sequence.
	return strings.Contains(line, kittyPrefix) || strings.Contains(line, itererm2PrefixSafe())
}

func itererm2PrefixSafe() string { return iterm2Prefix }

// AllocateImageID generates a random Kitty image ID in [1, 0xffffffff] to
// avoid collisions between module instances.
func AllocateImageID() int {
	return rand.Intn(0xfffffffe) + 1
}

// EncodeKittyOptions configures Kitty graphics transmission.
type EncodeKittyOptions struct {
	Columns    *int
	Rows       *int
	ImageID    *int
	MoveCursor *bool // default true; false adds C=1
}

// EncodeKitty encodes base64 image data as Kitty graphics chunks of 4096.
func EncodeKitty(base64Data string, options EncodeKittyOptions) string {
	const chunkSize = 4096

	params := []string{"a=T", "f=100", "q=2"}

	if options.MoveCursor != nil && !*options.MoveCursor {
		params = append(params, "C=1")
	}
	if options.Columns != nil && *options.Columns != 0 {
		params = append(params, "c="+strconv.Itoa(*options.Columns))
	}
	if options.Rows != nil && *options.Rows != 0 {
		params = append(params, "r="+strconv.Itoa(*options.Rows))
	}
	if options.ImageID != nil && *options.ImageID != 0 {
		params = append(params, "i="+strconv.Itoa(*options.ImageID))
	}

	if len(base64Data) <= chunkSize {
		return kittyPrefix + strings.Join(params, ",") + ";" + base64Data + "\x1b\\"
	}

	var chunks []string
	offset := 0
	isFirst := true

	for offset < len(base64Data) {
		chunkEnd := offset + chunkSize
		if chunkEnd > len(base64Data) {
			chunkEnd = len(base64Data)
		}
		chunk := base64Data[offset:chunkEnd]
		isLast := offset+chunkSize >= len(base64Data)

		if isFirst {
			chunks = append(chunks, kittyPrefix+strings.Join(params, ",")+",m=1;"+chunk+"\x1b\\")
			isFirst = false
		} else if isLast {
			chunks = append(chunks, kittyPrefix+"m=0;"+chunk+"\x1b\\")
		} else {
			chunks = append(chunks, kittyPrefix+"m=1;"+chunk+"\x1b\\")
		}

		offset += chunkSize
	}

	return strings.Join(chunks, "")
}

// DeleteKittyImage builds the delete-by-ID command (uppercase I frees data).
func DeleteKittyImage(imageID int) string {
	return "\x1b_Ga=d,d=I,i=" + strconv.Itoa(imageID) + ",q=2\x1b\\"
}

// DeleteAllKittyImages builds the delete-all command (uppercase A frees data).
func DeleteAllKittyImages() string {
	return "\x1b_Ga=d,d=A,q=2\x1b\\"
}

// DeleteAllKittyPlacements deletes visible placements, retaining image data.
func DeleteAllKittyPlacements() string {
	return "\x1b_Ga=d,d=a,q=2\x1b\\"
}

// EncodeITerm2Options configures iTerm2 inline image encoding.
type EncodeITerm2Options struct {
	Width               any // int or string
	Height              any // int or string
	Name                string
	PreserveAspectRatio *bool
	Inline              *bool
}

// EncodeITerm2 encodes base64 image data as an iTerm2 file sequence.
func EncodeITerm2(base64Data string, options EncodeITerm2Options) string {
	inline := true
	if options.Inline != nil {
		inline = *options.Inline
	}
	inlineValue := 0
	if inline {
		inlineValue = 1
	}
	size := base64StdDecodeLen(base64Data)
	params := []string{
		"inline=" + strconv.Itoa(inlineValue),
		"size=" + strconv.Itoa(size),
	}

	if options.Width != nil {
		params = append(params, "width="+formatITerm2Size(options.Width))
	}
	if options.Height != nil {
		params = append(params, "height="+formatITerm2Size(options.Height))
	}
	if options.Name != "" {
		params = append(params, "name="+base64.StdEncoding.EncodeToString([]byte(options.Name)))
	}
	if options.PreserveAspectRatio != nil && !*options.PreserveAspectRatio {
		params = append(params, "preserveAspectRatio=0")
	}

	return "\x1b]1337;File=" + strings.Join(params, ";") + ":" + base64Data + "\x07"
}

func formatITerm2Size(value any) string {
	switch v := value.(type) {
	case int:
		return strconv.Itoa(v)
	case string:
		return v
	}
	return ""
}

func base64StdDecodeLen(base64Data string) int {
	decoded := decodeBase64(base64Data)
	return len(decoded)
}

// ImageCellSize is an image size in terminal cells.
type ImageCellSize struct {
	Columns int
	Rows    int
}

// KittyImageMetadata describes a transmitted Kitty image placement.
type KittyImageMetadata struct {
	ImageID  int
	Columns  int
	Rows     int
	WidthPx  int
	HeightPx int
}

type registeredKittyImageMetadata struct {
	KittyImageMetadata
	transmissionGeneration int
}

// KittyImagePlacement is a placement-only command for an image line emitted
// by RenderImage.
type KittyImagePlacement struct {
	ImageID                int
	TransmissionGeneration int
	TransmissionBytes      int
	EstimatedDecodedBytes  int
	Sequence               string
	ReplacementLine        string
}

var kittyMetadataMu sync.Mutex
var kittyImageMetadata = map[int]*registeredKittyImageMetadata{}
var kittyMetadataOrder []int
var kittyTransmissionGeneration = 0

// RegisterKittyImageMetadata records placement metadata for an image ID,
// evicting the oldest entry beyond 1000.
func RegisterKittyImageMetadata(metadata KittyImageMetadata) {
	kittyMetadataMu.Lock()
	defer kittyMetadataMu.Unlock()
	kittyTransmissionGeneration++
	if _, exists := kittyImageMetadata[metadata.ImageID]; !exists {
		kittyMetadataOrder = append(kittyMetadataOrder, metadata.ImageID)
	}
	kittyImageMetadata[metadata.ImageID] = &registeredKittyImageMetadata{
		KittyImageMetadata:     metadata,
		transmissionGeneration: kittyTransmissionGeneration,
	}
	if len(kittyImageMetadata) > 1000 && len(kittyMetadataOrder) > 0 {
		oldest := kittyMetadataOrder[0]
		kittyMetadataOrder = kittyMetadataOrder[1:]
		delete(kittyImageMetadata, oldest)
	}
}

var kittyControlsPattern = regexp.MustCompile(`\x1b_G([^;]*);`)
var kittyImageIDPattern = regexp.MustCompile(`(?:^|,)i=(\d+)(?:,|$)`)
var kittyChunkedPattern = regexp.MustCompile(`(?:^|,)m=1(?:,|$)`)
var yhrControlPattern = regexp.MustCompile(`^[yhr]=`)

func getRegisteredKittyImageMetadata(line string) *registeredKittyImageMetadata {
	match := kittyControlsPattern.FindStringSubmatch(line)
	if match == nil {
		return nil
	}
	idMatch := kittyImageIDPattern.FindStringSubmatch(match[1])
	if idMatch == nil {
		return nil
	}
	imageID, _ := strconv.Atoi(idMatch[1])
	kittyMetadataMu.Lock()
	defer kittyMetadataMu.Unlock()
	return kittyImageMetadata[imageID]
}

// GetKittyImageMetadata returns the placement metadata for an image line.
func GetKittyImageMetadata(line string) *KittyImageMetadata {
	metadata := getRegisteredKittyImageMetadata(line)
	if metadata == nil {
		return nil
	}
	copied := metadata.KittyImageMetadata
	return &copied
}

var kittyPlacementControlKeys = map[string]bool{
	"i": true, "p": true, "x": true, "y": true, "w": true, "h": true,
	"X": true, "Y": true, "c": true, "r": true, "C": true, "U": true,
	"z": true, "P": true, "Q": true, "H": true, "V": true,
}

// GetKittyImagePlacement builds a placement-only command for an image line.
func GetKittyImagePlacement(line string) *KittyImagePlacement {
	match := kittyControlsPattern.FindStringIndex(line)
	metadata := getRegisteredKittyImageMetadata(line)
	if match == nil || metadata == nil {
		return nil
	}
	controlsText := kittyControlsPattern.FindStringSubmatch(line)[1]

	commandStart := match[0]
	commandControls := controlsText
	var transmissionEnd int
	for {
		terminator := strings.Index(line[commandStart:], "\x1b\\")
		if terminator == -1 {
			return nil
		}
		terminator += commandStart
		transmissionEnd = terminator + 2
		if !kittyChunkedPattern.MatchString(commandControls) {
			break
		}
		commandStart = transmissionEnd
		if !strings.HasPrefix(line[commandStart:], kittyPrefix) {
			return nil
		}
		controlsEnd := strings.Index(line[commandStart+len(kittyPrefix):], ";")
		if controlsEnd == -1 {
			return nil
		}
		commandControls = line[commandStart+len(kittyPrefix) : commandStart+len(kittyPrefix)+controlsEnd]
	}

	var controls []string
	for _, control := range strings.Split(controlsText, ",") {
		key := control
		if idx := strings.Index(control, "="); idx != -1 {
			key = control[:idx]
		}
		if kittyPlacementControlKeys[key] {
			controls = append(controls, control)
		}
	}
	sequence := kittyPrefix + "a=p,q=2," + strings.Join(controls, ",") + "\x1b\\"
	return &KittyImagePlacement{
		ImageID:                metadata.ImageID,
		TransmissionGeneration: metadata.transmissionGeneration,
		TransmissionBytes:      transmissionEnd - match[0],
		EstimatedDecodedBytes:  metadata.WidthPx * metadata.HeightPx * 4,
		Sequence:               sequence,
		ReplacementLine:        line[:match[0]] + sequence + line[transmissionEnd:],
	}
}

// CropKittyImageLine crops an image line to a visible row window by rewriting
// the y/h/r placement controls.
func CropKittyImageLine(line string, hiddenRows, visibleRows int) string {
	metadata := GetKittyImageMetadata(line)
	match := kittyControlsPattern.FindStringIndex(line)
	if match == nil {
		return line
	}
	controlsText := kittyControlsPattern.FindStringSubmatch(line)[1]
	if metadata == nil || hiddenRows < 0 || hiddenRows >= metadata.Rows || visibleRows <= 0 {
		return line
	}
	croppedRows := visibleRows
	if metadata.Rows-hiddenRows < croppedRows {
		croppedRows = metadata.Rows - hiddenRows
	}
	if hiddenRows == 0 && croppedRows == metadata.Rows {
		return line
	}
	sourceY := (metadata.HeightPx * hiddenRows) / metadata.Rows
	sourceEnd := ceilDiv(metadata.HeightPx*(hiddenRows+croppedRows), metadata.Rows)
	sourceHeight := metadata.HeightPx
	if sourceEnd < sourceHeight {
		sourceHeight = sourceEnd
	}
	sourceHeight -= sourceY
	if sourceHeight < 1 {
		sourceHeight = 1
	}
	var controls []string
	for _, control := range strings.Split(controlsText, ",") {
		if !yhrControlPattern.MatchString(control) {
			controls = append(controls, control)
		}
	}
	controls = append(controls,
		"y="+strconv.Itoa(sourceY),
		"h="+strconv.Itoa(sourceHeight),
		"r="+strconv.Itoa(croppedRows))
	return line[:match[0]] + kittyPrefix + strings.Join(controls, ",") + ";" + line[match[1]:]
}

func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}

func chooseLessDistortedCellCount(upperCount int, idealCount float64) int {
	if upperCount <= 1 {
		return upperCount
	}

	lowerCount := upperCount - 1
	upperDistortion := mathMax2(float64(upperCount)/idealCount, idealCount/float64(upperCount))
	lowerDistortion := mathMax2(float64(lowerCount)/idealCount, idealCount/float64(lowerCount))
	if lowerDistortion < upperDistortion {
		return lowerCount
	}
	return upperCount
}

func mathMax2(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// CalculateImageCellSize fits an image into a cell budget. With
// optimizeAspectRatio, cell counts snap toward the less-distorted side.
func CalculateImageCellSize(imageDimensions ImageDimensions, maxWidthCells int, maxHeightCells *int, cellDimensions CellDimensions, optimizeAspectRatio bool) ImageCellSize {
	maxWidth := maxInt(1, maxWidthCells)
	maxHeight := 0
	if maxHeightCells != nil {
		maxHeight = maxInt(1, *maxHeightCells)
	}
	imageWidth := maxInt(1, imageDimensions.WidthPx)
	imageHeight := maxInt(1, imageDimensions.HeightPx)

	widthScale := float64(maxWidth*cellDimensions.WidthPx) / float64(imageWidth)
	heightScale := widthScale
	if maxHeightCells != nil {
		heightScale = float64(maxHeight*cellDimensions.HeightPx) / float64(imageHeight)
	}
	scale := widthScale
	if heightScale < scale {
		scale = heightScale
	}

	scaledWidthPx := float64(imageWidth) * scale
	scaledHeightPx := float64(imageHeight) * scale
	columns := maxInt(1, minInt(maxWidth, ceilFloat(scaledWidthPx/float64(cellDimensions.WidthPx))))
	heightRows := scaledHeightPx / float64(cellDimensions.HeightPx)
	rows := maxInt(1, ceilFloat(heightRows))
	if maxHeightCells != nil {
		rows = minInt(maxHeight, rows)
	}

	if !optimizeAspectRatio {
		return ImageCellSize{Columns: columns, Rows: rows}
	}

	if widthScale <= heightScale {
		idealRows := float64(columns) * float64(cellDimensions.WidthPx) * float64(imageHeight) /
			(float64(imageWidth) * float64(cellDimensions.HeightPx))
		rows = chooseLessDistortedCellCount(rows, idealRows)
	} else {
		idealColumns := float64(rows) * float64(cellDimensions.HeightPx) * float64(imageWidth) /
			(float64(imageHeight) * float64(cellDimensions.WidthPx))
		columns = chooseLessDistortedCellCount(columns, idealColumns)
	}

	return ImageCellSize{Columns: columns, Rows: rows}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func ceilFloat(v float64) int {
	return int(jsCeil(v))
}

// CalculateImageRows returns the row count for an image at a target width.
func CalculateImageRows(imageDimensions ImageDimensions, targetWidthCells int, cellDimensions CellDimensions) int {
	return CalculateImageCellSize(imageDimensions, targetWidthCells, nil, cellDimensions, false).Rows
}

func decodeBase64(base64Data string) []byte {
	decoded, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		// Tolerate invalid padding like Buffer.from.
		decoded, _ = base64.RawStdEncoding.DecodeString(strings.TrimRight(base64Data, "="))
	}
	return decoded
}

// GetPngDimensions reads the PNG IHDR dimensions.
func GetPngDimensions(base64Data string) *ImageDimensions {
	buffer := decodeBase64(base64Data)
	if len(buffer) < 24 {
		return nil
	}
	if buffer[0] != 0x89 || buffer[1] != 0x50 || buffer[2] != 0x4e || buffer[3] != 0x47 {
		return nil
	}
	return &ImageDimensions{
		WidthPx:  int(binary.BigEndian.Uint32(buffer[16:20])),
		HeightPx: int(binary.BigEndian.Uint32(buffer[20:24])),
	}
}

// GetJpegDimensions scans JPEG markers for the SOF frame dimensions.
func GetJpegDimensions(base64Data string) *ImageDimensions {
	buffer := decodeBase64(base64Data)
	if len(buffer) < 2 {
		return nil
	}
	if buffer[0] != 0xff || buffer[1] != 0xd8 {
		return nil
	}

	offset := 2
	for offset < len(buffer)-9 {
		if buffer[offset] != 0xff {
			offset++
			continue
		}

		marker := buffer[offset+1]

		if marker >= 0xc0 && marker <= 0xc2 {
			return &ImageDimensions{
				HeightPx: int(binary.BigEndian.Uint16(buffer[offset+5 : offset+7])),
				WidthPx:  int(binary.BigEndian.Uint16(buffer[offset+7 : offset+9])),
			}
		}

		if offset+3 >= len(buffer) {
			return nil
		}
		length := int(binary.BigEndian.Uint16(buffer[offset+2 : offset+4]))
		if length < 2 {
			return nil
		}
		offset += 2 + length
	}

	return nil
}

// GetGifDimensions reads the GIF logical screen dimensions.
func GetGifDimensions(base64Data string) *ImageDimensions {
	buffer := decodeBase64(base64Data)
	if len(buffer) < 10 {
		return nil
	}
	sig := string(buffer[0:6])
	if sig != "GIF87a" && sig != "GIF89a" {
		return nil
	}
	return &ImageDimensions{
		WidthPx:  int(binary.LittleEndian.Uint16(buffer[6:8])),
		HeightPx: int(binary.LittleEndian.Uint16(buffer[8:10])),
	}
}

// GetWebpDimensions reads VP8/VP8L/VP8X chunk dimensions.
func GetWebpDimensions(base64Data string) *ImageDimensions {
	buffer := decodeBase64(base64Data)
	if len(buffer) < 30 {
		return nil
	}
	if string(buffer[0:4]) != "RIFF" || string(buffer[8:12]) != "WEBP" {
		return nil
	}

	chunk := string(buffer[12:16])
	switch chunk {
	case "VP8 ":
		return &ImageDimensions{
			WidthPx:  int(binary.LittleEndian.Uint16(buffer[26:28])) & 0x3fff,
			HeightPx: int(binary.LittleEndian.Uint16(buffer[28:30])) & 0x3fff,
		}
	case "VP8L":
		if len(buffer) < 25 {
			return nil
		}
		bits := binary.LittleEndian.Uint32(buffer[21:25])
		return &ImageDimensions{
			WidthPx:  int(bits&0x3fff) + 1,
			HeightPx: int((bits>>14)&0x3fff) + 1,
		}
	case "VP8X":
		width := int(uint32(buffer[24])|uint32(buffer[25])<<8|uint32(buffer[26])<<16) + 1
		height := int(uint32(buffer[27])|uint32(buffer[28])<<8|uint32(buffer[29])<<16) + 1
		return &ImageDimensions{WidthPx: width, HeightPx: height}
	}
	return nil
}

// GetImageDimensions dispatches dimension sniffing by MIME type.
func GetImageDimensions(base64Data, mimeType string) *ImageDimensions {
	switch mimeType {
	case "image/png":
		return GetPngDimensions(base64Data)
	case "image/jpeg":
		return GetJpegDimensions(base64Data)
	case "image/gif":
		return GetGifDimensions(base64Data)
	case "image/webp":
		return GetWebpDimensions(base64Data)
	}
	return nil
}

// RenderedImage is an inline image rendering result.
type RenderedImage struct {
	Sequence string
	Columns  int
	Rows     int
	ImageID  *int
}

// RenderImage encodes an image for the terminal's inline-image protocol.
func RenderImage(base64Data string, imageDimensions ImageDimensions, options ImageRenderOptions) *RenderedImage {
	caps := GetCapabilities()

	if caps.Images == ImageProtocolNone {
		return nil
	}

	maxWidth := 80
	if options.MaxWidthCells != nil {
		maxWidth = *options.MaxWidthCells
	}
	// Reduce Kitty's cell-aligned distortion without shrinking iTerm2
	// reservations.
	size := CalculateImageCellSize(imageDimensions, maxWidth, options.MaxHeightCells, GetCellDimensions(), caps.Images == ImageProtocolKitty)

	if caps.Images == ImageProtocolKitty {
		if options.ImageID != nil {
			RegisterKittyImageMetadata(KittyImageMetadata{
				ImageID:  *options.ImageID,
				Columns:  size.Columns,
				Rows:     size.Rows,
				WidthPx:  imageDimensions.WidthPx,
				HeightPx: imageDimensions.HeightPx,
			})
		}
		sequence := EncodeKitty(base64Data, EncodeKittyOptions{
			Columns:    &size.Columns,
			Rows:       &size.Rows,
			ImageID:    options.ImageID,
			MoveCursor: options.MoveCursor,
		})
		return &RenderedImage{Sequence: sequence, Columns: size.Columns, Rows: size.Rows, ImageID: options.ImageID}
	}

	if caps.Images == ImageProtocolITerm2 {
		sequence := EncodeITerm2(base64Data, EncodeITerm2Options{
			Width:               size.Columns,
			Height:              "auto",
			PreserveAspectRatio: &[]bool{options.PreserveAspectRatio}[0],
		})
		return &RenderedImage{Sequence: sequence, Columns: size.Columns, Rows: size.Rows}
	}

	return nil
}

// Hyperlink wraps text in an OSC 8 hyperlink sequence; terminals without
// OSC 8 ignore the escapes and show only the text.
func Hyperlink(text, urlAddress string) string {
	return "\x1b]8;;" + urlAddress + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// shortenImagePath shortens home-prefixed absolute paths to ~/...
func shortenImagePath(filename string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filename
	}
	if filename == home || strings.HasPrefix(filename, home+"/") || strings.HasPrefix(filename, home+"\\") {
		return "~" + filename[len(home):]
	}
	return filename
}

// ImageFallback renders the text fallback when the terminal cannot show
// inline images: absolute paths shorten to ~/... and link via file:// when
// OSC 8 hyperlinks are available.
func ImageFallback(mimeType string, dimensions *ImageDimensions, filename string) string {
	var parts []string
	if filename != "" {
		display := shortenImagePath(filename)
		if GetCapabilities().Hyperlinks && fileIsAbsolute(filename) {
			parts = append(parts, Hyperlink(display, filePathToURL(filename)))
		} else {
			parts = append(parts, display)
		}
	}
	parts = append(parts, "["+mimeType+"]")
	if dimensions != nil {
		parts = append(parts, strconv.Itoa(dimensions.WidthPx)+"x"+strconv.Itoa(dimensions.HeightPx))
	}
	return "[Image: " + strings.Join(parts, " ") + "]"
}

func fileIsAbsolute(path string) bool {
	return strings.HasPrefix(path, "/") || fileIsWindowsAbsolute(path)
}

func fileIsWindowsAbsolute(path string) bool {
	if len(path) < 3 {
		return false
	}
	c := path[0]
	isDriveLetter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	return isDriveLetter && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

func filePathToURL(path string) string {
	if len(path) > 0 && path[0] == '/' {
		return (&url.URL{Scheme: "file", Path: path}).String()
	}
	// Windows drive path: file:///C:/...
	return (&url.URL{Scheme: "file", Path: "/" + strings.ReplaceAll(path, "\\", "/")}).String()
}

func jsCeil(v float64) float64 {
	if mathIsNaN(v) || mathIsInf(v) {
		return v
	}
	return -jsFloor(-v)
}
