package wodbuster

import (
	"context"
	"time"
)

// Clock is the time source. It exists so callers can inject a fake in tests and
// a server-synchronised one in production.
type Clock interface {
	Now() time.Time
}

// SystemClock is the machine's own clock. Correct for everything except racing
// a remote server.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

// OffsetClock is the local clock plus a fixed correction.
type OffsetClock struct {
	Offset time.Duration
}

func (c OffsetClock) Now() time.Time { return time.Now().Add(c.Offset) }

// NewServerClock measures how far the local clock is from the server's and
// returns a Clock that corrects for it.
//
// Why bother: the machine's clock drifts, and a booking window is won or lost in
// milliseconds. A measured 0.6 s of local drift is enough to start polling after
// the places are gone.
//
// The Date header only has one-second resolution and is truncated downwards, so
// this takes several samples, keeps the one with the lowest round trip (least
// uncertainty about when the reading was taken) and adds 500 ms to cancel the
// average truncation bias.
func NewServerClock(ctx context.Context, c *Client, samples int) (Clock, error) {
	if samples < 1 {
		samples = 1
	}

	var (
		bestRTT    = time.Duration(1<<63 - 1)
		bestOffset time.Duration
		lastErr    error
		got        bool
	)

	for i := 0; i < samples; i++ {
		start := time.Now()
		serverTime, err := c.ServerTime(ctx)
		if err != nil {
			lastErr = err
			continue
		}
		rtt := time.Since(start)
		if rtt < bestRTT {
			bestRTT = rtt
			midpoint := start.Add(rtt / 2)
			bestOffset = serverTime.Add(500 * time.Millisecond).Sub(midpoint)
			got = true
		}
		if i < samples-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(120 * time.Millisecond):
			}
		}
	}

	if !got {
		return nil, lastErr
	}
	return OffsetClock{Offset: bestOffset}, nil
}
