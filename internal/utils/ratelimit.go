package utils

import (
	"sync"
	"time"
)

// RateLimiter implements a token bucket rate limiter
type RateLimiter struct {
	rate     time.Duration
	capacity int
	buckets  map[int64]*bucket
	mu       sync.RWMutex
}

type bucket struct {
	tokens   int
	lastTime time.Time
	mu       sync.Mutex
}

// NewRateLimiter creates a new rate limiter
// rate: how often tokens are refilled
// capacity: maximum number of tokens in bucket
func NewRateLimiter(rate time.Duration, capacity int) *RateLimiter {
	return &RateLimiter{
		rate:     rate,
		capacity: capacity,
		buckets:  make(map[int64]*bucket),
	}
}

// Allow checks if the given user ID is allowed to proceed
func (rl *RateLimiter) Allow(userID int64) bool {
	rl.mu.RLock()
	b, exists := rl.buckets[userID]
	rl.mu.RUnlock()

	if !exists {
		rl.mu.Lock()
		// Double-check after acquiring write lock
		if b, exists = rl.buckets[userID]; !exists {
			b = &bucket{
				tokens:   rl.capacity,
				lastTime: time.Now(),
			}
			rl.buckets[userID] = b
		}
		rl.mu.Unlock()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	// Calculate tokens to add based on elapsed time
	elapsed := now.Sub(b.lastTime)
	tokensToAdd := int(elapsed / rl.rate)

	if tokensToAdd > 0 {
		b.tokens = min(rl.capacity, b.tokens+tokensToAdd)
		b.lastTime = now
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}

	return false
}

// Reset resets the rate limiter for a specific user
func (rl *RateLimiter) Reset(userID int64) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.buckets, userID)
}

// Cleanup removes old unused buckets (call periodically)
func (rl *RateLimiter) Cleanup(maxAge time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	for userID, b := range rl.buckets {
		b.mu.Lock()
		if b.lastTime.Before(cutoff) {
			delete(rl.buckets, userID)
		}
		b.mu.Unlock()
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
