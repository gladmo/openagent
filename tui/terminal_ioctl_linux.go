//go:build linux

package tui

import (
	"os"
	"syscall"
)

const (
	tcGets     = syscallTCGETS
	tcSets     = syscallTCSETS
	tiocgwinsz = syscallTIOCGWINSZ
)

const (
	syscallTCGETS     = 0x5401
	syscallTCSETS     = 0x5402
	syscallTIOCGWINSZ = 0x5413
)

// resizeSignalValue exposes SIGWINCH for the resize watcher.
func resizeSignalValue() os.Signal { return syscall.SIGWINCH }
