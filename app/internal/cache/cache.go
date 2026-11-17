// Package cache remembers recent decisions so repeated requests for the same
// image do not have to be evaluated again.
package cache

import (
	"sync"
	"time"

	"gatekeeper/internal/attest"
	"gatekeeper/internal/policy"
	"gatekeeper/internal/ref"
)

// Cache is a time-limited store of decisions. The zero TTL disables it.
type Cache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[string]entry
}

type entry struct {
	decision policy.Decision
	expires  time.Time
}

// New returns a Cache whose entries live for ttl.
func New(ttl time.Duration) *Cache {
	return &Cache{ttl: ttl, now: time.Now, entries: make(map[string]entry)}
}

// SetClock replaces the cache's time source.
func (c *Cache) SetClock(now func() time.Time) {
	c.now = now
}

// Key builds the cache key for a decision request. Requests that carry
// different attestations are kept apart.
func Key(r ref.Reference, att *attest.Attestation) string {
	key := r.String()
	if att != nil {
		key += "#" + att.Builder + ":" + att.Signature
	}
	return key
}

// Get returns the cached decision for key, if there is a live one.
func (c *Cache) Get(key string) (policy.Decision, bool) {
	if c.ttl <= 0 {
		return policy.Decision{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return policy.Decision{}, false
	}
	if !c.now().Before(e.expires) {
		delete(c.entries, key)
		return policy.Decision{}, false
	}
	return e.decision, true
}

// Put stores a decision under key.
func (c *Cache) Put(key string, d policy.Decision) {
	if c.ttl <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry{decision: d, expires: c.now().Add(c.ttl)}
}

// Flush drops every cached decision.
func (c *Cache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]entry)
}

// Len returns the number of stored decisions, including expired ones that
// have not been evicted yet.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
