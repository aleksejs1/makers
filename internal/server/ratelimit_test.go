package server

import (
	"fmt"
	"testing"
	"time"
)

func fakeLimiter() (*limiter, *time.Time) {
	l := newLimiter()
	now := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	return l, &now
}

func hits(l *limiter, key string, n, max int, period time.Duration) int {
	ok := 0
	for range n {
		if l.allow(key, max, period) {
			ok++
		}
	}
	return ok
}

func TestLimiterNoBurstAcrossWindowBoundary(t *testing.T) {
	l, now := fakeLimiter()
	start := *now
	l.allow("k", 10, time.Hour) // opens the window
	*now = start.Add(59 * time.Minute)
	if got := hits(l, "k", 9, 10, time.Hour); got != 9 {
		t.Fatalf("first burst: %d allowed, want 9", got)
	}
	// Just after the window boundary: a fixed window would allow 10 more.
	*now = start.Add(61 * time.Minute)
	if got := hits(l, "k", 10, 10, time.Hour); got > 1 {
		t.Fatalf("burst after boundary: %d allowed, want at most 1", got)
	}
	// Half a window later about half of the budget is back.
	*now = start.Add(91 * time.Minute)
	if got := hits(l, "k", 10, 10, time.Hour); got < 3 || got > 6 {
		t.Fatalf("after half a window: %d allowed, want about 4", got)
	}
	// After two full windows everything is forgotten.
	*now = now.Add(2 * time.Hour)
	if got := hits(l, "k", 10, 10, time.Hour); got != 10 {
		t.Fatalf("after two windows: %d allowed, want 10", got)
	}
}

func TestLimiterPrunesAndBoundsMemory(t *testing.T) {
	l, now := fakeLimiter()
	for i := range 100 {
		l.allow(fmt.Sprint("old-", i), 1, time.Minute)
	}
	*now = now.Add(3 * time.Minute)
	l.cleanup()
	if len(l.windows) != 0 {
		t.Fatalf("%d windows left after cleanup, want 0", len(l.windows))
	}

	for i := range maxLimiterKeys {
		l.windows[fmt.Sprint("stale-", i)] = &window{start: now.Add(-time.Hour), period: time.Minute}
	}
	*now = now.Add(pruneInterval)
	l.allow("fresh", 1, time.Minute)
	if len(l.windows) != 1 {
		t.Fatalf("%d windows after reaching the cap, want stale ones pruned", len(l.windows))
	}
}

func TestLimiterFailsOpenWhenFull(t *testing.T) {
	l, now := fakeLimiter()
	for i := range maxLimiterKeys {
		l.windows[fmt.Sprint("live-", i)] = &window{start: *now, period: time.Hour}
	}
	// A flood of distinct keys must not lock everyone else out.
	if !l.allow("new-key", 10, time.Hour) {
		t.Fatal("new key rejected while the limiter is full")
	}
	if len(l.windows) != maxLimiterKeys {
		t.Fatalf("map grew to %d, want it capped at %d", len(l.windows), maxLimiterKeys)
	}
	// Known keys are still limited.
	if hits(l, "live-1", 20, 10, time.Hour) != 10 {
		t.Fatal("existing key not limited")
	}
	// Prunes are throttled: a burst of new keys does not rescan the map each time.
	before := l.lastPrune
	l.allow("another", 10, time.Hour)
	if l.lastPrune != before {
		t.Fatal("pruned again within pruneInterval")
	}
	if n := l.cleanup(); n != 2 {
		t.Fatalf("cleanup reported %d untracked requests, want 2", n)
	}
}

func TestDateTimeUsesRigaTime(t *testing.T) {
	v := &View{Lang: "en"}
	summer := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if got := v.DateTime(summer); got != "1 Jul 2026 15:00" {
		t.Errorf("summer: %q", got)
	}
	winter := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if got := v.DateTime(winter); got != "1 Jan 2026 14:00" {
		t.Errorf("winter: %q", got)
	}
}

func TestDateUsesRigaDay(t *testing.T) {
	v := &View{Lang: "lv"}
	lateUTC := time.Date(2026, 1, 1, 23, 30, 0, 0, time.UTC) // 01:30 on Jan 2 in Riga
	if got := v.Date(lateUTC); got != "02.01.2026" {
		t.Errorf("Date = %q, want 02.01.2026", got)
	}
}
