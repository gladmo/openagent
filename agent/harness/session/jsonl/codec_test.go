package jsonl

// Ports of jsonl-io.test.ts and codec round-trip behaviors.

import (
	"strings"
	"testing"

	session "github.com/gladmo/openagent/agent/harness/session"
	"github.com/gladmo/openagent/jsonx"
)

func TestParseSessionHeaderV4(t *testing.T) {
	line := `{"v":4,"kind":"header","id":"s1","storageVersion":1,"createdAt":1000,"cwd":"/work","parentSessionId":"p1"}`
	parsed, err := ParseSessionHeader(line)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Format != "v4" || parsed.Header.ID != "s1" || parsed.Header.Cwd != "/work" {
		t.Fatalf("parsed = %+v", parsed)
	}
	if parsed.Header.ParentSessionID == nil || *parsed.Header.ParentSessionID != "p1" {
		t.Fatal("parentSessionId lost")
	}
	if parsed.Header.NextSeq != nil {
		t.Fatal("nextSeq present")
	}
	// nextSeq preserved.
	parsed, err = ParseSessionHeader(`{"v":4,"kind":"header","id":"s","storageVersion":1,"createdAt":0,"cwd":"","nextSeq":42}`)
	if err != nil || parsed.Header.NextSeq == nil || *parsed.Header.NextSeq != 42 {
		t.Fatalf("nextSeq = %+v err = %v", parsed.Header.NextSeq, err)
	}
}

func TestParseSessionHeaderV3Legacy(t *testing.T) {
	line := `{"type":"session","version":3,"id":"old","timestamp":"2024-01-01T00:00:00.000Z","cwd":"/old","parentSession":"/other.jsonl"}`
	parsed, err := ParseSessionHeader(line)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Format != "v3-legacy" || parsed.LegacyID != "old" || parsed.LegacyCwd != "/old" {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestParseSessionHeaderRejects(t *testing.T) {
	for _, line := range []string{`not json`, `{"v":5,"kind":"header"}`, `{"type":"other"}`, `[]`} {
		if _, err := ParseSessionHeader(line); err == nil {
			t.Fatalf("accepted %q", line)
		}
	}
}

func TestSerializeHeaderKeyOrder(t *testing.T) {
	header := StorageHeader{
		V: 4, Kind: "header", ID: "s", StorageVersion: 1, CreatedAt: 5, Cwd: "/c",
	}
	next := int64(9)
	header.NextSeq = &next
	line := SerializeHeader(header)
	want := `{"v":4,"kind":"header","id":"s","storageVersion":1,"createdAt":5,"cwd":"/c","nextSeq":9}`
	if line != want {
		t.Fatalf("line = %s want %s", line, want)
	}
}

func committedEntry(seq int64, id string, parent *string) session.CommittedWrite {
	entry := &session.Entry{
		EntryBase: session.EntryBase{ID: id, ParentID: parent, Seq: seq, Timestamp: 1, Type: session.EntryTypeMessage},
		Message:   session.AgentMessagePayload{Role: "user", Message: jsonx.MustParseString(`{"role":"user","content":"hi","timestamp":1}`).(*jsonx.Obj)},
	}
	return session.CommittedWrite{Kind: "entry", Seq: seq, Timestamp: 1, Entry: entry}
}

func TestSerializeTransactionSingleVsArray(t *testing.T) {
	one := []session.CommittedWrite{committedEntry(1, "e1", nil)}
	line := SerializeTransaction(one)
	if !strings.HasPrefix(line, `{"kind":"entry"`) {
		t.Fatalf("single = %s", line)
	}
	if strings.HasPrefix(line, "[") {
		t.Fatal("single wrapped in array")
	}
	two := append(one, session.CommittedWrite{Kind: "value", Op: "set", Seq: 2, Namespace: "ns", Key: "k", Value: float64(1)})
	line = SerializeTransaction(two)
	if !strings.HasPrefix(line, "[") {
		t.Fatalf("array = %s", line)
	}
}

func TestTransactionRoundTrip(t *testing.T) {
	writes := []session.CommittedWrite{
		committedEntry(1, "e1", nil),
		{Kind: "usage", Seq: 2, Row: &session.UsageRow{ID: "u1", Usage: aiUsageZero()}},
		{Kind: "value", Op: "set", Seq: 3, Namespace: "ns", Key: "k", Value: "v"},
		{Kind: "value", Op: "delete", Seq: 4, Namespace: "ns", Key: "k"},
		{Kind: "list", Op: "append", Seq: 5, Namespace: "ns", Key: "l", Value: []any{float64(1)}},
		{Kind: "list", Op: "delete", Seq: 6, Namespace: "ns", Key: "l"},
	}
	line := SerializeTransaction(writes)
	parsed, err := ParseTransaction(line)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != len(writes) {
		t.Fatalf("len = %d", len(parsed))
	}
	for i := range parsed {
		if parsed[i].Kind != writes[i].Kind || parsed[i].Seq != writes[i].Seq {
			t.Fatalf("[%d] = %+v want %+v", i, parsed[i], writes[i])
		}
		if parsed[i].Op != writes[i].Op {
			t.Fatalf("[%d] op = %s want %s", i, parsed[i].Op, writes[i].Op)
		}
	}
	// Entry payload preserved.
	if parsed[0].Entry == nil || parsed[0].Entry.ID != "e1" || parsed[0].Entry.Message.Role != "user" {
		t.Fatalf("entry = %+v", parsed[0].Entry)
	}
	// Usage row preserved.
	if parsed[1].Row == nil || parsed[1].Row.ID != "u1" {
		t.Fatalf("row = %+v", parsed[1].Row)
	}
}

func TestParseTransactionRejects(t *testing.T) {
	for _, line := range []string{
		`not json`,
		`{"kind":"unknown","seq":1}`,
		`{"kind":"value","seq":0,"namespace":"n","key":"k"}`,
		`{"kind":"value","op":"weird","seq":1,"namespace":"n","key":"k"}`,
		`{"kind":"list","op":"weird","seq":1,"namespace":"n","key":"k"}`,
		`{"kind":"entry","seq":1,"timestamp":-1}`,
	} {
		if _, err := ParseTransaction(line); err == nil {
			t.Fatalf("accepted %q", line)
		}
	}
	// Single non-array object is wrapped.
	parsed, err := ParseTransaction(`{"kind":"value","op":"delete","seq":2,"namespace":"n","key":"k"}`)
	if err != nil || len(parsed) != 1 || parsed[0].Op != "delete" {
		t.Fatalf("parsed = %+v err = %v", parsed, err)
	}
}

func TestSplitCompleteLines(t *testing.T) {
	complete, torn := SplitCompleteLines("a\nb\n")
	if len(complete) != 2 || complete[0] != "a" || torn != "" {
		t.Fatalf("complete = %v torn = %q", complete, torn)
	}
	complete, torn = SplitCompleteLines("a\nb\ntorn")
	if len(complete) != 2 || torn != "torn" {
		t.Fatalf("complete = %v torn = %q", complete, torn)
	}
	complete, torn = SplitCompleteLines("only-torn")
	if complete != nil || torn != "only-torn" {
		t.Fatalf("complete = %v torn = %q", complete, torn)
	}
	complete, torn = SplitCompleteLines("")
	if complete != nil || torn != "" {
		t.Fatalf("empty = %v %q", complete, torn)
	}
}

func aiUsageZero() (u aiUsageAlias) { return }
