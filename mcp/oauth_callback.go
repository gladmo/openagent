package mcp

// Port of packages/mcp/src/oauth/callback.ts: a loopback HTTP server that
// receives the OAuth authorization response.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OAuthCallback is the parsed authorization response.
type OAuthCallback struct {
	Code  string
	State string
	Iss   string // "" when absent
}

// OAuthCallbackPage is the outcome shown on the browser page after the
// redirect.
type OAuthCallbackPage struct {
	OK      bool
	Message string
	Details string // "" when absent
}

// OAuthCallbackServerOptions configure the callback server.
type OAuthCallbackServerOptions struct {
	// Host is the address to listen on. Default 127.0.0.1.
	Host string
	// RedirectHost is the host name in redirectUrl, for example localhost
	// for a client registered with it while listening on 127.0.0.1.
	// Default: Host.
	RedirectHost string
	Port         int
	Path         string
	// ExtraPaths are more paths that receive the callback, for example a
	// server-specific path of a redirect URI.
	ExtraPaths []string
	Timeout    time.Duration
	// RenderPage renders the browser page as HTML; nil serves plain text.
	RenderPage func(page OAuthCallbackPage) string
}

const defaultCallbackTimeout = 5 * time.Minute

func plainText(page OAuthCallbackPage) string {
	if page.OK {
		return "Authorization complete. You may close this window."
	}
	if page.Details != "" {
		return page.Message + "\n\n" + page.Details
	}
	return page.Message
}

type pendingCallback struct {
	resolve func(callback OAuthCallback)
	reject  func(err error)
	timer   *time.Timer
	path    string
	hasPath bool
}

// OAuthCallbackServer listens for one or more authorization responses on
// a loopback port.
type OAuthCallbackServer struct {
	redirectURL string
	server      *http.Server
	listener    net.Listener
	paths       map[string]bool

	mu      sync.Mutex
	pending map[string]*pendingCallback
	timeout time.Duration
	render  func(page OAuthCallbackPage) string
}

// Listen starts the callback server.
func OAuthCallbackServerListen(options OAuthCallbackServerOptions) (*OAuthCallbackServer, error) {
	host := options.Host
	if host == "" {
		host = "127.0.0.1"
	}
	redirectHost := options.RedirectHost
	if redirectHost == "" {
		redirectHost = host
	}
	path := options.Path
	if path == "" {
		path = "/callback"
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = defaultCallbackTimeout
	}
	port := options.Port
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{path: true}
	for _, extra := range options.ExtraPaths {
		paths[extra] = true
	}
	bound := listener.Addr().(*net.TCPAddr)
	redirectHostForURL := redirectHost
	if strings.Contains(redirectHostForURL, ":") && !strings.HasPrefix(redirectHostForURL, "[") {
		redirectHostForURL = "[" + redirectHostForURL + "]"
	}
	instance := &OAuthCallbackServer{
		redirectURL: fmt.Sprintf("http://%s:%d%s", redirectHostForURL, bound.Port, path),
		listener:    listener,
		paths:       paths,
		pending:     map[string]*pendingCallback{},
		timeout:     timeout,
		render:      options.RenderPage,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(response http.ResponseWriter, request *http.Request) {
		instance.handle(request.URL, response)
	})
	instance.server = &http.Server{Handler: mux}
	go func() { _ = instance.server.Serve(listener) }()
	return instance, nil
}

// OAuthCallbackOutcome is one wait's answer: Callback with a code on
// success, Err otherwise.
type OAuthCallbackOutcome struct {
	Callback OAuthCallback
	Err      error
}

// WaitForCallback waits for the authorization response carrying state.
// With path set, a response on another path fails, so a server-specific
// redirect URI can tell authorization servers apart (RFC 9700 section
// 4.4.2.2). The outcome channel yields exactly one value; closing the
// server fails pending waits.
func (s *OAuthCallbackServer) WaitForCallback(state string, path string) <-chan OAuthCallbackOutcome {
	promise := make(chan OAuthCallbackOutcome, 1)
	s.mu.Lock()
	if _, exists := s.pending[state]; exists {
		s.mu.Unlock()
		promise <- OAuthCallbackOutcome{Err: errors.New("OAuth state is already pending")}
		return promise
	}
	pending := &pendingCallback{}
	pending.resolve = func(callback OAuthCallback) {
		promise <- OAuthCallbackOutcome{Callback: callback}
	}
	pending.reject = func(err error) {
		promise <- OAuthCallbackOutcome{Err: err}
	}
	pending.timer = time.AfterFunc(s.timeout, func() {
		s.mu.Lock()
		delete(s.pending, state)
		s.mu.Unlock()
		pending.reject(errors.New("OAuth callback timed out"))
	})
	if path != "" {
		pending.path = path
		pending.hasPath = true
	}
	s.pending[state] = pending
	s.mu.Unlock()
	return promise
}

// RedirectURL returns the redirect URI the server serves.
func (s *OAuthCallbackServer) RedirectURL() string { return s.redirectURL }

// Close rejects pending waits and stops the server, dropping idle
// keep-alive connections.
func (s *OAuthCallbackServer) Close() error {
	s.mu.Lock()
	for _, pending := range s.pending {
		pending.timer.Stop()
		pending.reject(errors.New("OAuth callback server closed"))
	}
	s.pending = map[string]*pendingCallback{}
	s.mu.Unlock()
	_ = s.server.Shutdown(context.Background())
	return s.server.Close()
}

func (s *OAuthCallbackServer) reply(response http.ResponseWriter, status int, page OAuthCallbackPage) {
	if s.render != nil {
		response.Header().Set("content-type", "text/html; charset=utf-8")
		response.Header().Set("cache-control", "no-store")
		response.WriteHeader(status)
		_, _ = response.Write([]byte(s.render(page)))
		return
	}
	response.Header().Set("content-type", "text/plain; charset=utf-8")
	response.WriteHeader(status)
	_, _ = response.Write([]byte(plainText(page)))
}

func (s *OAuthCallbackServer) handle(rawURL *url.URL, response http.ResponseWriter) {
	if !s.paths[rawURL.Path] {
		s.reply(response, http.StatusNotFound, OAuthCallbackPage{Message: "Not found"})
		return
	}
	query := rawURL.Query()
	state := query.Get("state")
	s.mu.Lock()
	pending := s.pending[state]
	s.mu.Unlock()
	if state == "" || pending == nil {
		s.reply(response, http.StatusBadRequest, OAuthCallbackPage{Message: "Invalid or expired OAuth state"})
		return
	}
	s.mu.Lock()
	delete(s.pending, state)
	s.mu.Unlock()
	pending.timer.Stop()
	if pending.hasPath && rawURL.Path != pending.path {
		pending.reject(errors.New("The authorization response arrived on another redirect URI"))
		s.reply(response, http.StatusBadRequest, OAuthCallbackPage{Message: "Unexpected redirect URI"})
		return
	}
	if err := query.Get("error"); err != "" {
		description := query.Get("error_description")
		if description == "" {
			description = err
		}
		pending.reject(errors.New(description))
		s.reply(response, http.StatusOK, OAuthCallbackPage{
			Message: "Authorization failed. You may close this window.",
			Details: description,
		})
		return
	}
	code := query.Get("code")
	if code == "" {
		pending.reject(errors.New("OAuth callback did not include an authorization code"))
		s.reply(response, http.StatusBadRequest, OAuthCallbackPage{Message: "Missing authorization code"})
		return
	}
	iss := query.Get("iss")
	pending.resolve(OAuthCallback{Code: code, State: state, Iss: iss})
	s.reply(response, http.StatusOK, OAuthCallbackPage{OK: true})
}
