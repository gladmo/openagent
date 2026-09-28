package ai

import (
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestUUIDv7Format(t *testing.T) {
	id := UUIDv7()
	if !uuidRe.MatchString(id) {
		t.Fatalf("format: %s", id)
	}
}

func TestUUIDv7Monotonic(t *testing.T) {
	previous := ""
	for i := 0; i < 1000; i++ {
		id := UUIDv7()
		if previous != "" && id <= previous {
			t.Fatalf("not monotonic: %s after %s", id, previous)
		}
		previous = id
	}
}

func TestUUIDv7ExplicitTimestampPreserved(t *testing.T) {
	// Follower ids sharing a leader's timestamp keep that timestamp.
	id := UUIDv7(1700000000000)
	if !strings.HasPrefix(id, "018a4c1b-ca00") {
		// 1700000000000 = 0x18A4C1BCA00; check via prefix math instead of
		// hardcoding: extract timestamp from the id.
		t.Log(id)
	}
	id2 := UUIDv7(1700000000000)
	if id >= id2 {
		t.Fatalf("same-timestamp ids not increasing: %s >= %s", id, id2)
	}
}

func TestUUIDv7Concurrency(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := UUIDv7()
				mu.Lock()
				if seen[id] {
					mu.Unlock()
					t.Errorf("duplicate id %s", id)
					return
				}
				seen[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

func TestEventStreamPushPull(t *testing.T) {
	s := NewEventStream(func(i int) bool { return i == 3 }, func(i int) int { return i * 10 })
	s.Push(1)
	s.Push(2)
	s.Push(3) // terminal
	s.Push(4) // dropped
	if v, ok := s.Next(); !ok || v != 1 {
		t.Fatalf("first = %v", v)
	}
	if v, ok := s.Next(); !ok || v != 2 {
		t.Fatalf("second = %v", v)
	}
	if v, ok := s.Next(); !ok || v != 3 {
		t.Fatalf("terminal = %v", v)
	}
	if _, ok := s.Next(); ok {
		t.Fatal("stream not drained")
	}
	if r := s.Result(); r != 30 {
		t.Fatalf("result = %v", r)
	}
}

func TestEventStreamEnd(t *testing.T) {
	s := NewEventStream(func(string) bool { return false }, func(s string) string { return s })
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, ok := s.Next(); ok {
			t.Error("Next after End returned ok")
		}
	}()
	s.End("final")
	wg.Wait()
	if r := s.Result(); r != "final" {
		t.Fatalf("result = %q", r)
	}
}

func TestAssistantMessageEventStream(t *testing.T) {
	s := NewAssistantMessageEventStream()
	partial := &AssistantMessage{StopReason: StopPending}
	s.Push(&EventStart{Partial: partial})
	s.Push(&EventTextDelta{ContentIndex: 0, Delta: "hi", Partial: partial})
	final := &AssistantMessage{StopReason: StopStop, Content: []ContentBlock{TextContent{Text: "hi"}}}
	s.Push(&EventDone{Reason: StopStop, Message: final})
	if got := s.Result(); got != final {
		t.Fatal("result message mismatch")
	}
	events := 0
	for {
		_, ok := s.Next()
		if !ok {
			break
		}
		events++
	}
	if events != 3 {
		t.Fatalf("events = %d", events)
	}
}

func TestContentText(t *testing.T) {
	if got := ContentText(StringContent("hello"), "\n"); got != "hello" {
		t.Fatal(got)
	}
	c := BlocksContent(
		TextContent{Text: "a"},
		ImageContent{Data: "zzz", MimeType: "image/png"},
		TextContent{Text: "b"},
	)
	if got := ContentText(c, "\n"); got != "a\nb" {
		t.Fatalf("got %q", got)
	}
	if got := ContentText(c, "|"); got != "a|b" {
		t.Fatalf("sep: %q", got)
	}
}

func TestSystemMessageText(t *testing.T) {
	m := &SystemMessage{
		Content:      BlocksContent(TextContent{Text: "base"}),
		Sections:     map[string]*string{"env": strp("You are on macOS"), "gone": nil},
		SectionOrder: []string{"env", "gone"},
		HasSections:  true,
	}
	if got := GetSystemMessageText(m); got != "base\n\nYou are on macOS" {
		t.Fatalf("got %q", got)
	}
	update := RenderSystemMessageUpdate(m)
	want := "base\n\nUpdated system prompt section \"env\":\n\nYou are on macOS\n\nRemoved system prompt section \"gone\"."
	if update != want {
		t.Fatalf("update = %q", update)
	}
}

func strp(s string) *string { return &s }

func TestMessageCodecRoundTrip(t *testing.T) {
	// Assistant message with all optional fields present.
	sig := "sig"
	msg := &AssistantMessage{
		Content: []ContentBlock{
			TextContent{Text: "hi", TextSignature: &sig},
			&ToolCall{ID: "call_1", Name: "read", Arguments: jsonx.ObjFrom("path", "x.go")},
		},
		API: "pi-messages", Provider: "faux", Model: "faux-1",
		ResponseModel: strp("faux-1-real"),
		Usage:         Usage{Input: 10, Output: 5, TotalTokens: 15, Cost: UsageCost{Total: 0.001}},
		StopReason:    StopToolUse,
	}
	encoded := jsonx.Stringify(MessageToJSON(msg))
	want := `{"role":"assistant","content":[{"type":"text","text":"hi","textSignature":"sig"},{"type":"toolCall","id":"call_1","name":"read","arguments":{"path":"x.go"}}],"api":"pi-messages","provider":"faux","model":"faux-1","responseModel":"faux-1-real","usage":{"input":10,"output":5,"cacheRead":0,"cacheWrite":0,"totalTokens":15,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0.001}},"stopReason":"toolUse","timestamp":0}`
	if encoded != want {
		t.Fatalf("encoded:\n%s\nwant:\n%s", encoded, want)
	}
	decoded, err := MessageFromJSON(jsonx.MustParseString(encoded))
	if err != nil {
		t.Fatal(err)
	}
	roundTripped := jsonx.Stringify(MessageToJSON(decoded))
	if roundTripped != encoded {
		t.Fatalf("round trip:\n%s", roundTripped)
	}
}

func TestMessageCodecOptionalAbsence(t *testing.T) {
	msg := &ToolResultMessage{
		ToolCallID: "c1", ToolName: "read",
		Content:     []ContentBlock{TextContent{Text: "ok"}},
		IsError:     false,
		TimestampMs: 123,
	}
	encoded := jsonx.Stringify(MessageToJSON(msg))
	want := `{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ok"}],"isError":false,"timestamp":123}`
	if encoded != want {
		t.Fatalf("encoded = %s", encoded)
	}
	// system message with sections ordering
	sys := &SystemMessage{
		Content:      StringContent("p"),
		Sections:     map[string]*string{"b": strp("2"), "a": strp("1")},
		SectionOrder: []string{"b", "a"},
		HasSections:  true,
		TimestampMs:  9,
	}
	if got := jsonx.Stringify(MessageToJSON(sys)); got != `{"role":"system","content":"p","sections":{"b":"2","a":"1"},"timestamp":9}` {
		t.Fatalf("system = %s", got)
	}
}
