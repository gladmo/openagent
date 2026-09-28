// trajectory/export_test.go: the stdout tap's bounds, the pipe, and the
// Markdown / JSON / JSONL exporters.
package trajectory

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func TestBoundRecordLineCapsStringsAndLines(t *testing.T) {
	rec := &Record{Type: KindToolResult, Seq: 1, Turn: 1, Step: 1,
		Data: jsonx.ObjFrom("payload", strings.Repeat("x", 20*1024))}

	line := BoundRecordLine(rec, 1024, 32*1024)
	if len(line)+1 > 32*1024 {
		t.Fatalf("line over budget: %d", len(line)+1)
	}
	if !strings.Contains(line, "…") {
		t.Fatal("string cut not marked")
	}

	tiny := BoundRecordLine(rec, 8, 64)
	obj, err := RecordFromJSON(jsonx.MustParseString(tiny))
	if err != nil {
		// The degraded line drops the data object; the envelope must
		// still parse or degrade to {type,truncated}.
		if !strings.Contains(tiny, `"truncated":true`) {
			t.Fatalf("degraded line: %s", tiny)
		}
		return
	}
	if obj.Type != KindToolResult {
		t.Fatalf("degraded line lost type: %s", tiny)
	}
}

func TestStdoutSinkAndPipe(t *testing.T) {
	traj := New(Options{ID: "tap", Now: fixedClock(1)})
	var buf bytes.Buffer
	stop := PipeSubscribe(traj, NewStdoutSink(&buf))
	defer stop()

	if _, err := traj.Append(Record{Type: KindTurnStart, Turn: 1, Data: jsonx.ObjFrom("turn", 1.0)}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout lines = %d", len(lines))
	}
	if !strings.Contains(lines[0], `"turn/start"`) {
		t.Fatalf("stdout line = %s", lines[0])
	}
}

func sampleExportRecords(t *testing.T) []Record {
	t.Helper()
	session := scriptedSession(t, []ai.FauxResponseStep{
		ai.FauxStep(ai.FauxAssistantMessage([]ai.ContentBlock{
			ai.FauxText("Checking."),
			ai.FauxToolCall("clock_time", jsonx.NewObj()),
		})),
		ai.FauxStep(ai.FauxAssistantMessage("It is 12:00.")),
	}, clockTimeTool())
	traj := New(Options{ID: "export", Now: fixedClock(1000)})
	_, dispose, err := Attach(session, RecorderOptions{Trajectory: traj})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.PromptText("time?"); err != nil {
		t.Fatal(err)
	}
	dispose()
	return traj.Snapshot()
}

func TestExportMarkdown(t *testing.T) {
	records := sampleExportRecords(t)
	var buf bytes.Buffer
	if err := ExportMarkdown(&buf, FileHeader{ID: "export", CreatedAt: 1, Provider: "test-prov", Model: "faux"}, records); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, needle := range []string{
		"# Trajectory export",
		"- model: test-prov/faux",
		"## Turn 1",
		"### Step 1",
		"**user** (prompt): time?",
		"**tool** `clock_time`",
		"result [ok]",
		"It is 12:00.",
		"*turn end*: completed",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("markdown missing %q:\n%s", needle, out)
		}
	}
}

func TestExportJSONAndJSONL(t *testing.T) {
	records := sampleExportRecords(t)
	var jsonBuf bytes.Buffer
	if err := ExportJSON(&jsonBuf, FileHeader{ID: "export", CreatedAt: 1}, records); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonBuf.String(), `"records"`) || !strings.Contains(jsonBuf.String(), `"turn/start"`) {
		t.Fatalf("json export malformed")
	}

	var jsonlBuf bytes.Buffer
	if err := ExportJSONL(&jsonlBuf, FileHeader{ID: "export", CreatedAt: 1}, records); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(jsonlBuf.String()), "\n")
	if len(lines) != len(records)+1 {
		t.Fatalf("jsonl lines = %d, records = %d", len(lines), len(records))
	}
	if !strings.Contains(lines[0], `"kind":"trajectory"`) {
		t.Fatalf("jsonl header = %s", lines[0])
	}
}
