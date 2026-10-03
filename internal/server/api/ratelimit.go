package api

import (
	"sync"
	"time"
)

// failureLimiter counts failures per key inside a sliding window. It backs the login and pairing-code endpoints,
// where the only thing worth limiting is guessing. Successes are not counted, so normal use never gets blocked.
type failureLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func newFailureLimiter(limit int, window time.Duration) *failureLimiter {
	return &failureLimiter{limit: limit, window: window, hits: map[string][]time.Time{}}
}

// prune drops entries older than the window. Caller holds the lock.
func (l *failureLimiter) prune(key string, now time.Time) []time.Time {
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.hits, key)
		return nil
	}
	l.hits[key] = kept
	return kept
}

// blocked reports whether key has used up its failures, and how long until it may try again.
func (l *failureLimiter) blocked(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	hits := l.prune(key, now)
	if len(hits) < l.limit {
		return false, 0
	}
	return true, l.window - now.Sub(hits[len(hits)-l.limit])
}

func (l *failureLimiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.hits) > 10_000 { // bound memory when someone sprays many addresses
		for k := range l.hits {
			l.prune(k, now)
		}
	}
	l.hits[key] = append(l.prune(key, now), now)
}

func (l *failureLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
