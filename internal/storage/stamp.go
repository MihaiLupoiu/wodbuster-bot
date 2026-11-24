package storage

import (
	"sync/atomic"
	"time"
)

// lastSessionStamp is the last updated_at handed to a session write, in Unix
// milliseconds.
var lastSessionStamp atomic.Int64

// sessionStamp is the updated_at of a session write: now, to the millisecond
// MongoDB stores, and always later than the one before it.
//
// ReplaceSession and DeleteSessionIfUnchanged tell writes apart by
// updated_at alone. Two writes in the same millisecond would get the same
// one, and a write based on the first would land on the second: a refresh
// overwriting the /login that came just after its read. The bot is the only
// writer of sessions, so one counter in the process is enough.
func sessionStamp() time.Time {
	now := time.Now().UnixMilli()
	for {
		last := lastSessionStamp.Load()
		next := max(now, last+1)
		if lastSessionStamp.CompareAndSwap(last, next) {
			return time.UnixMilli(next)
		}
	}
}
