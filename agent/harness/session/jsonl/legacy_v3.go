package jsonl

// legacy_v3.go ports harness/session/jsonl/legacy-v3.ts: reading legacy v3
// session files, reminting ids, deriving durable values, and computing
// imported usage.

import (
	"fmt"
	"strings"

	"github.com/gladmo/openagent/agent/harness"
	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// LegacyV3Source mirrors the TS class.
type LegacyV3Source struct {
	Header        StorageHeader
	NextSeq       int64
	ImportedUsage ai.Usage

	normalized []session.CommittedWrite
	// mappings
	mintedIDs   map[string]string // legacy id -> minted uuidv7
	entriesByID map[string]*legacyEntry
	parentOf    map[string]string
	// derived
	sessionName *string
	labels      []struct {
		targetID string
		label    *string
	}
	branchTip    *string
	laneConfig   *jsonx.Obj
	laneState    *jsonx.Obj
	messageCount int64
}

type legacyEntry struct {
	legacyID   string
	parentID   string
	timestamp  float64
	typeOf     string // message | custom | custom_message | branch_summary | compaction
	customType string
	message    *jsonx.Obj
	summary    string
	fromID     string
}

// ReadLegacyV3Source reads and normalizes a legacy v3 file.
func ReadLegacyV3Source(fs harness.FileSystem, path string, ctx harness.Context) (*LegacyV3Source, error) {
	contentResult := fs.ReadTextFile(path, ctx)
	if !contentResult.Ok {
		return nil, wrapAction("Failed to read JSONL storage "+path, contentResult.Error)
	}
	lines, _ := SplitCompleteLines(contentResult.Value)
	if len(lines) == 0 {
		return nil, fmt.Errorf("Invalid JSONL storage %s: missing header", path)
	}
	headerValue, err := jsonx.Parse(lines[0])
	if err != nil {
		return nil, fmt.Errorf("Invalid JSONL storage %s: invalid header", path)
	}
	headerObj, ok := headerValue.(*jsonx.Obj)
	if !ok || !isV3Header(headerObj) {
		return nil, fmt.Errorf("Invalid JSONL storage %s: invalid header", path)
	}
	legacyID := stringFrom(headerObj, "id")
	legacyCwd := stringFrom(headerObj, "cwd")

	source := &LegacyV3Source{
		mintedIDs:   map[string]string{},
		entriesByID: map[string]*legacyEntry{},
		parentOf:    map[string]string{},
	}
	// Parse retained entries; discard known metadata types.
	for index := 1; index < len(lines); index++ {
		value, err := jsonx.Parse(lines[index])
		if err != nil {
			return nil, fmt.Errorf("Legacy v3 source changed: line %d", index+1)
		}
		obj, ok := value.(*jsonx.Obj)
		if !ok {
			continue
		}
		id := stringFrom(obj, "id")
		typeOf := stringFrom(obj, "type")
		if id == "" {
			continue
		}
		if _, duplicate := source.entriesByID[id]; duplicate {
			return nil, fmt.Errorf("Legacy v3 source changed: duplicate id %s", id)
		}
		entry := &legacyEntry{
			legacyID:  id,
			parentID:  stringFrom(obj, "parentId"),
			timestamp: floatFrom(obj, "timestamp"),
			typeOf:    typeOf,
		}
		switch typeOf {
		case "message":
			entry.message = objOf(obj, "message")
			if entry.message != nil {
				source.messageCount++
				usage := usageOfMessage(entry.message)
				source.ImportedUsage = harness.AddUsage(source.ImportedUsage, usage)
			}
		case "custom":
			entry.customType = stringFrom(obj, "customType")
		case "custom_message":
			entry.customType = "custom"
			entry.message = objOf(obj, "message")
		case "branch_summary":
			entry.summary = stringFrom(obj, "summary")
			entry.fromID = stringFrom(obj, "fromId")
		case "compaction":
			entry.summary = stringFrom(obj, "summary")
			source.ImportedUsage = harness.AddUsage(source.ImportedUsage, usageFromJSON(obj))
		case "model_change", "thinking_level_change", "active_tools_change":
			// Discarded metadata.
		case "session_info":
			if name, ok := obj.Get("name"); ok {
				if s, ok := name.(string); ok {
					str := s
					source.sessionName = &str
				}
			}
		case "label":
			target := stringFrom(obj, "targetId")
			var label *string
			if l, ok := obj.Get("label"); ok && l != nil {
				if s, ok := l.(string); ok {
					label = &s
				}
			}
			source.labels = append(source.labels, struct {
				targetID string
				label    *string
			}{target, label})
		default:
			continue
		}
		if typeOf != "session_info" && typeOf != "label" {
			source.entriesByID[id] = entry
			if entry.parentID != "" {
				source.parentOf[id] = entry.parentID
			}
		}
	}

	// Remint ids for retained entries in file order, preserving timestamps.
	nextSeq := int64(0)
	for index := 1; index < len(lines); index++ {
		value, _ := jsonx.Parse(lines[index])
		obj, ok := value.(*jsonx.Obj)
		if !ok {
			continue
		}
		id := stringFrom(obj, "id")
		entry, retained := source.entriesByID[id]
		if !retained {
			continue
		}
		minted := ai.UUIDv7(entry.timestamp)
		source.mintedIDs[id] = minted
		nextSeq++
	}
	// Derived values get fresh seqs after the entries.
	derivedCount := int64(1) // branch tip
	if source.laneConfig != nil {
		derivedCount++
	}
	if source.laneState != nil {
		derivedCount++
	}
	derivedCount += int64(len(source.labels))
	if source.sessionName != nil {
		derivedCount++
	}
	source.NextSeq = nextSeq + derivedCount + 1

	// Branch tip = final retained entry (or null).
	var finalRetained string
	for index := len(lines) - 1; index >= 1; index-- {
		value, _ := jsonx.Parse(lines[index])
		obj, ok := value.(*jsonx.Obj)
		if !ok {
			continue
		}
		id := stringFrom(obj, "id")
		if _, retained := source.entriesByID[id]; retained {
			finalRetained = id
			break
		}
	}
	if finalRetained != "" {
		tip := source.mintedIDs[finalRetained]
		source.branchTip = &tip
	}

	// Metadata for header.
	createdAt := float64(0)
	header := StorageHeader{
		V:              JSONLFormatVersion,
		Kind:           "header",
		ID:             legacyID,
		StorageVersion: JSONLStorageVersion,
		CreatedAt:      createdAt,
		Cwd:            legacyCwd,
	}
	if parent, ok := headerObj.Get("parentSession"); ok {
		if s, ok := parent.(string); ok {
			header.LegacyParentSessionPath = &s
		}
	}
	source.Header = header

	// Normalize writes in a stable order.
	if err := source.normalize(lines); err != nil {
		return nil, err
	}
	return source, nil
}

func (s *LegacyV3Source) normalize(lines []string) error {
	_ = jsonx.NewObj
	seq := int64(1)
	appendEntry := func(entry *legacyEntry, minted string, parent *string, e *session.Entry) {
		e.ID = minted
		e.ParentID = parent
		e.Seq = seq
		seq++
		s.normalized = append(s.normalized, session.CommittedWrite{Kind: "entry", Seq: e.Seq, Entry: e})
	}
	for index := 1; index < len(lines); index++ {
		value, err := jsonx.Parse(lines[index])
		if err != nil {
			return fmt.Errorf("Legacy v3 source changed: line %d", index+1)
		}
		obj, ok := value.(*jsonx.Obj)
		if !ok {
			continue
		}
		id := stringFrom(obj, "id")
		entry, retained := s.entriesByID[id]
		if !retained {
			continue
		}
		minted := s.mintedIDs[id]
		parent := s.mintedParent(id)
		var parentPtr *string
		if parent != "" {
			parentPtr = &parent
		}
		switch entry.typeOf {
		case "message", "custom_message":
			role := ""
			if entry.message != nil {
				role = stringFrom(entry.message, "role")
			}
			e := &session.Entry{EntryBase: session.EntryBase{Type: session.EntryTypeMessage, Timestamp: entry.timestamp}}
			if entry.typeOf == "custom_message" {
				// Custom messages project as role "custom".
				e.Message = session.AgentMessagePayload{Role: "custom", Message: entry.message}
			} else {
				e.Message = session.AgentMessagePayload{Role: role, Message: entry.message}
			}
			appendEntry(entry, minted, parentPtr, e)
		case "custom":
			customType := entry.customType
			e := &session.Entry{
				EntryBase: session.EntryBase{Type: session.EntryTypeCustom, CustomType: &customType, Timestamp: entry.timestamp},
			}
			if data, ok := obj.Get("data"); ok {
				e.Data = data
			}
			appendEntry(entry, minted, parentPtr, e)
		case "branch_summary":
			e := &session.Entry{
				EntryBase: session.EntryBase{Type: session.EntryTypeBranchSummary, Timestamp: entry.timestamp},
				Summary:   entry.summary,
			}
			if entry.fromID != "" && entry.fromID != "root" {
				mintedFrom := s.mintedIDs[entry.fromID]
				if mintedFrom != "" {
					e.FromID = &mintedFrom
				} else {
					e.FromID = &entry.fromID
				}
			} else if entry.fromID == "root" {
				// root sentinel -> null parent
			}
			appendEntry(entry, minted, parentPtr, e)
		case "compaction":
			e := &session.Entry{
				EntryBase: session.EntryBase{Type: session.EntryTypeCompaction, Timestamp: entry.timestamp},
				Summary:   entry.summary,
			}
			appendEntry(entry, minted, parentPtr, e)
		}
	}
	// Derived values.
	appendValue := func(namespace, key string, value session.JsonValue) {
		s.normalized = append(s.normalized, session.CommittedWrite{
			Kind: "value", Op: "set", Seq: seq, Namespace: namespace, Key: key, Value: value,
		})
		seq++
	}
	if s.sessionName != nil {
		appendValue("pi.session.name", "", *s.sessionName)
	}
	for _, label := range s.labels {
		target := s.mintedIDs[label.targetID]
		if target == "" {
			target = label.targetID
		}
		if label.label != nil {
			appendValue("pi.entry.label", target, *label.label)
		}
	}
	if s.branchTip != nil {
		appendValue("pi.branch.tip", "main", *s.branchTip)
	} else {
		appendValue("pi.branch.tip", "main", nil)
	}
	return nil
}

func (s *LegacyV3Source) mintedParent(id string) string {
	parent, ok := s.parentOf[id]
	if !ok {
		return ""
	}
	if _, retained := s.entriesByID[parent]; !retained {
		// Parent was discarded (metadata type): forward-missing is an error
		// in TS; we map to the nearest retained ancestor.
		return s.mintedParent(parent)
	}
	return s.mintedIDs[parent]
}

// Writes yields the normalized writes (one per line in the TS stream).
func (s *LegacyV3Source) Writes(_ harness.Context) ([]session.CommittedWrite, error) {
	return s.normalized, nil
}

func objOf(obj *jsonx.Obj, key string) *jsonx.Obj {
	if v, ok := obj.Get(key); ok {
		if o, ok := v.(*jsonx.Obj); ok {
			return o
		}
	}
	return nil
}

func usageOfMessage(message *jsonx.Obj) (usage aiUsageAlias) {
	if usageValue, ok := message.Get("usage"); ok {
		if usageObj, ok := usageValue.(*jsonx.Obj); ok {
			usage.Input = floatFrom(usageObj, "input")
			usage.Output = floatFrom(usageObj, "output")
			usage.CacheRead = floatFrom(usageObj, "cacheRead")
			usage.CacheWrite = floatFrom(usageObj, "cacheWrite")
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

// VerifyLegacySource re-checks the header id/cwd on re-read (TS
// "Legacy v3 source changed").
func VerifyLegacySource(fs harness.FileSystem, path, expectedID, expectedCwd string, ctx harness.Context) error {
	contentResult := fs.ReadTextFile(path, ctx)
	if !contentResult.Ok {
		return wrapAction("Failed to read JSONL storage "+path, contentResult.Error)
	}
	lines, _ := SplitCompleteLines(contentResult.Value)
	if len(lines) == 0 {
		return fmt.Errorf("Legacy v3 source changed")
	}
	value, err := jsonx.Parse(lines[0])
	if err != nil {
		return fmt.Errorf("Legacy v3 source changed")
	}
	obj, ok := value.(*jsonx.Obj)
	if !ok || !isV3Header(obj) {
		return fmt.Errorf("Legacy v3 source changed")
	}
	if stringFrom(obj, "id") != expectedID || stringFrom(obj, "cwd") != expectedCwd {
		return fmt.Errorf("Legacy v3 source changed")
	}
	_ = strings.TrimSpace
	return nil
}
