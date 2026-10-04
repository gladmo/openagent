//go:build !windows

package codingagent

import "syscall"

func syscallENOTDIR() error { return syscall.ENOTDIR }
