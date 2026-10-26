//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly || solaris

package tui

import (
	"os"
	"os/signal"
	"syscall"
)

// exitRestoreSignalSet is the signals the exit-restore handler owns.
func exitRestoreSignalSet() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}
}

// resetExitRestoreSignals restores the default dispositions.
func resetExitRestoreSignals() {
	signal.Reset(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
}

// reraiseExitRestoreSignal re-delivers the signal to this process.
func reraiseExitRestoreSignal(received os.Signal) {
	if signaled, isSignal := received.(syscall.Signal); isSignal {
		_ = syscall.Kill(syscall.Getpid(), signaled)
	}
}
