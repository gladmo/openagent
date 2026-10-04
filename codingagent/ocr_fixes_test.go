package codingagent

// Regression tests for the OpenCodeReview findings: value-typed text
// blocks through the summarization chain, empty-frontmatter parsing,
// image-sniff bounds, tool-result bridging, and the read-limit clamp.

import (
	"context"
	"strings"
	"testing"

	"github.com/gladmo/openagent/agent"
	"github.com/gladmo/openagent/ai"
)

func valueTypedStream(text string) SummarizationStreamFn {
	return func(ctx context.Context, model *ai.Model, messages []ai.Message, options *SummarizationOptions) (*ai.AssistantMessage, error) {
		return &ai.AssistantMessage{
			StopReason: "stop",
			Content:    []ai.ContentBlock{ai.TextContent{Text: text}},
		}, nil
	}
}

func TestGenerateSummaryValueTypedBlocks(t *testing.T) {
	result, err := GenerateSummary(nil, &ai.Model{}, &GenerateSummaryOptions{ReserveTokens: 1000}, valueTypedStream("value typed summary"))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(result.Text, "value typed summary") {
		t.Fatalf("text = %q (value-typed blocks dropped)", result.Text)
	}
}

func TestGenerateBranchSummaryValueTypedBlocks(t *testing.T) {
	entries := []SessionEntry{branchSummaryEntry("user", "explored")}
	result := GenerateBranchSummary(entries, &ai.Model{}, nil, valueTypedStream("branch value text"))
	if result.Error != "" {
		t.Fatalf("error = %q", result.Error)
	}
	if !strings.Contains(result.Summary, "branch value text") {
		t.Fatalf("summary = %q (value-typed blocks dropped)", result.Summary)
	}
}

func TestGenerateTurnPrefixSummaryValueTypedBlocks(t *testing.T) {
	result, err := GenerateTurnPrefixSummary(nil, &ai.Model{}, 1000, nil, valueTypedStream("prefix value text"))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(result.Text, "prefix value text") {
		t.Fatalf("text = %q (value-typed blocks dropped)", result.Text)
	}
}

func TestConvertToLlmValueBlocksPassContentText(t *testing.T) {
	// BranchSummaryMessage converts to a user message with pointer-free
	// blocks; ContentText (the hard-asserting consumer) must accept it.
	message := CreateBranchSummaryMessage("branch summary body", "", "ts")
	converted := ConvertToLlm([]agent.AgentMessage{message})
	if len(converted) != 1 {
		t.Fatalf("converted = %d", len(converted))
	}
	user, ok := converted[0].(*ai.UserMessage)
	if !ok {
		t.Fatalf("type = %T", converted[0])
	}
	// ContentText panics on pointer blocks; reaching here means the fix holds.
	if !strings.Contains(ai.ContentText(user.Content, "\n"), "branch summary body") {
		t.Fatalf("text = %q", ai.ContentText(user.Content, "\n"))
	}
}

func TestExtractFrontmatterEmpty(t *testing.T) {
	yaml, body := extractFrontmatter("---\n---\nbody text")
	if yaml != "" {
		t.Fatalf("yaml = %q", yaml)
	}
	if body != "body text" {
		t.Fatalf("body = %q", body)
	}
}

func TestDetectSupportedImageMimeTypeShortJpeg(t *testing.T) {
	// Exactly the 3-byte signature: no panic, still detected as jpeg.
	if got := DetectSupportedImageMimeType([]byte{0xff, 0xd8, 0xff}); got != "image/jpeg" {
		t.Fatalf("mime = %q", got)
	}
}

func TestToAgentToolResultBuiltInShapes(t *testing.T) {
	// The bridge must carry tool outputs instead of emptying them.
	result := toAgentToolResult(&ToolResultText{Text: "wrote the file"})
	if len(result.Content) == 0 {
		t.Fatal("content dropped")
	}
	if text, ok := result.Content[0].(ai.TextContent); !ok || text.Text != "wrote the file" {
		t.Fatalf("content = %#v", result.Content[0])
	}

	withImage := toAgentToolResult(&ReadToolOutput{Text: "file", ImageData: "b64", MimeType: "image/png"})
	if len(withImage.Content) != 2 {
		t.Fatalf("blocks = %d (image dropped)", len(withImage.Content))
	}
	if _, ok := withImage.Content[1].(ai.ImageContent); !ok {
		t.Fatalf("block1 = %#v", withImage.Content[1])
	}

	// Unknown shapes surface an explicit error result.
	unknown := toAgentToolResult(42)
	if len(unknown.Content) == 0 {
		t.Fatal("unknown shape emptied")
	}
	if text, ok := unknown.Content[0].(ai.TextContent); !ok || !strings.Contains(text.Text, "unsupported output type") {
		t.Fatalf("unknown = %#v", unknown.Content[0])
	}
}
