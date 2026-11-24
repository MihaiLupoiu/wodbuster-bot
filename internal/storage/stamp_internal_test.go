package storage

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Many writes inside one millisecond, as on a fast CI runner, still get
// distinct, increasing stamps at Mongo's precision: the conditional writes
// tell sessions apart by updated_at alone.
func TestSessionStampsNeverRepeat(t *testing.T) {
	const n = 1000
	stamps := make([]time.Time, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() { stamps[i] = sessionStamp() })
	}
	wg.Wait()

	seen := make(map[int64]bool, n)
	for _, s := range stamps {
		require.Equal(t, s, s.Truncate(time.Millisecond), "whole milliseconds, as Mongo stores them")
		assert.False(t, seen[s.UnixMilli()], "repeated stamp %v", s)
		seen[s.UnixMilli()] = true
	}

	a, b := sessionStamp(), sessionStamp()
	assert.True(t, b.After(a))
}
