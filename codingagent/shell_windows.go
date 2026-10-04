//go:build windows

package codingagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// killProcessTreePlatform uses the trusted System32 taskkill so cleanup does
// not depend on PATH.
func killProcessTreePlatform(pid int) {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = "C:\\Windows"
	}
	taskkill := filepath.Join(systemRoot, "System32", "taskkill.exe")
	cmd := exec.Command(taskkill, "/F", "/T", "/PID", fmt.Sprintf("%d", pid))
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Run()
}
