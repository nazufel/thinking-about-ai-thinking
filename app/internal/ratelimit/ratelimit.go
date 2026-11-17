// Package ratelimit caps how many requests each caller may make per minute.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a token bucket per key. A nil Limiter allows everything.
type Limiter struct {
	mu        sync.Mutex
	perMinute float64
	now       func() time.Time
	buckets   map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a Limiter that admits perMinute requests per key each minute,
// with bursts of up to the same number. It returns nil when perMinute is not
// positive, which turns limiting off.
func New(perMinute int) *Limiter {
	if perMinute <= 0 {
		return nil
	}
	return &Limiter{
		perMinute: float64(perMinute),
		now:       time.Now,
		buckets:   make(map[string]*bucket),
	}
}

// SetClock replaces the limiter's time source.
func (l *Limiter) SetClock(now func() time.Time) {
	if l != nil {
		l.now = now
	}
}

// Allow reports whether the caller identified by key may make a request now.
func (l *Limiter) Allow(key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.perMinute, last: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Minutes()
	if elapsed > 0 {
		b.tokens += elapsed * l.perMinute
		if b.tokens > l.perMinute {
			b.tokens = l.perMinute
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
