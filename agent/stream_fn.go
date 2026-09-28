package agent

import "errors"

// stream_fn.go ports stream-fn.ts: the process-global default StreamFn
// registry.

var defaultStreamFn StreamFn

// SetDefaultStreamFn configures the fallback used by Agent and low-level
// loops when callers omit streamFn.
func SetDefaultStreamFn(streamFn StreamFn) { defaultStreamFn = streamFn }

// GetDefaultStreamFn returns the configured default or errors.
func GetDefaultStreamFn() (StreamFn, error) {
	if defaultStreamFn == nil {
		return nil, errors.New("No default stream function configured. Pass streamFn explicitly or call setDefaultStreamFn().")
	}
	return defaultStreamFn, nil
}
