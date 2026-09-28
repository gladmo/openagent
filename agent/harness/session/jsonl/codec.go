// Package jsonl ports harness/session/jsonl/*: the JSONL session
// persistence backends (header codec, transaction I/O, storage, repository,
// legacy v3 migration, fork).
package jsonl

import (
	"fmt"
	"strings"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

// Format and storage versions.
const (
	JSONLFormatVersion  = int64(4)
	JSONLStorageVersion = int64(1)
)

// StorageHeader mirrors JsonlStorageHeader.
type StorageHeader struct {
	V                       int64   `json:"v"`
	Kind                    string  `json:"kind"`
	ID                      string  `json:"id"`
	StorageVersion          int64   `json:"storageVersion"`
	CreatedAt               float64 `json:"createdAt"`
	Cwd                     string  `json:"cwd"`
	ParentSessionID         *string `json:"parentSessionId,omitempty"`
	LegacyParentSessionPath *string `json:"legacyParentSessionPath,omitempty"`
	NextSeq                 *int64  `json:"nextSeq,omitempty"`
}

// SessionMetadata mirrors JsonlSessionMetadata.
type SessionMetadata struct {
	session.SessionMetadata
	Cwd        string  `json:"cwd"`
	Path       string  `json:"path"`
	ModifiedAt float64 `json:"modifiedAt"`
}

// ParsedSessionHeader mirrors JsonlParsedSessionHeader.
type ParsedSessionHeader struct {
	Format          string // "v4" | "v3-legacy"
	Header          StorageHeader
	LegacyID        string
	LegacyCwd       string
	LegacyTimestamp string
}

// ParseSessionHeader mirrors parseJsonlSessionHeader.
func ParseSessionHeader(line string) (ParsedSessionHeader, error) {
	value, err := jsonx.Parse(line)
	if err != nil {
		return ParsedSessionHeader{}, fmt.Errorf("Invalid JSONL session header: not valid JSON")
	}
	obj, ok := value.(*jsonx.Obj)
	if !ok {
		return ParsedSessionHeader{}, fmt.Errorf("Unsupported JSONL session header")
	}
	if isV4Header(obj) {
		header := StorageHeader{
			V:              intFrom(obj, "v"),
			Kind:           stringFrom(obj, "kind"),
			ID:             stringFrom(obj, "id"),
			StorageVersion: intFrom(obj, "storageVersion"),
			CreatedAt:      floatFrom(obj, "createdAt"),
			Cwd:            stringFrom(obj, "cwd"),
		}
		if parent, ok := obj.Get("parentSessionId"); ok {
			if s, ok := parent.(string); ok {
				header.ParentSessionID = &s
			}
		}
		if legacy, ok := obj.Get("legacyParentSessionPath"); ok {
			if s, ok := legacy.(string); ok {
				header.LegacyParentSessionPath = &s
			}
		}
		if next, ok := obj.Get("nextSeq"); ok {
			if f, ok := jsonx.ToFloat(next); ok {
				n := int64(f)
				header.NextSeq = &n
			}
		}
		return ParsedSessionHeader{Format: "v4", Header: header}, nil
	}
	if isV3Header(obj) {
		return ParsedSessionHeader{
			Format:          "v3-legacy",
			LegacyID:        stringFrom(obj, "id"),
			LegacyCwd:       stringFrom(obj, "cwd"),
			LegacyTimestamp: stringFrom(obj, "timestamp"),
		}, nil
	}
	return ParsedSessionHeader{}, fmt.Errorf("Unsupported JSONL session header")
}

func isV4Header(obj *jsonx.Obj) bool {
	kind, _ := obj.Get("kind")
	v, _ := obj.Get("v")
	if kind != "header" {
		return false
	}
	if f, ok := jsonx.ToFloat(v); ok {
		return int64(f) == JSONLFormatVersion
	}
	return false
}

func isV3Header(obj *jsonx.Obj) bool {
	typ, _ := obj.Get("type")
	version, _ := obj.Get("version")
	return typ == "session" && version != nil
}

func intFrom(obj *jsonx.Obj, key string) int64 {
	if v, ok := obj.Get(key); ok {
		if f, ok := jsonx.ToFloat(v); ok {
			return int64(f)
		}
	}
	return 0
}

func floatFrom(obj *jsonx.Obj, key string) float64 {
	if v, ok := obj.Get(key); ok {
		if f, ok := jsonx.ToFloat(v); ok {
			return f
		}
	}
	return 0
}

func stringFrom(obj *jsonx.Obj, key string) string {
	if v, ok := obj.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// io.ts
// ---------------------------------------------------------------------------

func requireSafeInteger(value any, field string, minimum float64) error {
	f, ok := jsonx.ToFloat(value)
	if !ok || f != float64(int64(f)) || f < minimum {
		return fmt.Errorf("Invalid JSONL %s", field)
	}
	return nil
}

// ParseTransaction mirrors parseJsonlTransaction: single object or array.
func ParseTransaction(line string) ([]session.CommittedWrite, error) {
	value, err := jsonx.Parse(line)
	if err != nil {
		return nil, fmt.Errorf("Invalid JSONL transaction: not valid JSON")
	}
	list := []any{value}
	if arr, ok := value.([]any); ok {
		list = arr
	}
	out := make([]session.CommittedWrite, 0, len(list))
	for _, item := range list {
		write, err := parseCommittedWrite(item)
		if err != nil {
			return nil, err
		}
		out = append(out, write)
	}
	return out, nil
}

func parseCommittedWrite(value any) (session.CommittedWrite, error) {
	obj, ok := value.(*jsonx.Obj)
	if !ok {
		return session.CommittedWrite{}, fmt.Errorf("Invalid JSONL transaction write")
	}
	if err := requireSafeInteger(obj.MustGet("seq"), "write seq", 1); err != nil {
		return session.CommittedWrite{}, err
	}
	kind := stringFrom(obj, "kind")
	write := session.CommittedWrite{
		Kind:      kind,
		Seq:       intFrom(obj, "seq"),
		Namespace: stringFrom(obj, "namespace"),
		Key:       stringFrom(obj, "key"),
		Value:     mustGetOrNil(obj, "value"),
	}
	switch kind {
	case "entry":
		if err := requireSafeInteger(obj.MustGet("timestamp"), "entry timestamp", 0); err != nil {
			return session.CommittedWrite{}, err
		}
		write.Timestamp = floatFrom(obj, "timestamp")
		entry, err := entryFromJSON(obj)
		if err != nil {
			return session.CommittedWrite{}, err
		}
		write.Entry = entry
	case "usage":
		write.Row = usageRowFromJSON(obj)
	case "value":
		op := stringFrom(obj, "op")
		if op != "set" && op != "delete" {
			return session.CommittedWrite{}, fmt.Errorf("Invalid JSONL value operation: %s", op)
		}
		write.Op = op
	case "list":
		op := stringFrom(obj, "op")
		if op != "append" && op != "delete" {
			return session.CommittedWrite{}, fmt.Errorf("Invalid JSONL list operation: %s", op)
		}
		write.Op = op
	default:
		return session.CommittedWrite{}, fmt.Errorf("Invalid JSONL write kind: %s", kind)
	}
	return write, nil
}

func mustGetOrNil(obj *jsonx.Obj, key string) session.JsonValue {
	if v, ok := obj.Get(key); ok {
		return v
	}
	return nil
}

// EntryFromJSON decodes one entry write payload.
func entryFromJSON(obj *jsonx.Obj) (*session.Entry, error) {
	entry := &session.Entry{}
	entry.ID = stringFrom(obj, "id")
	if parent, ok := obj.Get("parentId"); ok && parent != nil {
		if s, ok := parent.(string); ok {
			entry.ParentID = &s
		}
	}
	entry.Seq = intFrom(obj, "seq")
	entry.Timestamp = floatFrom(obj, "timestamp")
	entry.Type = stringFrom(obj, "type")
	if custom, ok := obj.Get("customType"); ok && custom != nil {
		if s, ok := custom.(string); ok {
			entry.CustomType = &s
		}
	}
	if message, ok := obj.Get("message"); ok && message != nil {
		if msgObj, ok := message.(*jsonx.Obj); ok {
			entry.Message = session.AgentMessagePayload{
				Role:    stringFrom(msgObj, "role"),
				Message: msgObj,
			}
		}
	}
	if term, ok := obj.Get("terminate"); ok && term == true {
		entry.Terminate = true
	}
	entry.Summary = stringFrom(obj, "summary")
	if tail, ok := obj.Get("retainedTail"); ok && tail != nil {
		if arr, ok := tail.([]any); ok {
			for _, item := range arr {
				if msgObj, ok := item.(*jsonx.Obj); ok {
					entry.RetainedTail = append(entry.RetainedTail, session.AgentMessagePayload{
						Role:    stringFrom(msgObj, "role"),
						Message: msgObj,
					})
				}
			}
		}
	}
	entry.TokensBefore = floatFrom(obj, "tokensBefore")
	if details, ok := obj.Get("details"); ok && details != nil {
		entry.Details = details
	}
	if fromHook, ok := obj.Get("fromHook"); ok {
		entry.FromHook = fromHook == true
	}
	if fromID, ok := obj.Get("fromId"); ok {
		if s, ok := fromID.(string); ok {
			entry.FromID = &s
		}
	}
	if data, ok := obj.Get("data"); ok && data != nil {
		entry.Data = data
	}
	return entry, nil
}

func usageRowFromJSON(obj *jsonx.Obj) *session.UsageRow {
	row := &session.UsageRow{
		ID:    stringFrom(obj, "id"),
		Seq:   intFrom(obj, "seq"),
		Usage: usageFromJSON(obj),
	}
	if entryID, ok := obj.Get("entryId"); ok && entryID != nil {
		if s, ok := entryID.(string); ok {
			row.EntryID = &s
		}
	}
	if adj, ok := obj.Get("adjustment"); ok {
		row.Adjustment = adj == true
	}
	if details, ok := obj.Get("details"); ok && details != nil {
		row.Details = details
	}
	return row
}

func usageFromJSON(obj *jsonx.Obj) (usage aiUsageAlias) {
	if usageValue, ok := obj.Get("usage"); ok {
		if usageObj, ok := usageValue.(*jsonx.Obj); ok {
			usage.Input = floatFrom(usageObj, "input")
			usage.Output = floatFrom(usageObj, "output")
			usage.CacheRead = floatFrom(usageObj, "cacheRead")
			usage.CacheWrite = floatFrom(usageObj, "cacheWrite")
			if v, ok := usageObj.Get("cacheWrite1h"); ok {
				if f, ok := v.(float64); ok {
					usage.CacheWrite1h = &f
				}
			}
			if v, ok := usageObj.Get("reasoning"); ok {
				if f, ok := v.(float64); ok {
					usage.Reasoning = &f
				}
			}
			usage.TotalTokens = floatFrom(usageObj, "totalTokens")
			if cost, ok := usageObj.Get("cost"); ok {
				if costObj, ok := cost.(*jsonx.Obj); ok {
					usage.Cost.Input = floatFrom(costObj, "input")
					usage.Cost.Output = floatFrom(costObj, "output")
					usage.Cost.CacheRead = floatFrom(costObj, "cacheRead")
					usage.Cost.CacheWrite = floatFrom(costObj, "cacheWrite")
					usage.Cost.Total = floatFrom(costObj, "total")
				}
			}
		}
	}
	return usage
}

// SerializeTransaction mirrors serializeJsonlTransaction: single object when
// one write, array otherwise.
func SerializeTransaction(writes []session.CommittedWrite) string {
	if len(writes) == 1 {
		return jsonx.Stringify(committedWriteToJSON(&writes[0]))
	}
	list := make([]any, 0, len(writes))
	for i := range writes {
		list = append(list, committedWriteToJSON(&writes[i]))
	}
	return jsonx.Stringify(list)
}

// CommittedWriteToJSON renders one committed write with the TS field order:
// kind first, payload fields, then seq (and timestamp for entries).
func committedWriteToJSON(w *session.CommittedWrite) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("kind", w.Kind)
	switch w.Kind {
	case "entry":
		if w.Entry != nil {
			entryToJSON(obj, w.Entry)
		}
		obj.Set("seq", float64(w.Seq))
		obj.Set("timestamp", w.Timestamp)
		return obj
	case "usage":
		if w.Row != nil {
			obj.Set("id", w.Row.ID)
			obj.Set("usage", usageToJSON(&w.Row.Usage))
			obj.Set("seq", float64(w.Seq))
			if w.Row.EntryID != nil {
				obj.Set("entryId", *w.Row.EntryID)
			}
			if w.Row.Adjustment {
				obj.Set("adjustment", true)
			}
			if w.Row.Details != nil {
				obj.Set("details", w.Row.Details)
			}
		}
		return obj
	default:
		obj.Set("op", w.Op)
		obj.Set("seq", float64(w.Seq))
		obj.Set("namespace", w.Namespace)
		obj.Set("key", w.Key)
		if w.Op == "set" || w.Op == "append" {
			obj.Set("value", w.Value)
		}
		return obj
	}
}

func entryToJSON(obj *jsonx.Obj, entry *session.Entry) {
	obj.Set("id", entry.ID)
	if entry.ParentID != nil {
		obj.Set("parentId", *entry.ParentID)
	} else {
		obj.Set("parentId", nil)
	}
	obj.Set("type", entry.Type)
	if entry.CustomType != nil {
		obj.Set("customType", *entry.CustomType)
	}
	switch entry.Type {
	case session.EntryTypeMessage:
		obj.Set("message", entry.Message.Message)
		if entry.Terminate {
			obj.Set("terminate", true)
		}
	case session.EntryTypeCompaction:
		obj.Set("summary", entry.Summary)
		if entry.RetainedTail != nil {
			tail := make([]any, 0, len(entry.RetainedTail))
			for _, msg := range entry.RetainedTail {
				tail = append(tail, msg.Message)
			}
			obj.Set("retainedTail", tail)
		}
		obj.Set("tokensBefore", entry.TokensBefore)
		if entry.Details != nil {
			obj.Set("details", entry.Details)
		}
		if entry.Usage != nil {
			obj.Set("usage", usageToJSON(entry.Usage))
		}
		if entry.FromHook {
			obj.Set("fromHook", true)
		}
	case session.EntryTypeBranchSummary:
		if entry.FromID != nil {
			obj.Set("fromId", *entry.FromID)
		} else {
			obj.Set("fromId", nil)
		}
		obj.Set("summary", entry.Summary)
		if entry.Details != nil {
			obj.Set("details", entry.Details)
		}
		if entry.Usage != nil {
			obj.Set("usage", usageToJSON(entry.Usage))
		}
		if entry.FromHook {
			obj.Set("fromHook", true)
		}
	case session.EntryTypeCustom:
		obj.Set("customType", *entry.CustomType)
		if entry.Data != nil {
			obj.Set("data", entry.Data)
		}
	}
}

func usageToJSON(usage *aiUsageAlias) *jsonx.Obj {
	obj := jsonx.NewObj()
	obj.Set("input", usage.Input)
	obj.Set("output", usage.Output)
	obj.Set("cacheRead", usage.CacheRead)
	obj.Set("cacheWrite", usage.CacheWrite)
	if usage.CacheWrite1h != nil {
		obj.Set("cacheWrite1h", *usage.CacheWrite1h)
	}
	if usage.Reasoning != nil {
		obj.Set("reasoning", *usage.Reasoning)
	}
	obj.Set("totalTokens", usage.TotalTokens)
	cost := jsonx.NewObj()
	cost.Set("input", usage.Cost.Input)
	cost.Set("output", usage.Cost.Output)
	cost.Set("cacheRead", usage.Cost.CacheRead)
	cost.Set("cacheWrite", usage.Cost.CacheWrite)
	cost.Set("total", usage.Cost.Total)
	obj.Set("cost", cost)
	return obj
}

// SplitCompleteLines mirrors splitCompleteLines: content ending in \n is
// fully complete; otherwise the last partial line is the torn tail.
func SplitCompleteLines(content string) (complete []string, torn string) {
	if content == "" {
		return nil, ""
	}
	if strings.HasSuffix(content, "\n") {
		lines := strings.Split(content[:len(content)-1], "\n")
		return lines, ""
	}
	idx := strings.LastIndex(content, "\n")
	if idx < 0 {
		return nil, content
	}
	return strings.Split(content[:idx], "\n"), content[idx+1:]
}

// keep harness referenced for the FileSystem contract.
var _ harness.FileSystem = (harness.FileSystem)(nil)
