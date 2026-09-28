package agent

import (
	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/typebox"
)

// aiAbortSignal aliases abort.Signal for test readability.
type aiAbortSignal = abort.Signal

// typeboxObjectX builds a tiny {x: number} schema for tools in tests.
func typeboxObjectX() *typebox.Schema {
	return typebox.Object([]*typebox.Property{
		typebox.Prop("x", typebox.Number()),
	})
}
