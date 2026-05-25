package smtp

import (
	"sync"
	"time"
)

const domainCacheTTL = 30 * time.Second

// domainCache is a tiny in-process cache of domain enabled/disabled state,
// keyed by lowercased domain name. It bounds how often inbound mail triggers a
// database lookup while keeping staleness short (domainCacheTTL), so domains
// added or disabled via the admin UI take effect within the TTL without a
// restart. Same in-process, single-server scope as the rate limiter.
type domainCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	now     func() time.Time
	entries map[string]domainCacheEntry
}

type domainCacheEntry struct {
	enabled   bool
	expiresAt time.Time
}

func newDomainCache(ttl time.Duration) *domainCache {
	return &domainCache{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]domainCacheEntry),
	}
}

// get returns the cached enabled state for name. ok is false when there is no
// entry or it has expired, in which case the caller should consult the source
// of truth and call set.
func (c *domainCache) get(name string) (enabled bool, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, found := c.entries[name]
	if !found || !c.now().Before(entry.expiresAt) {
		return false, false
	}
	return entry.enabled, true
}

func (c *domainCache) set(name string, enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[name] = domainCacheEntry{
		enabled:   enabled,
		expiresAt: c.now().Add(c.ttl),
	}
}
