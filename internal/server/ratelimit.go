package server

import (
	"sync"
	"time"
)

// maxLimiterKeys bounds the limiter's memory. When it is reached, expired
// entries are pruned (at most every pruneInterval); if the map is still
// full, requests with new keys are allowed without being tracked. Failing
// open keeps the site usable during a flood of distinct keys; the limits
// that protect accounts and mailboxes live in the database and still apply.
const (
	maxLimiterKeys = 100_000
	pruneInterval  = 10 * time.Second
)

// limiter is an in-memory sliding-window rate limiter. It approximates a
// true sliding window by weighting the previous window's count, so bursts
// around a window boundary cannot exceed the limit. It is good enough for a
// single-instance deployment.
type limiter struct {
	mu        sync.Mutex
	windows   map[string]*window
	now       func() time.Time
	lastPrune time.Time
	untracked int // requests allowed untracked because the map was full
}

type window struct {
	start  time.Time // start of the current window
	period time.Duration
	prev   int // hits in the previous window
	curr   int // hits in the current window
}

func newLimiter() *limiter {
	return &limiter{windows: map[string]*window{}, now: time.Now}
}

// allow records a hit for key and reports whether it is within max hits
// per period.
func (l *limiter) allow(key string, max int, period time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	w, ok := l.windows[key]
	if !ok {
		if len(l.windows) >= maxLimiterKeys {
			if now.Sub(l.lastPrune) >= pruneInterval {
				l.pruneLocked(now)
			}
			if len(l.windows) >= maxLimiterKeys {
				l.untracked++
				return true
			}
		}
		w = &window{start: now, period: period}
		l.windows[key] = w
	}
	elapsed := now.Sub(w.start)
	switch {
	case elapsed >= 2*period:
		w.start, w.prev, w.curr, elapsed = now, 0, 0, 0
	case elapsed >= period:
		w.start, w.prev, w.curr = w.start.Add(period), w.curr, 0
		elapsed -= period
	}
	weight := 1 - float64(elapsed)/float64(period)
	if float64(w.prev)*weight+float64(w.curr) >= float64(max) {
		return false
	}
	w.curr++
	return true
}

// cleanup prunes expired windows and returns how many requests were
// allowed untracked since the previous call.
func (l *limiter) cleanup() (untracked int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(l.now())
	untracked, l.untracked = l.untracked, 0
	return untracked
}

// pruneLocked drops windows that no longer affect any decision.
func (l *limiter) pruneLocked(now time.Time) {
	l.lastPrune = now
	for k, w := range l.windows {
		if now.Sub(w.start) >= 2*w.period {
			delete(l.windows, k)
		}
	}
}
