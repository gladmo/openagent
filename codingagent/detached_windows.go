//go:build windows

package codingagent

import "syscall"

// detachedSysProcAttr keeps default attributes on Windows (taskkill /T
// handles tree kills there).
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}
