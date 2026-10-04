//go:build windows

package codingagent

import "os"

func stdinIsATTYImpl() bool {
	return fileIsATTYImpl(os.Stdin)
}

func stdoutIsATTYImpl() bool {
	return fileIsATTYImpl(os.Stdout)
}

func fileIsATTYImpl(file *os.File) bool {
	stat, err := file.Stat()
	if err != nil {
		return false
	}
	if stat.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	// Exclude char devices that are not terminals (e.g., NUL).
	name := stat.Name()
	return name != "null" && name != "nul" && name != ""
}
