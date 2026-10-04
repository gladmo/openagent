package codingagent

// Port of core/session-manager.ts (part 1): entry types, versioned JSONL
// parsing, v1->v2->v3 migrations, tree path building, compaction-aware
// context entries, and context projection.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// CurrentSessionVersion is the session file format version.
const CurrentSessionVersion = 3

// Session entry type discriminants.
const (
	EntryTypeSession             = "session"
	EntryTypeMessage             = "message"
	EntryTypeThinkingLevelChange = "thinking_level_change"
	EntryTypeModelChange         = "model_change"
	EntryTypeUsage               = "usage"
	EntryTypeCompaction          = "compaction"
	EntryTypeBranchSummary       = "branch_summary"
	EntryTypeCustom              = "custom"
	EntryTypeCustomMessage       = "custom_message"
	EntryTypeContextEdit         = "context_edit"
	EntryTypeLabel               = "label"
	EntryTypeSessionInfo         = "session_info"
)

// SessionHeader is the first line of a session file.
type SessionHeader struct {
	Type          string `json:"type"`
	Version       *int   `json:"version,omitempty"`
	ID            string `json:"id"`
	Timestamp     string `json:"timestamp"`
	Cwd           string `json:"cwd"`
	ParentSession string `json:"parentSession,omitempty"`
}

// SessionEntryBase carries the tree linkage every entry shares.
type SessionEntryBase struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
}

// SessionMessageEntry wraps one AgentMessage.
type SessionMessageEntry struct {
	SessionEntryBase
	Message json.RawMessage `json:"message"`
}

// ThinkingLevelChangeEntry records a thinking-level switch.
type ThinkingLevelChangeEntry struct {
	SessionEntryBase
	ThinkingLevel string `json:"thinkingLevel"`
}

// ModelChangeEntry records an explicit model switch.
type ModelChangeEntry struct {
	SessionEntryBase
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}

// UsageEntry records token usage by category.
type UsageEntry struct {
	SessionEntryBase
	Kind     string          `json:"kind"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Usage    json.RawMessage `json:"usage"`
	Note     string          `json:"note,omitempty"`
}

// CompactionEntry records a compaction boundary.
type CompactionEntry struct {
	SessionEntryBase
	Summary          string          `json:"summary"`
	FirstKeptEntryID string          `json:"firstKeptEntryId"`
	TokensBefore     int64           `json:"tokensBefore"`
	Details          json.RawMessage `json:"details,omitempty"`
	Usage            json.RawMessage `json:"usage,omitempty"`
	FromHook         bool            `json:"fromHook,omitempty"`
	SystemMessage    json.RawMessage `json:"systemMessage,omitempty"`
}

// BranchSummaryEntry records an abandoned branch summary.
type BranchSummaryEntry struct {
	SessionEntryBase
	FromID   string          `json:"fromId"`
	Summary  string          `json:"summary"`
	Details  json.RawMessage `json:"details,omitempty"`
	Usage    json.RawMessage `json:"usage,omitempty"`
	FromHook bool            `json:"fromHook,omitempty"`
}

// CustomEntry stores extension state (not LLM context).
type CustomEntry struct {
	SessionEntryBase
	CustomType string          `json:"customType"`
	Data       json.RawMessage `json:"data,omitempty"`
}

// CustomMessageEntry injects messages into LLM context.
type CustomMessageEntry struct {
	SessionEntryBase
	CustomType string          `json:"customType"`
	Content    json.RawMessage `json:"content"`
	Details    json.RawMessage `json:"details,omitempty"`
	Display    bool            `json:"display"`
}

// ContextEditEntry replaces or omits an earlier entry's content.
type ContextEditEntry struct {
	SessionEntryBase
	TargetID string `json:"targetId"`
	// Replacement is nil to omit; otherwise carries replacement content JSON.
	Replacement *ContextEditReplacement `json:"replacement"`
}

// ContextEditReplacement wraps replacement content.
type ContextEditReplacement struct {
	Content json.RawMessage `json:"content"`
}

// LabelEntry bookmarks a target entry.
type LabelEntry struct {
	SessionEntryBase
	TargetID string  `json:"targetId"`
	Label    *string `json:"label"`
}

// SessionInfoEntry records user-defined session metadata.
type SessionInfoEntry struct {
	SessionEntryBase
	Name string `json:"name,omitempty"`
}

// FileEntry is the JSONL union (header or entry).
type FileEntry interface{ entryType() string }

func (e *SessionHeader) entryType() string            { return EntryTypeSession }
func (e *SessionMessageEntry) entryType() string      { return EntryTypeMessage }
func (e *ThinkingLevelChangeEntry) entryType() string { return EntryTypeThinkingLevelChange }
func (e *ModelChangeEntry) entryType() string         { return EntryTypeModelChange }
func (e *UsageEntry) entryType() string               { return EntryTypeUsage }
func (e *CompactionEntry) entryType() string          { return EntryTypeCompaction }
func (e *BranchSummaryEntry) entryType() string       { return EntryTypeBranchSummary }
func (e *CustomEntry) entryType() string              { return EntryTypeCustom }
func (e *CustomMessageEntry) entryType() string       { return EntryTypeCustomMessage }
func (e *ContextEditEntry) entryType() string         { return EntryTypeContextEdit }
func (e *LabelEntry) entryType() string               { return EntryTypeLabel }
func (e *SessionInfoEntry) entryType() string         { return EntryTypeSessionInfo }

// SessionEntry is a FileEntry with tree linkage.
type SessionEntry interface {
	FileEntry
	base() *SessionEntryBase
}

func (e *SessionMessageEntry) base() *SessionEntryBase      { return &e.SessionEntryBase }
func (e *ThinkingLevelChangeEntry) base() *SessionEntryBase { return &e.SessionEntryBase }
func (e *ModelChangeEntry) base() *SessionEntryBase         { return &e.SessionEntryBase }
func (e *UsageEntry) base() *SessionEntryBase               { return &e.SessionEntryBase }
func (e *CompactionEntry) base() *SessionEntryBase          { return &e.SessionEntryBase }
func (e *BranchSummaryEntry) base() *SessionEntryBase       { return &e.SessionEntryBase }
func (e *CustomEntry) base() *SessionEntryBase              { return &e.SessionEntryBase }
func (e *CustomMessageEntry) base() *SessionEntryBase       { return &e.SessionEntryBase }
func (e *ContextEditEntry) base() *SessionEntryBase         { return &e.SessionEntryBase }
func (e *LabelEntry) base() *SessionEntryBase               { return &e.SessionEntryBase }
func (e *SessionInfoEntry) base() *SessionEntryBase         { return &e.SessionEntryBase }

// ParseFileEntry decodes one JSONL line into its typed entry.
func ParseFileEntry(line string) FileEntry {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(line), &probe); err != nil {
		return nil
	}
	decode := func(target any) FileEntry {
		if err := json.Unmarshal([]byte(line), target); err != nil {
			return nil
		}
		return target.(FileEntry)
	}
	switch probe.Type {
	case EntryTypeSession:
		return decode(&SessionHeader{})
	case EntryTypeMessage:
		return decode(&SessionMessageEntry{})
	case EntryTypeThinkingLevelChange:
		return decode(&ThinkingLevelChangeEntry{})
	case EntryTypeModelChange:
		return decode(&ModelChangeEntry{})
	case EntryTypeUsage:
		return decode(&UsageEntry{})
	case EntryTypeCompaction:
		return decode(&CompactionEntry{})
	case EntryTypeBranchSummary:
		return decode(&BranchSummaryEntry{})
	case EntryTypeCustom:
		return decode(&CustomEntry{})
	case EntryTypeCustomMessage:
		return decode(&CustomMessageEntry{})
	case EntryTypeContextEdit:
		return decode(&ContextEditEntry{})
	case EntryTypeLabel:
		return decode(&LabelEntry{})
	case EntryTypeSessionInfo:
		return decode(&SessionInfoEntry{})
	}
	return nil
}

// ParseSessionEntries parses JSONL content, skipping malformed lines.
func ParseSessionEntries(content string) []FileEntry {
	var entries []FileEntry
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if entry := ParseFileEntry(line); entry != nil {
			entries = append(entries, entry)
		}
	}
	return entries
}

// sessionIDPattern validates session ids.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// AssertValidSessionID rejects unsafe session ids.
func AssertValidSessionID(id string) error {
	if !sessionIDPattern.MatchString(id) {
		return fmt.Errorf("Session id must be non-empty, contain only alphanumeric characters, '-', '_', and '.', and start and end with an alphanumeric character")
	}
	return nil
}

// getLatestCompactionEntry returns the newest compaction entry.
func getLatestCompactionEntry(entries []SessionEntry) *CompactionEntry {
	for i := len(entries) - 1; i >= 0; i-- {
		if compaction, ok := entries[i].(*CompactionEntry); ok {
			return compaction
		}
	}
	return nil
}

// GetLatestCompactionEntry is the exported lookup.
func GetLatestCompactionEntry(entries []SessionEntry) *CompactionEntry {
	return getLatestCompactionEntry(entries)
}

func buildEntryIndex(entries []SessionEntry, byId map[string]SessionEntry) map[string]SessionEntry {
	if byId != nil {
		return byId
	}
	index := map[string]SessionEntry{}
	for _, entry := range entries {
		index[entry.base().ID] = entry
	}
	return index
}

// BuildSessionPath walks the leaf-to-root path (all entries when leafId is
// nil; none when explicitly null).
func BuildSessionPath(entries []SessionEntry, leafID *string, byId map[string]SessionEntry) []SessionEntry {
	index := buildEntryIndex(entries, byId)
	var leaf SessionEntry
	if leafID != nil && *leafID == "" {
		return nil
	}
	if leafID != nil {
		leaf = index[*leafID]
	}
	if leaf == nil && len(entries) > 0 {
		leaf = entries[len(entries)-1]
	}
	if leaf == nil {
		return nil
	}

	var path []SessionEntry
	current := leaf
	for current != nil {
		path = append(path, current)
		parentID := current.base().ParentID
		if parentID == nil {
			break
		}
		current = index[*parentID]
	}
	// Reverse.
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// SessionContextSettings carries the resolved thinking level and model.
type SessionContextSettings struct {
	ThinkingLevel string
	Model         *SessionModelSelection
}

// SessionModelSelection names a provider/model pair.
type SessionModelSelection struct {
	Provider string
	ModelID  string
}

func getSessionContextSettings(path []SessionEntry) SessionContextSettings {
	settings := SessionContextSettings{ThinkingLevel: "off"}
	for _, entry := range path {
		switch typed := entry.(type) {
		case *ThinkingLevelChangeEntry:
			settings.ThinkingLevel = typed.ThinkingLevel
		case *ModelChangeEntry:
			settings.Model = &SessionModelSelection{Provider: typed.Provider, ModelID: typed.ModelID}
		case *SessionMessageEntry:
			var message struct {
				Role     string `json:"role"`
				Provider string `json:"provider"`
				Model    string `json:"model"`
			}
			if err := json.Unmarshal(typed.Message, &message); err == nil && message.Role == "assistant" {
				settings.Model = &SessionModelSelection{Provider: message.Provider, ModelID: message.Model}
			}
		}
	}
	return settings
}

// BuildContextEntries follows the leaf path and applies compaction
// retention: the newest compaction stands in for the summarized prefix.
func BuildContextEntries(entries []SessionEntry, leafID *string, byId map[string]SessionEntry) []SessionEntry {
	path := BuildSessionPath(entries, leafID, byId)
	var compaction *CompactionEntry
	for _, entry := range path {
		if typed, ok := entry.(*CompactionEntry); ok {
			compaction = typed
		}
	}

	if compaction == nil {
		return path
	}

	compactionIdx := -1
	for i, entry := range path {
		if entry.base().ID == compaction.ID {
			compactionIdx = i
			break
		}
	}
	if compactionIdx < 0 {
		return path
	}

	contextEntries := []SessionEntry{compaction}
	foundFirstKept := false
	for i := 0; i < compactionIdx; i++ {
		entry := path[i]
		if entry.base().ID == compaction.FirstKeptEntryID {
			foundFirstKept = true
		}
		if foundFirstKept {
			if message, ok := entry.(*SessionMessageEntry); ok {
				var role struct {
					Role string `json:"role"`
				}
				if err := json.Unmarshal(message.Message, &role); err == nil && role.Role == "system" {
					continue
				}
			}
			contextEntries = append(contextEntries, entry)
		}
	}
	contextEntries = append(contextEntries, path[compactionIdx+1:]...)
	return contextEntries
}

// ProjectedSessionEntry preserves provenance for projected messages.
type ProjectedSessionEntry struct {
	SourceEntry SessionEntry
	Messages    []interface{}
}

// SessionProjection is the provenance-preserving context.
type SessionProjection struct {
	Entries       []ProjectedSessionEntry
	ThinkingLevel string
	Model         *SessionModelSelection
}

// BuildSessionProjection builds compaction-aware, edit-aware context.
func BuildSessionProjection(entries []SessionEntry, leafID *string, byId map[string]SessionEntry) *SessionProjection {
	path := BuildSessionPath(entries, leafID, byId)
	settings := getSessionContextSettings(path)
	contextEntries := BuildContextEntries(entries, leafID, byId)

	edits := map[string]*ContextEditEntry{}
	for _, entry := range contextEntries {
		if typed, ok := entry.(*ContextEditEntry); ok {
			edits[typed.TargetID] = typed
		}
	}

	projected := make([]ProjectedSessionEntry, len(contextEntries))
	for index, sourceEntry := range contextEntries {
		var messages []interface{}
		if compaction, ok := sourceEntry.(*CompactionEntry); ok && index > 0 {
			// Older compaction entries inside the retained range contribute
			// nothing; only the newest (index zero) is the checkpoint.
			_ = compaction
			messages = nil
		} else {
			messages = projectContextEntry(sourceEntry, edits[sourceEntry.base().ID])
		}
		projected[index] = ProjectedSessionEntry{SourceEntry: sourceEntry, Messages: messages}
	}

	return &SessionProjection{
		Entries:       projected,
		ThinkingLevel: settings.ThinkingLevel,
		Model:         settings.Model,
	}
}

func projectContextEntry(entry SessionEntry, edit *ContextEditEntry) []interface{} {
	messages := sessionEntryToContextMessages(entry)
	if edit == nil {
		return messages
	}
	if edit.Replacement == nil {
		return nil
	}

	var out []interface{}
	for _, message := range messages {
		// Content replacement applies to user/assistant/toolResult/custom
		// roles only (mirroring the reference role filter).
		if typed, ok := message.(interface{ Role() string }); ok {
			switch typed.Role() {
			case "user", "assistant", "toolResult", "custom":
				out = append(out, replacedContentMessage{underlying: typed, content: edit.Replacement.Content})
				continue
			}
		}
		out = append(out, message)
	}
	return out
}

// replacedContentMessage wraps a message with substituted content JSON.
type replacedContentMessage struct {
	underlying interface{ Role() string }
	content    json.RawMessage
}

// Role implements the message interface.
func (m replacedContentMessage) Role() string { return m.underlying.Role() }

// sessionEntryToContextMessages projects one entry to runtime messages.
// Plain custom entries are display/state entries and do not participate.
func sessionEntryToContextMessages(entry SessionEntry) []interface{} {
	switch typed := entry.(type) {
	case *SessionMessageEntry:
		// Messages are carried as raw JSON to preserve unknown fields; the
		// runtime decodes them against the ported agent message shapes.
		return []interface{}{rawAgentMessage{raw: typed.Message}}
	case *CustomMessageEntry:
		return []interface{}{CreateCustomMessage(
			typed.CustomType,
			json.RawMessage(typed.Content),
			typed.Display,
			json.RawMessage(typed.Details),
			typed.Timestamp,
		)}
	case *BranchSummaryEntry:
		if typed.Summary != "" {
			return []interface{}{CreateBranchSummaryMessage(typed.Summary, typed.FromID, typed.Timestamp)}
		}
		return nil
	case *CompactionEntry:
		summary := CreateCompactionSummaryMessage(typed.Summary, typed.TokensBefore, typed.Timestamp)
		if len(typed.SystemMessage) > 0 {
			return []interface{}{rawAgentMessage{raw: typed.SystemMessage}, summary}
		}
		return []interface{}{summary}
	}
	return nil
}

// rawAgentMessage defers message decoding to the consumer while satisfying
// the Role() interface via a JSON probe.
type rawAgentMessage struct {
	raw json.RawMessage
}

// Role implements the message interface by probing the payload.
func (m rawAgentMessage) Role() string {
	var probe struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(m.raw, &probe); err != nil {
		return ""
	}
	return probe.Role
}

// Raw exposes the underlying JSON.
func (m rawAgentMessage) Raw() json.RawMessage { return m.raw }

// decodeAgentMessage materializes a raw session message into its typed
// shape (system/user/assistant/toolResult) for token estimation and
// compaction logic; unknown shapes stay raw. The ported ai message structs
// carry interface ContentBlock slices that encoding/json cannot unmarshal,
// so blocks are rebuilt from their generic map forms by type tag.
func decodeAgentMessage(raw json.RawMessage) interface{ Role() string } {
	var probe struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"Content"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil
	}

	decodeBlocks := func(content json.RawMessage) []ai.ContentBlock {
		if len(content) == 0 {
			return nil
		}
		var generic []map[string]any
		if err := json.Unmarshal(content, &generic); err != nil {
			return nil
		}
		blocks := make([]ai.ContentBlock, 0, len(generic))
		for _, entry := range generic {
			// Blocks carry a "type" tag when authored with one; the ported
			// structs marshal without tags, so infer by shape.
			tag, _ := entry["type"].(string)
			if tag == "" {
				switch {
				case entry["Name"] != nil:
					tag = "toolCall"
				case entry["Thinking"] != nil:
					tag = "thinking"
				default:
					tag = "text"
				}
			}
			switch tag {
			case "text":
				text, _ := entry["Text"].(string)
				blocks = append(blocks, ai.TextContent{Text: text})
			case "thinking":
				thinking, _ := entry["Thinking"].(string)
				blocks = append(blocks, ai.ThinkingContent{Thinking: thinking})
			case "toolCall":
				name, _ := entry["Name"].(string)
				call := &ai.ToolCall{Name: name}
				if args, ok := entry["Arguments"].(map[string]any); ok {
					obj := jsonx.NewObj()
					for key, value := range args {
						obj.Set(key, value)
					}
					call.Arguments = obj
				}
				blocks = append(blocks, call)
			default:
				if text, ok := entry["Text"].(string); ok {
					blocks = append(blocks, ai.TextContent{Text: text})
				}
			}
		}
		return blocks
	}
	decodeStringContent := func(content json.RawMessage) ai.Content {
		if len(content) == 0 {
			return ai.Content{}
		}
		var text string
		if err := json.Unmarshal(content, &text); err == nil {
			return ai.StringContent(text)
		}
		// The ported Content struct marshals as {IsText, Text, Blocks}.
		var asStruct struct {
			IsText bool            `json:"IsText"`
			Text   string          `json:"Text"`
			Blocks json.RawMessage `json:"Blocks"`
		}
		if err := json.Unmarshal(content, &asStruct); err == nil {
			if asStruct.IsText {
				return ai.StringContent(asStruct.Text)
			}
			return ai.Content{Blocks: decodeBlocks(asStruct.Blocks)}
		}
		return ai.Content{Blocks: decodeBlocks(content)}
	}

	switch probe.Role {
	case "system":
		var message ai.SystemMessage
		_ = json.Unmarshal(raw, &message)
		message.Content = decodeStringContent(probe.Content)
		return &message
	case "user":
		return &ai.UserMessage{Content: decodeStringContent(probe.Content)}
	case "assistant":
		var message ai.AssistantMessage
		_ = json.Unmarshal(raw, &message)
		message.Content = decodeBlocks(probe.Content)
		return &message
	case "toolResult":
		var message ai.ToolResultMessage
		_ = json.Unmarshal(raw, &message)
		message.Content = decodeBlocks(probe.Content)
		return &message
	}
	return rawAgentMessage{raw: raw}
}
