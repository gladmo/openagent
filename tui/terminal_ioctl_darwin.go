//go:build darwin

package tui

import (
	"os"
	"syscall"
)

const (
	tcGets     = 0x40487413 // TIOCGETA
	tcSets     = 0x80487414 // TIOCSETA
	tiocgwinsz = 0x40087468
)

var _ = syscall.ECHO

// resizeSignalValue exposes SIGWINCH for the resize watcher.
func resizeSignalValue() os.Signal { return syscall.SIGWINCH }
