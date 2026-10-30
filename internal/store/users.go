package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aleksejs1/makers/internal/slug"
)

// User is a registered member.
type User struct {
	ID        int64
	Email     string
	Handle    string
	Name      string
	Bio       string
	Website   string
	GitHub    string
	Social    string
	BannedAt  *time.Time
	CreatedAt time.Time

	// IsAdmin is derived from configuration, not stored.
	IsAdmin bool
}

const userCols = "id, email, handle, name, bio, website, github, social, banned_at, created_at"

type scanner interface{ Scan(dest ...any) error }

func scanUser(row scanner) (*User, error) {
	var u User
	var banned sql.NullString
	var created string
	err := row.Scan(&u.ID, &u.Email, &u.Handle, &u.Name, &u.Bio, &u.Website, &u.GitHub, &u.Social, &banned, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.BannedAt = parseNullTime(banned)
	u.CreatedAt = parseTime(created)
	return &u, nil
}

// UserByID returns the user with the given id.
func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE id = ?", id))
}

// UserByEmail returns the user with the given normalized email.
func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE email = ?", email))
}

// UserByHandle returns the user with the given handle (case-insensitive).
func (s *Store) UserByHandle(ctx context.Context, handle string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, "SELECT "+userCols+" FROM users WHERE handle = ?", handle))
}

// CreateUser registers a new user with a random handle; the user picks a
// real name and handle in the settings afterwards.
func (s *Store) CreateUser(ctx context.Context, email string) (*User, error) {
	now := fmtTime(s.now())
	for range 10 {
		handle := slug.RandomHandle()
		res, err := s.db.ExecContext(ctx,
			"INSERT INTO users (email, handle, name, created_at) VALUES (?, ?, ?, ?)",
			email, handle, handle, now)
		if isUniqueViolation(err) {
			if _, e := s.UserByEmail(ctx, email); e == nil {
				return nil, fmt.Errorf("user %s already exists", email)
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		return s.UserByID(ctx, id)
	}
	return nil, errors.New("could not allocate a unique handle")
}

// UpdateProfile saves editable profile fields.
func (s *Store) UpdateProfile(ctx context.Context, u *User) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE users SET handle = ?, name = ?, bio = ?, website = ?, github = ?, social = ? WHERE id = ?",
		u.Handle, u.Name, u.Bio, u.Website, u.GitHub, u.Social, u.ID)
	if isUniqueViolation(err) {
		return ErrHandleTaken
	}
	return err
}

// DeleteUser removes a user together with their projects, likes, comments
// and sessions.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM projects_fts WHERE rowid IN (SELECT id FROM projects WHERE owner_id = ?)", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetBanned bans or unbans a user. Banning also ends all their sessions.
func (s *Store) SetBanned(ctx context.Context, id int64, banned bool) error {
	var at any
	if banned {
		at = fmtTime(s.now())
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE users SET banned_at = ? WHERE id = ?", at, id); err != nil {
		return err
	}
	if banned {
		return s.DeleteUserSessions(ctx, id)
	}
	return nil
}

// AllUserHandles lists handles of active users (for the sitemap).
func (s *Store) AllUserHandles(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT handle FROM users u WHERE banned_at IS NULL
		 AND EXISTS (SELECT 1 FROM projects p WHERE p.owner_id = u.id AND p.hidden_at IS NULL)
		 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
