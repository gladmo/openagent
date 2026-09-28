// trajectory_demo 自测:以库调用驱动主流程(不打印 stdout 流),验证
// 采集→存储→读回→回放→等价断言的完整闭环。
package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/trajectory"
)

// messagePreviewText extracts the text blocks of one message.
func messagePreviewText(message ai.Message) string {
	switch m := message.(type) {
	case *ai.AssistantMessage:
		var out string
		for _, block := range m.Content {
			if text, ok := block.(ai.TextContent); ok {
				out += text.Text
			}
		}
		return out
	case *ai.UserMessage:
		return ai.ContentText(m.Content, "")
	}
	return ""
}

func TestDemoPipelineEndToEnd(t *testing.T) {
	session := buildScriptedAgent()
	traj := trajectory.New(trajectory.Options{ID: "demo-test"})
	_, dispose, err := trajectory.Attach(session, trajectory.RecorderOptions{Trajectory: traj})
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "demo.trajectory.jsonl")
	sink, err := trajectory.NewFileSink(logPath, trajectory.FileHeader{ID: traj.ID(), Model: "faux-1", Provider: "demo-prov"})
	if err != nil {
		t.Fatal(err)
	}
	stop := trajectory.PipeSubscribeAfter(traj, sink, 0)

	if err := session.PromptText("把 hello trajectory 原样回显出来"); err != nil {
		t.Fatal(err)
	}
	dispose()
	stop()
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := trajectory.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Records) == 0 {
		t.Fatal("stored trajectory is empty")
	}
	if file.TornTail {
		t.Fatal("clean close left a torn tail")
	}
	summary := trajectory.UsageSummaryFrom(file.Records)
	if summary.Turns != 2 {
		t.Fatalf("turns = %d", summary.Turns)
	}

	script, err := trajectory.DeriveScriptFromRecords(file.Records)
	if err != nil {
		t.Fatal(err)
	}
	replay, _, err := trajectory.BuildReplayAgent(script, trajectory.ReplayOptions{
		Tools: []*agent.AgentTool{echoTool()}, SystemPrompt: demoPrompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	live := trajectory.New(trajectory.Options{ID: "demo-replay"})
	_, replayDispose, err := trajectory.Attach(replay, trajectory.RecorderOptions{Trajectory: live})
	if err != nil {
		t.Fatal(err)
	}
	if err := replay.PromptText("把 hello trajectory 原样回显出来"); err != nil {
		t.Fatal(err)
	}
	replayDispose()
	if err := trajectory.CompareRecords(file.Records, live.Snapshot()); err != nil {
		t.Fatalf("replay diverged: %v", err)
	}

	var finalText string
	for _, message := range trajectory.Transcript(live.Snapshot()) {
		if text := messagePreviewText(message); text != "" {
			finalText = text
		}
	}
	if !strings.Contains(finalText, "hello trajectory") {
		t.Fatalf("final replay text = %q", finalText)
	}
}
