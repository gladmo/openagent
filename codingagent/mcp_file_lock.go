package codingagent

// Cross-process file locking for MCP credential storage and refreshes:
// an O_EXCL lock file with mtime staleness takeover. A lock whose holder
// stopped touching it (the process was killed) is taken over after
// staleAfter; a live holder is waited out up to waitTimeout. This is the
// Go shape of proper-lockfile's mkdir locks (documented deviation:
// single-host locking, no renewal timer).

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// withExclusiveFileLock runs fn while holding an exclusive lock on path.
// Callers in the same process wait each other out through the same file
// lock, so nesting the same path deadlocks until the wait timeout.
func withExclusiveFileLock(path string, staleAfter, waitTimeout time.Duration, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lockPath := path + ".lock"
	deadline := time.Now().Add(waitTimeout)
	backoff := 25 * time.Millisecond
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = file.WriteString(strconv.Itoa(os.Getpid()))
			_ = file.Close()
			break
		}
		if info, statErr := os.Stat(lockPath); statErr == nil {
			if time.Since(info.ModTime()) > staleAfter {
				// Stale: the holder died without releasing it.
				_ = os.Remove(lockPath)
				continue
			}
		} else {
			// Removed between open and stat; retry at once.
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("could not acquire lock %s within %s", lockPath, waitTimeout)
		}
		time.Sleep(backoff)
		if backoff < 200*time.Millisecond {
			backoff *= 2
		}
	}
	defer os.Remove(lockPath)
	return fn()
}

// touchLockFile refreshes a held lock's staleness window.
func touchLockFile(path string) {
	if info, err := os.Stat(path + ".lock"); err == nil {
		_ = os.Chtimes(path+".lock", time.Now(), time.Now())
		_ = info
	}
}
