//go:build !windows

package codingagent

import "syscall"

// detachedSysProcAttr creates a new process group so kills reach the whole
// tree (mirrors the reference's detached spawn on non-Windows).
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
