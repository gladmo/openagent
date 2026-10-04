//go:build !windows

package mcp

// Unix process-group management for the stdio transport: the server runs
// in its own group so closing the transport can terminate the server's
// children too (wrappers like npx or uvx do not forward signals).

import (
	"os/exec"
	"syscall"
)

// useProcessGroups reports whether children get their own process group.
const useProcessGroups = true

func setChildProcessGroup(child *exec.Cmd) {
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree signals the child's whole process group, falling back to
// the direct child when the group is gone or was never created.
func killProcessTree(child *exec.Cmd, signal syscall.Signal) {
	if child.Process == nil {
		return
	}
	pid := child.Process.Pid
	if useProcessGroups {
		if err := syscall.Kill(-pid, signal); err == nil {
			return
		}
	}
	_ = child.Process.Signal(signal)
}
