package tui

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Golden token trees generated from the reference's marked v18.0.11 +
// latex extensions via /tmp/md-golden/dump-tokens.mjs (Node oracle). The
// dump shape: {"type", "raw", "text", "depth", "lang", "ordered", "start",
// "loose", "task", "checked", "href", "pending", "items", "header", "rows",
// "tokens", "align"}.

type mdGoldenToken map[string]any

func loadMdGolden(t *testing.T) []struct {
	input string
	want  []mdGoldenToken
} {
	t.Helper()
	data, err := os.ReadFile("testdata/markdown-tokens-golden.txt")
	if err != nil {
		t.Skip("golden file not present (regenerate with scripts/md-token-oracle.mjs)")
	}
	var cases []struct {
		input string
		want  []mdGoldenToken
	}
	parts := strings.Split(string(data), "---CASE---")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lines := strings.SplitN(part, "\n", 2)
		inputLine := strings.TrimSpace(lines[0])
		if !strings.HasPrefix(inputLine, "INPUT:") {
			t.Fatalf("golden case missing INPUT marker: %q", inputLine)
		}
		input := strings.TrimPrefix(inputLine, "INPUT:")
		var want []mdGoldenToken
		if len(lines) > 1 && strings.TrimSpace(lines[1]) != "" {
			body := strings.TrimSpace(lines[1])
			// Single-token dumps render as one object; wrap into an array.
			if strings.HasPrefix(body, "{") {
				body = "[" + body + "]"
			}
			decoder := json.NewDecoder(strings.NewReader(body))
			decoder.UseNumber()
			if err := decoder.Decode(&want); err != nil {
				t.Fatalf("golden decode %q: %v", input, err)
			}
			stripMdGoldenRaw(want)
		}
		cases = append(cases, struct {
			input string
			want  []mdGoldenToken
		}{input, want})
	}
	return cases
}

func mdTokenToGolden(token MdToken) mdGoldenToken {
	out := mdGoldenToken{}
	switch t := token.(type) {
	case MdHeading:
		out["type"] = "heading"
		out["depth"] = float64(t.Depth)
		out["text"] = mdTokensText(t.Tokens)
		out["tokens"] = mdTokensToGolden(t.Tokens)
	case MdParagraph:
		out["type"] = "paragraph"
		out["text"] = t.Text
		out["tokens"] = mdTokensToGolden(t.Tokens)
	case MdCode:
		out["type"] = "code"
		out["lang"] = t.Lang
		out["text"] = t.Text
	case *MdList:
		out["type"] = "list"
		out["ordered"] = t.Ordered
		if t.Ordered {
			out["start"] = float64(t.Start)
		} else {
			out["start"] = ""
		}
		out["loose"] = t.Loose
		var items []any
		for _, item := range t.Items {
			entry := mdGoldenToken{
				"type":   "list_item",
				"tokens": mdTokensToGolden(item.Tokens),
				"task":   item.Task,
				"loose":  t.Loose,
				"text":   mdTokensBlockText(item.Tokens),
			}
			if item.Task {
				entry["checked"] = item.Checked
			}
			items = append(items, entry)
		}
		out["items"] = items
	case MdTable:
		out["type"] = "table"
		var header []any
		for _, cell := range t.Header {
			header = append(header, mdGoldenToken{"tokens": mdTokensToGolden(cell.Tokens)})
		}
		out["header"] = header
		var rows []any
		for _, row := range t.Rows {
			var rowOut []any
			for _, cell := range row {
				rowOut = append(rowOut, mdGoldenToken{"tokens": mdTokensToGolden(cell.Tokens)})
			}
			rows = append(rows, rowOut)
		}
		out["rows"] = rows
	case MdBlockquote:
		out["type"] = "blockquote"
		out["text"] = mdTokensBlockText(t.Tokens)
		out["tokens"] = mdTokensToGolden(t.Tokens)
	case MdHr:
		out["type"] = "hr"
	case MdSpace:
		out["type"] = "space"
	case MdHtml:
		out["type"] = "html"
		out["text"] = t.Raw
	case MdTextBlock:
		out["type"] = "text"
		out["text"] = t.Text
		out["tokens"] = mdTokensToGolden(t.Tokens)
	case MdLatexBlock:
		out["type"] = "latexBlock"
		out["text"] = t.Text
		out["pending"] = t.Pending
	case MdText:
		out["type"] = "text"
		out["text"] = t.Text
	case MdStrong:
		out["type"] = "strong"
		out["text"] = mdTokensText(t.Tokens)
		out["tokens"] = mdTokensToGolden(t.Tokens)
	case MdEm:
		out["type"] = "em"
		out["text"] = mdTokensText(t.Tokens)
		out["tokens"] = mdTokensToGolden(t.Tokens)
	case MdCodespan:
		out["type"] = "codespan"
		out["text"] = t.Text
	case MdLink:
		out["type"] = "link"
		out["href"] = t.Href
		out["text"] = t.Text
		out["tokens"] = mdTokensToGolden(t.Tokens)
	case MdDel:
		out["type"] = "del"
		out["text"] = mdTokensText(t.Tokens)
		out["tokens"] = mdTokensToGolden(t.Tokens)
	case MdEscape:
		out["type"] = "escape"
		out["text"] = t.Text
	case MdCheckbox:
		out["type"] = "checkbox"
		out["checked"] = t.Checked
	case MdBr:
		out["type"] = "br"
	case MdLatex:
		out["type"] = "latex"
		out["text"] = t.Text
		if t.Pending {
			out["pending"] = true
		}
	}
	return out
}

func mdTokensToGolden(tokens []MdToken) []any {
	out := make([]any, 0, len(tokens))
	for _, token := range tokens {
		out = append(out, mdTokenToGolden(token))
	}
	return out
}

func TestMdLexerGolden(t *testing.T) {
	for _, tc := range loadMdGolden(t) {
		got := mdTokensToGolden(mdLexBlock(tc.input))
		gotJSON, _ := json.MarshalIndent(got, "", " ")
		wantJSON, _ := json.MarshalIndent(tc.want, "", " ")
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("lex(%q):\ngot  %s\nwant %s", tc.input, gotJSON, wantJSON)
		}
	}
}

// stripMdGoldenRaw drops implementation-detail "raw" fields the Go lexer
// does not carry identically (the renderer never reads them).
func stripMdGoldenRaw(tokens []mdGoldenToken) {
	for _, token := range tokens {
		delete(token, "raw")
		if items, ok := token["items"].([]any); ok {
			for _, item := range items {
				if entry, ok := item.(map[string]any); ok {
					stripMapRaw(entry)
				}
			}
		}
		stripChildRaw(token["tokens"])
		stripChildRaw(token["header"])
		if rows, ok := token["rows"].([]any); ok {
			for _, row := range rows {
				stripChildRaw(row)
			}
		}
	}
}

func stripChildRaw(value any) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			stripChildRaw(item)
		}
	case map[string]any:
		stripMapRaw(v)
	}
}

func stripMapRaw(entry map[string]any) {
	delete(entry, "raw")
	stripChildRaw(entry["tokens"])
}

// mdTokensBlockText flattens a block token list to its text (marked's
// list-item "text" field).
func mdTokensBlockText(tokens []MdToken) string {
	var b strings.Builder
	for _, token := range tokens {
		switch t := token.(type) {
		case MdParagraph:
			b.WriteString(mdTokensText(t.Tokens))
		case MdTextBlock:
			b.WriteString(t.Text)
		case MdCode:
			b.WriteString(t.Text)
		}
	}
	return b.String()
}
