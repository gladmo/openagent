package pico3

// Ports of membrane.ts behaviors (Go handle-based adaptation).

import (
	"strings"
	"testing"

	"github.com/gladmo/openagent/jsonx"
)

func TestMembraneLifecycle(t *testing.T) {
	membrane := NewMembrane("sticky")
	doc := jsonx.ObjFrom("a", float64(1))
	handle, err := membrane.Wrap(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Set("b", float64(2)); err != nil {
		t.Fatal(err)
	}
	value, err := handle.Get("a")
	if err != nil || value != float64(1) {
		t.Fatalf("get = %v err = %v", value, err)
	}
	// Revoke: every later operation fails.
	membrane.Revoke()
	if membrane.Alive() {
		t.Fatal("membrane alive after revoke")
	}
	if _, err := handle.Get("a"); err == nil || !strings.Contains(err.Error(), "outside its transaction") {
		t.Fatalf("get err = %v", err)
	}
	if err := handle.Set("c", float64(3)); err == nil {
		t.Fatal("set after revoke")
	}
	if err := handle.Delete("a"); err == nil {
		t.Fatal("delete after revoke")
	}
	if _, err := handle.Keys(); err == nil {
		t.Fatal("keys after revoke")
	}
	// Wrap after revoke fails too.
	if _, err := membrane.Wrap(jsonx.NewObj()); err == nil {
		t.Fatal("wrap after revoke")
	}
}

func TestMembraneGetReturnsOwnedCopy(t *testing.T) {
	membrane := NewMembrane("rewindable")
	nested := jsonx.ObjFrom("x", float64(1))
	doc := jsonx.ObjFrom("nested", nested)
	handle, _ := membrane.Wrap(doc)

	value, err := handle.Get("nested")
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the returned copy must not touch the document.
	value.(*jsonx.Obj).Set("x", float64(99))
	if nested.MustGet("x") != float64(1) {
		t.Fatal("read leaked an alias into the document")
	}
}

func TestMembraneRejectsHandleAssignment(t *testing.T) {
	membrane := NewMembrane("sticky")
	docA := jsonx.NewObj()
	docB := jsonx.NewObj()
	handleA, _ := membrane.Wrap(docA)
	handleB, _ := membrane.Wrap(docB)
	// Assigning one wrapper into another is the classic footgun.
	err := handleA.Set("child", handleB)
	if err == nil || !strings.Contains(err.Error(), "assign a plain value") {
		t.Fatalf("err = %v", err)
	}
}

func TestMembraneRejectsCycles(t *testing.T) {
	membrane := NewMembrane("session")
	doc := jsonx.NewObj()
	handle, _ := membrane.Wrap(doc)

	// Build a cyclic value: obj.self = obj.
	cyclic := jsonx.NewObj()
	cyclic.Set("self", cyclic)
	err := handle.Set("cycle", cyclic)
	if err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("err = %v", err)
	}
	// Nested cycles reject too.
	outer := jsonx.NewObj()
	inner := jsonx.NewObj()
	outer.Set("inner", inner)
	inner.Set("outer", outer)
	if err := handle.Set("nested", outer); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("nested err = %v", err)
	}
	// Acyclic nested objects are fine.
	ok := jsonx.ObjFrom("k", jsonx.ObjFrom("deep", float64(1)))
	if err := handle.Set("ok", ok); err != nil {
		t.Fatalf("acyclic rejected: %v", err)
	}
}

func TestMembraneDeleteAndKeys(t *testing.T) {
	membrane := NewMembrane("sticky")
	doc := jsonx.ObjFrom("a", float64(1), "b", float64(2))
	handle, _ := membrane.Wrap(doc)
	keys, err := handle.Keys()
	if err != nil || len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
		t.Fatalf("keys = %v err = %v", keys, err)
	}
	if err := handle.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if value, _ := handle.Get("a"); value != nil {
		t.Fatal("delete did not remove")
	}
	keys, _ = handle.Keys()
	if len(keys) != 1 || keys[0] != "b" {
		t.Fatalf("keys after delete = %v", keys)
	}
}
