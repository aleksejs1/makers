package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// OTP is a one-time login code (only its hash is stored). A code belongs to
// an email address and to the browser that requested it, identified by the
// hash of a random nonce kept in that browser's cookie.
type OTP struct {
	ID        int64
	Email     string
	NonceHash string
	CodeHash  string
	Attempts  int
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Errors returned by CreateOTP.
var (
	// ErrOTPCooldown means this browser got a code for the address recently.
	ErrOTPCooldown = errors.New("login code requested too recently")
	// ErrOTPLimit means the address received too many codes in the last hour.
	ErrOTPLimit = errors.New("too many login codes for this address")
)

// OTPLimits bounds how often codes are issued.
type OTPLimits struct {
	Cooldown time.Duration // per address and browser
	PerHour  int           // per address, against mail flooding
}

// CreateOTP stores a new, still pending code and returns its id. The limits
// are checked and the row inserted in one write transaction, so concurrent
// requests cannot exceed them. Pending codes count towards the limits but
// are not usable until ActivateOTP marks them as sent.
func (s *Store) CreateOTP(ctx context.Context, email, nonceHash, codeHash string, ttl time.Duration, lim OTPLimits) (int64, error) {
	now := s.now()
	tx, err := s.db.BeginTx(ctx, nil) // BEGIN IMMEDIATE: serializes writers
	if err != nil {
		return 0, err
	}
	defer rollback(tx)

	var recent int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM otp_codes WHERE email = ? AND nonce_hash = ? AND created_at > ?`,
		email, nonceHash, fmtTime(now.Add(-lim.Cooldown))).Scan(&recent); err != nil {
		return 0, err
	}
	if recent > 0 {
		return 0, ErrOTPCooldown
	}
	var hourly int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM otp_codes WHERE email = ? AND created_at > ?`,
		email, fmtTime(now.Add(-time.Hour))).Scan(&hourly); err != nil {
		return 0, err
	}
	if hourly >= lim.PerHour {
		return 0, ErrOTPLimit
	}

	res, err := tx.ExecContext(ctx,
		"INSERT INTO otp_codes (email, nonce_hash, code_hash, expires_at, created_at) VALUES (?, ?, ?, ?, ?)",
		email, nonceHash, codeHash, fmtTime(now.Add(ttl)), fmtTime(now))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// ActivateOTP marks a code as sent, making it the active code for its
// address and browser, and invalidates that browser's older codes. Codes
// requested from other browsers are not affected.
func (s *Store) ActivateOTP(ctx context.Context, id int64) error {
	now := fmtTime(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx, "UPDATE otp_codes SET sent_at = ? WHERE id = ?", now, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE otp_codes SET used_at = ?
		 WHERE used_at IS NULL AND sent_at IS NOT NULL AND id < ?
		 AND (email, nonce_hash) = (SELECT email, nonce_hash FROM otp_codes WHERE id = ?)`, now, id, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteOTP removes a code, e.g. when its email could not be sent.
func (s *Store) DeleteOTP(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM otp_codes WHERE id = ?", id)
	return err
}

// ActiveOTP returns the newest sent, unused and unexpired code for the
// address and browser.
func (s *Store) ActiveOTP(ctx context.Context, email, nonceHash string) (*OTP, error) {
	var o OTP
	var exp, created string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, nonce_hash, code_hash, attempts, expires_at, created_at FROM otp_codes
		 WHERE email = ? AND nonce_hash = ? AND used_at IS NULL AND sent_at IS NOT NULL AND expires_at > ?
		 ORDER BY id DESC LIMIT 1`, email, nonceHash, fmtTime(s.now())).
		Scan(&o.ID, &o.Email, &o.NonceHash, &o.CodeHash, &o.Attempts, &exp, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	o.ExpiresAt = parseTime(exp)
	o.CreatedAt = parseTime(created)
	return &o, nil
}

// ClaimOTPAttempt atomically reserves one verification attempt for a code.
// It reports false when the code is used, expired or out of attempts. The
// attempt is reserved before the guess is checked, so concurrent requests
// can never test more than maxAttempts guesses against one code.
func (s *Store) ClaimOTPAttempt(ctx context.Context, id int64, maxAttempts int) (attempt int, ok bool, err error) {
	err = s.db.QueryRowContext(ctx,
		`UPDATE otp_codes SET attempts = attempts + 1
		 WHERE id = ? AND used_at IS NULL AND sent_at IS NOT NULL AND expires_at > ? AND attempts < ?
		 RETURNING attempts`, id, fmtTime(s.now()), maxAttempts).Scan(&attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return attempt, err == nil, err
}

// UseOTP marks a code as used. It reports false if it was already used.
func (s *Store) UseOTP(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		"UPDATE otp_codes SET used_at = ? WHERE id = ? AND used_at IS NULL", fmtTime(s.now()), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// CreateSession stores a session token hash for user.
func (s *Store) CreateSession(ctx context.Context, tokenHash string, userID int64, ttl time.Duration) error {
	now := s.now()
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO sessions (token_hash, user_id, expires_at, created_at, last_seen_at) VALUES (?, ?, ?, ?, ?)",
		tokenHash, userID, fmtTime(now.Add(ttl)), fmtTime(now), fmtTime(now))
	return err
}

// SessionUser returns the active, non-banned user owning the session,
// and when the session was last seen.
func (s *Store) SessionUser(ctx context.Context, tokenHash string) (*User, time.Time, error) {
	var lastSeen string
	row := s.db.QueryRowContext(ctx,
		`SELECT s.last_seen_at, u.id, u.email, u.handle, u.name, u.bio, u.website, u.github, u.social, u.banned_at, u.created_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ? AND s.expires_at > ? AND u.banned_at IS NULL`,
		tokenHash, fmtTime(s.now()))
	var u User
	var banned sql.NullString
	var created string
	err := row.Scan(&lastSeen, &u.ID, &u.Email, &u.Handle, &u.Name, &u.Bio, &u.Website, &u.GitHub, &u.Social, &banned, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, ErrNotFound
	}
	if err != nil {
		return nil, time.Time{}, err
	}
	u.CreatedAt = parseTime(created)
	return &u, parseTime(lastSeen), nil
}

// TouchSession extends a session and updates its last-seen time.
func (s *Store) TouchSession(ctx context.Context, tokenHash string, ttl time.Duration) error {
	now := s.now()
	_, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?",
		fmtTime(now), fmtTime(now.Add(ttl)), tokenHash)
	return err
}

// DeleteSession removes a single session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash)
	return err
}

// DeleteUserSessions removes all sessions of a user.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id = ?", userID)
	return err
}
