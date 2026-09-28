package ai

// utils_provider.go ports the small provider utilities: sanitize-unicode.ts,
// headers.ts, pi-user-agent.ts, and hash.ts.

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SanitizeSurrogates removes characters that cannot survive JSON
// serialization to a provider. TS removes unpaired UTF-16 surrogates; Go
// strings are UTF-8, so the equivalent is stripping invalid UTF-8 sequences
// (json.Marshal would otherwise replace them with U+FFFD).
func SanitizeSurrogates(text string) string {
	if utf8.ValidString(text) {
		return text
	}
	var builder strings.Builder
	builder.Grow(len(text))
	for i := 0; i < len(text); {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size <= 1 {
			i++
			continue
		}
		builder.WriteString(text[i : i+size])
		i += size
	}
	return builder.String()
}

// HeadersToRecord converts a Go http.Header-style map into a plain record.
func HeadersToRecord(headers map[string][]string) map[string]string {
	result := map[string]string{}
	for key, values := range headers {
		if len(values) > 0 {
			result[key] = values[0]
		}
	}
	return result
}

// ProviderHeadersToRecord merges ProviderHeaders sources (nil value deletes
// a previously merged name), deduplicating case-insensitively while keeping
// the original casing of the last write. Nil return = no headers.
func ProviderHeadersToRecord(sources ...ProviderHeaders) map[string]string {
	type nameValue struct{ name, value string }
	merged := map[string]nameValue{}
	var order []string
	for _, source := range sources {
		if source == nil {
			continue
		}
		for _, name := range sortedHeaderNames(source) {
			value := source[name]
			normalizedName := strings.ToLower(name)
			delete(merged, normalizedName)
			if value != nil {
				if _, seen := merged[normalizedName]; !seen {
					order = append(order, normalizedName)
				}
				merged[normalizedName] = nameValue{name: name, value: *value}
			} else {
				order = removeOrdered(order, normalizedName)
			}
		}
	}
	if len(merged) == 0 {
		return nil
	}
	out := make(map[string]string, len(merged))
	for _, key := range order {
		if entry, ok := merged[key]; ok {
			out[entry.name] = entry.value
		}
	}
	return out
}

func sortedHeaderNames(source ProviderHeaders) []string {
	names := make([]string, 0, len(source))
	for name := range source {
		names = append(names, name)
	}
	// JS object iteration is insertion-ordered; Go maps are not. Sort for
	// determinism (the merged result is keyed by name, so order only affects
	// which duplicate spelling survives, and deletes re-run anyway).
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}

func removeOrdered(order []string, name string) []string {
	for i, entry := range order {
		if entry == name {
			return append(order[:i], order[i+1:]...)
		}
	}
	return order
}

// MergeHeaders merges auth headers with request headers (models.ts
// mergeHeaders: request headers win per name, case-insensitively).
func MergeHeaders(base ProviderHeaders, overlay ProviderHeaders) ProviderHeaders {
	if base == nil && overlay == nil {
		return nil
	}
	merged := ProviderHeaders{}
	lower := map[string]string{}
	for name, value := range base {
		if value != nil {
			merged[name] = value
			lower[strings.ToLower(name)] = name
		}
	}
	for name, value := range overlay {
		if value == nil {
			continue
		}
		if existing, ok := lower[strings.ToLower(name)]; ok {
			delete(merged, existing)
		}
		merged[name] = value
		lower[strings.ToLower(name)] = name
	}
	return merged
}

// GetPiUserAgent returns the pi user-agent. The TS version reports
// platform/release/arch from node:os; Go has no kernel-release accessor in
// the stdlib, so the release segment is omitted (documented deviation).
func GetPiUserAgent() string {
	return fmt.Sprintf("pi (%s; %s)", runtime.GOOS, runtime.GOARCH)
}

// ShortHash is a fast deterministic hash to shorten long strings (hash.ts).
func ShortHash(str string) string {
	h1 := uint32(0xdeadbeef)
	h2 := uint32(0x41c6ce57)
	for i := 0; i < len(str); i++ {
		ch := uint32(str[i])
		h1 = imul(h1^ch, 2654435761)
		h2 = imul(h2^ch, 1597334677)
	}
	h1 = imul(h1^(h1>>16), 2246822507) ^ imul(h2^(h2>>13), 3266489909)
	h2 = imul(h2^(h2>>16), 2246822507) ^ imul(h1^(h1>>13), 3266489909)
	return strconv.FormatUint(uint64(h2), 36) + strconv.FormatUint(uint64(h1), 36)
}

func imul(a, b uint32) uint32 { return a * b }
