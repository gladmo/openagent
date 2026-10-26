package ai

// Port of pi-ai test/identity.test.ts: user-agent formatting, field-by-field
// identity merging, provider attribution headers, and request-path header
// precedence. The package.json version-sync case has no Go equivalent.

import (
	"runtime"
	"strings"
	"testing"
)

func goPlatform() string { return runtime.GOOS }

func goRuntime() string { return "go/" + strings.TrimPrefix(runtime.Version(), "go") }

func identityTarget(provider, baseURL string) *Model {
	return &Model{Provider: provider, BaseURL: baseURL}
}

var acmeAttribution = ClientAttribution{Name: "acme", URL: "https://acme.example"}

func TestFormatUserAgent(t *testing.T) {
	detailed := "pi/" + PiAiVersion + " (" + goPlatform() + "; " + goRuntime() + "; " + runtime.GOARCH + ")"
	if got := FormatUserAgent(nil); got != detailed {
		t.Fatalf("default = %q", got)
	}
	if got := FormatUserAgent(&ClientIdentity{Version: "9.9.9"}); got != "pi/9.9.9 ("+goPlatform()+"; "+goRuntime()+"; "+runtime.GOARCH+")" {
		t.Fatalf("versioned = %q", got)
	}
	if got := FormatUserAgent(&ClientIdentity{UserAgent: ShortUserAgent(), Version: "9.9.9"}); got != "pi" {
		t.Fatalf("short = %q", got)
	}
	if got := FormatUserAgent(&ClientIdentity{UserAgent: CustomUserAgent("acme/1.0")}); got != "acme/1.0" {
		t.Fatalf("custom = %q", got)
	}
}

func TestMergeClientIdentities(t *testing.T) {
	if merged := MergeClientIdentity(nil, nil); merged != nil {
		t.Fatalf("merged = %+v", merged)
	}
	pi := PiAttribution
	merged := MergeClientIdentity(
		&ClientIdentity{Attribution: &pi, UserAgent: ShortUserAgent()},
		&ClientIdentity{Version: "1.0.0"},
	)
	if merged.Attribution == nil || merged.Attribution.Name != "pi" || merged.UserAgent.Kind != UserAgentShort || merged.Version != "1.0.0" {
		t.Fatalf("merged = %+v", merged)
	}
	off := MergeClientIdentity(&ClientIdentity{Attribution: &pi}, &ClientIdentity{AttributionOff: true})
	if off.Attribution != nil || !off.AttributionOff {
		t.Fatalf("off = %+v", off)
	}
}

func TestNoAttributionUnlessEnabled(t *testing.T) {
	openRouter := identityTarget("openrouter", "https://openrouter.ai/api/v1")
	if headers := AttributionHeaders(openRouter, nil); len(headers) != 0 {
		t.Fatalf("default attribution = %v", headers)
	}
	if headers := AttributionHeaders(openRouter, &ClientIdentity{AttributionOff: true}); len(headers) != 0 {
		t.Fatalf("off attribution = %v", headers)
	}
	identity := IdentityHeaders(openRouter, nil)
	if len(identity) != 1 || identity["User-Agent"] != FormatUserAgent(nil) {
		t.Fatalf("identity headers = %v", identity)
	}
}

func TestAttributesOpenRouterRequests(t *testing.T) {
	openRouter := identityTarget("openrouter", "https://openrouter.ai/api/v1")
	pi := PiAttribution
	headers := AttributionHeaders(openRouter, &ClientIdentity{Attribution: &pi})
	if headers["HTTP-Referer"] != "https://pi.dev" || headers["X-OpenRouter-Title"] != "pi" || headers["X-OpenRouter-Categories"] != "cli-agent" {
		t.Fatalf("headers = %v", headers)
	}
	categorized := acmeAttribution
	categorized.Categories = []string{"cli-agent", "cloud-agent"}
	if headers := AttributionHeaders(openRouter, &ClientIdentity{Attribution: &categorized}); headers["X-OpenRouter-Categories"] != "cli-agent,cloud-agent" {
		t.Fatalf("categories = %v", headers)
	}
	empty := acmeAttribution
	empty.Categories = []string{}
	if headers := AttributionHeaders(openRouter, &ClientIdentity{Attribution: &empty}); len(headers) != 2 {
		t.Fatalf("categories = %v", headers)
	}
	// Custom providers routed through OpenRouter, including base URLs that
	// are not valid URLs.
	custom := identityTarget("custom", "not-a-url-openrouter.ai")
	if headers := AttributionHeaders(custom, &ClientIdentity{Attribution: &pi}); headers["X-OpenRouter-Title"] != "pi" {
		t.Fatalf("custom routed = %v", headers)
	}
}

func TestAttributesNvidiaAndOpencodeRequests(t *testing.T) {
	pi := PiAttribution
	if headers := AttributionHeaders(identityTarget("nvidia", "https://example.test/v1"), &ClientIdentity{Attribution: &pi}); headers["X-BILLING-INVOKE-ORIGIN"] != "Pi" {
		t.Fatalf("nvidia = %v", headers)
	}
	if headers := AttributionHeaders(identityTarget("custom-nim", "https://integrate.api.nvidia.com/v1"), &ClientIdentity{Attribution: &acmeAttribution}); headers["X-BILLING-INVOKE-ORIGIN"] != "Acme" {
		t.Fatalf("custom nim = %v", headers)
	}
	if headers := AttributionHeaders(identityTarget("opencode", "https://opencode.ai/zen/v1"), &ClientIdentity{Attribution: &pi}); headers["x-opencode-client"] != "pi" {
		t.Fatalf("opencode = %v", headers)
	}
	if headers := AttributionHeaders(identityTarget("custom", "https://opencode.ai/zen/v1"), &ClientIdentity{Attribution: &acmeAttribution}); headers["x-opencode-client"] != "acme" {
		t.Fatalf("custom opencode = %v", headers)
	}
	// NVIDIA models routed through gateways are not attributed to NVIDIA.
	if headers := AttributionHeaders(identityTarget("vercel-ai-gateway", "https://ai-gateway.vercel.sh/v1"), &ClientIdentity{Attribution: &pi}); len(headers) != 0 {
		t.Fatalf("gateway = %v", headers)
	}
}

func TestIdentityBelowModelAndRequestHeaders(t *testing.T) {
	chunks := []string{
		`{"id":"chatcmpl-1","model":"gpt-5.1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
	}
	var capture capturedRequest
	server := completionsServer(t, chunks, &capture)
	model := openaiCompletionsModel(server.URL)
	model.Headers = map[string]string{"User-Agent": "model-agent/1"}
	apiKey := "sk-oai"
	requestAgent := "request-agent/2"
	_ = OpenAICompletionsApi().StreamCompletions(model, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{
			APIKey:   &apiKey,
			Headers:  ProviderHeaders{"User-Agent": &requestAgent},
			Identity: &ClientIdentity{UserAgent: CustomUserAgent("identity-agent/0")},
		},
	}).Result()
	if capture.headers.Get("User-Agent") != "request-agent/2" {
		t.Fatalf("User-Agent = %q", capture.headers.Get("User-Agent"))
	}
	// Model headers still win over the identity when no request header sets it.
	capture2 := capturedRequest{}
	server2 := completionsServer(t, chunks, &capture2)
	model2 := openaiCompletionsModel(server2.URL)
	model2.Headers = map[string]string{"User-Agent": "model-agent/1"}
	_ = OpenAICompletionsApi().StreamCompletions(model2, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey, Identity: &ClientIdentity{UserAgent: CustomUserAgent("identity-agent/0")}},
	}).Result()
	if capture2.headers.Get("User-Agent") != "model-agent/1" {
		t.Fatalf("model User-Agent = %q", capture2.headers.Get("User-Agent"))
	}
	// Without overrides the identity User-Agent is sent.
	capture3 := capturedRequest{}
	server3 := completionsServer(t, chunks, &capture3)
	model3 := openaiCompletionsModel(server3.URL)
	_ = OpenAICompletionsApi().StreamCompletions(model3, userContext("Hello"), &OpenAICompletionsOptions{
		StreamOptions: StreamOptions{APIKey: &apiKey, Identity: &ClientIdentity{UserAgent: CustomUserAgent("identity-agent/0")}},
	}).Result()
	if capture3.headers.Get("User-Agent") != "identity-agent/0" {
		t.Fatalf("identity User-Agent = %q", capture3.headers.Get("User-Agent"))
	}
}

func TestOpenCodeSessionHeadersForCustomProviders(t *testing.T) {
	model := identityTarget("custom", "https://opencode.ai/zen/v1")
	if headers := OpenCodeSessionHeaders(model, "session-1"); headers == nil || headers["x-opencode-session"] != "session-1" {
		t.Fatalf("headers = %v", headers)
	}
	if headers := OpenCodeSessionHeaders(identityTarget("custom", "https://example.test/v1"), "session-1"); headers != nil {
		t.Fatalf("foreign headers = %v", headers)
	}
	if headers := OpenCodeSessionHeaders(model, ""); headers != nil {
		t.Fatalf("sessionless headers = %v", headers)
	}
}

// Ports of the ocr review fixes: the wrapper injects its identity on nil
// options too; AttributionOff wins over a stale Attribution; NVIDIA's
// capitalized name survives multibyte runes.
func TestWithClientIdentityInjectsOnNilOptions(t *testing.T) {
	inner := CreateModels()
	var seen *ClientIdentity
	// Capture what the wrapper forwards into the inner Models.
	fake := &captureIdentityModels{inner: inner, seen: &seen}
	w := WithClientIdentity(fake, &ClientIdentity{Version: "9.9.9"})
	_, _ = w.Complete(nil, Context{}, nil)
	if seen == nil || seen.Version != "9.9.9" {
		t.Fatalf("identity = %+v", seen)
	}
	seen = nil
	_, _ = w.Complete(nil, Context{}, &StreamOptions{})
	if seen == nil || seen.Version != "9.9.9" {
		t.Fatalf("explicit options identity = %+v", seen)
	}
}

type captureIdentityModels struct {
	inner MutableModels
	seen  **ClientIdentity
}

func (c *captureIdentityModels) Stream(model *Model, context Context, options *StreamOptions) *AssistantMessageEventStream {
	*c.seen = options.Identity
	return NewAssistantMessageEventStream()
}
func (c *captureIdentityModels) StreamSimple(model *Model, context Context, options *SimpleStreamOptions) *AssistantMessageEventStream {
	*c.seen = options.Identity
	return NewAssistantMessageEventStream()
}
func (c *captureIdentityModels) Complete(model *Model, context Context, options *StreamOptions) (*AssistantMessage, error) {
	*c.seen = options.Identity
	return &AssistantMessage{}, nil
}
func (c *captureIdentityModels) CompleteSimple(model *Model, context Context, options *SimpleStreamOptions) (*AssistantMessage, error) {
	*c.seen = options.Identity
	return &AssistantMessage{}, nil
}
func (c *captureIdentityModels) FetchDeferred(model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream {
	*c.seen = options.Identity
	return NewAssistantMessageEventStream()
}
func (c *captureIdentityModels) CancelDeferred(model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error {
	*c.seen = options.Identity
	return nil
}
func (c *captureIdentityModels) GetProviders() []Provider       { return c.inner.GetProviders() }
func (c *captureIdentityModels) GetProvider(id string) Provider { return c.inner.GetProvider(id) }
func (c *captureIdentityModels) GetModels(provider ...string) []*Model {
	return c.inner.GetModels(provider...)
}
func (c *captureIdentityModels) GetModel(provider, id string) *Model {
	return c.inner.GetModel(provider, id)
}
func (c *captureIdentityModels) SetProvider(provider Provider) { c.inner.SetProvider(provider) }
func (c *captureIdentityModels) DeleteProvider(id string)      { c.inner.DeleteProvider(id) }
func (c *captureIdentityModels) ClearProviders()               { c.inner.ClearProviders() }
func (c *captureIdentityModels) GetAuth(modelOrProvider any) (*AuthResult, bool) {
	return c.inner.GetAuth(modelOrProvider)
}

func TestAttributionHeadersOffWins(t *testing.T) {
	target := identityTarget("openrouter", "https://openrouter.ai/api/v1")
	if headers := AttributionHeaders(target, &ClientIdentity{Attribution: &PiAttribution, AttributionOff: true}); len(headers) != 0 {
		t.Fatalf("off headers = %v", headers)
	}
}

func TestNvidiaAttributionMultibyteName(t *testing.T) {
	headers := AttributionHeaders(identityTarget("nvidia", "https://example.test/v1"), &ClientIdentity{Attribution: &ClientAttribution{Name: "名前"}})
	if headers["X-BILLING-INVOKE-ORIGIN"] != "名前" {
		t.Fatalf("origin = %q", headers["X-BILLING-INVOKE-ORIGIN"])
	}
}

func TestBuildBaseOptionsCarriesIdentity(t *testing.T) {
	model := &Model{ID: "m", Provider: "p", ContextWindow: 1000, MaxTokens: 100}
	identity := &ClientIdentity{Version: "9.9.9"}
	options := BuildBaseOptions(model, &TranscriptContext{}, &SimpleStreamOptions{StreamOptions: StreamOptions{Identity: identity}}, "")
	if options.Identity != identity {
		t.Fatalf("identity = %+v", options.Identity)
	}
}
