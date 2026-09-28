// trajectory/recorder_test.go: the collector against a real agent loop —
// scripted with the faux provider so the whole flow runs without network.
// Pins the record sequence, payload shapes, error/abort paths, image
// capping, and retry-callback capture.
package trajectory

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// clockTimeTool is a deterministic, side-effect-free tool.
func clockTimeTool() *agent.AgentTool {
	return &agent.AgentTool{
		Name:        "clock_time",
		Description: "Returns the current time string.",
		Execute: func(string, any, *abort.Signal, agent.AgentToolUpdateCallback) (*agent.AgentToolResult, error) {
			return &agent.AgentToolResult{Content: []ai.ContentBlock{ai.TextContent{Text: "12:00"}}}, nil
		},
	}
}

// scriptedSession builds an agent over a faux provider with the given
// response script.
func scriptedSession(t *testing.T, responses []ai.FauxResponseStep, tools ...*agent.AgentTool) *agent.Agent {
	t.Helper()
	faux := ai.FauxProvider(ai.RegisterFauxProviderOptions{API: "test-api", Provider: "test-prov"})
	faux.SetResponses(responses)
	models := ai.CreateModels()
	models.SetProvider(faux.Provider)
	prompt := "You are a test agent."
	thinking := agent.ThinkingOff
	return agent.NewAgent(agent.AgentOptions{
		InitialState: &agent.AgentInitialState{
			SystemPrompt:  &prompt,
			Model:         faux.GetModel(),
			ThinkingLevel: &thinking,
			Tools:         tools,
		},
		StreamFn: func(m *ai.Model, ctx *ai.TranscriptContext, opts *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
			return models.StreamSimple(m, ai.Context{Messages: ctx.Messages}, opts)
		},
	})
}

func recordTypes(records []Record) []string {
	types := make([]string, 0, len(records))
	for i := range records {
		types = append(types, records[i].Type)
	}
	return types
}

func TestRecorderFullToolFlow(t *testing.T) {
	toolUse := ai.StopToolUse
	session := scriptedSession(t, []ai.FauxResponseStep{
		ai.FauxStep(ai.FauxAssistantMessage([]ai.ContentBlock{
			ai.FauxText("Let me check the time."),
			ai.FauxToolCall("clock_time", jsonx.NewObj()),
		}, ai.FauxAssistantMessageOptions{StopReason: &toolUse})),
		ai.FauxStep(ai.FauxAssistantMessage([]ai.ContentBlock{ai.FauxText("It is 12:00.")})),
	}, clockTimeTool())

	traj := New(Options{ID: "flow", Now: fixedClock(1000)})
	recorder, dispose, err := Attach(session, RecorderOptions{Trajectory: traj})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PromptText("What time is it?"); err != nil {
		t.Fatal(err)
	}
	dispose()

	records := traj.Snapshot()
	got := strings.Join(recordTypes(records), ",")
	want := strings.Join([]string{
		KindSessionStart,
		KindTurnStart, KindUserMessage,
		KindStepStart, KindRequestHeader, KindModelInput, KindAssistantMessage,
		KindToolCall, KindToolResult, KindStepEnd, KindTurnEnd,
		KindTurnStart,
		KindStepStart, KindRequestHeader, KindModelInput, KindAssistantMessage,
		KindStepEnd, KindTurnEnd,
		KindSessionEnd,
	}, ",")
	if got != want {
		t.Fatalf("record sequence:\n got %s\nwant %s", got, want)
	}

	byType := map[string]*Record{}
	for i := range records {
		byType[records[i].Type] = &records[i]
	}

	// session/start describes the agent.
	start := byType[KindSessionStart]
	if start.Turn != 0 || start.Step != 0 {
		t.Fatalf("session/start scoped to turn %d step %d", start.Turn, start.Step)
	}
	if !strings.Contains(jsonx.Stringify(start.Data), `"test-prov"`) {
		t.Fatalf("session/start missing model: %s", jsonx.Stringify(start.Data))
	}
	if !strings.Contains(jsonx.Stringify(start.Data), "clock_time") {
		t.Fatalf("session/start missing tools: %s", jsonx.Stringify(start.Data))
	}

	// First user message is the prompt; the recorded message round-trips.
	user := byType[KindUserMessage]
	if user.Turn != 1 || user.Step != 0 {
		t.Fatalf("user/message scoped to turn %d step %d", user.Turn, user.Step)
	}
	if stringField(user.Data, "source") != "prompt" {
		t.Fatalf("first message source = %q", stringField(user.Data, "source"))
	}
	transcript := Transcript(records)
	if len(transcript) < 3 {
		t.Fatalf("transcript rebuilt %d messages", len(transcript))
	}
	if u, ok := transcript[0].(*ai.UserMessage); !ok || !strings.Contains(ai.ContentText(u.Content, ""), "What time is it?") {
		t.Fatalf("transcript[0] = %#v", transcript[0])
	}

	// Model input captures the normalized request, system first.
	input := byType[KindModelInput]
	messages, _ := input.Data.(*jsonx.Obj).Get("messages")
	inputArr, ok := messages.([]any)
	if !ok || len(inputArr) < 2 {
		t.Fatalf("model/input messages = %s", jsonx.Stringify(messages))
	}
	firstMessage := inputArr[0].(*jsonx.Obj)
	if firstMessage.MustGet("role") != "system" {
		t.Fatalf("model/input does not start with system: %s", jsonx.Stringify(firstMessage))
	}

	// The assistant messages keep their stop reasons: the tool-calling
	// first step ends toolUse, the final step ends stop.
	var assistantRecords []*Record
	for i := range records {
		if records[i].Type == KindAssistantMessage {
			assistantRecords = append(assistantRecords, &records[i])
		}
	}
	if len(assistantRecords) != 2 {
		t.Fatalf("assistant messages = %d", len(assistantRecords))
	}
	if stringField(assistantRecords[0].Data, "stopReason") != "toolUse" {
		t.Fatalf("first assistant stopReason = %q", stringField(assistantRecords[0].Data, "stopReason"))
	}
	if stringField(assistantRecords[1].Data, "stopReason") != "stop" {
		t.Fatalf("final assistant stopReason = %q", stringField(assistantRecords[1].Data, "stopReason"))
	}
	assistant := assistantRecords[0]
	if assistant.Turn == 0 || assistant.Step == 0 {
		t.Fatalf("assistant/message scoped to turn %d step %d", assistant.Turn, assistant.Step)
	}
	streamValue, hasStream := assistant.Data.(*jsonx.Obj).Get("stream")
	if !hasStream {
		t.Fatalf("assistant/message has no stream")
	}
	var text string
	for _, d := range ExpandStreamRecords(streamValue.([]any)) {
		if d.Kind == streamTextChunks {
			text += d.Text
		}
	}
	if !strings.Contains(text, "Let me check the time.") {
		t.Fatalf("stream text = %q", text)
	}

	// Tool call and result pair up with durations.
	call := byType[KindToolCall]
	if stringField(call.Data, "name") != "clock_time" {
		t.Fatalf("tool/call name = %q", stringField(call.Data, "name"))
	}
	result := byType[KindToolResult]
	if boolField(result.Data, "isError") {
		t.Fatal("tool/result flagged error")
	}
	if !strings.Contains(toolResultPreview(result, 100), "12:00") {
		t.Fatalf("tool/result content missing: %s", jsonx.Stringify(result.Data))
	}
	if obj, ok := result.Data.(*jsonx.Obj); ok && durationOf(obj) < 0 {
		t.Fatal("tool/result duration negative")
	}

	// Step end carries accumulated usage and the turn ended completed.
	stepEnd := byType[KindStepEnd]
	if _, ok := usageFromRecord(stepEnd); !ok {
		t.Fatalf("step/end has no usage: %s", jsonx.Stringify(stepEnd.Data))
	}
	if stringField(byType[KindTurnEnd].Data, "reason") != "completed" {
		t.Fatalf("turn/end reason = %q", stringField(byType[KindTurnEnd].Data, "reason"))
	}

	// session/end closes the log with totals.
	end := byType[KindSessionEnd]
	if intField(end.Data, "turns") != 2 {
		t.Fatalf("session/end turns = %d", intField(end.Data, "turns"))
	}
	if recorder == nil {
		t.Fatal("recorder missing")
	}
}

func TestRecorderErrorPathRecordsAttemptAndReason(t *testing.T) {
	session := scriptedSession(t, nil)
	errorMessage := "provider exploded"
	stop := ai.StopError
	session.StreamFunction = func(m *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		stream.Push(&ai.EventStart{Partial: &ai.AssistantMessage{}})
		message := ai.FauxAssistantMessage("", ai.FauxAssistantMessageOptions{StopReason: &stop, ErrorMessage: &errorMessage})
		message.API, message.Provider, message.Model = m.API, m.Provider, m.ID
		stream.Push(&ai.EventError{Reason: "error", Error: message})
		return stream
	}

	traj := New(Options{ID: "err", Now: fixedClock(1)})
	_, dispose, err := Attach(session, RecorderOptions{Trajectory: traj})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PromptText("boom"); err != nil {
		t.Fatal(err)
	}
	dispose()

	records := traj.Snapshot()
	byType := map[string]*Record{}
	for i := range records {
		byType[records[i].Type] = &records[i]
	}
	attempt, ok := byType[KindAssistantAttempt]
	if !ok {
		t.Fatalf("no assistant/attempt record: %v", recordTypes(records))
	}
	if attempt.Turn != 1 || attempt.Step != 1 {
		t.Fatalf("attempt scoped to turn %d step %d", attempt.Turn, attempt.Step)
	}
	if !strings.Contains(jsonx.Stringify(attempt.Data), "provider exploded") {
		t.Fatalf("attempt missing failure: %s", jsonx.Stringify(attempt.Data))
	}
	assistant, ok := byType[KindAssistantMessage]
	if !ok || stringField(assistant.Data, "stopReason") != "error" {
		t.Fatalf("error message not recorded: %v", recordTypes(records))
	}
	if stringField(byType[KindTurnEnd].Data, "reason") != "error" {
		t.Fatalf("turn/end reason = %q", stringField(byType[KindTurnEnd].Data, "reason"))
	}
}

func TestRecorderAbortMarksInterrupted(t *testing.T) {
	session := scriptedSession(t, nil)
	controller := abort.NewController()
	session.StreamFunction = func(m *ai.Model, _ *ai.TranscriptContext, _ *ai.SimpleStreamOptions) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		stream.Push(&ai.EventStart{Partial: &ai.AssistantMessage{}})
		<-controller.Signal().Done()
		aborted := ai.StopAborted
		message := ai.FauxAssistantMessage("partial answer", ai.FauxAssistantMessageOptions{StopReason: &aborted})
		message.API, message.Provider, message.Model = m.API, m.Provider, m.ID
		stream.Push(&ai.EventError{Reason: "aborted", Error: message})
		return stream
	}

	traj := New(Options{ID: "abort", Now: fixedClock(1)})
	_, dispose, err := Attach(session, RecorderOptions{Trajectory: traj})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		controller.Abort()
	}()
	if err := session.PromptText("slow"); err != nil {
		t.Fatal(err)
	}
	dispose()

	byType := map[string]*Record{}
	for _, rec := range traj.Snapshot() {
		byType[rec.Type] = &rec
	}
	assistant := byType[KindAssistantMessage]
	if obj, ok := assistant.Data.(*jsonx.Obj); ok {
		if interrupted, _ := obj.Get("interrupted"); interrupted != true {
			t.Fatalf("aborted message not marked interrupted: %s", jsonx.Stringify(obj))
		}
	} else {
		t.Fatal("assistant/message missing")
	}
	if stringField(byType[KindTurnEnd].Data, "reason") != "aborted" {
		t.Fatalf("turn/end reason = %q", stringField(byType[KindTurnEnd].Data, "reason"))
	}
}

func TestRecorderCapsLargeImages(t *testing.T) {
	session := scriptedSession(t, []ai.FauxResponseStep{
		ai.FauxStep(ai.FauxAssistantMessage("got it")),
	})
	bigImage := ai.ImageContent{Data: strings.Repeat("A", DefaultMaxInlineImageBytes+10), MimeType: "image/png"}

	traj := New(Options{ID: "img", Now: fixedClock(1)})
	_, dispose, err := Attach(session, RecorderOptions{Trajectory: traj, MaxInlineImageBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PromptText("look", bigImage); err != nil {
		t.Fatal(err)
	}
	dispose()

	var user *Record
	for i, rec := range traj.Snapshot() {
		if rec.Type == KindUserMessage {
			user = &traj.Snapshot()[i]
		}
	}
	if user == nil {
		t.Fatal("user/message missing")
	}
	payload := jsonx.Stringify(user.Data)
	if !strings.Contains(payload, `"dataOmitted":true`) || !strings.Contains(payload, `"dataBytes":`) {
		t.Fatalf("large image not capped: %s", payload[:min(len(payload), 400)])
	}
	if strings.Contains(payload, strings.Repeat("A", 2048)) {
		t.Fatal("image data leaked past the cap")
	}
}

func TestRecorderRetryCallbacksRecordChain(t *testing.T) {
	session := scriptedSession(t, nil)
	traj := New(Options{ID: "retry", Now: fixedClock(1)})
	recorder, dispose, err := Attach(session, RecorderOptions{Trajectory: traj})
	if err != nil {
		t.Fatal(err)
	}
	callbacks := recorder.RetryCallbacks()

	// A prompt runs (the empty faux script errors out immediately), leaving
	// the turn counters at 1; the retry chain attaches to that turn.
	if err := session.PromptText("retry me"); err != nil {
		t.Fatal(err)
	}
	callbacks.OnRetryScheduled(1, 3, 250, "transient 503")
	callbacks.OnRetryAttemptStart()
	dispose()

	records := traj.Snapshot()
	var retry, started *Record
	for i := range records {
		switch records[i].Type {
		case KindLlmRetry:
			retry = &records[i]
		case KindLlmRetryStarted:
			started = &records[i]
		}
	}
	if retry == nil || started == nil {
		t.Fatalf("retry records missing: %v", recordTypes(records))
	}
	if intField(retry.Data, "retry") != 1 || intField(retry.Data, "maxRetries") != 3 {
		t.Fatalf("retry payload = %s", jsonx.Stringify(retry.Data))
	}
	if !strings.Contains(jsonx.Stringify(retry.Data), "transient 503") {
		t.Fatalf("retry failure missing: %s", jsonx.Stringify(retry.Data))
	}
	if stringField(started.Data, "retryId") != stringField(retry.Data, "retryId") {
		t.Fatalf("retry-started does not pair: %s vs %s",
			jsonx.Stringify(started.Data), jsonx.Stringify(retry.Data))
	}
}

func TestRecorderContinuesTurnNumberingAcrossRuns(t *testing.T) {
	session := scriptedSession(t, []ai.FauxResponseStep{
		ai.FauxStep(ai.FauxAssistantMessage("one")),
		ai.FauxStep(ai.FauxAssistantMessage("two")),
	})
	traj := New(Options{ID: "runs", Now: fixedClock(1)})
	_, dispose, err := Attach(session, RecorderOptions{Trajectory: traj})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PromptText("first"); err != nil {
		t.Fatal(err)
	}
	if err := session.PromptText("second"); err != nil {
		t.Fatal(err)
	}
	dispose()

	turns := 0
	maxTurn := 0
	for _, rec := range traj.Snapshot() {
		if rec.Type == KindTurnStart {
			turns++
			if rec.Turn > maxTurn {
				maxTurn = rec.Turn
			}
		}
	}
	if turns != 2 || maxTurn != 2 {
		t.Fatalf("turn numbering across runs: %d starts, max %d", turns, maxTurn)
	}
	// Second user message is an injection of a fresh prompt run… no: a new
	// Prompt run's first message is again the run's prompt.
	sources := []string{}
	for _, rec := range traj.Snapshot() {
		if rec.Type == KindUserMessage {
			sources = append(sources, stringField(rec.Data, "source"))
		}
	}
	if strings.Join(sources, ",") != "prompt,prompt" {
		t.Fatalf("prompt sources = %v", sources)
	}
}

func TestRecorderDisposeRestoresStreamFn(t *testing.T) {
	session := scriptedSession(t, []ai.FauxResponseStep{ai.FauxStep(ai.FauxAssistantMessage("x"))})
	original := session.StreamFunction
	traj := New(Options{ID: "restore", Now: fixedClock(1)})
	_, dispose, err := Attach(session, RecorderOptions{Trajectory: traj})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.ValueOf(session.StreamFunction).Pointer() == reflect.ValueOf(original).Pointer() {
		t.Fatal("StreamFn not wrapped")
	}
	dispose()
	if reflect.ValueOf(session.StreamFunction).Pointer() != reflect.ValueOf(original).Pointer() {
		t.Fatal("StreamFn not restored")
	}
	dispose() // idempotent
	if traj.Closed() != nil {
		t.Fatal("recorder dispose closed the trajectory")
	}
}
