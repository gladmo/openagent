package codingagent

// Port of core/agent-session.ts (part 1): the skill-block parser, session
// event vocabulary, and config shapes. The AgentSession class itself lands
// after settings-manager and resource-loader (its construction
// dependencies).

import (
	"encoding/json"
	"regexp"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// ============================================================================
// defaults.ts
// ============================================================================

// DefaultThinkingLevel is the out-of-box thinking level.
const DefaultThinkingLevel = agent.ThinkingMedium

// ThinkingLevelOptions lists every level in order.
var ThinkingLevelOptions = []string{
	agent.ThinkingOff,
	agent.ThinkingMinimal,
	agent.ThinkingLow,
	agent.ThinkingMedium,
	agent.ThinkingHigh,
	agent.ThinkingXHigh,
	agent.ThinkingMax,
}

// ============================================================================
// Skill blocks
// ============================================================================

// ParsedSkillBlock is a skill invocation extracted from message text.
type ParsedSkillBlock struct {
	Name     string
	Location string
	Content  string
	// UserMessage is the optional trailing message after the block.
	UserMessage string
}

var skillBlockPattern = regexp.MustCompile(`(?s)^<skill name="([^"]+)" location="([^"]+)">\n(.*?)\n</skill>(?:\n\n([\s\S]+))?$`)

// ParseSkillBlock extracts a skill block from message text (nil when the
// text has none).
func ParseSkillBlock(text string) *ParsedSkillBlock {
	match := skillBlockPattern.FindStringSubmatch(text)
	if match == nil {
		return nil
	}
	return &ParsedSkillBlock{
		Name:        match[1],
		Location:    match[2],
		Content:     match[3],
		UserMessage: trimOrEmpty(match[4]),
	}
}

func trimOrEmpty(s string) string {
	trimmed := regexpTrimSpace(s)
	if trimmed == "" {
		return ""
	}
	return trimmed
}

func regexpTrimSpace(s string) string {
	start := 0
	for start < len(s) && isSpaceByte(s[start]) {
		start++
	}
	end := len(s)
	for end > start && isSpaceByte(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// ============================================================================
// Session events
// ============================================================================

// AgentSessionEvent discriminants (extending the core agent events).
const (
	EventAgentSettled                   = "agent_settled"
	EventQueueUpdate                    = "queue_update"
	EventCompactionStart                = "compaction_start"
	EventEntryAppended                  = "entry_appended"
	EventSessionInfoChanged             = "session_info_changed"
	EventThinkingLevelChanged           = "thinking_level_changed"
	EventCompactionEnd                  = "compaction_end"
	EventAutoRetryStart                 = "auto_retry_start"
	EventAutoRetryEnd                   = "auto_retry_end"
	EventSummarizationRetryScheduled    = "summarization_retry_scheduled"
	EventSummarizationRetryAttemptStart = "summarization_retry_attempt_start"
	EventSummarizationRetryFinished     = "summarization_retry_finished"
	EventBashExecutionUpdate            = "bash_execution_update"
)

// CompactionReason names why a compaction started.
type CompactionReason string

const (
	CompactionManual    CompactionReason = "manual"
	CompactionThreshold CompactionReason = "threshold"
	CompactionOverflow  CompactionReason = "overflow"
)

// AgentSessionEvent is one session-lifecycle event payload. Core agent
// events arrive through the agent package's event stream; these carry the
// session-specific extensions.
type AgentSessionEvent struct {
	Type string `json:"type"`

	// agent_end
	Messages  []agent.AgentMessage `json:"messages,omitempty"`
	WillRetry bool                 `json:"willRetry,omitempty"`

	// queue_update
	Steering []string `json:"steering,omitempty"`
	FollowUp []string `json:"followUp,omitempty"`

	// compaction_start / compaction_end
	Reason       CompactionReason       `json:"reason,omitempty"`
	Compaction   *CompactionResultShape `json:"result,omitempty"`
	Aborted      bool                   `json:"aborted,omitempty"`
	ErrorMessage string                 `json:"errorMessage,omitempty"`

	// entry_appended
	Entry SessionEntry `json:"entry,omitempty"`

	// session_info_changed
	Name string `json:"name,omitempty"`

	// thinking_level_changed
	Level string `json:"level,omitempty"`

	// auto_retry_start / auto_retry_end / summarization_retry_*
	Attempt     int    `json:"attempt,omitempty"`
	MaxAttempts int    `json:"maxAttempts,omitempty"`
	DelayMs     int    `json:"delayMs,omitempty"`
	Success     bool   `json:"success,omitempty"`
	FinalError  string `json:"finalError,omitempty"`
	Source      string `json:"source,omitempty"` // "branchSummary" | "compaction"

	// bash_execution_update
	BashExecutionID string `json:"id,omitempty"`
	Delta           string `json:"delta,omitempty"`

	// message_update: raw assistant-message streaming event for JSON
	// projection (not serialized; the projection strips snapshots).
	AssistantMessageEvent json.RawMessage `json:"-"`
}

// CompactionResultShape mirrors the compaction result for events.
type CompactionResultShape struct {
	Summary              string
	FirstKeptEntryID     string
	TokensBefore         int64
	EstimatedTokensAfter int
}

// AgentSessionEventListener receives session events.
type AgentSessionEventListener func(event *AgentSessionEvent)

// ============================================================================
// Config shapes
// ============================================================================

// withoutDeletedHeaders drops null-valued headers (deletion markers).
func withoutDeletedHeaders(headers ai.ProviderHeaders) map[string]string {
	if headers == nil {
		return nil
	}
	out := map[string]string{}
	for name, value := range headers {
		if value != nil {
			out[name] = *value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AgentSessionConfig wires the session's collaborators.
type AgentSessionConfig struct {
	Agent           *agent.Agent
	SessionManager  *SessionManager
	SettingsManager *SettingsManager
	Cwd             string
	// ScopedModels cycle with Ctrl+P (--models).
	ScopedModels []ScopedModel
	// CustomTools are SDK tools registered outside extensions.
	CustomTools []*ToolDefinition
	// ModelRuntime is the canonical model/auth runtime.
	ModelRuntime *ModelRuntime
	// InitialActiveToolNames sets the initial active tools: nil defaults to
	// read/bash/edit/write (after transcript restore), an empty slice starts
	// with no tools.
	InitialActiveToolNames []string
	// AllowedToolNames is an optional allowlist of tool names or patterns
	// (* matches any characters); empty = no tools allowed.
	AllowedToolNames []string
	// ExcludedToolNames is an optional denylist of tool names or patterns;
	// it applies after the allowlist.
	ExcludedToolNames []string
}

// QueuedInputDisposition values.
const (
	DispositionHandled = "handled"
	DispositionQueued  = "queued"
	DispositionStarted = "started"
)

// ModelMutationOptions gate model changes.
type ModelMutationOptions struct {
	Persist bool
}

// ModelCycleResult reports a model-cycle step.
type ModelCycleResult struct {
	Model         *ai.Model
	ThinkingLevel string
	Cycled        bool
}

// SessionStats summarize the session.
type SessionStats struct {
	TotalTokens      int
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	Cost             float64
	DurationMs       int64
	Turns            int
}
