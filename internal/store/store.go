// Package store implements data access on top of SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// ErrHandleTaken is returned when a user handle is already in use.
var ErrHandleTaken = errors.New("handle taken")

// Store provides access to all persisted data.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// DB returns the underlying database handle.
func (s *Store) DB() *sql.DB { return s.db }

// Ping checks database connectivity.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// OTPRetention is how long issued one-time codes are kept. It must be
// longer than any window used to rate-limit code requests.
const OTPRetention = 24 * time.Hour

// Cleanup removes old one-time codes and expired sessions.
func (s *Store) Cleanup(ctx context.Context) error {
	now := s.now()
	if _, err := s.db.ExecContext(ctx,
		"DELETE FROM otp_codes WHERE created_at < ?", fmtTime(now.Add(-OTPRetention))); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", fmtTime(now))
	return err
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func parseNullTime(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	t := parseTime(s.String)
	return &t
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// rollback aborts a transaction; it is a no-op after a successful Commit.
func rollback(tx *sql.Tx) { _ = tx.Rollback() }
