// trajectory/query.go: filtering over committed records — in memory or over
// a file iteration.
package trajectory

import (
	"strings"

	"github.com/gladmo/openagent/jsonx"
)

// Query selects records. Zero-value fields match anything; set fields are
// ANDed, list members ORed, ranges inclusive.
type Query struct {
	Types    []string // record types (OR)
	FromSeq  int64    // seq >= FromSeq (0 = no floor)
	ToSeq    int64    // seq <= ToSeq (0 = no ceiling)
	FromTime float64  // time >= FromTime (0 = no floor)
	ToTime   float64  // time <= ToTime (0 = no ceiling)
	Turn     int      // exact turn (0 = any)
	Step     int      // exact step (0 = any); implies Turn when set
	Text     string   // case-insensitive substring over extracted text
	Limit    int      // stop after Limit matches (0 = unlimited)
	Order    string   // "asc" (default) | "desc"
}

// FilterRecords applies the query to a record slice. The input order must
// be ascending by seq (Snapshot order).
func FilterRecords(records []Record, q Query) []Record {
	matched := make([]Record, 0, len(records))
	for i := range records {
		rec := &records[i]
		if !q.Match(rec) {
			continue
		}
		matched = append(matched, *rec)
	}
	if strings.ToLower(q.Order) == "desc" {
		for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
			matched[i], matched[j] = matched[j], matched[i]
		}
	}
	if q.Limit > 0 && len(matched) > q.Limit {
		matched = matched[:q.Limit]
	}
	return matched
}

// Match reports whether one record satisfies the query.
func (q *Query) Match(rec *Record) bool {
	if len(q.Types) > 0 && !containsString(q.Types, rec.Type) {
		return false
	}
	if q.FromSeq > 0 && rec.Seq < q.FromSeq {
		return false
	}
	if q.ToSeq > 0 && rec.Seq > q.ToSeq {
		return false
	}
	if q.FromTime > 0 && rec.TimeMs < q.FromTime {
		return false
	}
	if q.ToTime > 0 && rec.TimeMs > q.ToTime {
		return false
	}
	if q.Turn > 0 && rec.Turn != q.Turn {
		return false
	}
	if q.Step > 0 && rec.Step != q.Step {
		return false
	}
	if q.Text != "" && !recordTextContains(rec, q.Text) {
		return false
	}
	return true
}

func containsString(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// recordTextContains reports whether the needle appears (case-insensitive)
// in any string inside the record's type or data payload.
func recordTextContains(rec *Record, needle string) bool {
	needle = strings.ToLower(needle)
	if strings.Contains(strings.ToLower(rec.Type), needle) {
		return true
	}
	found := false
	walkJSONStrings(rec.Data, func(s string) {
		if !found && strings.Contains(strings.ToLower(s), needle) {
			found = true
		}
	})
	return found
}

// walkJSONStrings visits every string in a JSON value (object keys
// excluded). Records are bounded, so the walk always completes.
func walkJSONStrings(v any, visit func(string)) {
	switch t := v.(type) {
	case string:
		visit(t)
	case []any:
		for _, item := range t {
			walkJSONStrings(item, visit)
		}
	case *jsonx.Obj:
		for _, entry := range t.Entries() {
			walkJSONStrings(entry[1], visit)
		}
	}
}

// ExtractText concatenates every string inside a JSON value, in order —
// the minimal corpus for text search and export previews.
func ExtractText(v any) string {
	var sb strings.Builder
	walkJSONStrings(v, func(s string) {
		sb.WriteString(s)
		sb.WriteString("\n")
	})
	return sb.String()
}
