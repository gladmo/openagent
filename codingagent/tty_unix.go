//go:build !windows

package codingagent

import (
	"os"
	"syscall"
	"unsafe"
)

func stdinIsATTYImpl() bool {
	return fdIsATTYImpl(os.Stdin.Fd())
}

func stdoutIsATTYImpl() bool {
	return fdIsATTYImpl(os.Stdout.Fd())
}

func fdIsATTYImpl(fd uintptr) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, ttyTcGets, uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return errno == 0
}
