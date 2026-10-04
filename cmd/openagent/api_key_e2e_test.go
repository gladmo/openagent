package main

// End-to-end proof that the CLI reaches the wire: print mode against a
// local fake provider must send the --api-key override as a Bearer token.

import (
	"testing"
)

func TestPrintModeSendsAPIKeyOverride(t *testing.T) {
	captured := setupFakeProviderEnv(t)

	code := run([]string{
		"--provider", "zai", "--model", "glm-5.3",
		"--api-key", "secret-key", "-p", "hi",
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}

	requested, authHeader, requestPath := captured.snapshot()
	if !requested {
		t.Fatal("no request reached the fake provider")
	}
	if authHeader != "Bearer secret-key" {
		t.Fatalf("Authorization = %q, want Bearer secret-key", authHeader)
	}
	if requestPath == "" {
		t.Fatal("empty request path")
	}
}
