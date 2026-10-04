package tui

// UndoStack is a generic undo stack with clone-on-push semantics: snapshots
// are detached copies. Port of undo-stack.ts; structuredClone becomes the
// injectable Clone function (nil Clone stores the value as-is, which is
// correct for value-type states).
type UndoStack[S any] struct {
	stack []S
	clone func(S) S
}

// NewUndoStack builds a stack; clone detaches pushed snapshots (pass nil for
// value-type states where assignment already copies).
func NewUndoStack[S any](clone func(S) S) *UndoStack[S] {
	return &UndoStack[S]{clone: clone}
}

// Push stores a detached clone of state.
func (u *UndoStack[S]) Push(state S) {
	if u.clone != nil {
		state = u.clone(state)
	}
	u.stack = append(u.stack, state)
}

// Pop returns the most recent snapshot, or ok=false when empty. Popped
// snapshots are returned directly (already detached).
func (u *UndoStack[S]) Pop() (S, bool) {
	if len(u.stack) == 0 {
		var zero S
		return zero, false
	}
	state := u.stack[len(u.stack)-1]
	u.stack = u.stack[:len(u.stack)-1]
	return state, true
}

// Clear removes all snapshots.
func (u *UndoStack[S]) Clear() {
	u.stack = nil
}

// Len returns the number of snapshots.
func (u *UndoStack[S]) Len() int {
	return len(u.stack)
}
