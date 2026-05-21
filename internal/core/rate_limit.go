package core

import (
	"sync"
	"time"
)

// RateLimiter is a simple, thread-safe token bucket rate limiter.
type RateLimiter struct {
	mu         sync.Mutex
	limit      float64
	burst      int
	tokens     float64
	lastRefill time.Time
}

// NewRateLimiter creates a new RateLimiter.
// If limit <= 0, it represents unlimited rate.
func NewRateLimiter(limit float64, burst int) *RateLimiter {
	return &RateLimiter{
		limit:      limit,
		burst:      burst,
		tokens:     float64(burst),
		lastRefill: time.Now(),
	}
}

// Allow checks if a request is allowed by consuming a token.
func (rl *RateLimiter) Allow() bool {
	if rl.limit <= 0 {
		return true
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.lastRefill = now

	rl.tokens += elapsed * rl.limit
	if rl.tokens > float64(rl.burst) {
		rl.tokens = float64(rl.burst)
	}

	if rl.tokens >= 1.0 {
		rl.tokens -= 1.0
		return true
	}

	return false
}
