// trajectory/replay_test.go: record → derive script → replay → compare, the
// override sidecar, and replay:never tool refusal.
package trajectory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

// echoTool echoes its "text" argument — deterministic across runs.
func echoTool() *agent.AgentTool {
	return &agent.AgentTool{
		Name:        "echo",
		Description: "Echoes text.",
		Execute: func(_ string, params any, _ *abort.Signal, _ agent.AgentToolUpdateCallback) (*agent.AgentToolResult, error) {
			text := "silence"
			if obj, ok := params.(*jsonx.Obj); ok {
				if v, ok := obj.Get("text"); ok {
					if s, ok := v.(string); ok {
						text = s
					}
				}
			}
			return &agent.AgentToolResult{Content: []ai.ContentBlock{ai.TextContent{Text: text}}}, nil
		},
	}
}

// recordedRun produces a golden trajectory from a scripted session.
func recordedRun(t *testing.T, id string) *Trajectory {
	t.Helper()
	toolUse := ai.StopToolUse
	session := scriptedSession(t, []ai.FauxResponseStep{
		ai.FauxStep(ai.FauxAssistantMessage([]ai.ContentBlock{
			ai.FauxText("Echoing."),
			ai.FauxToolCall("echo", jsonx.ObjFrom("text", "hello trajectory")),
		}, ai.FauxAssistantMessageOptions{StopReason: &toolUse})),
		ai.FauxStep(ai.FauxAssistantMessage([]ai.ContentBlock{ai.FauxText("echoed: hello trajectory")})),
	}, echoTool())
	traj := New(Options{ID: id, Now: fixedClock(5000)})
	_, dispose, err := Attach(session, RecorderOptions{Trajectory: traj})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PromptText("echo hello trajectory"); err != nil {
		t.Fatal(err)
	}
	dispose()
	return traj
}

func TestReplayReproducesGoldenRun(t *testing.T) {
	golden := recordedRun(t, "golden")
	script, err := DeriveScript(golden)
	if err != nil {
		t.Fatal(err)
	}
	if len(script) != 2 {
		t.Fatalf("script steps = %d", len(script))
	}

	replay, _, err := BuildReplayAgent(script, ReplayOptions{Tools: []*agent.AgentTool{echoTool()}, SystemPrompt: "You are a test agent."})
	if err != nil {
		t.Fatal(err)
	}
	live := New(Options{ID: "live", Now: fixedClock(9000)})
	_, dispose, err := Attach(replay, RecorderOptions{Trajectory: live})
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.PromptText("echo hello trajectory"); err != nil {
		t.Fatal(err)
	}
	dispose()

	if err := CompareRecords(golden.Snapshot(), live.Snapshot()); err != nil {
		t.Fatalf("replay diverged: %v", err)
	}

	// The transcript itself round-trips: same final assistant text.
	goldenFinal := lastAssistantText(golden)
	liveFinal := lastAssistantText(live)
	if goldenFinal != liveFinal || !strings.Contains(liveFinal, "echoed: hello trajectory") {
		t.Fatalf("final text = %q vs %q", goldenFinal, liveFinal)
	}
}

func lastAssistantText(traj *Trajectory) string {
	var text string
	for _, message := range Transcript(traj.Snapshot()) {
		if assistant, ok := message.(*ai.AssistantMessage); ok {
			var out string
			for _, block := range assistant.Content {
				if t, ok := block.(ai.TextContent); ok {
					out += t.Text
				}
			}
			text = out
		}
	}
	return text
}

func TestReplayOverrideInjectsFailure(t *testing.T) {
	golden := recordedRun(t, "override")
	script, err := DeriveScript(golden)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	overridePath := filepath.Join(dir, "replay.override.json")
	// Replace the first call with a thrown error; the loop surfaces it as
	// an error message, and the second step still replays.
	if err := os.WriteFile(overridePath, []byte(`[{"at":0,"kind":"throw","message":"injected 500","code":"E500"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	patches, err := LoadReplayOverride(overridePath)
	if err != nil {
		t.Fatal(err)
	}
	patched, err := ApplyOverride(script, patches)
	if err != nil {
		t.Fatal(err)
	}
	if len(patched) != len(script) {
		t.Fatalf("patch changed script length")
	}

	replay, _, err := BuildReplayAgent(patched, ReplayOptions{Tools: []*agent.AgentTool{echoTool()}})
	if err != nil {
		t.Fatal(err)
	}
	live := New(Options{ID: "live-throw", Now: fixedClock(1)})
	_, dispose, err := Attach(replay, RecorderOptions{Trajectory: live})
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.PromptText("echo hello trajectory"); err != nil {
		t.Fatal(err)
	}
	dispose()

	byType := map[string]*Record{}
	for i, rec := range live.Snapshot() {
		byType[rec.Type] = &live.Snapshot()[i]
	}
	if attempt, ok := byType[KindAssistantAttempt]; !ok || !strings.Contains(jsonx.Stringify(attempt.Data), "injected 500") {
		t.Fatalf("injected failure not recorded as attempt: %v", recordTypes(live.Snapshot()))
	}
	if stringField(byType[KindTurnEnd].Data, "reason") != "error" {
		t.Fatalf("turn ended %q", stringField(byType[KindTurnEnd].Data, "reason"))
	}
}

func TestApplyOverrideBounds(t *testing.T) {
	script, err := DeriveScript(recordedRun(t, "bounds"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyOverride(script, []ReplayPatch{{At: 9, Kind: "throw"}}); err == nil {
		t.Fatal("out-of-range patch accepted")
	}
	if _, err := ApplyOverride(script, []ReplayPatch{{At: 0, Kind: "bogus"}}); err == nil {
		t.Fatal("unknown patch kind accepted")
	}
}

func TestReplayRefusesNeverTools(t *testing.T) {
	never := "never"
	toolUse := ai.StopToolUse
	dangerous := &agent.AgentTool{
		Name:   "danger",
		Replay: &never,
		Execute: func(string, any, *abort.Signal, agent.AgentToolUpdateCallback) (*agent.AgentToolResult, error) {
			return &agent.AgentToolResult{Content: []ai.ContentBlock{ai.TextContent{Text: "side effect"}}}, nil
		},
	}
	session := scriptedSession(t, []ai.FauxResponseStep{
		ai.FauxStep(ai.FauxAssistantMessage([]ai.ContentBlock{
			ai.FauxToolCall("danger", jsonx.NewObj()),
		}, ai.FauxAssistantMessageOptions{StopReason: &toolUse})),
		ai.FauxStep(ai.FauxAssistantMessage("done")),
	}, dangerous)
	traj := New(Options{ID: "never", Now: fixedClock(1)})
	_, dispose, err := Attach(session, RecorderOptions{Trajectory: traj})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PromptText("run danger"); err != nil {
		t.Fatal(err)
	}
	dispose()

	script, err := DeriveScript(traj)
	if err != nil {
		t.Fatal(err)
	}
	replay, faux, err := BuildReplayAgent(script, ReplayOptions{Tools: []*agent.AgentTool{dangerous}})
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.PromptText("run danger"); err != nil {
		t.Fatal(err)
	}
	if faux.GetPendingResponseCount() != 0 {
		t.Fatalf("script not fully consumed: %d left", faux.GetPendingResponseCount())
	}
	// The refused execution surfaces as an error tool result.
	refused := false
	for _, message := range replay.Messages() {
		if result, ok := message.(*ai.ToolResultMessage); ok && result.ToolName == "danger" && result.IsError {
			if strings.Contains(ai.ContentText(ai.BlocksContent(result.Content...), ""), "replay refused") {
				refused = true
			}
		}
	}
	if !refused {
		t.Fatal("replay:never tool executed instead of refused")
	}
}
