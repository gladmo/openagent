package tui

import (
	"math"
	"strconv"

	"github.com/gladmo/openagent/jsonx"
)

// jsRound mirrors JS Math.round: half cases round toward +Infinity
// (Math.round(-0.5) is -0, not -1).
func jsRound(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	if x < 0 {
		r := math.Floor(x + 0.5)
		if r == 0 {
			return math.Copysign(0, x)
		}
		return r
	}
	return math.Floor(x + 0.5)
}

// jsPow mirrors JS exponentiation for the positive bases this package uses.
func jsPow(base, exponent float64) float64 {
	return math.Pow(base, exponent)
}

// jsMod mirrors JS remainder after wrapping into [0, m): ((x % m) + m) % m.
func jsMod(x, m float64) float64 {
	return math.Mod(math.Mod(x, m)+m, m)
}

// formatJSNumber renders a number the way a JS template literal would.
func formatJSNumber(value float64) string {
	return jsonx.FormatNumber(value)
}

func mathIsNaN(v float64) bool { return v != v }

func mathIsInf(v float64) bool {
	return v > 1.797693134862315708145274237317043567981e+308 ||
		v < -1.797693134862315708145274237317043567981e+308
}

func jsFloor(v float64) float64 { return math.Floor(v) }

func jsNaN() float64 { return math.NaN() }

func sortStrings(list []string) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func sortKeyIDs[V any](list map[KeyId]V) []KeyId {
	var ids []KeyId
	for id := range list {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids
}

func sortKeybindings(list map[Keybinding]bool) []Keybinding {
	var ids []Keybinding
	for id := range list {
		ids = append(ids, id)
	}
	sortStrings(ids)
	return ids
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
