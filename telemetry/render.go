package telemetry

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
)

// Port of pi-telemetry render.ts: render stored spans as a chronological
// forest, treating absent parents as roots.

// jsJSONString formats an attribute value the way JSON.stringify does for
// the primitive and array shapes telemetry attributes allow (HTML escaping
// disabled, shortest float form).
func jsJSONString(value AttributeValue) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "null"
	}
	return string(bytes.TrimRight(buf.Bytes(), "\n"))
}

func formatRenderAttributes(attributes SpanAttributes) string {
	out := ""
	for _, key := range sortedAttributeNames(attributes) {
		out += " " + key + "=" + jsJSONString(attributes[key])
	}
	return out
}

func sortedAttributeNames(attributes SpanAttributes) []string {
	names := make([]string, 0, len(attributes))
	for name := range attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// formatFloatMs renders a millisecond offset the way a JS number-to-string
// conversion does (shortest round-trip form).
func formatFloatMs(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// RenderTrace renders stored spans as a chronological forest, treating
// absent parents as roots. Spans sharing a global ID (merged from stores)
// collapse to the last occurrence at its first position.
func RenderTrace(spans []StoredSpan) string {
	byID := map[string]StoredSpan{}
	idOrder := make([]string, 0, len(spans))
	for _, span := range spans {
		if _, seen := byID[span.ID]; !seen {
			idOrder = append(idOrder, span.ID)
		}
		byID[span.ID] = span
	}
	deduped := make([]StoredSpan, 0, len(idOrder))
	for _, id := range idOrder {
		deduped = append(deduped, byID[id])
	}
	sort.SliceStable(deduped, func(i, j int) bool { return deduped[i].StartedMs < deduped[j].StartedMs })
	roots := []StoredSpan{}
	children := map[string][]StoredSpan{}
	for _, span := range deduped {
		_, parentKnown := byID[span.ParentID]
		if span.ParentID == "" || !parentKnown {
			roots = append(roots, span)
		} else {
			children[span.ParentID] = append(children[span.ParentID], span)
		}
	}

	lines := []string{}
	type pendingSpan struct {
		span  StoredSpan
		depth int
	}
	pending := make([]pendingSpan, 0, len(roots))
	for i := len(roots) - 1; i >= 0; i-- {
		pending = append(pending, pendingSpan{span: roots[i], depth: 0})
	}
	for len(pending) > 0 {
		entry := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		indent := ""
		for i := 0; i < entry.depth; i++ {
			indent += "  "
		}
		span := entry.span
		error := ""
		if span.Status.Status == "error" {
			error = " error"
			if span.Status.Error != nil {
				error += " " + span.Status.Error.Name + ": " + span.Status.Error.Message
			}
		}
		lines = append(lines, indent+span.Name+formatRenderAttributes(span.Attributes)+" ("+
			strconv.FormatInt(span.EndedMs-span.StartedMs, 10)+"ms)"+error)
		for _, event := range span.Events {
			lines = append(lines, indent+"  · "+event.Name+formatRenderAttributes(event.Attributes)+" (+"+
				formatFloatMs(event.TimeMs)+"ms)")
		}
		for _, link := range span.Links {
			lines = append(lines, indent+"  → link trace "+link.TraceID+" span "+link.SpanID+
				formatRenderAttributes(link.Attributes))
		}
		descendants := children[span.ID]
		for i := len(descendants) - 1; i >= 0; i-- {
			pending = append(pending, pendingSpan{span: descendants[i], depth: entry.depth + 1})
		}
	}
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}
