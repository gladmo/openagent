package runtime

// Ports of drive/recovery.ts behaviors.

import (
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func TestInterruptedAssistantMessageNoPartial(t *testing.T) {
	message := InterruptedAssistantMessage("prov", "model-x", nil)
	if message.MustGet("role") != "assistant" || message.MustGet("api") != "unknown" {
		t.Fatalf("message = %v", message)
	}
	if message.MustGet("provider") != "prov" || message.MustGet("model") != "model-x" {
		t.Fatal("identity lost")
	}
	if message.MustGet("stopReason") != "error" || message.MustGet("errorMessage") != InterruptedAssistantWarning {
		t.Fatal("error shape")
	}
	content := message.MustGet("content").([]any)
	if len(content) != 0 {
		t.Fatal("no-partial content not empty")
	}
	usage := message.MustGet("usage").(*jsonx.Obj)
	if usage.MustGet("totalTokens") != float64(0) || usage.MustGet("cost").(*jsonx.Obj).MustGet("total") != float64(0) {
		t.Fatal("usage not zeroed")
	}
}

func TestInterruptedAssistantMessageWithPartial(t *testing.T) {
	partial := jsonx.ObjFrom(
		"role", "assistant",
		"content", []any{jsonx.ObjFrom("type", "text", "text", "kept")},
		"provider", "orig",
		"stopReason", "pending",
	)
	message := InterruptedAssistantMessage("prov", "model-x", partial)
	// Content preserved; usage zeroed; error forced.
	content := message.MustGet("content").([]any)
	if !contains(string(jsonx.Stringify(content[0])), "kept") {
		t.Fatal("partial content lost")
	}
	if message.MustGet("stopReason") != "error" || message.MustGet("errorMessage") != InterruptedAssistantWarning {
		t.Fatal("error not forced")
	}
	if message.MustGet("usage").(*jsonx.Obj).MustGet("input") != float64(0) {
		t.Fatal("usage not zeroed")
	}
	// Source untouched.
	if partial.MustGet("stopReason") != "pending" {
		t.Fatal("partial mutated")
	}
}

func TestRecoverAssistantMessageEmptyFrames(t *testing.T) {
	message := RecoverAssistantMessage("prov", "model-x", nil)
	if message.MustGet("content").([]any) != nil && len(message.MustGet("content").([]any)) != 0 {
		t.Fatal("empty frames produced content")
	}
	if message.MustGet("api") != "unknown" {
		t.Fatal("empty frames identity")
	}
}

func TestSettleRecovered(t *testing.T) {
	frames := []*jsonx.Obj{
		jsonx.MustParseString(`{"type":"start","partial":{"role":"assistant","content":[]}}`).(*jsonx.Obj),
		jsonx.MustParseString(`{"type":"text_start","contentIndex":0,"content":{"type":"text","text":""}}`).(*jsonx.Obj),
		jsonx.MustParseString(`{"type":"text_delta","contentIndex":0,"delta":"recovered"}`).(*jsonx.Obj),
	}
	outcome := SettleRecovered("prov", "model-x", frames)
	if !outcome.Settled || outcome.Message == nil {
		t.Fatalf("outcome = %+v", outcome)
	}
	content := outcome.Message.MustGet("content").([]any)
	if len(content) == 0 {
		t.Fatal("reduced content empty")
	}
	if !contains(string(jsonx.Stringify(content[0])), "recovered") {
		t.Fatalf("text = %v", content[0])
	}
}
