//go:build !windows

package codingagent

import "syscall"

// killProcessTreePlatform kills the process group, falling back to the pid.
func killProcessTreePlatform(pid int) {
	syscall.Kill(-pid, syscall.SIGKILL)
	syscall.Kill(pid, syscall.SIGKILL)
}
