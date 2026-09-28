package pico3

// membrane.go ports harness/pico3/membrane.ts: a transaction-scoped
// revocable guard over a Chord-tracked document. Go has no proxies, so the
// wrap-everything-reached surface becomes a guarded handle type: every
// operation checks the shared liveness flag; reads return owned copies;
// writes validate plain input (no other membrane handle may be assigned
// into a document; cycles reject). At transaction finish — success,
// callback failure, validation failure, storage failure — Revoke flips the
// flag and every retained handle throws on any operation.

import (
	"fmt"

	"github.com/gladmo/openagent/jsonx"
)

// Membrane guards one transaction's document handles.
type Membrane struct {
	alive   bool
	what    string
	handles map[*MembraneHandle]bool
}

// NewMembrane builds a live membrane.
func NewMembrane(what string) *Membrane {
	return &Membrane{alive: true, what: what, handles: map[*MembraneHandle]bool{}}
}

// Revoke kills every handle at transaction finish.
func (m *Membrane) Revoke() {
	m.alive = false
}

// Alive reports liveness.
func (m *Membrane) Alive() bool { return m.alive }

func (m *Membrane) dead() error {
	return fmt.Errorf("document proxy (%s) used outside its transaction", m.what)
}

// MembraneHandle is the Go analog of one wrapped object: a guarded view
// over a plain jsonx object.
type MembraneHandle struct {
	membrane *Membrane
	target   *jsonx.Obj
}

// Wrap returns the handle for a target document (identity per target).
func (m *Membrane) Wrap(target *jsonx.Obj) (*MembraneHandle, error) {
	if !m.alive {
		return nil, m.dead()
	}
	handle := &MembraneHandle{membrane: m, target: target}
	m.handles[handle] = true
	return handle, nil
}

func (h *MembraneHandle) guard() error {
	if !h.membrane.alive {
		return h.membrane.dead()
	}
	return nil
}

// Get reads one key (owned copy).
func (h *MembraneHandle) Get(key string) (any, error) {
	if err := h.guard(); err != nil {
		return nil, err
	}
	value, ok := h.target.Get(key)
	if !ok {
		return nil, nil
	}
	return cloneJSONValue(value), nil
}

// Set writes one key after plain-input validation.
func (h *MembraneHandle) Set(key string, value any) error {
	if err := h.guard(); err != nil {
		return err
	}
	if err := h.membrane.assertPlainInput(value, nil); err != nil {
		return err
	}
	h.target.Set(key, cloneJSONValue(value))
	return nil
}

// Delete removes one key.
func (h *MembraneHandle) Delete(key string) error {
	if err := h.guard(); err != nil {
		return err
	}
	h.target.Delete(key)
	return nil
}

// Keys lists the target's keys.
func (h *MembraneHandle) Keys() ([]string, error) {
	if err := h.guard(); err != nil {
		return nil, err
	}
	return h.target.Keys(), nil
}

// assertPlainInput rejects membrane handles and cycles.
func (m *Membrane) assertPlainInput(value any, seen map[any]bool) error {
	if handle, ok := value.(*MembraneHandle); ok {
		_ = handle
		return fmt.Errorf("assigning a document proxy into a document (%s); assign a plain value", m.what)
	}
	obj, ok := value.(*jsonx.Obj)
	if !ok {
		return nil
	}
	if seen == nil {
		seen = map[any]bool{}
	}
	if seen[obj] {
		return fmt.Errorf("assigning a cyclic value into a document (%s)", m.what)
	}
	seen[obj] = true
	defer delete(seen, obj)
	for _, key := range obj.Keys() {
		nested, _ := obj.Get(key)
		if nestedObj, ok := nested.(*jsonx.Obj); ok {
			if err := m.assertPlainInput(nestedObj, seen); err != nil {
				return err
			}
		}
	}
	return nil
}
