package ratelimit

import (
	"sync"
	"time"
)

type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
	Remaining  int
}

type FixedWindowLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	entries map[string]entry
}

type entry struct {
	windowStart time.Time
	count       int
}

func NewFixedWindowLimiter(limit int, window time.Duration) *FixedWindowLimiter {
	return &FixedWindowLimiter{
		limit:   limit,
		window:  window,
		now:     time.Now,
		entries: make(map[string]entry),
	}
}

func (l *FixedWindowLimiter) Allow(key string) Decision {
	if l == nil || l.limit <= 0 || l.window <= 0 {
		return Decision{Allowed: true}
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	current := l.entries[key]
	if current.windowStart.IsZero() || now.Sub(current.windowStart) >= l.window {
		current = entry{
			windowStart: now,
			count:       0,
		}
	}

	if current.count >= l.limit {
		retryAfter := l.window - now.Sub(current.windowStart)
		if retryAfter < 0 {
			retryAfter = 0
		}
		return Decision{
			Allowed:    false,
			RetryAfter: retryAfter,
			Remaining:  0,
		}
	}

	current.count++
	l.entries[key] = current

	return Decision{
		Allowed:   true,
		Remaining: l.limit - current.count,
	}
}
