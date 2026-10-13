package api

import (
	"sync"
	"time"
)

// rateLimiter is a per-key token bucket. Idle buckets are pruned lazily so
// memory stays bounded.
type rateLimiter struct {
	rate  float64 // tokens added per second
	burst float64

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastPrune time.Time
	now       func() time.Time // replaceable in tests
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(ratePerSec float64, burst int) *rateLimiter {
	return &rateLimiter{
		rate:    ratePerSec,
		burst:   float64(burst),
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// allow takes one token for key. When the bucket is empty it returns how long
// until a token is available.
func (l *rateLimiter) allow(key string) (retryAfter time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.pruneLocked(now)

	b, exists := l.buckets[key]
	if !exists {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return 0, true
	}
	missing := 1 - b.tokens
	return time.Duration(missing / l.rate * float64(time.Second)), false
}

// pruneLocked drops buckets that have been idle long enough to be full again.
func (l *rateLimiter) pruneLocked(now time.Time) {
	if now.Sub(l.lastPrune) < time.Minute {
		return
	}
	l.lastPrune = now
	idle := time.Duration(l.burst/l.rate*float64(time.Second)) + time.Minute
	for k, b := range l.buckets {
		if now.Sub(b.last) > idle {
			delete(l.buckets, k)
		}
	}
}
