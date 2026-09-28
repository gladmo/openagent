package services

// Ports of the chord services core.

import (
	"strings"
	"testing"
)

func TestRemoteServiceErrorCodes(t *testing.T) {
	for _, code := range RemoteServiceErrorCodes {
		if !IsRemoteServiceErrorCode(string(code)) {
			t.Fatalf("code %s not recognized", code)
		}
	}
	if IsRemoteServiceErrorCode("bogus") {
		t.Fatal("bogus code recognized")
	}
	// The error carries its code.
	err := NewRemoteServiceError(ErrServiceNotFound, "no such service")
	if err.Code != ErrServiceNotFound || err.Error() != "no such service" {
		t.Fatalf("err = %+v", err)
	}
}

func TestDefineServiceModes(t *testing.T) {
	local := DefineService("pi.harness", struct{ Local bool }{Local: true})
	if local.Mode != ServiceModeLocal {
		t.Fatalf("mode = %s", local.Mode)
	}
	remote := DefineService("pi.conversation", struct{ Local bool }{})
	if remote.Mode != ServiceModeBoth {
		t.Fatalf("mode = %s", remote.Mode)
	}
}

func TestRegistry(t *testing.T) {
	registry := NewRegistry()
	definition := DefineService("pi.conversation", struct{ Local bool }{})
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	resolved, ok := registry.Resolve("pi.conversation")
	if !ok || resolved.Name != "pi.conversation" {
		t.Fatalf("resolved = %+v ok = %v", resolved, ok)
	}
	// Duplicate registration rejects.
	if err := registry.Register(definition); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("err = %v", err)
	}
	// Unknown name misses.
	if _, ok := registry.Resolve("ghost"); ok {
		t.Fatal("ghost resolved")
	}
}

func TestServiceSlotBindResolve(t *testing.T) {
	slot := NewServiceSlot("ui.theme")
	// Disconnected slot errors.
	if _, err := slot.Resolve("dark", func() error { return nil }); err == nil || !strings.Contains(err.Error(), "disconnected") {
		t.Fatalf("err = %v", err)
	}
	// Bind + resolve.
	slot.Bind(map[string]any{"dark": true})
	value, err := slot.Resolve("dark", func() error { return nil })
	if err != nil || value != true {
		t.Fatalf("value = %v err = %v", value, err)
	}
	// Access assertion runs first.
	_, deniedErr := slot.Resolve("dark", func() error { return NewRemoteServiceError(ErrServiceNotAllowed, "denied") })
	if deniedErr == nil || !strings.Contains(deniedErr.Error(), "denied") {
		t.Fatalf("err = %v", deniedErr)
	}
	// Missing member.
	if _, err := slot.Resolve("missing", func() error { return nil }); err == nil || !strings.Contains(err.Error(), "no member") {
		t.Fatalf("err = %v", err)
	}
	// Unbind disconnects again.
	slot.Unbind()
	if _, err := slot.Resolve("dark", func() error { return nil }); err == nil {
		t.Fatal("unbind did not disconnect")
	}
}
