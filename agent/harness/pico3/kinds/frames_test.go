package kinds

// Ports of kinds/frames.ts applyFrame behaviors.

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func frameOf(pairs ...any) *jsonx.Obj {
	obj := jsonx.NewObj()
	for i := 0; i+1 < len(pairs); i += 2 {
		obj.Set(pairs[i].(string), pairs[i+1])
	}
	return obj
}

func TestApplyFrameStartSetsMessage(t *testing.T) {
	output := jsonx.NewObj()
	partial := jsonx.ObjFrom("role", "assistant", "content", []any{})
	ApplyFrame(output, frameOf("type", "start", "partial", partial))
	message := output.MustGet("message").(*jsonx.Obj)
	if message.MustGet("role") != "assistant" {
		t.Fatalf("message = %v", message)
	}
}

func TestApplyFrameBeforeStartErrors(t *testing.T) {
	output := jsonx.NewObj()
	err := ApplyFrame(output, frameOf("type", "text_delta", "contentIndex", float64(0), "delta", "x"))
	if err == nil || !strings.Contains(err.Error(), "before start") {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyFrameTextLifecycle(t *testing.T) {
	output := jsonx.NewObj()
	ApplyFrame(output, frameOf("type", "start", "partial", jsonx.ObjFrom("content", []any{})))

	ApplyFrame(output, frameOf("type", "text_start", "contentIndex", float64(0), "content", jsonx.ObjFrom("type", "text", "text", "")))
	ApplyFrame(output, frameOf("type", "text_delta", "contentIndex", float64(0), "delta", "hello "))
	ApplyFrame(output, frameOf("type", "text_delta", "contentIndex", float64(0), "delta", "world"))
	content := output.MustGet("message").(*jsonx.Obj).MustGet("content").([]any)
	if content[0].(*jsonx.Obj).MustGet("text") != "hello world" {
		t.Fatalf("text = %v", content[0].(*jsonx.Obj).MustGet("text"))
	}

	// text_end replaces the text, clears/sets signature.
	ApplyFrame(output, frameOf("type", "text_end", "contentIndex", float64(0), "content", "final", "textSignature", "sig1"))
	block := content[0].(*jsonx.Obj)
	if block.MustGet("text") != "final" || block.MustGet("textSignature") != "sig1" {
		t.Fatalf("block = %v", block)
	}
	// Without a signature the key clears.
	ApplyFrame(output, frameOf("type", "text_end", "contentIndex", float64(0), "content", "again"))
	block = content[0].(*jsonx.Obj)
	if _, has := block.Get("textSignature"); has {
		t.Fatal("signature survived")
	}
}

func TestApplyFrameThinkingLifecycle(t *testing.T) {
	output := jsonx.NewObj()
	ApplyFrame(output, frameOf("type", "start", "partial", jsonx.ObjFrom("content", []any{})))
	ApplyFrame(output, frameOf("type", "thinking_start", "contentIndex", float64(0), "content", jsonx.ObjFrom("type", "thinking", "thinking", "")))
	ApplyFrame(output, frameOf("type", "thinking_delta", "contentIndex", float64(0), "delta", "deep"))
	ApplyFrame(output, frameOf("type", "thinking_end", "contentIndex", float64(0), "content", "final thought", "thinkingSignature", "ts", "redacted", true))
	content := output.MustGet("message").(*jsonx.Obj).MustGet("content").([]any)
	block := content[0].(*jsonx.Obj)
	if block.MustGet("thinking") != "final thought" || block.MustGet("thinkingSignature") != "ts" || block.MustGet("redacted") != true {
		t.Fatalf("block = %v", block)
	}
	// Without optional fields both clear.
	ApplyFrame(output, frameOf("type", "thinking_end", "contentIndex", float64(0), "content", "again"))
	if _, has := block.Get("thinkingSignature"); has {
		t.Fatal("signature survived")
	}
	if _, has := block.Get("redacted"); has {
		t.Fatal("redacted survived")
	}
}

func TestApplyFrameToolCallLifecycle(t *testing.T) {
	output := jsonx.NewObj()
	ApplyFrame(output, frameOf("type", "start", "partial", jsonx.ObjFrom("content", []any{})))
	ApplyFrame(output, frameOf("type", "toolcall_start", "contentIndex", float64(0), "toolCall", jsonx.ObjFrom("type", "toolCall", "id", "", "name", "", "arguments", jsonx.NewObj())))

	// Deltas are no-ops for the tracked output.
	ApplyFrame(output, frameOf("type", "toolcall_delta", "contentIndex", float64(0), "delta", `{"a":`))
	// Checkpoint parses partial JSON.
	ApplyFrame(output, frameOf("type", "toolcall_checkpoint", "contentIndex", float64(0), "json", `{"command":"ls"}`))
	content := output.MustGet("message").(*jsonx.Obj).MustGet("content").([]any)
	args := content[0].(*jsonx.Obj).MustGet("arguments").(*jsonx.Obj)
	if args.MustGet("command") != "ls" {
		t.Fatalf("args = %v", args)
	}

	// End finalizes id/name/arguments + optional fields.
	ApplyFrame(output, frameOf("type", "toolcall_end", "contentIndex", float64(0), "id", "c1", "name", "bash", "arguments", jsonx.ObjFrom("command", "pwd"), "thoughtSignature", "sig", "namespace", "core"))
	block := content[0].(*jsonx.Obj)
	if block.MustGet("id") != "c1" || block.MustGet("name") != "bash" || block.MustGet("namespace") != "core" {
		t.Fatalf("block = %v", block)
	}
	if block.MustGet("arguments").(*jsonx.Obj).MustGet("command") != "pwd" {
		t.Fatal("final arguments lost")
	}
}

func TestApplyFrameCheckpointInvalidJSON(t *testing.T) {
	output := jsonx.NewObj()
	ApplyFrame(output, frameOf("type", "start", "partial", jsonx.ObjFrom("content", []any{})))
	ApplyFrame(output, frameOf("type", "toolcall_start", "contentIndex", float64(0), "toolCall", jsonx.ObjFrom("type", "toolCall", "arguments", jsonx.NewObj())))
	ApplyFrame(output, frameOf("type", "toolcall_checkpoint", "contentIndex", float64(0), "json", `not json`))
	content := output.MustGet("message").(*jsonx.Obj).MustGet("content").([]any)
	args := content[0].(*jsonx.Obj).MustGet("arguments").(*jsonx.Obj)
	if len(args.Keys()) != 0 {
		t.Fatalf("args = %v", args)
	}
}
