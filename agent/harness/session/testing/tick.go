package sessiontesting

import "time"

// waitTick yields briefly for polling waits.
func waitTick() { time.Sleep(time.Millisecond) }
