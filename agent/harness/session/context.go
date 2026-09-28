package session

import chordcontext "github.com/gladmo/openagent/chord/context"

// contextAlias is the chord Context carried by session operations.
type contextAlias = chordcontext.Context

// BackgroundContext re-exports the chord background context.
var BackgroundContext = chordcontext.BackgroundContext
