package codingagent

// Port of core/export-html/ansi-to-html.ts: ANSI escape codes to HTML with
// inline styles (standard/bright/256/RGB colors, bold/dim/italic/
// underline, reset).

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ansiColors is the 16-color palette.
var ansiColors = [16]string{
	"#000000", "#800000", "#008000", "#808000",
	"#000080", "#800080", "#008080", "#c0c0c0",
	"#808080", "#ff0000", "#00ff00", "#ffff00",
	"#0000ff", "#ff00ff", "#00ffff", "#ffffff",
}

// color256ToHex converts a 256-color index to hex.
func color256ToHex(index int) string {
	if index < 16 {
		return ansiColors[index]
	}
	if index < 232 {
		cubeIndex := index - 16
		r := cubeIndex / 36
		g := (cubeIndex % 36) / 6
		b := cubeIndex % 6
		toComponent := func(n int) int {
			if n == 0 {
				return 0
			}
			return 55 + n*40
		}
		return fmt.Sprintf("#%02x%02x%02x", toComponent(r), toComponent(g), toComponent(b))
	}
	gray := 8 + (index-232)*10
	return fmt.Sprintf("#%02x%02x%02x", gray, gray, gray)
}

// escapeHtml escapes the five specials.
func escapeHtml(text string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#039;",
	)
	return replacer.Replace(text)
}

// textStyle tracks the SGR state.
type textStyle struct {
	fg        string
	bg        string
	bold      bool
	dim       bool
	italic    bool
	underline bool
}

func (s *textStyle) any() bool {
	return s.fg != "" || s.bg != "" || s.bold || s.dim || s.italic || s.underline
}

func (s *textStyle) css() string {
	var parts []string
	if s.fg != "" {
		parts = append(parts, "color:"+s.fg)
	}
	if s.bg != "" {
		parts = append(parts, "background-color:"+s.bg)
	}
	if s.bold {
		parts = append(parts, "font-weight:bold")
	}
	if s.dim {
		parts = append(parts, "opacity:0.6")
	}
	if s.italic {
		parts = append(parts, "font-style:italic")
	}
	if s.underline {
		parts = append(parts, "text-decoration:underline")
	}
	return strings.Join(parts, ";")
}

// applySgrCode parses SGR parameters into the style.
func applySgrCode(params []int, style *textStyle) {
	i := 0
	for i < len(params) {
		code := params[i]
		switch {
		case code == 0:
			*style = textStyle{}
		case code == 1:
			style.bold = true
		case code == 2:
			style.dim = true
		case code == 3:
			style.italic = true
		case code == 4:
			style.underline = true
		case code == 22:
			style.bold = false
			style.dim = false
		case code == 23:
			style.italic = false
		case code == 24:
			style.underline = false
		case code >= 30 && code <= 37:
			style.fg = ansiColors[code-30]
		case code == 38:
			if i+1 < len(params) && params[i+1] == 5 && i+2 < len(params) {
				style.fg = color256ToHex(params[i+2])
				i += 2
			} else if i+1 < len(params) && params[i+1] == 2 && i+4 < len(params) {
				style.fg = fmt.Sprintf("rgb(%d,%d,%d)", params[i+2], params[i+3], params[i+4])
				i += 4
			}
		case code == 39:
			style.fg = ""
		case code >= 40 && code <= 47:
			style.bg = ansiColors[code-40]
		case code == 48:
			if i+1 < len(params) && params[i+1] == 5 && i+2 < len(params) {
				style.bg = color256ToHex(params[i+2])
				i += 2
			} else if i+1 < len(params) && params[i+1] == 2 && i+4 < len(params) {
				style.bg = fmt.Sprintf("rgb(%d,%d,%d)", params[i+2], params[i+3], params[i+4])
				i += 4
			}
		case code == 49:
			style.bg = ""
		case code >= 90 && code <= 97:
			style.fg = ansiColors[code-90+8]
		case code >= 100 && code <= 107:
			style.bg = ansiColors[code-100+8]
		}
		i++
	}
}

var ansiSgrPattern = regexp.MustCompile(`\x1b\[([\d;]*)m`)

// AnsiToHtml converts ANSI-escaped text to HTML with inline styles.
func AnsiToHtml(text string) string {
	style := &textStyle{}
	var result strings.Builder
	lastIndex := 0
	inSpan := false

	for _, loc := range ansiSgrPattern.FindAllStringSubmatchIndex(text, -1) {
		matchStart, matchEnd := loc[0], loc[1]
		beforeText := text[lastIndex:matchStart]
		if beforeText != "" {
			result.WriteString(escapeHtml(beforeText))
		}

		paramStr := text[loc[2]:loc[3]]
		params := []int{0}
		if paramStr != "" {
			params = params[:0]
			for _, part := range strings.Split(paramStr, ";") {
				value, err := strconv.Atoi(part)
				if err != nil {
					value = 0
				}
				params = append(params, value)
			}
			if len(params) == 0 {
				params = []int{0}
			}
		}

		if inSpan {
			result.WriteString("</span>")
			inSpan = false
		}

		applySgrCode(params, style)

		if style.any() {
			result.WriteString(`<span style="` + style.css() + `">`)
			inSpan = true
		}

		lastIndex = matchEnd
	}

	if remaining := text[lastIndex:]; remaining != "" {
		result.WriteString(escapeHtml(remaining))
	}
	if inSpan {
		result.WriteString("</span>")
	}
	return result.String()
}

// AnsiLinesToHtml wraps each line in a div (empty lines become &nbsp;).
func AnsiLinesToHtml(lines []string) string {
	var out strings.Builder
	for _, line := range lines {
		rendered := AnsiToHtml(line)
		if rendered == "" {
			rendered = "&nbsp;"
		}
		out.WriteString(`<div class="ansi-line">` + rendered + `</div>`)
	}
	return out.String()
}
