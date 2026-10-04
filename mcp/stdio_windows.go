//go:build windows

package mcp

// Windows process-tree management for the stdio transport. Windows has no
// graceful signals, and .cmd shims run through cmd.exe; killing only that
// would leave the server running, so the tree is force-killed by pid.

import (
	"os/exec"
	"syscall"
)

const useProcessGroups = false

func setChildProcessGroup(child *exec.Cmd) {
	child.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// killProcessTree force-terminates the child and its descendants.
func killProcessTree(child *exec.Cmd, _ syscall.Signal) {
	if child.Process == nil || child.ProcessState != nil {
		return
	}
	command := exec.Command("taskkill", "/pid", itoa(child.Process.Pid), "/T", "/F")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = command.Run()
}
