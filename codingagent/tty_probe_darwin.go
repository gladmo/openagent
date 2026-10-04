//go:build darwin && !windows

package codingagent

const ttyTcGets = 0x40487413 // TIOCGETA
