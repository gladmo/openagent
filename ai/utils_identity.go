package ai

// Port of pi-ai utils/identity.ts + utils/version.ts (adc5fa9f8 +
// 04a93bad8): how pi-ai identifies the calling app to providers.
//
// Go mapping notes:
//   - TS `UserAgentMode = "short" | "detailed" | { custom }` becomes a
//     struct with a kind discriminator; nil means the "detailed" default.
//   - TS `attribution?: ClientAttribution | null` distinguishes absent
//     (inherit on merge) from null (off, overriding a merged identity); Go
//     carries AttributionOff for the null case.
//   - The detailed User-Agent reports the Go runtime (`go/<version>`)
//     where the reference reports `node/<version>` or `bun/<version>`.

import (
	"net/url"
	"runtime"
	"strings"
)

// PiAiVersion is the pi-ai package version the port tracks.
const PiAiVersion = "1.3.0"

// UserAgentMode kinds.
const (
	UserAgentDetailed = "detailed"
	UserAgentShort    = "short"
	UserAgentCustom   = "custom"
)

// UserAgentMode is the User-Agent policy: "detailed"
// `pi/<version> (<platform>; go/<go version>; <arch>)`, "short" `pi`, or a
// custom value. A zero value means "detailed".
type UserAgentMode struct {
	Kind   string
	Custom string
}

// ShortUserAgent returns the short mode.
func ShortUserAgent() *UserAgentMode { return &UserAgentMode{Kind: UserAgentShort} }

// DetailedUserAgent returns the detailed mode.
func DetailedUserAgent() *UserAgentMode { return &UserAgentMode{Kind: UserAgentDetailed} }

// CustomUserAgent returns a custom mode with the given value.
func CustomUserAgent(value string) *UserAgentMode {
	return &UserAgentMode{Kind: UserAgentCustom, Custom: value}
}

// ClientAttribution says who requests are attributed to;
// AttributionHeaders maps the fields to each provider's headers.
type ClientAttribution struct {
	// Name: OpenRouter app title, NVIDIA invoke origin (first letter
	// upper-cased), OpenCode client.
	Name string `json:"name"`
	// URL: OpenRouter HTTP-Referer, the app's identity in OpenRouter's
	// rankings, grouped by origin.
	URL string `json:"url"`
	// Categories, e.g. ["cli-agent"]: OpenRouter
	// X-OpenRouter-Categories, comma-separated.
	Categories []string `json:"categories,omitempty"`
}

// PiAttribution is pi's attribution, used by pi-agent-core, pi-durable, and
// the coding agent.
var PiAttribution = ClientAttribution{Name: "pi", URL: "https://pi.dev", Categories: []string{"cli-agent"}}

// ClientIdentity is how pi-ai identifies the calling app to providers.
// Every field is optional; absent fields use pi-ai's defaults (a detailed
// User-Agent with pi-ai's version and no attribution headers). Plain JSON,
// so it can be persisted.
type ClientIdentity struct {
	// UserAgent policy; nil is the "detailed" default.
	UserAgent *UserAgentMode `json:"userAgent,omitempty"`
	// Attribution headers. Nil with AttributionOff false: none set;
	// AttributionOff true turns attribution off, including attribution set
	// by a merged identity.
	Attribution    *ClientAttribution `json:"attribution,omitempty"`
	AttributionOff bool               `json:"attributionOff,omitempty"`
	// Version in the detailed User-Agent; empty uses PiAiVersion.
	Version string `json:"version,omitempty"`
}

// MergeClientIdentity merges identities field by field; later sources win
// and nil fields are skipped. Nil when every source is nil.
func MergeClientIdentity(sources ...*ClientIdentity) *ClientIdentity {
	var merged *ClientIdentity
	for _, source := range sources {
		if source == nil {
			continue
		}
		if merged == nil {
			merged = &ClientIdentity{}
		}
		if source.UserAgent != nil {
			userAgent := *source.UserAgent
			merged.UserAgent = &userAgent
		}
		if source.Attribution != nil || source.AttributionOff {
			if source.AttributionOff {
				merged.Attribution = nil
				merged.AttributionOff = true
			} else {
				attribution := *source.Attribution
				merged.Attribution = &attribution
				merged.AttributionOff = false
			}
		}
		if source.Version != "" {
			merged.Version = source.Version
		}
	}
	return merged
}

// GetClientVersion returns the version used in the detailed User-Agent.
func GetClientVersion(identity *ClientIdentity) string {
	if identity != nil && identity.Version != "" {
		return identity.Version
	}
	return PiAiVersion
}

func runtimeDescription() string {
	return "go/" + strings.TrimPrefix(runtime.Version(), "go")
}

// FormatUserAgent returns the User-Agent value of an identity.
func FormatUserAgent(identity *ClientIdentity) string {
	mode := UserAgentMode{Kind: UserAgentDetailed}
	if identity != nil && identity.UserAgent != nil {
		mode = *identity.UserAgent
	}
	switch mode.Kind {
	case UserAgentCustom:
		return mode.Custom
	case UserAgentShort:
		return "pi"
	default:
		product := "pi/" + GetClientVersion(identity)
		return product + " (" + runtime.GOOS + "; " + runtimeDescription() + "; " + runtime.GOARCH + ")"
	}
}

const nvidiaNimHost = "integrate.api.nvidia.com"
const openCodeHost = "opencode.ai"

func matchesHost(baseURL, expectedHost string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return parsed.Hostname() == expectedHost
}

func isOpenRouterTarget(model *Model) bool {
	return model.Provider == "openrouter" || strings.Contains(model.BaseURL, "openrouter.ai")
}

func isNvidiaNimTarget(model *Model) bool {
	return model.Provider == "nvidia" || matchesHost(model.BaseURL, nvidiaNimHost)
}

func isOpenCodeTarget(model *Model) bool {
	return model.Provider == "opencode" || model.Provider == "opencode-go" || matchesHost(model.BaseURL, openCodeHost)
}

// AttributionHeaders returns the provider attribution headers of an
// identity for a request target; empty when attribution is off.
func AttributionHeaders(model *Model, identity *ClientIdentity) map[string]string {
	if identity == nil || identity.Attribution == nil || identity.AttributionOff {
		return map[string]string{}
	}
	attribution := identity.Attribution
	if isOpenRouterTarget(model) {
		headers := map[string]string{
			"HTTP-Referer":       attribution.URL,
			"X-OpenRouter-Title": attribution.Name,
		}
		if len(attribution.Categories) > 0 {
			headers["X-OpenRouter-Categories"] = strings.Join(attribution.Categories, ",")
		}
		return headers
	}
	// NVIDIA expects a capitalized origin, such as `Pi`.
	if isNvidiaNimTarget(model) {
		name := attribution.Name
		if runes := []rune(name); len(runes) > 0 {
			name = strings.ToUpper(string(runes[0])) + string(runes[1:])
		}
		return map[string]string{"X-BILLING-INVOKE-ORIGIN": name}
	}
	if isOpenCodeTarget(model) {
		return map[string]string{"x-opencode-client": attribution.Name}
	}
	return map[string]string{}
}

// OpenCodeSessionHeaders returns OpenCode's per-conversation routing header
// for requests to OpenCode, including custom providers pointing at it; nil
// without a session or for other targets.
func OpenCodeSessionHeaders(model *Model, sessionID string) map[string]string {
	if sessionID == "" || !isOpenCodeTarget(model) {
		return nil
	}
	return map[string]string{"x-opencode-session": sessionID}
}

// IdentityHeaders returns the default headers an identity adds to a
// request: User-Agent and provider attribution. APIs apply them below
// model, provider, and request headers, so configured headers override
// them.
func IdentityHeaders(model *Model, identity *ClientIdentity) map[string]string {
	headers := map[string]string{"User-Agent": FormatUserAgent(identity)}
	for key, value := range AttributionHeaders(model, identity) {
		headers[key] = value
	}
	return headers
}

// identityModels wraps Models so identity is the default identity of its
// requests: a request's identity overrides it field by field, and it
// overrides the identity models was created with.
type identityModels struct {
	inner    MutableModels
	identity *ClientIdentity
}

// WithClientIdentity returns models with identity as the default identity
// of its requests; everything else is delegated unchanged.
func WithClientIdentity(models MutableModels, identity *ClientIdentity) MutableModels {
	return &identityModels{inner: models, identity: identity}
}

func (w *identityModels) withIdentity(identity *ClientIdentity) *ClientIdentity {
	return MergeClientIdentity(w.identity, identity)
}

func (w *identityModels) GetProviders() []Provider { return w.inner.GetProviders() }
func (w *identityModels) GetProvider(id string) Provider {
	return w.inner.GetProvider(id)
}
func (w *identityModels) GetModels(provider ...string) []*Model {
	return w.inner.GetModels(provider...)
}
func (w *identityModels) GetModel(provider, id string) *Model {
	return w.inner.GetModel(provider, id)
}
func (w *identityModels) SetProvider(provider Provider) { w.inner.SetProvider(provider) }
func (w *identityModels) DeleteProvider(id string)      { w.inner.DeleteProvider(id) }
func (w *identityModels) ClearProviders()               { w.inner.ClearProviders() }

func (w *identityModels) GetAuth(modelOrProvider any) (*AuthResult, bool) {
	return w.inner.GetAuth(modelOrProvider)
}

func (w *identityModels) Stream(model *Model, context Context, options *StreamOptions) *AssistantMessageEventStream {
	if options == nil {
		options = &StreamOptions{}
	}
	merged := *options
	merged.Identity = w.withIdentity(options.Identity)
	return w.inner.Stream(model, context, &merged)
}

func (w *identityModels) StreamSimple(model *Model, context Context, options *SimpleStreamOptions) *AssistantMessageEventStream {
	if options == nil {
		options = &SimpleStreamOptions{}
	}
	merged := *options
	merged.Identity = w.withIdentity(options.Identity)
	return w.inner.StreamSimple(model, context, &merged)
}

func (w *identityModels) Complete(model *Model, context Context, options *StreamOptions) (*AssistantMessage, error) {
	if options == nil {
		options = &StreamOptions{}
	}
	merged := *options
	merged.Identity = w.withIdentity(options.Identity)
	return w.inner.Complete(model, context, &merged)
}

func (w *identityModels) CompleteSimple(model *Model, context Context, options *SimpleStreamOptions) (*AssistantMessage, error) {
	if options == nil {
		options = &SimpleStreamOptions{}
	}
	merged := *options
	merged.Identity = w.withIdentity(options.Identity)
	return w.inner.CompleteSimple(model, context, &merged)
}

func (w *identityModels) FetchDeferred(model *Model, handle *DeferredHandle, options *DeferredFetchOptions) *AssistantMessageEventStream {
	if options == nil {
		options = &DeferredFetchOptions{}
	}
	merged := *options
	merged.Identity = w.withIdentity(options.Identity)
	return w.inner.FetchDeferred(model, handle, &merged)
}

func (w *identityModels) CancelDeferred(model *Model, handle *DeferredHandle, options *DeferredCancelOptions) error {
	if options == nil {
		options = &DeferredCancelOptions{}
	}
	merged := *options
	merged.Identity = w.withIdentity(options.Identity)
	return w.inner.CancelDeferred(model, handle, &merged)
}

// optionsIdentity extracts the request identity from an options struct
// carrying a StreamOptions (directly or as a named field).
func optionsIdentity(options any) *ClientIdentity {
	switch options := options.(type) {
	case *StreamOptions:
		if options != nil {
			return options.Identity
		}
	case *SimpleStreamOptions:
		if options != nil {
			return options.Identity
		}
	case *DeferredFetchOptions:
		if options != nil {
			return options.Identity
		}
	case *AnthropicOptions:
		if options != nil {
			return options.StreamOptions.Identity
		}
	case *OpenAICompletionsOptions:
		if options != nil {
			return options.StreamOptions.Identity
		}
	case *OpenAIResponsesOptions:
		if options != nil {
			return options.StreamOptions.Identity
		}
	case *OpenAICodexResponsesOptions:
		if options != nil {
			return options.StreamOptions.Identity
		}
	case StreamOptions:
		return options.Identity
	case SimpleStreamOptions:
		return options.Identity
	case DeferredFetchOptions:
		return options.Identity
	case AnthropicOptions:
		return options.StreamOptions.Identity
	case OpenAICompletionsOptions:
		return options.StreamOptions.Identity
	case OpenAIResponsesOptions:
		return options.StreamOptions.Identity
	case OpenAICodexResponsesOptions:
		return options.StreamOptions.Identity
	}
	return nil
}
