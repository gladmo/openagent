// trajectory/util.go: clock and identity helpers.
package trajectory

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

func unixMilliNow() int64 { return time.Now().UnixMilli() }

// generateID mints a random session id ("traj-" + 12 hex chars). The
// trajectory is an observation artifact, not a durable session: ids need
// uniqueness, not ordering (callers supply ULID-style ids when they want
// time-sortable logs).
func generateID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		// Fall back to the clock: uniqueness within one process suffices
		// for the degraded case and the value stays printable.
		return "traj-" + hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))
	}
	return "traj-" + hex.EncodeToString(buf)
}
