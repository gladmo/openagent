//go:build darwin || linux

package tui

import (
	"os"
	"syscall"
	"unsafe"
)

// Raw-mode control via termios ioctls, mirroring Node's setRawMode flags.

func ioctl(fd, request uintptr, arg unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

func getTermios(fd uintptr) (*syscall.Termios, error) {
	var termios syscall.Termios
	request := uintptr(tcGets)
	if err := ioctl(fd, request, unsafe.Pointer(&termios)); err != nil {
		return nil, err
	}
	return &termios, nil
}

func setTermios(fd uintptr, termios *syscall.Termios) error {
	return ioctl(fd, uintptr(tcSets), unsafe.Pointer(termios))
}

// terminalIsRaw reports whether the fd is already in raw mode (canonical off,
// echo off).
func terminalIsRaw(f *os.File) bool {
	termios, err := getTermios(f.Fd())
	if err != nil {
		return false
	}
	return termios.Lflag&syscall.ECHO == 0 && termios.Lflag&syscall.ICANON == 0
}

// terminalSetRaw enables or disables raw mode, returning success.
func terminalSetRaw(f *os.File, raw bool) bool {
	termios, err := getTermios(f.Fd())
	if err != nil {
		return false
	}
	if raw {
		termios.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK |
			syscall.ISTRIP | syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
		termios.Oflag &^= syscall.OPOST
		termios.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON |
			syscall.ISIG | syscall.IEXTEN
		termios.Cflag &^= syscall.CSIZE | syscall.PARENB
		termios.Cflag |= syscall.CS8
		termios.Cc[syscall.VMIN] = 1
		termios.Cc[syscall.VTIME] = 0
	} else {
		// Restore cooked defaults matching the saved pre-raw state is the
		// caller's responsibility; here restore the standard cooked mode.
		termios.Iflag |= syscall.ICRNL | syscall.IXON
		termios.Oflag |= syscall.OPOST
		termios.Lflag |= syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	}
	return setTermios(f.Fd(), termios) == nil
}

type winsize struct {
	rows, cols, xpixel, ypixel uint16
}

// terminalSize returns the window width in columns, or 0 when unavailable.
func terminalSize(f *os.File) int {
	var ws winsize
	if err := ioctl(f.Fd(), uintptr(tiocgwinsz), unsafe.Pointer(&ws)); err != nil {
		return 0
	}
	return int(ws.cols)
}

func terminalRows(f *os.File) int {
	var ws winsize
	if err := ioctl(f.Fd(), uintptr(tiocgwinsz), unsafe.Pointer(&ws)); err != nil {
		return 0
	}
	return int(ws.rows)
}

func sendSIGWINCH() {
	syscall.Kill(os.Getpid(), syscall.SIGWINCH)
}
