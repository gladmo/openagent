//go:build !darwin && !linux

package tui

import (
	"errors"
	"os"
)

// Windows and other platforms: console raw mode is a documented gap; the
// terminal fails loud rather than silently misbehaving.

var errRawModeUnsupported = errors.New("raw mode unsupported on this platform in the openagent port")

func terminalIsRaw(f *os.File) bool { return false }

func terminalSetRaw(f *os.File, raw bool) bool { return false }

func terminalSize(f *os.File) int { return 0 }

func terminalRows(f *os.File) int { return 0 }

func sendSIGWINCH() {}

// resizeSignalValue: no SIGWINCH off unix.
func resizeSignalValue() os.Signal { return nil }
