package security

import (
	"sync"
	"time"
)

type FailureLimiter struct {
	mu          sync.Mutex
	now         func() time.Time
	maxFailures int
	window      time.Duration
	block       time.Duration
	entries     map[string]failureEntry
}

type failureEntry struct {
	windowStart  time.Time
	blockedUntil time.Time
	lastSeen     time.Time
	failures     int
}

func NewFailureLimiter(maxFailures int, window, block time.Duration, now func() time.Time) *FailureLimiter {
	if now == nil {
		now = time.Now
	}
	return &FailureLimiter{now: now, maxFailures: maxFailures, window: window, block: block, entries: map[string]failureEntry{}}
}

func (l *FailureLimiter) Allow(keys ...string) (bool, time.Duration) {
	if l == nil || l.maxFailures <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.cleanup(now)
	var retryAfter time.Duration
	for _, key := range keys {
		entry := l.entries[key]
		if entry.blockedUntil.After(now) && entry.blockedUntil.Sub(now) > retryAfter {
			retryAfter = entry.blockedUntil.Sub(now)
		}
	}
	return retryAfter == 0, retryAfter
}

func (l *FailureLimiter) Failure(keys ...string) {
	if l == nil || l.maxFailures <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for _, key := range keys {
		entry := l.entries[key]
		if entry.windowStart.IsZero() || now.Sub(entry.windowStart) >= l.window {
			entry.windowStart = now
			entry.failures = 0
		}
		entry.failures++
		entry.lastSeen = now
		if entry.failures >= l.maxFailures {
			entry.blockedUntil = now.Add(l.block)
		}
		l.entries[key] = entry
	}
}

func (l *FailureLimiter) Success(keys ...string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		delete(l.entries, key)
	}
}

func (l *FailureLimiter) cleanup(now time.Time) {
	retention := l.window + l.block
	for key, entry := range l.entries {
		if !entry.lastSeen.IsZero() && now.Sub(entry.lastSeen) > retention && !entry.blockedUntil.After(now) {
			delete(l.entries, key)
		}
	}
}
