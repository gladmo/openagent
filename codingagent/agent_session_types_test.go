package codingagent

// Tests for skill-block parsing, defaults, and header filtering (ported
// from agent-session.test.ts parseSkillBlock surfaces).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
)

func TestParseSkillBlock(t *testing.T) {
	text := `<skill name="review" location="/skills/review.md">
# Review
Do the review.
</skill>

Please review my code`
	parsed := ParseSkillBlock(text)
	if parsed == nil {
		t.Fatal("parse failed")
	}
	if parsed.Name != "review" || parsed.Location != "/skills/review.md" {
		t.Fatalf("meta = %+v", parsed)
	}
	if !strings.Contains(parsed.Content, "Do the review.") {
		t.Fatalf("content = %q", parsed.Content)
	}
	if parsed.UserMessage != "Please review my code" {
		t.Fatalf("user message = %q", parsed.UserMessage)
	}
}

func TestParseSkillBlockWithoutUserMessage(t *testing.T) {
	text := "<skill name=\"s\" location=\"/l\">\ncontent\n</skill>"
	parsed := ParseSkillBlock(text)
	if parsed == nil {
		t.Fatal("parse failed")
	}
	if parsed.UserMessage != "" {
		t.Fatalf("user message = %q", parsed.UserMessage)
	}
}

func TestParseSkillBlockNegative(t *testing.T) {
	cases := []string{
		"",
		"plain text",
		"<skill name=\"x\">no location</skill>",
		`<skill name="x" location="/l">missing closing tag`,
		"leading text <skill name=\"x\" location=\"/l\">\nc\n</skill>",
	}
	for _, text := range cases {
		if parsed := ParseSkillBlock(text); parsed != nil {
			t.Errorf("ParseSkillBlock(%q) = %+v, want nil", text, parsed)
		}
	}
}

func TestDefaultThinkingLevel(t *testing.T) {
	if DefaultThinkingLevel != "medium" {
		t.Fatalf("default = %q", DefaultThinkingLevel)
	}
	if len(ThinkingLevelOptions) != 7 || ThinkingLevelOptions[0] != "off" || ThinkingLevelOptions[6] != "max" {
		t.Fatalf("options = %v", ThinkingLevelOptions)
	}
}

func TestWithoutDeletedHeaders(t *testing.T) {
	value := "v"
	headers := ai.ProviderHeaders{
		"keep": &value,
		"drop": nil,
	}
	filtered := withoutDeletedHeaders(headers)
	if filtered == nil || len(filtered) != 1 || filtered["keep"] != "v" {
		t.Fatalf("filtered = %v", filtered)
	}
	if withoutDeletedHeaders(nil) != nil {
		t.Fatal("nil headers should stay nil")
	}
	if withoutDeletedHeaders(ai.ProviderHeaders{"drop": nil}) != nil {
		t.Fatal("all-deleted headers should collapse to nil")
	}
}
