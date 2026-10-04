package codingagent

// Tests for the AgentSession core (ported from agent-session.test.ts
// surfaces: registry, prompt, queuing, model management).

import (
	"strings"
	"sync"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

func newTestSession(t *testing.T) *AgentSession {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(EnvAgentDirName(), dir)

	manager, err := SessionManagerCreate(dir, "", nil)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	settings := SettingsManagerInMemory(Settings{}, nil)
	runtime, err := NewModelRuntime(&CreateModelRuntimeOptions{
		AuthPath:      dir + "/auth.json",
		ModelsPath:    dir + "/absent-models.json",
		HasModelsPath: true,
	})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}

	session := NewAgentSession(AgentSessionRuntimeOptions{
		Config: AgentSessionConfig{
			Agent:           agent.NewAgent(agent.AgentOptions{}),
			SessionManager:  manager,
			SettingsManager: settings,
			ModelRuntime:    runtime,
			Cwd:             dir,
		},
	})
	return session
}

func TestAgentSessionToolRegistry(t *testing.T) {
	session := newTestSession(t)

	// Default tools active.
	active := session.GetActiveToolNames()
	if len(active) != 4 {
		t.Fatalf("active = %v", active)
	}
	for _, expected := range []string{"read", "bash", "edit", "write"} {
		found := false
		for _, name := range active {
			if name == expected {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %s", expected)
		}
	}

	// Full registry includes discovery tools.
	all := session.GetAllTools()
	for _, expected := range []string{"grep", "find", "ls"} {
		found := false
		for _, name := range all {
			if name == expected {
				found = true
			}
		}
		if !found {
			t.Errorf("registry missing %s", expected)
		}
	}

	// Activation filters and rebuilds the prompt.
	session.SetActiveToolsByName([]string{"read", "grep", "unknown-tool"})
	active = session.GetActiveToolNames()
	if len(active) != 2 || active[0] != "read" || active[1] != "grep" {
		t.Fatalf("filtered = %v", active)
	}
	prompt := session.SystemPrompt()
	if !strings.Contains(prompt, "- read: Read") {
		t.Fatalf("prompt tools = %q", prompt[:min(400, len(prompt))])
	}
	if strings.Contains(prompt, "- bash:") {
		t.Fatal("inactive tool in prompt")
	}
}

func TestAgentSessionSystemPrompt(t *testing.T) {
	session := newTestSession(t)

	prompt := session.SystemPrompt()
	if !strings.Contains(prompt, "expert coding assistant") {
		t.Fatal("preamble missing")
	}
	if !strings.Contains(prompt, "<rules>") {
		t.Fatal("rules section missing")
	}
	if !strings.Contains(prompt, "<tools>") {
		t.Fatal("tools section missing")
	}
	if !strings.Contains(prompt, "<cwd>") {
		t.Fatal("cwd section missing")
	}
	if !strings.Contains(prompt, "Be concise in your responses") {
		t.Fatal("default rule missing")
	}
}

func TestAgentSessionQueuing(t *testing.T) {
	session := newTestSession(t)

	var mu sync.Mutex
	events := []string{}
	session.Subscribe(func(event *AgentSessionEvent) {
		mu.Lock()
		events = append(events, event.Type)
		mu.Unlock()
	})

	session.Steer("mid-run")
	session.FollowUp("after run")

	if steering := session.GetSteeringMessages(); len(steering) != 1 || steering[0] != "mid-run" {
		t.Fatalf("steering = %v", steering)
	}
	if followUp := session.GetFollowUpMessages(); len(followUp) != 1 || followUp[0] != "after run" {
		t.Fatalf("follow-up = %v", followUp)
	}

	// Queue updates emitted per enqueue.
	queueUpdates := 0
	mu.Lock()
	for _, eventType := range events {
		if eventType == EventQueueUpdate {
			queueUpdates++
		}
	}
	mu.Unlock()
	if queueUpdates != 2 {
		t.Fatalf("queue updates = %d", queueUpdates)
	}

	// Draining empties the queues.
	if drained := session.ClearSteering(); len(drained) != 1 {
		t.Fatalf("drain = %v", drained)
	}
	if len(session.GetSteeringMessages()) != 0 {
		t.Fatal("steering survived drain")
	}
}

func TestAgentSessionNextTurnMessages(t *testing.T) {
	session := newTestSession(t)

	message := &CustomMessage{CustomType: "note", Content: "context"}
	session.QueueForNextTurn(message)

	drained := session.DrainPendingNextTurn()
	if len(drained) != 1 {
		t.Fatalf("drained = %d", len(drained))
	}
	if len(session.DrainPendingNextTurn()) != 0 {
		t.Fatal("drain not idempotent")
	}
}

func TestAgentSessionEventFanOut(t *testing.T) {
	session := newTestSession(t)

	var mu sync.Mutex
	settled := 0
	unsubscribe := session.Subscribe(func(event *AgentSessionEvent) {
		mu.Lock()
		if event.Type == EventAgentSettled {
			settled++
		}
		mu.Unlock()
	})

	session.SetAgentRunActive(true)
	session.SetAgentRunActive(false)
	mu.Lock()
	count := settled
	mu.Unlock()
	if count != 1 {
		t.Fatalf("settled = %d", count)
	}

	// Unsubscribe stops delivery.
	unsubscribe()
	session.SetAgentRunActive(true)
	session.SetAgentRunActive(false)
	mu.Lock()
	count = settled
	mu.Unlock()
	if count != 1 {
		t.Fatalf("after unsubscribe = %d", count)
	}
}

func TestAgentSessionThinkingLevel(t *testing.T) {
	session := newTestSession(t)

	var mu sync.Mutex
	levels := []string{}
	session.Subscribe(func(event *AgentSessionEvent) {
		mu.Lock()
		if event.Type == EventThinkingLevelChanged {
			levels = append(levels, event.Level)
		}
		mu.Unlock()
	})

	session.SetThinkingLevel("high")
	if session.ThinkingLevel() != "high" {
		t.Fatalf("level = %q", session.ThinkingLevel())
	}
	// Recorded in the session transcript.
	entries := session.SessionManager.GetEntries()
	found := false
	for _, entry := range entries {
		if change, ok := entry.(*ThinkingLevelChangeEntry); ok && change.ThinkingLevel == "high" {
			found = true
		}
	}
	if !found {
		t.Fatal("thinking change not recorded")
	}
	// Event emitted once.
	mu.Lock()
	count := len(levels)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("events = %d", count)
	}

	// Same level: no-op.
	session.SetThinkingLevel("high")
	mu.Lock()
	count = len(levels)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("duplicate event = %d", count)
	}
}

func TestAgentSessionModelManagement(t *testing.T) {
	session := newTestSession(t)

	// Unauthenticated model errors.
	model := &ai.Model{Provider: "test-provider", ID: "m1", API: "openai-completions", BaseURL: "https://x"}
	if err := session.SetModel(model, false); err == nil || !strings.Contains(err.Error(), "No API key") {
		t.Fatalf("err = %v", err)
	}

	// Cycle with a single available model returns nil.
	if result := session.CycleModel("forward", false); result != nil {
		t.Fatalf("cycle = %+v", result)
	}

	// Scoped models with a static key authenticate and cycle.
	hasKey := true
	if err := session.ModelRuntime.RegisterProvider("test-provider", &ProviderConfigInput{
		APIKey: "k", HasAPIKey: hasKey,
		BaseURL: "https://x", API: "openai-completions",
		Models: []ProviderModelConfig{
			{ProviderModelConfigBase: ProviderModelConfigBase{ID: "one", API: "openai-completions", Input: []string{"text"}}},
			{ProviderModelConfigBase: ProviderModelConfigBase{ID: "two", API: "openai-completions", Input: []string{"text"}}},
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := session.ModelRuntime.Refresh(); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	session.mu.Lock()
	session.scopedModels = []ScopedModel{
		{Model: session.ModelRuntime.GetModel("test-provider", "one")},
		{Model: session.ModelRuntime.GetModel("test-provider", "two")},
	}
	session.mu.Unlock()

	if err := session.SetModel(session.ModelRuntime.GetModel("test-provider", "one"), false); err != nil {
		t.Fatalf("set: %v", err)
	}
	result := session.CycleModel("forward", false)
	if result == nil || result.Model.ID != "two" {
		t.Fatalf("cycle = %+v", result)
	}
	if !result.Cycled {
		t.Fatal("not cycled")
	}

	// Backward wraps.
	result = session.CycleModel("backward", false)
	if result == nil || result.Model.ID != "one" {
		t.Fatalf("backward = %+v", result)
	}

	// Model change recorded in the transcript.
	entries := session.SessionManager.GetEntries()
	found := false
	for _, entry := range entries {
		if change, ok := entry.(*ModelChangeEntry); ok && change.Provider == "test-provider" {
			found = true
		}
	}
	if !found {
		t.Fatal("model change not recorded")
	}
}

func TestAgentSessionDispose(t *testing.T) {
	session := newTestSession(t)
	called := false
	session.Subscribe(func(event *AgentSessionEvent) { called = true })
	session.Dispose()

	// No further events after dispose (listeners dropped).
	session.SetAgentRunActive(false)
	if called {
		t.Fatal("listener fired after dispose")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
