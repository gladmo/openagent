package runtime

import "github.com/gladmo/openagent/abort"

// SignalAlias is the abort signal surface used by retry waits.
type SignalAlias = abort.Signal

// nilSignal reports whether the optional signal is absent.
func nilSignal(s *abort.Signal) bool { return s == nil }
