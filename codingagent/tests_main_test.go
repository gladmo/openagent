package codingagent

// The suite must never launch a real browser: OpenBrowser is a
// best-effort side effect in the reference, and test URLs would open
// tabs on the developer machine. TestMain installs a recorder for every
// test; individual tests may swap it further via SetBrowserOpener.

import (
	"os"
	"sync"
	"testing"
)

var (
	browserOpenMu      sync.Mutex
	browserOpenTargets []string
)

// openedBrowserTargets returns the URLs recorded since the last reset.
func openedBrowserTargets() []string {
	browserOpenMu.Lock()
	defer browserOpenMu.Unlock()
	out := append([]string(nil), browserOpenTargets...)
	return out
}

func resetOpenedBrowserTargets() {
	browserOpenMu.Lock()
	browserOpenTargets = nil
	browserOpenMu.Unlock()
}

func TestMain(m *testing.M) {
	SetBrowserOpener(func(target string) {
		browserOpenMu.Lock()
		browserOpenTargets = append(browserOpenTargets, target)
		browserOpenMu.Unlock()
	})
	os.Exit(m.Run())
}
