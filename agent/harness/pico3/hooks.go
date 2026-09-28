package pico3

// hooks.go ports harness/pico3/hooks.ts: hook registration scoping and the
// error-isolating runner.

import (
	chordcontext "github.com/gladmo/openagent/chord/context"
	"github.com/gladmo/openagent/jsonx"
)

// HookHandlers is one namespace's handler bundle (partial by hook name).
type HookHandlers = map[string]any

// HookRegistration mirrors the TS interface.
type HookRegistration struct {
	Namespace      HookNamespace
	Kind           *KindPhases
	Handlers       HookHandlers
	ConversationID *Id
	Subtree        bool
}

// HookNamespace identifies the registering namespace.
type HookNamespace struct {
	ID string
}

// HookInfo is the per-call context handed to handlers.
type HookInfo struct {
	ConversationID Id
	Kind           string
	Extra          *jsonx.Obj
}

// HookBinding pairs one handler bundle with its API view.
type HookBinding struct {
	Handlers  HookHandlers
	Namespace HookNamespace
	API       *jsonx.Obj
}

// HookRunner runs scoped handlers with error isolation.
type HookRunner struct {
	handlers func() []HookBinding
	onReport func(err error)
}

// CreateHookRunners mirrors createHookRunners: the factory closes over the
// registration list, the ancestor walk, and the error reporter.
func CreateHookRunners(
	registrations func() []HookRegistration,
	ancestors func(conversationID Id) []Id,
	onReport func(err error),
) func(kind *KindPhases, info HookInfo) *HookRunner {
	return func(kind *KindPhases, info HookInfo) *HookRunner {
		handlers := func() []HookBinding {
			var out []HookBinding
			for _, registration := range registrations() {
				if registration.Kind != kind {
					continue
				}
				if registration.ConversationID != nil {
					scoped := *registration.ConversationID == info.ConversationID
					if !scoped && registration.Subtree {
						for _, ancestor := range ancestors(info.ConversationID) {
							if ancestor == *registration.ConversationID {
								scoped = true
								break
							}
						}
					}
					if !scoped {
						continue
					}
				}
				api := jsonx.NewObj()
				api.Set("conversationId", float64(info.ConversationID))
				if info.Kind != "" {
					api.Set("kind", info.Kind)
				} else {
					api.Set("kind", kind.Name)
				}
				if info.Extra != nil {
					for _, key := range info.Extra.Keys() {
						value, _ := info.Extra.Get(key)
						api.Set(key, value)
					}
				}
				out = append(out, HookBinding{
					Handlers:  registration.Handlers,
					Namespace: registration.Namespace,
					API:       api,
				})
			}
			return out
		}
		return &HookRunner{handlers: handlers, onReport: onReport}
	}
}

// Each runs fn over each binding in registration order. Handler errors are
// reported (not surfaced) unless the context aborted; onValue returning
// true stops the iteration early.
func (r *HookRunner) Each(ctx chordcontext.Context, fn func(handlers HookHandlers, api *jsonx.Obj) (any, error), onValue func(value any) bool) {
	for _, binding := range r.handlers() {
		value, err := fn(binding.Handlers, binding.API)
		if err != nil {
			if ctx != nil && ctx.AbortSignal().Aborted() {
				return
			}
			if r.onReport != nil {
				r.onReport(err)
			}
			continue
		}
		if value != nil && onValue != nil && onValue(value) {
			return
		}
	}
}

// Bindings exposes the scoped bindings (tests).
func (r *HookRunner) Bindings() []HookBinding { return r.handlers() }
