//go:build windows

package tui

import (
	"os"
	"os/signal"
	"syscall"
)

// exitRestoreSignalSet is the signals the exit-restore handler owns; on
// Windows only SIGINT is delivered (Ctrl+C) and SIGTERM maps to it.
func exitRestoreSignalSet() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

// resetExitRestoreSignals restores the default dispositions.
func resetExitRestoreSignals() {
	signal.Reset(os.Interrupt, syscall.SIGTERM)
}

// reraiseExitRestoreSignal has no Windows equivalent of kill(); the caller
// still exits with status 128.
func reraiseExitRestoreSignal(received os.Signal) {}
