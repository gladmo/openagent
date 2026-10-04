package main

// Shared test helper: a local fake OpenAI-completions provider that
// records the Authorization header and streams one "pong" reply.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gladmo/openagent/codingagent"
)

type capturedAuth struct {
	mu          sync.Mutex
	requested   bool
	authHeader  string
	requestPath string
}

func (c *capturedAuth) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requested = true
	c.authHeader = r.Header.Get("Authorization")
	c.requestPath = r.URL.Path
}

func (c *capturedAuth) snapshot() (requested bool, authHeader, requestPath string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requested, c.authHeader, c.requestPath
}

// fakeProviderServer streams a one-chunk "pong" completion.
func fakeProviderServer(t *testing.T) (*httptest.Server, *capturedAuth) {
	t.Helper()
	captured := &capturedAuth{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.record(r)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("response writer is not a flusher")
			return
		}
		fmt.Fprint(w, "data: {\"id\":\"1\",\"model\":\"glm-5.3\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"pong\\n\\n- alpha item\\n- beta item\"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"id\":\"1\",\"model\":\"glm-5.3\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	t.Cleanup(server.Close)
	return server, captured
}

// writeProviderOverlay points the builtin zai provider at the fake server.
func writeProviderOverlay(t *testing.T, agentDir, baseURL string) {
	t.Helper()
	modelsJSON := fmt.Sprintf(`{"providers":{"zai":{"baseUrl":%q}}}`, baseURL)
	if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(modelsJSON), 0o644); err != nil {
		t.Fatalf("models.json: %v", err)
	}
}

// setupFakeProviderEnv isolates the agent dir and points builtin zai at a
// fake server, returning the captured request state.
func setupFakeProviderEnv(t *testing.T) *capturedAuth {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(codingagent.EnvAgentDirName(), dir)
	server, captured := fakeProviderServer(t)
	writeProviderOverlay(t, dir, server.URL)
	return captured
}

// newFakeProviderSession builds the CLI session for zai against a fake
// server, applying --api-key the way the CLI does.
func newFakeProviderSession(t *testing.T) (*codingagent.PrintSessionHost, *capturedAuth) {
	t.Helper()
	captured := setupFakeProviderEnv(t)

	host, err := codingagent.SetupPrintSession(codingagent.PrintSessionOptions{Cwd: t.TempDir(), Mode: "text"})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	parsed := codingagent.ParseArgs([]string{
		"--provider", "zai", "--model", "glm-5.3", "--api-key", "secret-key",
	})
	if message, fatal := applyCliModelSelection(host.Session(), parsed); fatal {
		t.Fatalf("model selection: %s", message)
	}
	return host, captured
}
