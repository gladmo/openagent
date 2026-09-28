package pico3

// Ports of bounded.ts behaviors.

import (
	"strings"
	"testing"
)

func TestBoundedHeadByteBudget(t *testing.T) {
	b := NewBounded(10, 100, "head")
	b.Push([]byte("hello "))
	b.Push([]byte("world and more"))
	if b.Text() != "hello worl" {
		t.Fatalf("text = %q", b.Text())
	}
	if b.Total != len("hello world and more") {
		t.Fatalf("total = %d", b.Total)
	}
	// Total 20, retained 10 -> dropped 10.
	if b.Dropped() != 10 {
		t.Fatalf("dropped = %d", b.Dropped())
	}
}

func TestBoundedHeadLineBudget(t *testing.T) {
	b := NewBounded(1000, 2, "head")
	b.Push([]byte("line1\nline2\nline3\n"))
	if b.Text() != "line1\nline2\n" {
		t.Fatalf("text = %q", b.Text())
	}
	if b.DroppedLines != 1 {
		t.Fatalf("droppedLines = %d", b.DroppedLines)
	}
	// Further pushes drop wholesale once the budget is spent.
	b.Push([]byte("line4\n"))
	if b.Text() != "line1\nline2\n" {
		t.Fatalf("text after = %q", b.Text())
	}
	if b.DroppedLines != 2 {
		t.Fatalf("droppedLines = %d", b.DroppedLines)
	}
}

func TestBoundedTailByteBudget(t *testing.T) {
	b := NewBounded(5, 100, "tail")
	b.Push([]byte("hello world"))
	if b.Text() != "world" {
		t.Fatalf("text = %q", b.Text())
	}
	if b.Dropped() != 6 {
		t.Fatalf("dropped = %d", b.Dropped())
	}
}

func TestBoundedTailLineBudget(t *testing.T) {
	b := NewBounded(1000, 2, "tail")
	b.Push([]byte("l1\nl2\nl3\nl4\n"))
	// The last two lines are retained (the final newline belongs to l4).
	if !strings.HasSuffix(b.Text(), "l3\nl4\n") {
		t.Fatalf("text = %q", b.Text())
	}
	if b.DroppedLines != 2 {
		t.Fatalf("droppedLines = %d", b.DroppedLines)
	}
}

func TestBoundedZeroBudgetDropsAll(t *testing.T) {
	for _, retain := range []string{"head", "tail"} {
		b := NewBounded(0, 10, retain)
		b.Push([]byte("data\nmore\n"))
		if b.Text() != "" || b.Dropped() != 10 || b.DroppedLines != 2 {
			t.Fatalf("[%s] text = %q dropped = %d lines = %d", retain, b.Text(), b.Dropped(), b.DroppedLines)
		}
		b = NewBounded(10, 0, retain)
		b.Push([]byte("data\n"))
		if b.Text() != "" || b.DroppedLines != 1 {
			t.Fatalf("[%s lines=0] text = %q", retain, b.Text())
		}
	}
}

func TestBoundedEmptyChunks(t *testing.T) {
	b := NewBounded(10, 10, "head")
	b.Push(nil)
	b.Push([]byte{})
	if b.Text() != "" || b.Total != 0 || b.Dropped() != 0 {
		t.Fatal("empty chunks changed state")
	}
}

func TestBoundedHeadAcrossChunks(t *testing.T) {
	b := NewBounded(4, 10, "head")
	b.Push([]byte("ab"))
	b.Push([]byte("cd"))
	b.Push([]byte("ef"))
	if b.Text() != "abcd" {
		t.Fatalf("text = %q", b.Text())
	}
	if b.Dropped() != 2 {
		t.Fatalf("dropped = %d", b.Dropped())
	}
}

func TestBoundedTailAcrossChunks(t *testing.T) {
	b := NewBounded(3, 10, "tail")
	b.Push([]byte("ab"))
	b.Push([]byte("cd"))
	b.Push([]byte("ef"))
	if b.Text() != "def" {
		t.Fatalf("text = %q", b.Text())
	}
}

func TestBoundedNegativeBudgetClamps(t *testing.T) {
	b := NewBounded(-5, -3, "head")
	b.Push([]byte("x\n"))
	if b.Text() != "" || b.Dropped() != 2 || b.DroppedLines != 1 {
		t.Fatal("negative budgets not clamped")
	}
}
