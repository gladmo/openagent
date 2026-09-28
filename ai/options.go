package ai

// options.go ports the request option structs from types.ts
// (ProviderRequestOptions/StreamOptions/SimpleStreamOptions/deferred
// options) and the minimal auth types the closure needs.

import (
	"io"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/telemetry"
)

// Transport values.
const (
	TransportSSE             = "sse"
	TransportWebsocket       = "websocket"
	TransportWebsocketCached = "websocket-cached"
	TransportAuto            = "auto"
)

// CacheRetention values.
const (
	CacheRetentionNone  = "none"
	CacheRetentionShort = "short"
	CacheRetentionLong  = "long"
)

// ProviderResponse mirrors the TS interface.
type ProviderResponse struct {
	Status  int
	Headers map[string]string
}

// ProviderEnv mirrors Record<string, string>.
type ProviderEnv = map[string]string

// FetchFunction mirrors typeof fetch: one blocking HTTP round-trip returning
// a streaming body.
type FetchFunction func(req FetchRequest) (FetchResponse, error)

// FetchRequest is the minimal shape pi passes to custom fetchers.
type FetchRequest struct {
	URL       string
	Method    string
	Headers   map[string]string
	Body      []byte
	Signal    *abort.Signal
	TimeoutMs *float64
}

// FetchResponse is the minimal fetch response shape (Body streams).
type FetchResponse struct {
	Status  int
	Headers map[string]string
	Body    io.Reader
}

// StreamOptions mirrors the TS interface. Callback hooks are function
// values; onPayload may return a replacement payload (nil keeps it).
type StreamOptions struct {
	Signal                    *abort.Signal
	TelemetryContext          telemetry.TelemetryContext
	APIKey                    *string
	Fetch                     FetchFunction
	Env                       ProviderEnv
	OnPayload                 func(payload any, model *Model) any
	OnResponse                func(response ProviderResponse, model *Model)
	Headers                   ProviderHeaders
	TimeoutMs                 *float64
	MaxRetries                *float64
	MaxRetryDelayMs           *float64
	OnProviderStreamEvent     func(data any, model *Model)
	Temperature               *float64
	SamplingParams            map[string]any
	MaxTokens                 *float64
	Transport                 *string
	CacheRetention            *string
	SessionID                 *string
	WebsocketConnectTimeoutMs *float64
	Metadata                  map[string]any
}

// SimpleStreamOptions adds the unified reasoning fields.
type SimpleStreamOptions struct {
	StreamOptions
	ToolChoice      *string
	Reasoning       *string
	Deferred        any // bool or {window}
	ThinkingBudgets map[string]float64
}

// DeferredFetchOptions extends ProviderRequestOptions with a long-poll wait.
type DeferredFetchOptions struct {
	StreamOptions
	Wait *float64
}

// DeferredCancelOptions mirrors the TS alias.
type DeferredCancelOptions struct {
	StreamOptions
}

// ---------------------------------------------------------------------------
// Auth shapes live in auth_provider.go (env-key subset; Credential is kept
// here because it belongs to the request-options model).
// ---------------------------------------------------------------------------

// Credential mirrors the TS union: an api-key credential (key + provider env)
// or an OAuth credential (refresh/access/expires; unused until OAuth flows
// are ported).
type Credential struct {
	Provider string
	Type     string // CredentialTypeAPIKey | CredentialTypeOAuth
	APIKey   *string
	Env      ProviderEnv

	// OAuth credential fields (type "oauth").
	Refresh string
	Access  string
	Expires float64
	Extra   map[string]any
}
