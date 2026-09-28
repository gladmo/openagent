package agent

// Ports of pi/packages/agent/test/proxy.test.ts against a local httptest
// server standing in for the proxy.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gladmo/openagent/ai"
	"github.com/gladmo/openagent/jsonx"
)

func proxyTestModel() *ai.Model {
	return &ai.Model{
		ID: "gpt-5.4", Name: "GPT-5.4", API: "openai-responses", Provider: "openai",
		BaseURL: "https://api.openai.com/v1", Reasoning: true, Input: []string{"text"},
		ContextWindow: 400000, MaxTokens: 128000,
	}
}

func proxyOptions(server *httptest.Server) *ProxyStreamOptions {
	return &ProxyStreamOptions{
		AuthToken: "test-token",
		ProxyURL:  server.URL,
	}
}

func collectProxyEvents(t *testing.T, stream *ai.AssistantMessageEventStream) ([]string, *ai.AssistantMessage) {
	t.Helper()
	var types []string
	for {
		event, ok := stream.Next()
		if !ok {
			break
		}
		types = append(types, event.EventType())
	}
	return types, stream.Result()
}

func TestStreamProxyPreservesToolCallEndMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/stream" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Errorf("auth = %s", auth)
		}
		events := []string{
			`{"type":"start"}`,
			`{"type":"toolcall_start","contentIndex":0,"id":"call_test|fc_test","toolName":"lookup"}`,
			`{"type":"toolcall_delta","contentIndex":0,"delta":"{\"value\":\"hello\"}"}`,
			`{"type":"toolcall_end","contentIndex":0,"toolCall":{"type":"toolCall","id":"call_test|fc_test","name":"lookup","arguments":{"value":"hello"},"namespace":"dynamic_tools"}}`,
			`{"type":"done","reason":"toolUse","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}`,
		}
		for _, event := range events {
			_, _ = w.Write([]byte("data: " + event + "\n\n"))
		}
	}))
	defer server.Close()

	stream := StreamProxy(proxyTestModel(), ai.NormalizeContext(ai.Context{}), proxyOptions(server))
	types, result := collectProxyEvents(t, stream)
	if strings.Join(types, ",") != "start,toolcall_start,toolcall_delta,toolcall_end,done" {
		t.Fatalf("types = %v", types)
	}
	call, ok := result.Content[0].(*ai.ToolCall)
	if !ok {
		t.Fatalf("content[0] = %T", result.Content[0])
	}
	if call.ID != "call_test|fc_test" || call.Name != "lookup" {
		t.Fatalf("call = %+v", call)
	}
	if call.Namespace == nil || *call.Namespace != "dynamic_tools" {
		t.Fatalf("namespace = %v", call.Namespace)
	}
	if got := stringifyArgs(call); got != `{"value":"hello"}` {
		t.Fatalf("arguments = %s", got)
	}
	if result.StopReason != ai.StopToolUse {
		t.Fatalf("stopReason = %s", result.StopReason)
	}
}

func stringifyArgs(call *ai.ToolCall) string {
	clone := call.Arguments.Clone()
	clone.Delete("\x00partialJson")
	return jsonxStringifyAgent(clone)
}

func jsonxStringifyAgent(v any) string {
	return jsonx.Stringify(v)
}

func TestStreamProxyTerminalWithoutNewline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"type\":\"start\"}\n\n"))
		// Final event NOT newline-terminated.
		_, _ = w.Write([]byte(`data: {"type":"done","reason":"stop","usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"providerThinkingLevel":"high"}`))
	}))
	defer server.Close()

	stream := StreamProxy(proxyTestModel(), ai.NormalizeContext(ai.Context{}), proxyOptions(server))
	types, result := collectProxyEvents(t, stream)
	if strings.Join(types, ",") != "start,done" {
		t.Fatalf("types = %v", types)
	}
	if result.StopReason != ai.StopStop {
		t.Fatalf("stopReason = %s", result.StopReason)
	}
	if result.ProviderThinkingLevel == nil || *result.ProviderThinkingLevel != "high" {
		t.Fatalf("providerThinkingLevel = %v", result.ProviderThinkingLevel)
	}
}

func TestStreamProxyCleanEOFWithoutTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data: {\"type\":\"start\"}\n\n"))
	}))
	defer server.Close()

	stream := StreamProxy(proxyTestModel(), ai.NormalizeContext(ai.Context{}), proxyOptions(server))
	types, result := collectProxyEvents(t, stream)
	if strings.Join(types, ",") != "start,error" {
		t.Fatalf("types = %v", types)
	}
	if result.StopReason != ai.StopError || result.ErrorMessage == nil || !strings.Contains(*result.ErrorMessage, "Connection closed by proxy server") {
		t.Fatalf("result = %+v", result)
	}
}

func TestStreamProxyHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer server.Close()

	stream := StreamProxy(proxyTestModel(), ai.NormalizeContext(ai.Context{}), proxyOptions(server))
	types, result := collectProxyEvents(t, stream)
	if len(types) != 1 || types[0] != "error" {
		t.Fatalf("types = %v", types)
	}
	if result.ErrorMessage == nil || *result.ErrorMessage != "Proxy error: invalid token" {
		t.Fatalf("errorMessage = %v", result.ErrorMessage)
	}
}

func TestStreamProxyTextThinkingStreaming(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events := []string{
			`{"type":"start"}`,
			`{"type":"thinking_start","contentIndex":0}`,
			`{"type":"thinking_delta","contentIndex":0,"delta":"reason"}`,
			`{"type":"thinking_end","contentIndex":0}`,
			`{"type":"text_start","contentIndex":1}`,
			`{"type":"text_delta","contentIndex":1,"delta":"Hel"}`,
			`{"type":"text_delta","contentIndex":1,"delta":"lo"}`,
			`{"type":"text_end","contentIndex":1,"contentSignature":"sig"}`,
			`{"type":"done","reason":"stop","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}}`,
		}
		for _, event := range events {
			_, _ = w.Write([]byte("data: " + event + "\n\n"))
		}
	}))
	defer server.Close()

	stream := StreamProxy(proxyTestModel(), ai.NormalizeContext(ai.Context{}), proxyOptions(server))
	types, result := collectProxyEvents(t, stream)
	want := "start,thinking_start,thinking_delta,thinking_end,text_start,text_delta,text_delta,text_end,done"
	if strings.Join(types, ",") != want {
		t.Fatalf("types = %v", types)
	}
	if thinking, ok := result.Content[0].(ai.ThinkingContent); !ok || thinking.Thinking != "reason" {
		t.Fatalf("thinking = %+v", result.Content[0])
	}
	text, ok := result.Content[1].(ai.TextContent)
	if !ok || text.Text != "Hello" || text.TextSignature == nil || *text.TextSignature != "sig" {
		t.Fatalf("text = %+v", result.Content[1])
	}
	if result.Usage.Input != 1 || result.Usage.Output != 2 || result.Usage.TotalTokens != 3 {
		t.Fatalf("usage = %+v", result.Usage)
	}
}
