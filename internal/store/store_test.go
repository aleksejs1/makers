package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aleksejs1/makers/internal/db"
)

func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	s := New(sqlDB)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return s, &now
}

var testLimits = OTPLimits{Cooldown: time.Minute, PerHour: 30}

// issue creates a code for the address and browser and marks it as sent.
func issue(t *testing.T, s *Store, email, nonce, hash string) int64 {
	t.Helper()
	id, err := s.CreateOTP(context.Background(), email, nonce, hash, 10*time.Minute, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateOTP(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestClaimOTPAttemptIsAtomic(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	id := issue(t, s, "a@b.lv", "n1", "hash")

	const workers, maxAttempts = 20, 5
	var mu sync.Mutex
	granted := 0
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			_, ok, err := s.ClaimOTPAttempt(ctx, id, maxAttempts)
			if err != nil {
				t.Error(err)
				return
			}
			if ok {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if granted != maxAttempts {
		t.Fatalf("granted %d attempts, want exactly %d", granted, maxAttempts)
	}
}

func TestClaimOTPAttemptRejectsUsedAndExpired(t *testing.T) {
	s, now := newStore(t)
	ctx := context.Background()
	used := issue(t, s, "a@b.lv", "n1", "h1")
	if ok, _ := s.UseOTP(ctx, used); !ok {
		t.Fatal("UseOTP failed")
	}
	if _, ok, _ := s.ClaimOTPAttempt(ctx, used, 5); ok {
		t.Error("attempt granted on a used code")
	}
	*now = now.Add(2 * time.Minute)
	expiring := issue(t, s, "a@b.lv", "n1", "h2")
	*now = now.Add(11 * time.Minute)
	if _, ok, _ := s.ClaimOTPAttempt(ctx, expiring, 5); ok {
		t.Error("attempt granted on an expired code")
	}
}

func TestOTPIsBoundToBrowser(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	mine := issue(t, s, "a@b.lv", "mine", "h1")
	// Someone else requesting a code for the same address in another
	// browser neither replaces nor reveals mine.
	theirs := issue(t, s, "a@b.lv", "theirs", "h2")
	if o, err := s.ActiveOTP(ctx, "a@b.lv", "mine"); err != nil || o.ID != mine {
		t.Fatalf("active for my browser = %+v, %v; want my code", o, err)
	}
	if o, err := s.ActiveOTP(ctx, "a@b.lv", "theirs"); err != nil || o.ID != theirs {
		t.Fatalf("active for their browser = %+v, %v; want their code", o, err)
	}
	if _, err := s.ActiveOTP(ctx, "a@b.lv", "nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown browser got a code: %v", err)
	}
}

func TestOTPReplacedOnlyAfterActivation(t *testing.T) {
	s, now := newStore(t)
	ctx := context.Background()
	first := issue(t, s, "a@b.lv", "n1", "h1")

	// A new code whose email failed is deleted; the first one stays valid.
	*now = now.Add(2 * time.Minute)
	failed, err := s.CreateOTP(ctx, "a@b.lv", "n1", "h2", 10*time.Minute, testLimits)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.DeleteOTP(ctx, failed)
	if o, err := s.ActiveOTP(ctx, "a@b.lv", "n1"); err != nil || o.ID != first {
		t.Fatalf("active = %+v, %v; want first code", o, err)
	}

	// While a newer code is still being sent, the delivered one stays active.
	pending, _ := s.CreateOTP(ctx, "a@b.lv", "n1", "h-pending", 10*time.Minute, testLimits)
	if o, err := s.ActiveOTP(ctx, "a@b.lv", "n1"); err != nil || o.ID != first {
		t.Fatalf("active during delivery = %+v, %v; want first code", o, err)
	}
	if _, ok, _ := s.ClaimOTPAttempt(ctx, pending, 5); ok {
		t.Error("attempt granted on an unsent code")
	}

	// Once delivered, it replaces the first one.
	_ = s.ActivateOTP(ctx, pending)
	if o, err := s.ActiveOTP(ctx, "a@b.lv", "n1"); err != nil || o.ID != pending {
		t.Fatalf("active = %+v, %v; want the new code", o, err)
	}
	if _, ok, _ := s.ClaimOTPAttempt(ctx, first, 5); ok {
		t.Error("replaced code is still usable")
	}
}

func TestCreateOTPLimits(t *testing.T) {
	s, now := newStore(t)
	ctx := context.Background()
	lim := OTPLimits{Cooldown: time.Minute, PerHour: 3}

	if _, err := s.CreateOTP(ctx, "a@b.lv", "n1", "h", time.Minute, lim); err != nil {
		t.Fatal(err)
	}
	// Same browser within the cooldown, even while the first is pending.
	if _, err := s.CreateOTP(ctx, "a@b.lv", "n1", "h", time.Minute, lim); !errors.Is(err, ErrOTPCooldown) {
		t.Fatalf("want ErrOTPCooldown, got %v", err)
	}
	// Other browsers are not affected by the cooldown...
	for _, n := range []string{"n2", "n3"} {
		if _, err := s.CreateOTP(ctx, "a@b.lv", n, "h", time.Minute, lim); err != nil {
			t.Fatal(err)
		}
	}
	// ...but the address is capped per hour.
	if _, err := s.CreateOTP(ctx, "a@b.lv", "n4", "h", time.Minute, lim); !errors.Is(err, ErrOTPLimit) {
		t.Fatalf("want ErrOTPLimit, got %v", err)
	}
	*now = now.Add(61 * time.Minute)
	if _, err := s.CreateOTP(ctx, "a@b.lv", "n4", "h", time.Minute, lim); err != nil {
		t.Fatalf("after an hour: %v", err)
	}
}

func TestCreateOTPLimitIsAtomic(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	lim := OTPLimits{Cooldown: time.Minute, PerHour: 5}
	var mu sync.Mutex
	created := 0
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Go(func() {
			_, err := s.CreateOTP(ctx, "a@b.lv", fmt.Sprint("n", i), "h", time.Minute, lim)
			if err == nil {
				mu.Lock()
				created++
				mu.Unlock()
			} else if !errors.Is(err, ErrOTPLimit) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if created != lim.PerHour {
		t.Fatalf("created %d codes concurrently, want exactly %d", created, lim.PerHour)
	}
}

func TestCleanupKeepsCodesInsideRateLimitWindow(t *testing.T) {
	s, now := newStore(t)
	ctx := context.Background()
	lim := OTPLimits{Cooldown: time.Minute, PerHour: 3}
	start := *now
	for i := range 3 {
		if _, err := s.CreateOTP(ctx, "a@b.lv", fmt.Sprint("n", i), "h", 10*time.Minute, lim); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(time.Minute)
	}

	// The codes have expired, but must still count for rate limiting.
	*now = start.Add(30 * time.Minute)
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOTP(ctx, "a@b.lv", "n9", "h", 10*time.Minute, lim); !errors.Is(err, ErrOTPLimit) {
		t.Fatalf("after cleanup: want ErrOTPLimit, got %v", err)
	}

	*now = start.Add(OTPRetention + time.Hour)
	if err := s.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM otp_codes").Scan(&n)
	if n != 0 {
		t.Fatalf("%d codes left after retention, want 0", n)
	}
}

func TestPopularTagsSkipBannedOwners(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	good, _ := s.CreateUser(ctx, "good@b.lv")
	bad, _ := s.CreateUser(ctx, "bad@b.lv")
	in := ProjectInput{Name: "P", Tagline: "t", Description: "d", URL: "https://p.lv",
		Status: "idea", Category: "ai", ContentLang: "en"}
	in.Tech = []string{"Go"}
	if _, err := s.CreateProject(ctx, good.ID, in); err != nil {
		t.Fatal(err)
	}
	in.Name, in.Tech = "Spam", []string{"spamtool"}
	if _, err := s.CreateProject(ctx, bad.ID, in); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBanned(ctx, bad.ID, true); err != nil {
		t.Fatal(err)
	}
	tags, err := s.PopularTags(ctx, TagTech, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0] != "Go" {
		t.Fatalf("tags = %v, want [Go]", tags)
	}
}

func TestNewUsersGetRandomHandles(t *testing.T) {
	s, _ := newStore(t)
	u, err := s.CreateUser(context.Background(), "jane.ozola@company.lv")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{u.Handle, u.Name} {
		if strings.Contains(v, "jane") || strings.Contains(v, "ozola") {
			t.Fatalf("public profile leaks the email: handle=%q name=%q", u.Handle, u.Name)
		}
	}
}
