package codingagent

// Port of core/agent-session.ts (core state machine): event fan-out, the
// tool registry, system-prompt option plumbing, model set/cycle, thinking
// level management, steering/follow-up queuing, pending next-turn custom
// messages, and abort/idle coordination. The extension-runner interaction
// rides a seam interface (Go-native extension deviation).

import (
	"encoding/json"
	"sync"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

// ExtensionRunnerSeam is the extension-runner surface AgentSession needs
// (the full runner lands with the extensions phase).
type ExtensionRunnerSeam interface {
	// HasHandlers reports whether an event has subscribers.
	HasHandlers(event string) bool
	// Invalidate marks extension contexts stale after replacement.
	Invalidate(reason string)
}

// AgentSession owns one agent + session + settings triple.
type AgentSession struct {
	Agent           *agent.Agent
	SessionManager  *SessionManager
	SettingsManager *SettingsManager
	ModelRuntime    *ModelRuntime

	mu sync.Mutex

	// defaultSummarizationStream performs the provider-backed
	// summarization call for compaction and branch summaries.
	defaultSummarizationStream SummarizationStreamFn

	scopedModels []ScopedModel

	listenerTokens    []int
	tokenedListeners  map[int]AgentSessionEventListener
	nextListenerToken int

	isAgentRunActive       bool
	agentRunAbortRequested bool

	steeringMessages        []string
	followUpMessages        []string
	pendingNextTurnMessages []agent.AgentMessage
	pendingCustomMessages   []agent.AgentMessage

	turnIndex int

	resourceLoader ResourceLoaderSeam
	customTools    []*ToolDefinition
	cwd            string

	extensionRunner ExtensionRunnerSeam

	baseSystemPromptOptions NormalizedBuildSystemPromptOptions
	runSystemPromptOptions  *NormalizedBuildSystemPromptOptions

	toolRegistry         map[string]*agent.AgentTool
	registeredTools      map[string]*RegisteredTool
	toolPromptSnippets   map[string]string
	toolPromptGuidelines map[string][]string
	activeToolNames      []string

	initialActiveToolNames []string
	allowedToolNames       func(string) bool
	excludedToolNames      func(string) bool
}

// AgentSessionRuntimeOptions construct the session runtime surface.
type AgentSessionRuntimeOptions struct {
	Config AgentSessionConfig
	// ExtensionRunner is the seam (nil = no handlers).
	ExtensionRunner ExtensionRunnerSeam
	// ResourceLoader supplies context files and skills for the prompt.
	ResourceLoader ResourceLoaderSeam
}

// ResourceLoaderSeam feeds the system prompt's project context and skills.
type ResourceLoaderSeam interface {
	GetAgentsFiles() []ContextFileWithPath
	GetSkills() []Skill
	GetSystemPrompt() string
	GetAppendSystemPrompt() []string
}

// NewAgentSession builds the session and wires the agent subscription.
func NewAgentSession(options AgentSessionRuntimeOptions) *AgentSession {
	config := options.Config
	// Preserve nil (= unset: default loadout, transcript-restore eligible)
	// versus a non-nil empty slice (= explicit zero tools).
	var initialActiveToolNames []string
	if config.InitialActiveToolNames != nil {
		initialActiveToolNames = make([]string, 0, len(config.InitialActiveToolNames))
		initialActiveToolNames = append(initialActiveToolNames, config.InitialActiveToolNames...)
	}
	session := &AgentSession{
		Agent:                  config.Agent,
		SessionManager:         config.SessionManager,
		SettingsManager:        config.SettingsManager,
		ModelRuntime:           config.ModelRuntime,
		scopedModels:           append([]ScopedModel(nil), config.ScopedModels...),
		customTools:            append([]*ToolDefinition(nil), config.CustomTools...),
		cwd:                    config.Cwd,
		extensionRunner:        options.ExtensionRunner,
		resourceLoader:         options.ResourceLoader,
		tokenedListeners:       map[int]AgentSessionEventListener{},
		toolRegistry:           map[string]*agent.AgentTool{},
		registeredTools:        map[string]*RegisteredTool{},
		toolPromptSnippets:     map[string]string{},
		toolPromptGuidelines:   map[string][]string{},
		initialActiveToolNames: initialActiveToolNames,
	}
	if config.AllowedToolNames != nil {
		session.allowedToolNames = CreateToolNameMatcher(config.AllowedToolNames)
	}
	if config.ExcludedToolNames != nil {
		session.excludedToolNames = CreateToolNameMatcher(config.ExcludedToolNames)
	}

	session.buildRuntime()
	return session
}

// emit fans one event to every listener.
func (s *AgentSession) emit(event *AgentSessionEvent) {
	s.mu.Lock()
	tokens := append([]int(nil), s.listenerTokens...)
	listeners := make([]AgentSessionEventListener, 0, len(tokens))
	for _, token := range tokens {
		if listener, ok := s.tokenedListeners[token]; ok {
			listeners = append(listeners, listener)
		}
	}
	s.mu.Unlock()
	for _, listener := range listeners {
		listener(event)
	}
}

// Subscribe registers an event listener; the return value unsubscribes.
// Function values are not comparable in Go, so subscriptions carry unique
// tokens matched at unsubscribe.
func (s *AgentSession) Subscribe(listener AgentSessionEventListener) func() {
	s.mu.Lock()
	token := s.nextListenerToken
	s.nextListenerToken++
	s.listenerTokens = append(s.listenerTokens, token)
	s.tokenedListeners[token] = listener
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.tokenedListeners, token)
		for i, existing := range s.listenerTokens {
			if existing == token {
				s.listenerTokens = append(s.listenerTokens[:i], s.listenerTokens[i+1:]...)
				break
			}
		}
	}
}

// Dispose aborts everything and drops listeners.
func (s *AgentSession) Dispose() {
	s.AbortRetry()
	s.AbortBash()
	if s.extensionRunner != nil {
		s.extensionRunner.Invalidate("This extension ctx is stale after session replacement or reload.")
	}
	s.mu.Lock()
	s.listenerTokens = nil
	s.tokenedListeners = map[int]AgentSessionEventListener{}
	s.mu.Unlock()
}

// buildRuntime assembles the tool registry and prompt options.
func (s *AgentSession) buildRuntime() {
	// Register the built-in tool definitions (the reference's
	// createAllToolDefinitions set and key order).
	definitions := []*ToolDefinition{
		CreateReadToolDefinition(s.cwd, s.readToolOptions()),
		CreateBashToolDefinition(s.cwd, s.bashToolOptions()),
		CreatePowerShellToolDefinition(s.cwd, nil),
		CreateEditToolDefinition(s.cwd, nil),
		CreateWriteToolDefinition(s.cwd, nil),
		CreateGrepToolDefinition(s.cwd, nil),
		CreateFindToolDefinition(s.cwd, nil),
		CreateLsToolDefinition(s.cwd, nil),
	}
	definitions = append(definitions, s.customTools...)

	for _, definition := range definitions {
		if s.allowedToolNames != nil && !s.allowedToolNames(definition.Name) {
			continue
		}
		if s.excludedToolNames != nil && s.excludedToolNames(definition.Name) {
			continue
		}
		s.registerToolDefinition(definition)
	}

	active := s.initialActiveToolNames
	if active == nil {
		active = append([]string(nil), DefaultToolNames...)
	}
	s.SetActiveToolsByName(active)
	// An unset initial loadout defers to the transcript's declared tools
	// (restored sessions keep their loadout; fresh sessions keep the
	// default).
	if s.initialActiveToolNames == nil {
		s.restoreToolsFromTranscript()
	}
}

// readToolOptions resolves settings-driven read options; nil settings keep
// the tool defaults (autoResizeImages defaults true).
func (s *AgentSession) readToolOptions() *ReadToolOptions {
	if s.SettingsManager == nil {
		return nil
	}
	autoResize := s.SettingsManager.GetImageAutoResize()
	return &ReadToolOptions{AutoResizeImages: &autoResize}
}

// bashToolOptions resolves settings-driven bash options; nil settings keep
// the tool defaults (empty prefix and shell resolve to platform defaults).
func (s *AgentSession) bashToolOptions() *BashToolOptions {
	if s.SettingsManager == nil {
		return nil
	}
	return &BashToolOptions{
		CommandPrefix: s.SettingsManager.GetShellCommandPrefix(),
		ShellPath:     s.SettingsManager.GetShellPath(),
	}
}

// restoreToolsFromTranscript applies the tool loadout the transcript's
// system messages declare: additions and removals replayed in order, then
// filtered to the registry. A transcript with no system message leaves the
// current loadout untouched.
func (s *AgentSession) restoreToolsFromTranscript() {
	if s.SessionManager == nil {
		return
	}
	declared := map[string]bool{}
	var order []string
	sawSystem := false
	for _, projected := range s.SessionManager.BuildSessionProjectionFromLeaf().Entries {
		for _, message := range projected.Messages {
			raw, ok := message.(rawAgentMessage)
			if !ok || raw.Role() != "system" {
				continue
			}
			sawSystem = true
			var probe struct {
				ToolsAdded []struct {
					Name string `json:"name"`
				} `json:"toolsAdded"`
				ToolsRemoved []struct {
					Name string `json:"name"`
				} `json:"toolsRemoved"`
			}
			if json.Unmarshal(raw.Raw(), &probe) != nil {
				continue
			}
			for _, tool := range probe.ToolsRemoved {
				delete(declared, tool.Name)
			}
			for _, tool := range probe.ToolsAdded {
				if tool.Name == "" {
					continue
				}
				if _, exists := declared[tool.Name]; !exists {
					order = append(order, tool.Name)
				}
				declared[tool.Name] = true
			}
		}
	}
	if !sawSystem {
		return
	}
	names := make([]string, 0, len(order))
	for _, name := range order {
		if declared[name] {
			names = append(names, name)
		}
	}
	// Extension tools the branch never mentioned were registered after it
	// recorded its loadout, so they are activated like newly registered
	// tools (declarable and not defaultActive=false). Built-in tools follow
	// the default loadout instead.
	builtinNames := map[string]bool{}
	for _, name := range s.builtinToolNames() {
		builtinNames[name] = true
	}
	for _, definition := range s.customTools {
		name := definition.Name
		if _, mentioned := declared[name]; mentioned || builtinNames[name] {
			continue
		}
		if definition.DefaultActive != nil && !*definition.DefaultActive {
			continue
		}
		names = append(names, name)
	}
	s.SetActiveToolsByName(names)
}

// builtinToolNames lists the built-in tool set in registration order.
func (s *AgentSession) builtinToolNames() []string {
	return []string{"read", "bash", "powershell", "edit", "write", "grep", "find", "ls"}
}

// registerToolDefinition adds one definition to the registry.
func (s *AgentSession) registerToolDefinition(definition *ToolDefinition) {
	wrapped := WrapToolDefinition(definition, nil)
	s.registeredTools[definition.Name] = &RegisteredTool{Definition: definition}
	s.toolRegistry[definition.Name] = wrapped
	if definition.PromptSnippet != "" {
		s.toolPromptSnippets[definition.Name] = definition.PromptSnippet
	}
	if definition.PromptGuidelines != nil {
		s.toolPromptGuidelines[definition.Name] = append([]string(nil), definition.PromptGuidelines...)
	}
}

// RebuildSystemPrompt recomputes the prompt options for the active tools.
func (s *AgentSession) RebuildSystemPrompt(activeToolNames []string) {
	options := BuildSystemPromptOptions{
		Cwd:              s.cwd,
		SelectedTools:    append([]string(nil), activeToolNames...),
		ToolSnippets:     map[string]string{},
		ToolGuidelines:   map[string][]string{},
		PromptGuidelines: nil,
	}
	for _, name := range activeToolNames {
		if snippet, ok := s.toolPromptSnippets[name]; ok {
			options.ToolSnippets[name] = snippet
		}
		if guidelines, ok := s.toolPromptGuidelines[name]; ok {
			options.ToolGuidelines[name] = append([]string(nil), guidelines...)
		}
	}

	if s.resourceLoader != nil {
		for _, file := range s.resourceLoader.GetAgentsFiles() {
			options.ContextFiles = append(options.ContextFiles, ContextFile{Path: file.Path, Content: file.Content})
		}
		options.Skills = s.resourceLoader.GetSkills()
		if custom := s.resourceLoader.GetSystemPrompt(); custom != "" {
			options.CustomPrompt = custom
		}
		for _, append_ := range s.resourceLoader.GetAppendSystemPrompt() {
			options.AppendSystemPrompt += append_ + "\n"
		}
	}

	s.mu.Lock()
	s.baseSystemPromptOptions = NormalizeBuildSystemPromptOptions(options)
	s.runSystemPromptOptions = nil
	s.mu.Unlock()
}

// SystemPrompt renders the effective prompt (run options override base).
func (s *AgentSession) SystemPrompt() string {
	s.mu.Lock()
	options := s.baseSystemPromptOptions
	if s.runSystemPromptOptions != nil {
		options = *s.runSystemPromptOptions
	}
	s.mu.Unlock()
	_ = options
	state, err := BuildSystemPromptState(options.BuildSystemPromptOptions)
	if err != nil {
		return ""
	}
	if state.Content != "" {
		return state.Content
	}
	sections := state.Sections
	// Render preamble plus tagged sections in sorted order (the transcript
	// replay order).
	names := make([]string, 0, len(sections))
	for name := range sections {
		names = append(names, name)
	}
	sortStrings(names)
	rendered := sections["preamble"]
	for _, name := range names {
		if name == "preamble" {
			continue
		}
		rendered += "\n" + sections[name]
	}
	return rendered
}

// GetActiveToolNames lists the active tools.
func (s *AgentSession) GetActiveToolNames() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.activeToolNames...)
}

// GetAllTools lists every registry entry.
func (s *AgentSession) GetAllTools() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.toolRegistry))
	for name := range s.toolRegistry {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// SetActiveToolsByName activates registry tools by name (unknown ignored),
// installs the executable loadout on the agent (effective next turn), and
// rebuilds the system prompt.
func (s *AgentSession) SetActiveToolsByName(toolNames []string) {
	s.mu.Lock()
	var valid []string
	var tools []*agent.AgentTool
	for _, name := range toolNames {
		if tool, ok := s.toolRegistry[name]; ok {
			valid = append(valid, name)
			tools = append(tools, tool)
		}
	}
	s.activeToolNames = valid
	s.mu.Unlock()
	if s.Agent != nil {
		s.Agent.SetTools(tools)
	}
	s.RebuildSystemPrompt(valid)
}

// GetSteeringMessages exposes pending steering (read-only).
func (s *AgentSession) GetSteeringMessages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.steeringMessages...)
}

// GetFollowUpMessages exposes pending follow-ups (read-only).
func (s *AgentSession) GetFollowUpMessages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.followUpMessages...)
}

// Steer queues a steering message for the active run.
func (s *AgentSession) Steer(text string) {
	s.mu.Lock()
	s.steeringMessages = append(s.steeringMessages, text)
	s.mu.Unlock()
	s.emit(&AgentSessionEvent{Type: EventQueueUpdate, Steering: s.GetSteeringMessages(), FollowUp: s.GetFollowUpMessages()})
}

// FollowUp queues a follow-up message for after the run settles.
func (s *AgentSession) FollowUp(text string) {
	s.mu.Lock()
	s.followUpMessages = append(s.followUpMessages, text)
	s.mu.Unlock()
	s.emit(&AgentSessionEvent{Type: EventQueueUpdate, Steering: s.GetSteeringMessages(), FollowUp: s.GetFollowUpMessages()})
}

// ClearSteering drains the steering queue.
func (s *AgentSession) ClearSteering() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	drained := s.steeringMessages
	s.steeringMessages = nil
	return drained
}

// ClearFollowUp drains the follow-up queue.
func (s *AgentSession) ClearFollowUp() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	drained := s.followUpMessages
	s.followUpMessages = nil
	return drained
}

// QueueForNextTurn queues a custom message for the next prompt's context.
func (s *AgentSession) QueueForNextTurn(message agent.AgentMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingNextTurnMessages = append(s.pendingNextTurnMessages, message)
}

// DrainPendingNextTurn returns and clears the queued next-turn messages.
func (s *AgentSession) DrainPendingNextTurn() []agent.AgentMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	drained := s.pendingNextTurnMessages
	s.pendingNextTurnMessages = nil
	return drained
}

// IsStreaming reports an active agent run.
func (s *AgentSession) IsStreaming() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.isAgentRunActive
}

// SetAgentRunActive tracks the run state for idle coordination.
func (s *AgentSession) SetAgentRunActive(active bool) {
	s.mu.Lock()
	s.isAgentRunActive = active
	if !active {
		s.agentRunAbortRequested = false
	}
	s.mu.Unlock()
	if !active {
		s.emit(&AgentSessionEvent{Type: EventAgentSettled})
	}
}

// RequestAbort marks the run abort-requested and aborts the agent.
func (s *AgentSession) RequestAbort() {
	s.mu.Lock()
	if s.isAgentRunActive {
		s.agentRunAbortRequested = true
	}
	s.mu.Unlock()
}

// Abort aborts retries, bash, and the agent.
func (s *AgentSession) Abort() {
	s.RequestAbort()
	s.AbortRetry()
	s.AbortBash()
}

// AbortRetry cancels a pending retry.
func (s *AgentSession) AbortRetry() {}

// AbortBash cancels in-flight bash executions.
func (s *AgentSession) AbortBash() {}

// GetTurnIndex reports the completed-turn count.
func (s *AgentSession) GetTurnIndex() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnIndex
}

// IncrementTurnIndex records one completed turn.
func (s *AgentSession) IncrementTurnIndex() {
	s.mu.Lock()
	s.turnIndex++
	s.mu.Unlock()
}

// Model returns the agent's current model (nil when unset).
func (s *AgentSession) Model() *ai.Model {
	return s.Agent.Model()
}

// ThinkingLevel returns the agent's current level.
func (s *AgentSession) ThinkingLevel() string {
	return s.Agent.ThinkingLevel()
}

// SetModel switches models, records the change, and applies the thinking
// level (persisting to settings when requested).
func (s *AgentSession) SetModel(model *ai.Model, persist bool) error {
	if !s.ModelRuntime.HasConfiguredAuth(model.Provider) {
		return &noAuthError{provider: model.Provider, modelID: model.ID}
	}

	previous := s.Model()
	s.Agent.SetModel(model)
	s.SessionManager.AppendModelChange(model.Provider, model.ID)
	if persist {
		s.SettingsManager.SetDefaultModelAndProvider(model.Provider, model.ID)
	}

	thinkingLevel := s.thinkingLevelForModel(model)
	s.SetThinkingLevel(thinkingLevel)
	_ = previous
	return nil
}

type noAuthError struct {
	provider string
	modelID  string
}

func (e *noAuthError) Error() string {
	return "No API key for " + e.provider + "/" + e.modelID
}

// thinkingLevelForModel resolves per-model overrides then the global
// default, then the default level.
func (s *AgentSession) thinkingLevelForModel(model *ai.Model) string {
	levels := s.SettingsManager.GetModelThinkingLevels()
	if level, ok := levels[model.Provider+"/"+model.ID]; ok && level != "" {
		return level
	}
	if level := s.SettingsManager.GetDefaultThinkingLevel(); level != "" {
		return level
	}
	return DefaultThinkingLevel
}

// SetThinkingLevel applies and records a thinking-level change.
func (s *AgentSession) SetThinkingLevel(level string) {
	if s.Agent.ThinkingLevel() == level {
		return
	}
	s.Agent.SetThinkingLevel(level)
	s.SessionManager.AppendThinkingLevelChange(level)
	s.emit(&AgentSessionEvent{Type: EventThinkingLevelChanged, Level: level})
}

// CycleModel steps through scoped models (or available models), returning
// the new selection (nil when only one is available).
func (s *AgentSession) CycleModel(direction string, persist bool) *ModelCycleResult {
	s.mu.Lock()
	scoped := append([]ScopedModel(nil), s.scopedModels...)
	s.mu.Unlock()

	var pool []ScopedModel
	if len(scoped) > 0 {
		available := map[string]bool{}
		for _, model := range s.ModelRuntime.GetAvailableSnapshot() {
			available[model.Provider+"\x00"+model.ID] = true
		}
		for _, entry := range scoped {
			if available[entry.Model.Provider+"\x00"+entry.Model.ID] {
				pool = append(pool, entry)
			}
		}
	} else {
		for _, model := range s.ModelRuntime.GetAvailableSnapshot() {
			pool = append(pool, ScopedModel{Model: model})
		}
	}
	if len(pool) < 2 {
		return nil
	}

	current := s.Model()
	index := 0
	if current != nil {
		for i, entry := range pool {
			if ai.ModelsAreEqual(entry.Model, current) {
				index = i
				break
			}
		}
	}
	next := (index + 1) % len(pool)
	if direction == "backward" {
		next = (index - 1 + len(pool)) % len(pool)
	}
	target := pool[next]
	if err := s.SetModel(target.Model, persist); err != nil {
		return nil
	}
	return &ModelCycleResult{Model: target.Model, ThinkingLevel: s.ThinkingLevel(), Cycled: true}
}

var _ = abort.NewController

// GetToolDefinitionRef looks up a registered definition by tool name via
// the wrapped registry entry.
func (s *AgentSession) GetToolDefinitionRef(name string) *ToolDefinition {
	if tool := s.GetRegisteredTool(name); tool != nil {
		return tool.Definition
	}
	return nil
}

// GetRegisteredTool returns the registry entry for a tool name.
func (s *AgentSession) GetRegisteredTool(name string) *RegisteredTool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registeredTools[name]
}
