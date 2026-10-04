package codingagent

// Port of core/export-html/index.ts (theme derivation + session-data
// assembly): color parsing/luminance/brightness adjustment, export color
// derivation from userMessageBg, theme CSS variables, and the session-data
// payload. The template-file rendering (marked/hljs vendor bundles) rides
// a template seam until the assets land.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// parseColorString parses #RRGGBB or rgb(r,g,b).
func parseColorString(color string) (r, g, b float64, ok bool) {
	hexPattern := regexp.MustCompile(`^#([0-9a-fA-F]{2})([0-9a-fA-F]{2})([0-9a-fA-F]{2})$`)
	if match := hexPattern.FindStringSubmatch(color); match != nil {
		parseHex := func(s string) float64 {
			value, _ := strconvParseUintHex(s)
			return float64(value)
		}
		return parseHex(match[1]), parseHex(match[2]), parseHex(match[3]), true
	}
	rgbPattern := regexp.MustCompile(`^rgb\s*\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*\)$`)
	if match := rgbPattern.FindStringSubmatch(color); match != nil {
		parseDec := func(s string) float64 {
			value, _ := strconvParseUintDec(s)
			return float64(value)
		}
		return parseDec(match[1]), parseDec(match[2]), parseDec(match[3]), true
	}
	return 0, 0, 0, false
}

func strconvParseUintHex(s string) (uint64, error) {
	var value uint64
	for _, c := range s {
		digit := -1
		switch {
		case c >= '0' && c <= '9':
			digit = int(c - '0')
		case c >= 'a' && c <= 'f':
			digit = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			digit = int(c-'A') + 10
		}
		if digit < 0 {
			return 0, errInvalidEntity
		}
		value = value*16 + uint64(digit)
	}
	return value, nil
}

func strconvParseUintDec(s string) (uint64, error) {
	if s == "" {
		return 0, errInvalidEntity
	}
	var value uint64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errInvalidEntity
		}
		value = value*10 + uint64(c-'0')
	}
	return value, nil
}

// luminanceOf computes the WCAG relative luminance (0-1).
func luminanceOf(r, g, b float64) float64 {
	return rgbLuminance(r, g, b)
}

// adjustBrightness multiplies channel values (clamped 0-255).
func adjustBrightness(color string, factor float64) string {
	r, g, b, ok := parseColorString(color)
	if !ok {
		return color
	}
	adjust := func(c float64) float64 {
		return math.Min(255, math.Max(0, math.Round(c*factor)))
	}
	return fmt.Sprintf("rgb(%d, %d, %d)", int(adjust(r)), int(adjust(g)), int(adjust(b)))
}

// ExportColors are the derived page/card/info backgrounds.
type ExportColors struct {
	PageBg string
	CardBg string
	InfoBg string
}

// deriveExportColors derives export backgrounds from a base color.
func deriveExportColors(baseColor string) ExportColors {
	r, g, b, ok := parseColorString(baseColor)
	if !ok {
		return ExportColors{
			PageBg: "rgb(24, 24, 30)",
			CardBg: "rgb(30, 30, 36)",
			InfoBg: "rgb(60, 55, 40)",
		}
	}

	luminance := luminanceOf(r, g, b)
	if luminance > 0.5 {
		return ExportColors{
			PageBg: adjustBrightness(baseColor, 0.96),
			CardBg: baseColor,
			InfoBg: fmt.Sprintf("rgb(%d, %d, %d)",
				int(math.Min(255, r+10)), int(math.Min(255, g+5)), int(math.Max(0, b-20))),
		}
	}
	return ExportColors{
		PageBg: adjustBrightness(baseColor, 0.7),
		CardBg: adjustBrightness(baseColor, 0.85),
		InfoBg: fmt.Sprintf("rgb(%d, %d, %d)",
			int(math.Min(255, r+20)), int(math.Min(255, g+15)), int(b)),
	}
}

// GenerateThemeVars renders the CSS custom properties from theme colors.
func GenerateThemeVars(colors map[string]string, userMessageBg string) string {
	var lines []string
	keys := make([]string, 0, len(colors))
	for key := range colors {
		keys = append(keys, key)
	}
	sortStrings(keys)
	for _, key := range keys {
		lines = append(lines, "--"+key+": "+colors[key]+";")
	}

	base := userMessageBg
	if base == "" {
		base = "#343541"
	}
	derived := deriveExportColors(base)
	lines = append(lines, "--exportPageBg: "+derived.PageBg+";")
	lines = append(lines, "--exportCardBg: "+derived.CardBg+";")
	lines = append(lines, "--exportInfoBg: "+derived.InfoBg+";")
	return strings.Join(lines, "\n      ")
}

// SessionData is the exported payload (base64-embedded in the template).
type SessionData struct {
	Header       *SessionHeader    `json:"header"`
	Entries      []json.RawMessage `json:"entries"`
	LeafID       string            `json:"leafId"`
	SystemPrompt string            `json:"systemPrompt,omitempty"`
	Tools        []ToolExport      `json:"tools,omitempty"`
}

// ToolExport is one exported tool declaration.
type ToolExport struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// BuildSessionData assembles the export payload from a session.
func BuildSessionData(manager *SessionManager, tools []ToolExport, systemPrompt string) (*SessionData, error) {
	header := manager.GetHeader()
	if header == nil {
		return nil, fmt.Errorf("Session has no header")
	}

	entries := manager.GetEntries()
	encoded := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, raw)
	}

	leafID := ""
	if leaf := manager.GetLeafID(); leaf != nil {
		leafID = *leaf
	}

	return &SessionData{
		Header:       header,
		Entries:      encoded,
		LeafID:       leafID,
		SystemPrompt: systemPrompt,
		Tools:        tools,
	}, nil
}

// EncodeSessionDataBase64 embeds the payload for the template.
func EncodeSessionDataBase64(data *SessionData) (string, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encoded), nil
}
