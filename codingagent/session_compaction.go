package codingagent

// Session-level compaction execution (agent-session.ts compactManual /
// auto-compaction path): prepare the current path, run the LLM
// summarization, and append the compaction entry plus its summary
// message so subsequent turns continue from the checkpoint.

import (
	"encoding/json"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// CompactSessionResult reports one executed compaction.
type CompactSessionResult struct {
	// Executed is false when nothing was compressible.
	Executed         bool
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int64
	Usage            ai.Usage
	ReadFiles        []string
	ModifiedFiles    []string
}

// SessionCompactionOptions configure one compaction.
type SessionCompactionOptions struct {
	Settings           CompactionSettings
	CustomInstructions string
	// GenerateSummaryOptions carries api key/headers/env/retry routing.
	GenerateSummaryOptions *GenerateSummaryOptions
	// StreamFn overrides the session's stream function (tests).
	StreamFn SummarizationStreamFn
}

// CompactSession prepares and executes one compaction over the session's
// current path; a preparation with nothing to summarize reports
// Executed=false.
func (s *AgentSession) CompactSession(options *SessionCompactionOptions) (*CompactSessionResult, error) {
	if options == nil {
		options = &SessionCompactionOptions{}
	}
	settings := options.Settings
	if settings.ReserveTokens == 0 && settings.KeepRecentTokens == 0 {
		settings = DefaultCompactionSettings
	}

	path := s.currentBranchPath()
	preparation := PrepareCompaction(path, settings)
	if preparation == nil {
		return &CompactSessionResult{Executed: false}, nil
	}

	generateOptions := options.GenerateSummaryOptions
	if generateOptions == nil {
		generateOptions = &GenerateSummaryOptions{}
	}
	if options.CustomInstructions != "" {
		generateOptions.CustomInstructions = options.CustomInstructions
	}
	generateOptions.ReserveTokens = settings.ReserveTokens

	outcome, err := Compact(preparation, s.Model(), generateOptions, s.summarizationStreamFn(options.StreamFn))
	if err != nil {
		s.emit(&AgentSessionEvent{
			Type:         EventCompactionEnd,
			Reason:       CompactionManual,
			ErrorMessage: err.Error(),
		})
		return nil, err
	}

	// Persist the checkpoint, then replay it as the summary message the
	// next turn sees.
	details, _ := json.Marshal(BranchSummaryDetails{
		ReadFiles:     outcome.ReadFiles,
		ModifiedFiles: outcome.ModifiedFiles,
	})
	usageJSON, _ := json.Marshal(outcome.Usage)
	firstKept := outcome.FirstKeptEntryID
	s.SessionManager.AppendCompaction(outcome.Summary, &firstKept, outcome.TokensBefore, details, false, usageJSON)

	s.emit(&AgentSessionEvent{
		Type:   EventCompactionEnd,
		Reason: CompactionManual,
		Compaction: &CompactionResultShape{
			Summary:          outcome.Summary,
			FirstKeptEntryID: outcome.FirstKeptEntryID,
			TokensBefore:     outcome.TokensBefore,
			EstimatedTokensAfter: EstimateTokens(CreateCompactionSummaryMessage(
				outcome.Summary, outcome.TokensBefore, "")),
		},
	})

	return &CompactSessionResult{
		Executed:         true,
		Summary:          outcome.Summary,
		FirstKeptEntryID: outcome.FirstKeptEntryID,
		TokensBefore:     outcome.TokensBefore,
		Usage:            outcome.Usage,
		ReadFiles:        outcome.ReadFiles,
		ModifiedFiles:    outcome.ModifiedFiles,
	}, nil
}

// ShouldAutoCompact reports whether the current context crossed the
// reserve threshold.
func (s *AgentSession) ShouldAutoCompact(settings CompactionSettings) bool {
	contextWindow := 128000
	if model := s.Model(); model != nil && model.ContextWindow > 0 {
		contextWindow = int(model.ContextWindow)
	}
	estimate := EstimateContextTokens(s.agentMessages())
	return ShouldCompact(estimate.Tokens, contextWindow, settings)
}

// AutoCompactIfNeeded compacts when the threshold is crossed (no-op
// otherwise).
func (s *AgentSession) AutoCompactIfNeeded(settings CompactionSettings, options *SessionCompactionOptions) (*CompactSessionResult, error) {
	if !s.ShouldAutoCompact(settings) {
		return &CompactSessionResult{Executed: false}, nil
	}
	if options == nil {
		options = &SessionCompactionOptions{}
	}
	options.Settings = settings
	return s.CompactSession(options)
}

// currentBranchPath walks the session's leaf-to-root path.
func (s *AgentSession) currentBranchPath() []SessionEntry {
	if s.SessionManager == nil {
		return nil
	}
	entries := s.SessionManager.GetEntries()
	return entries
}

// agentMessages returns the agent transcript as agent messages.
func (s *AgentSession) agentMessages() []agent.AgentMessage {
	if s.Agent == nil {
		return nil
	}
	return s.Agent.Messages()
}

// summarizationStreamFn resolves the stream function used for
// summarization: an explicit override, else the provider-backed default.
func (s *AgentSession) summarizationStreamFn(override SummarizationStreamFn) SummarizationStreamFn {
	if override != nil {
		return override
	}
	return s.defaultSummarizationStream
}

// SetSummarizationStreamFn installs the provider-backed summarization
// call (wired by the runtime; nil keeps summarization unavailable).
func (s *AgentSession) SetSummarizationStreamFn(fn SummarizationStreamFn) {
	s.defaultSummarizationStream = fn
}
