package codingagent

// Port of cli/list-models.ts: list available models with optional fuzzy
// search, as a fixed-width table over the model runtime's availability
// snapshot.

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/tui"
)

// FormatTokenCount formats a token count human-readably (200000 -> "200K",
// 1500000 -> "1.5M").
func FormatTokenCount(count float64) string {
	switch {
	case count >= 1_000_000:
		return formatCompactTokens(count, 1_000_000, "M")
	case count >= 1_000:
		return formatCompactTokens(count, 1_000, "K")
	default:
		return strconv.FormatFloat(count, 'f', -1, 64)
	}
}

// formatCompactTokens renders count/unit with one decimal unless whole,
// mirroring the reference's `${millions % 1 === 0 ? millions : toFixed(1)}`.
func formatCompactTokens(count, unit float64, suffix string) string {
	value := count / unit
	if value == math.Trunc(value) {
		return strconv.FormatFloat(value, 'f', -1, 64) + suffix
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + suffix
}

// ListModels writes the available-models table to stdout. searchPattern,
// when non-empty, fuzzy-filters on "provider id". A load error is reported
// to stderr first; an empty availability snapshot prints the auth-guidance
// message instead of a table.
func ListModels(stdout, stderr io.Writer, modelRuntime *ModelRuntime, searchPattern string) {
	if loadError := modelRuntime.GetError(); loadError != "" {
		fmt.Fprintf(stderr, "Warning: errors loading models.json:\n%s\n", loadError)
	}

	models := modelRuntime.GetAvailableSnapshot()
	if len(models) == 0 {
		fmt.Fprintln(stdout, FormatNoModelsAvailableMessage())
		return
	}

	filtered := models
	if searchPattern != "" {
		filtered = tui.FuzzyFilter(models, searchPattern, func(m *ai.Model) string {
			return m.Provider + " " + m.ID
		})
	}
	if len(filtered) == 0 {
		fmt.Fprintf(stdout, "No models matching \"%s\"\n", searchPattern)
		return
	}

	// Sort by provider, then by model id (byte-wise; the reference uses
	// localeCompare over ASCII ids).
	sort.Slice(filtered, func(i, j int) bool {
		if c := strings.Compare(filtered[i].Provider, filtered[j].Provider); c != 0 {
			return c < 0
		}
		return filtered[i].ID < filtered[j].ID
	})

	headers := []string{"provider", "model", "context", "max-out", "thinking", "images"}
	rows := make([][]string, 0, len(filtered))
	for _, model := range filtered {
		rows = append(rows, []string{
			model.Provider,
			model.ID,
			FormatTokenCount(model.ContextWindow),
			FormatTokenCount(model.MaxTokens),
			yesNo(model.Reasoning),
			yesNo(slicesContains(model.Input, "image")),
		})
	}

	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	writePaddedRow(stdout, headers, widths)
	for _, row := range rows {
		writePaddedRow(stdout, row, widths)
	}
}

// writePaddedRow writes one table row: each cell padded to its column
// width, cells joined by two spaces.
func writePaddedRow(w io.Writer, row []string, widths []int) {
	cells := make([]string, len(row))
	for i, cell := range row {
		cells[i] = cell + strings.Repeat(" ", widths[i]-len(cell))
	}
	fmt.Fprintln(w, strings.Join(cells, "  "))
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
