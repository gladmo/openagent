// trajectory/record_test.go: envelope codec strictness and the timed
// stream accumulator's compaction contracts.
package trajectory

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func TestRecordJSONRoundTrip(t *testing.T) {
	rec := &Record{Type: KindToolCall, Seq: 7, TimeMs: 1727000000000, Turn: 2, Step: 1,
		Data: jsonx.ObjFrom("callId", "c1", "name", "read")}
	line := rec.String()
	if strings.Contains(line, `"ignorable"`) || strings.Contains(line, `"step":0`) {
		t.Fatalf("empty fields not omitted: %s", line)
	}
	back, err := RecordFromJSON(jsonx.MustParseString(line))
	if err != nil {
		t.Fatal(err)
	}
	if back.Type != rec.Type || back.Seq != rec.Seq || back.Turn != rec.Turn || back.Step != rec.Step {
		t.Fatalf("round trip mismatch: %+v", back)
	}
	if !jsonx.Equal(back.Data, rec.Data) {
		t.Fatalf("data mismatch: %s", jsonx.Stringify(back.Data))
	}
}

func TestRecordFromJSONRejectsUnknownKeysAndBadShapes(t *testing.T) {
	if _, err := RecordFromJSON(jsonx.MustParseString(`{"type":"error","seq":1,"time":2,"turn":0,"data":{},"extra":1}`)); err == nil {
		t.Fatal("unknown key accepted")
	}
	if _, err := RecordFromJSON(jsonx.MustParseString(`{"type":"error","seq":1,"time":2,"turn":0}`)); err == nil {
		t.Fatal("missing data accepted")
	}
	if _, err := RecordFromJSON(jsonx.MustParseString(`{"type":"error","seq":1.5,"time":2,"turn":0,"data":{}}`)); err == nil {
		t.Fatal("fractional seq accepted")
	}
	if _, err := RecordFromJSON(jsonx.MustParseString(`{"seq":1,"time":2,"turn":0,"data":{}}`)); err == nil {
		t.Fatal("missing type accepted")
	}
	if _, err := RecordFromJSON(jsonx.MustParseString(`{"type":"","seq":1,"time":2,"turn":0,"data":{}}`)); err == nil {
		t.Fatal("empty type accepted")
	}
}

func TestStreamAccumulatorPacksRunsAndKeepsArrivalOrder(t *testing.T) {
	acc := &StreamAccumulator{}
	acc.Push(100, &ai.EventStart{Partial: &ai.AssistantMessage{}})
	acc.Push(101, &ai.EventTextStart{ContentIndex: 0})
	acc.Push(102, &ai.EventTextDelta{ContentIndex: 0, Delta: "Hel"})
	acc.Push(104, &ai.EventTextDelta{ContentIndex: 0, Delta: "lo"})
	acc.Push(107, &ai.EventTextEnd{ContentIndex: 0})
	acc.Push(108, &ai.EventThinkingStart{ContentIndex: 1})
	acc.Push(109, &ai.EventThinkingDelta{ContentIndex: 1, Delta: "hmm"})
	acc.Push(112, &ai.EventTextStart{ContentIndex: 2}) // raw splits runs
	acc.Push(113, &ai.EventTextDelta{ContentIndex: 2, Delta: "!"})
	acc.Push(115, &ai.EventDone{Reason: ai.StopStop})

	records := acc.Snapshot()
	if len(records) != 9 {
		t.Fatalf("record count = %d: %s", len(records), jsonx.Stringify(records))
	}
	kinds := make([]string, 0, len(records))
	for _, r := range records {
		kinds = append(kinds, r.(*jsonx.Obj).MustGet("type").(string))
	}
	want := "chunk,chunk,text-chunks,chunk,chunk,thinking-chunks,chunk,text-chunks,chunk"
	if strings.Join(kinds, ",") != want {
		t.Fatalf("order = %v, want %v", kinds, strings.Split(want, ","))
	}
	run := records[2].(*jsonx.Obj)
	if run.MustGet("time0").(float64) != 102 {
		t.Fatalf("time0 = %v", run.MustGet("time0"))
	}
	dt, _ := run.Get("dt")
	if jsonx.Stringify(dt) != "[2]" {
		t.Fatalf("dt = %s", jsonx.Stringify(dt))
	}
	texts, _ := run.Get("texts")
	if jsonx.Stringify(texts) != `["Hel","lo"]` {
		t.Fatalf("texts = %s", jsonx.Stringify(texts))
	}
	if acc.Count() != 10 {
		t.Fatalf("count = %d", acc.Count())
	}
	first, ok := acc.FirstTime()
	if !ok || first != 100 {
		t.Fatalf("first time = %v %v", first, ok)
	}
}

func TestStreamAccumulatorStampsToolCallIdentity(t *testing.T) {
	acc := &StreamAccumulator{}
	acc.Push(10, &ai.EventToolCallStart{ContentIndex: 0})
	acc.Push(11, &ai.EventToolCallDelta{ContentIndex: 0, Delta: `{"p`})
	acc.Push(13, &ai.EventToolCallDelta{ContentIndex: 0, Delta: `ath":"x"}`})
	acc.Push(14, &ai.EventToolCallEnd{ContentIndex: 0, ToolCall: &ai.ToolCall{ID: "c9", Name: "read"}})
	records := acc.Snapshot()
	run := records[1].(*jsonx.Obj)
	if run.MustGet("id") != "c9" || run.MustGet("name") != "read" {
		t.Fatalf("identity not stamped: %s", jsonx.Stringify(run))
	}
	var args string
	for _, d := range ExpandStreamRecords(records) {
		if d.Kind == streamToolCallChunks {
			args += d.Text
		}
	}
	if args != `{"path":"x"}` {
		t.Fatalf("expanded args = %q", args)
	}
}

func TestExpandStreamRecords(t *testing.T) {
	acc := &StreamAccumulator{}
	acc.Push(1, &ai.EventTextDelta{ContentIndex: 0, Delta: "a"})
	acc.Push(2, &ai.EventTextDelta{ContentIndex: 0, Delta: "b"})
	acc.Push(3, &ai.EventThinkingDelta{ContentIndex: 1, Delta: "t"})
	acc.Push(4, &ai.EventDone{Reason: ai.StopStop})
	deltas := ExpandStreamRecords(acc.Snapshot())
	var text string
	for _, d := range deltas {
		if d.Kind == streamTextChunks {
			text += d.Text
		}
	}
	if text != "ab" || len(deltas) != 3 {
		t.Fatalf("expanded = %+v", deltas)
	}
}
