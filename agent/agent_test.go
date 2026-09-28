package agent

// Ports of representative cases from pi/packages/agent/test/agent.test.ts.

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
	"github.com/gladmo/openagent/typebox"
)

func agentTestStreamFn(messages ...*ai.AssistantMessage) StreamFn {
	index := 0
	return func(_ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		message := messages[index]
		index++
		go func(message *ai.AssistantMessage) {
			stream.Push(&ai.EventStart{Partial: &ai.AssistantMessage{Content: []ai.ContentBlock{}, API: message.API, Provider: message.Provider, Model: message.Model, StopReason: ai.StopPending}})
			stream.Push(&ai.EventDone{Reason: message.StopReason, Message: message})
		}(message)
		return stream
	}
}

func agentText(text string) *ai.AssistantMessage {
	return &ai.AssistantMessage{
		Content: []ai.ContentBlock{ai.TextContent{Text: text}},
		API:     "openai-responses", Provider: "openai", Model: "mock",
		StopReason: ai.StopStop, TimestampMs: float64(time.Now().UnixMilli()),
	}
}

func emptyObjectTool(name string) *AgentTool {
	return &AgentTool{
		Name: name, Label: name, Description: name + " tool",
		Parameters: typebox.Object(nil),
		Execute: func(string, any, *abort.Signal, AgentToolUpdateCallback) (*AgentToolResult, error) {
			return &AgentToolResult{Content: []ai.ContentBlock{ai.TextContent{Text: name}}, Details: jsonx.NewObj()}, nil
		},
	}
}

func TestAgentDefaultState(t *testing.T) {
	agent := NewAgent(AgentOptions{StreamFn: agentTestStreamFn(agentText("x"))})
	if agent.IsStreaming() {
		t.Fatal("streaming initially")
	}
	if agent.SystemPrompt() != "" {
		t.Fatalf("prompt = %q", agent.SystemPrompt())
	}
	if agent.Model().ID != "unknown" {
		t.Fatalf("model = %s", agent.Model().ID)
	}
	if agent.ThinkingLevel() != ThinkingOff {
		t.Fatal("thinking level")
	}
	if len(agent.Tools()) != 0 || len(agent.Messages()) != 0 {
		t.Fatal("non-empty state")
	}
	if len(agent.PendingToolCalls()) != 0 {
		t.Fatal("pending tool calls")
	}
}

func TestAgentCustomInitialState(t *testing.T) {
	prompt := "You are helpful."
	agent := NewAgent(AgentOptions{
		StreamFn: agentTestStreamFn(agentText("x")),
		InitialState: &AgentInitialState{
			SystemPrompt:  &prompt,
			Model:         testModel(),
			ThinkingLevel: strptrAgent(ThinkingHigh),
			Tools:         []*AgentTool{emptyObjectTool("read")},
		},
	})
	if agent.SystemPrompt() != "You are helpful." {
		t.Fatalf("prompt = %q", agent.SystemPrompt())
	}
	if agent.Model().ID != "mock" {
		t.Fatal("model")
	}
	if agent.ThinkingLevel() != ThinkingHigh {
		t.Fatal("thinking")
	}
	if len(agent.Tools()) != 1 {
		t.Fatal("tools")
	}
	// The initial system message leads the transcript.
	messages := agent.Messages()
	if len(messages) != 1 {
		t.Fatalf("messages = %d", len(messages))
	}
	system, ok := messages[0].(*ai.SystemMessage)
	if !ok {
		t.Fatalf("messages[0] = %T", messages[0])
	}
	// The leading system message declares the tool.
	if len(system.ToolsAdded) != 1 || system.ToolsAdded[0].Name != "read" {
		t.Fatalf("toolsAdded = %+v", system.ToolsAdded)
	}
}

func strptrAgent(s string) *string { return &s }

func TestAgentPromptLifecycleEvents(t *testing.T) {
	agent := NewAgent(AgentOptions{StreamFn: agentTestStreamFn(agentText("hi"))})
	var types []string
	agent.Subscribe(func(event AgentEvent, _ *abort.Signal) {
		types = append(types, event.EventType())
	})
	if err := agent.Prompt(testUser("hello")); err != nil {
		t.Fatal(err)
	}
	want := "agent_start,turn_start,message_start,message_end,message_start,message_end,turn_end,agent_end"
	if strings.Join(types, ",") != want {
		t.Fatalf("types = %v", types)
	}
	// Transcript holds system+user? No initial prompt: user + assistant.
	messages := agent.Messages()
	if len(messages) != 2 {
		t.Fatalf("messages = %d", len(messages))
	}
	if _, ok := messages[1].(*ai.AssistantMessage); !ok {
		t.Fatalf("messages[1] = %T", messages[1])
	}
}

func TestAgentAwaitsSubscribersBeforePromptResolves(t *testing.T) {
	release := make(chan struct{})
	agent := NewAgent(AgentOptions{StreamFn: agentTestStreamFn(agentText("x"))})
	resumed := false
	agent.Subscribe(func(event AgentEvent, _ *abort.Signal) {
		if event.EventType() == "message_end" && !resumed {
			<-release
			resumed = true
		}
	})
	done := make(chan error, 1)
	go func() { done <- agent.Prompt(testUser("q")) }()
	// Give the run a moment; prompt must not resolve while the listener blocks.
	select {
	case err := <-done:
		t.Fatalf("prompt resolved early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !resumed {
		t.Fatal("listener never resumed")
	}
}

func TestAgentHandleRunFailureEmitsLifecycle(t *testing.T) {
	failingStream := func(_ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		go func() {
			errMsg := "provider exploded"
			message := agentText("")
			message.StopReason = ai.StopError
			message.ErrorMessage = &errMsg
			stream.Push(&ai.EventError{Reason: ai.StopError, Error: message})
		}()
		return stream
	}
	agent := NewAgent(AgentOptions{StreamFn: failingStream})
	var types []string
	agent.Subscribe(func(event AgentEvent, _ *abort.Signal) { types = append(types, event.EventType()) })
	if err := agent.Prompt(testUser("q")); err != nil {
		t.Fatal(err)
	}
	// The provider error is encoded in the stream; the loop still completes
	// its normal event sequence.
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, "agent_end") || !strings.Contains(joined, "turn_end") {
		t.Fatalf("types = %v", types)
	}
	if msg, ok := agent.ErrorMessage(); !ok || msg != "provider exploded" {
		t.Fatalf("errorMessage = %q %v", msg, ok)
	}
	if agent.IsStreaming() {
		t.Fatal("still streaming")
	}
}

func TestAgentPromptWhileStreamingRejected(t *testing.T) {
	block := make(chan struct{})
	stream := func(_ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		s := ai.NewAssistantMessageEventStream()
		go func() {
			<-block
			s.Push(&ai.EventDone{Reason: ai.StopStop, Message: agentText("late")})
		}()
		return s
	}
	agent := NewAgent(AgentOptions{StreamFn: stream})
	done := make(chan error, 1)
	go func() { done <- agent.Prompt(testUser("a")) }()
	deadline := time.Now().Add(2 * time.Second)
	for !agent.IsStreaming() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := agent.Prompt(testUser("b")); err == nil {
		t.Fatal("second prompt accepted while streaming")
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// After completion a new prompt works.
	if err := agent.Prompt(testUser("c")); err != nil {
		t.Fatal(err)
	}
}

func TestAgentSteeringQueue(t *testing.T) {
	responses := []*ai.AssistantMessage{agentText("first"), agentText("second")}
	index := 0
	stream := func(_ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		s := ai.NewAssistantMessageEventStream()
		message := responses[index]
		index++
		go func() { s.Push(&ai.EventDone{Reason: ai.StopStop, Message: message}) }()
		return s
	}
	agent := NewAgent(AgentOptions{StreamFn: stream})
	// Queue steering before the run: injected at the first drain point.
	agent.Steer(testUser("steer 1"))
	if !agent.HasQueuedMessages() {
		t.Fatal("queue empty")
	}
	if peeked := agent.PeekQueuedMessages(); len(peeked) != 1 {
		t.Fatalf("peek = %d", len(peeked))
	}
	if err := agent.Prompt(testUser("go")); err != nil {
		t.Fatal(err)
	}
	if agent.HasQueuedMessages() {
		t.Fatal("queue not drained")
	}
	messages := agent.Messages()
	found := false
	for _, m := range messages {
		if u, ok := m.(*ai.UserMessage); ok && ai.ContentText(u.Content, "\n") == "steer 1" {
			found = true
		}
	}
	if !found {
		t.Fatal("steering message not injected")
	}
}

func TestAgentAbortController(t *testing.T) {
	block := make(chan struct{})
	stream := func(_ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		s := ai.NewAssistantMessageEventStream()
		go func() {
			<-block
			s.Push(&ai.EventDone{Reason: ai.StopStop, Message: agentText("late")})
		}()
		return s
	}
	agent := NewAgent(AgentOptions{StreamFn: stream})
	done := make(chan error, 1)
	go func() { done <- agent.Prompt(testUser("a")) }()
	deadline := time.Now().Add(2 * time.Second)
	for (agent.Signal() == nil || !agent.IsStreaming()) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if agent.Signal() == nil {
		t.Fatal("no signal")
	}
	agent.Abort()
	close(block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if agent.IsStreaming() {
		t.Fatal("still streaming after abort")
	}
}

func TestAgentResetRetainsBaseline(t *testing.T) {
	prompt := "baseline prompt"
	agent := NewAgent(AgentOptions{
		StreamFn:     agentTestStreamFn(agentText("x")),
		InitialState: &AgentInitialState{SystemPrompt: &prompt, Tools: []*AgentTool{emptyObjectTool("read")}},
	})
	if err := agent.Prompt(testUser("hello")); err != nil {
		t.Fatal(err)
	}
	if len(agent.Messages()) != 3 { // system + user + assistant
		t.Fatalf("messages = %d", len(agent.Messages()))
	}
	if err := agent.Reset(); err != nil {
		t.Fatal(err)
	}
	messages := agent.Messages()
	if len(messages) != 1 {
		t.Fatalf("after reset messages = %d", len(messages))
	}
	system, ok := messages[0].(*ai.SystemMessage)
	if !ok {
		t.Fatalf("messages[0] = %T", messages[0])
	}
	// The replayed baseline keeps the prompt and the tool.
	if ai.GetSystemMessageText(system) != "baseline prompt" {
		t.Fatalf("prompt = %q", ai.GetSystemMessageText(system))
	}
	if len(system.ToolsAdded) != 1 || system.ToolsAdded[0].Name != "read" {
		t.Fatalf("tools = %+v", system.ToolsAdded)
	}
}

func TestAgentSettersCopyArrays(t *testing.T) {
	agent := NewAgent(AgentOptions{StreamFn: agentTestStreamFn(agentText("x"))})
	tools := []*AgentTool{emptyObjectTool("a")}
	agent.SetTools(tools)
	tools[0] = nil
	if got := agent.Tools(); got[0] == nil || len(got) != 1 {
		t.Fatal("tools not copied")
	}
	messages := []AgentMessage{testUser("m")}
	agent.SetMessages(messages)
	messages[0] = testUser("other")
	if got := agent.Messages(); got[0] == messages[0] || len(got) != 1 {
		t.Fatal("messages not copied")
	}
}

func TestAgentSignalToSubscribers(t *testing.T) {
	block := make(chan struct{})
	stream := func(_ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		s := ai.NewAssistantMessageEventStream()
		go func() {
			<-block
			s.Push(&ai.EventDone{Reason: ai.StopStop, Message: agentText("late")})
		}()
		return s
	}
	agent := NewAgent(AgentOptions{StreamFn: stream})
	var mu sync.Mutex
	var gotSignal *abort.Signal
	agent.Subscribe(func(event AgentEvent, signal *abort.Signal) {
		mu.Lock()
		gotSignal = signal
		mu.Unlock()
	})
	done := make(chan error, 1)
	go func() { done <- agent.Prompt(testUser("a")) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		has := gotSignal != nil
		mu.Unlock()
		if has || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotSignal == nil {
		t.Fatal("subscribers did not receive the run signal")
	}
}

func TestAgentContinueQueuedFollowUps(t *testing.T) {
	responses := []*ai.AssistantMessage{agentText("turn-1"), agentText("turn-2")}
	index := 0
	stream := func(_ *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		s := ai.NewAssistantMessageEventStream()
		message := responses[index]
		index++
		go func() { s.Push(&ai.EventDone{Reason: ai.StopStop, Message: message}) }()
		return s
	}
	agent := NewAgent(AgentOptions{StreamFn: stream})
	if err := agent.Prompt(testUser("start")); err != nil {
		t.Fatal(err)
	}
	if index != 1 {
		t.Fatalf("calls = %d", index)
	}
	// The transcript tail is the assistant; continue() with a queued
	// follow-up runs it as a new prompt.
	agent.FollowUp(testUser("later"))
	if err := agent.ContinueFrom(); err != nil {
		t.Fatal(err)
	}
	if index != 2 {
		t.Fatalf("calls after follow-up = %d", index)
	}
	found := false
	for _, m := range agent.Messages() {
		if u, ok := m.(*ai.UserMessage); ok && ai.ContentText(u.Content, "\n") == "later" {
			found = true
		}
	}
	if !found {
		t.Fatal("follow-up message missing")
	}
	// Continuing again without a queue fails (assistant tail, no queue).
	if err := agent.ContinueFrom(); err == nil {
		t.Fatal("assistant tail accepted")
	}
}
