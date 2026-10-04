package codingagent

// Port of utils/version-check.ts and utils/tool-result-images.ts:
// release-check comparisons with the pi.dev endpoint, and normalization
// of oversized images entering session history.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// semverVersion is the parsed release triple.
type semverVersion struct {
	major, minor, patch int
}

// parseSemverStrict parses exact "M.m.p" (with an optional leading "v").
func parseSemverStrict(version string) *semverVersion {
	trimmed := strings.TrimPrefix(strings.TrimSpace(version), "v")
	parts := strings.SplitN(trimmed, ".", 3)
	if len(parts) != 3 {
		return nil
	}
	// Reject prerelease/build suffixes for the comparison path.
	for _, part := range parts {
		if part == "" {
			return nil
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return nil
			}
		}
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])
	return &semverVersion{major: major, minor: minor, patch: patch}
}

func compareSemver(a, b *semverVersion) int {
	if a.major != b.major {
		if a.major < b.major {
			return -1
		}
		return 1
	}
	if a.minor != b.minor {
		if a.minor < b.minor {
			return -1
		}
		return 1
	}
	if a.patch != b.patch {
		if a.patch < b.patch {
			return -1
		}
		return 1
	}
	return 0
}

// LatestPiRelease is the release-check response.
type LatestPiRelease struct {
	Version     string
	PackageName string
	Note        string
}

const (
	latestVersionURL             = "https://pi.dev/api/latest-version"
	defaultVersionCheckTimeoutMS = 10000
)

// FormatVersionCheckError includes errno details hidden behind generic
// transport errors.
func FormatVersionCheckError(rootMessage string, codes ...string) string {
	unique := map[string]bool{}
	var distinct []string
	for _, code := range codes {
		if code != "" && !unique[code] {
			unique[code] = true
			distinct = append(distinct, code)
		}
	}
	if len(distinct) > 0 {
		return rootMessage + " (" + strings.Join(distinct, ", ") + ")"
	}
	return rootMessage
}

// ComparePackageVersions semver-compares two versions (ok=false when
// either is invalid).
func ComparePackageVersions(leftVersion, rightVersion string) (int, bool) {
	left := parseSemverStrict(leftVersion)
	right := parseSemverStrict(rightVersion)
	if left == nil || right == nil {
		return 0, false
	}
	return compareSemver(left, right), true
}

// IsNewerPackageVersion reports a strictly newer candidate (exact string
// inequality when either side is invalid semver).
func IsNewerPackageVersion(candidateVersion, currentVersion string) bool {
	if comparison, ok := ComparePackageVersions(candidateVersion, currentVersion); ok {
		return comparison > 0
	}
	return strings.TrimSpace(candidateVersion) != strings.TrimSpace(currentVersion)
}

// GetLatestPiRelease fetches the release metadata (nil on failure or
// offline).
func GetLatestPiRelease(currentVersion string, timeoutMs int, retry bool) *LatestPiRelease {
	if osGetenv("PI_OFFLINE") != "" {
		return nil
	}
	if timeoutMs <= 0 {
		timeoutMs = defaultVersionCheckTimeoutMS
	}

	maxRetries := 0
	if retry {
		maxRetries = 2
	}
	client := &http.Client{Timeout: time.Duration(timeoutMs) * time.Millisecond}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		release, err := fetchLatestRelease(client, currentVersion)
		if err == nil {
			return release
		}
		lastErr = err
	}
	_ = lastErr
	return nil
}

func fetchLatestRelease(client *http.Client, currentVersion string) (*LatestPiRelease, error) {
	request, err := http.NewRequest("GET", latestVersionURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", GetPiUserAgent(currentVersion))
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", response.StatusCode)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var data struct {
		PackageName string `json:"packageName"`
		Version     string `json:"version"`
		Note        string `json:"note"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}
	version := strings.TrimSpace(data.Version)
	if version == "" {
		return nil, fmt.Errorf("missing version")
	}
	return &LatestPiRelease{
		Version:     version,
		PackageName: strings.TrimSpace(data.PackageName),
		Note:        strings.TrimSpace(data.Note),
	}, nil
}

// GetLatestPiVersion fetches just the version ("" when unavailable).
func GetLatestPiVersion(currentVersion string, timeoutMs int, retry bool) string {
	if release := GetLatestPiRelease(currentVersion, timeoutMs, retry); release != nil {
		return release.Version
	}
	return ""
}

// CheckForNewPiVersion returns the newer release when one exists (nil on
// any failure; suppressed by PI_SKIP_VERSION_CHECK).
func CheckForNewPiVersion(currentVersion string) *LatestPiRelease {
	if osGetenv("PI_SKIP_VERSION_CHECK") != "" {
		return nil
	}
	release := GetLatestPiRelease(currentVersion, 0, false)
	if release != nil && IsNewerPackageVersion(release.Version, currentVersion) {
		return release
	}
	return nil
}

// ============================================================================
// tool-result-images.ts
// ============================================================================

// ToolResultContentBlock is one normalized result block.
type ToolResultContentBlock struct {
	Type     string // "text" | "image"
	Text     string
	Data     string // base64
	MimeType string
}

// NormalizeToolResultImagesOptions bound normalization.
type NormalizeToolResultImagesOptions struct {
	AutoResizeImages bool
	HasResizeOptions bool
	MaxDimension     int
}

// NormalizeToolResultImages resizes oversized image blocks once as they
// enter history (providers reject the whole conversation otherwise).
// Returns the original slice when nothing changed.
func NormalizeToolResultImages(content []ToolResultContentBlock, options *NormalizeToolResultImagesOptions) []ToolResultContentBlock {
	hasImage := false
	for _, block := range content {
		if block.Type == "image" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return content
	}

	autoResize := true
	if options != nil {
		autoResize = options.AutoResizeImages
	}

	normalized := make([]ToolResultContentBlock, 0, len(content))
	changed := false
	for _, block := range content {
		if block.Type != "image" {
			normalized = append(normalized, block)
			continue
		}
		if !autoResize {
			normalized = append(normalized, block)
			continue
		}
		processed, wasChanged := processToolImageBlock(block, options)
		if wasChanged {
			changed = true
		}
		normalized = append(normalized, processed)
	}
	if !changed {
		return content
	}
	return normalized
}

// processToolImageBlock runs one image through the resize pipeline
// (ResizeImageInProcess owns decode/resize/encode).
func processToolImageBlock(block ToolResultContentBlock, options *NormalizeToolResultImagesOptions) (ToolResultContentBlock, bool) {
	decoded, err := base64DecodeStd(block.Data)
	if err != nil {
		return block, false
	}
	maxDimension := 0
	if options != nil && options.HasResizeOptions {
		maxDimension = options.MaxDimension
	}
	if maxDimension <= 0 {
		maxDimension = 8000
	}
	dimension := maxDimension
	resized := ResizeImageInProcess(decoded, block.MimeType, &ImageResizeOptions{
		MaxWidth:  &dimension,
		MaxHeight: &dimension,
	})
	if resized == nil || !resized.WasResized {
		return block, false
	}
	return ToolResultContentBlock{
		Type:     "image",
		Data:     resized.Data,
		MimeType: resized.MimeType,
	}, true
}

func base64DecodeStd(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}
