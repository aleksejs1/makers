package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ToggleLike likes or unlikes a project and returns the new state and count.
func (s *Store) ToggleLike(ctx context.Context, userID, projectID int64) (liked bool, count int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer rollback(tx)
	res, err := tx.ExecContext(ctx, "DELETE FROM likes WHERE user_id = ? AND project_id = ?", userID, projectID)
	if err != nil {
		return false, 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO likes (user_id, project_id, created_at) VALUES (?, ?, ?)",
			userID, projectID, fmtTime(s.now())); err != nil {
			return false, 0, err
		}
		liked = true
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM likes WHERE project_id = ?", projectID).Scan(&count); err != nil {
		return false, 0, err
	}
	return liked, count, tx.Commit()
}

// Comment is a comment on a project.
type Comment struct {
	ID         int64
	ProjectID  int64
	UserID     int64
	UserHandle string
	UserName   string
	Body       string
	HiddenAt   *time.Time
	CreatedAt  time.Time
}

const commentSelect = `SELECT c.id, c.project_id, c.user_id, u.handle, u.name, c.body, c.hidden_at, c.created_at
	FROM comments c JOIN users u ON u.id = c.user_id`

func scanComment(row scanner) (*Comment, error) {
	var c Comment
	var hidden sql.NullString
	var created string
	err := row.Scan(&c.ID, &c.ProjectID, &c.UserID, &c.UserHandle, &c.UserName, &c.Body, &hidden, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.HiddenAt = parseNullTime(hidden)
	c.CreatedAt = parseTime(created)
	return &c, nil
}

// ListComments returns a project's comments, oldest first. Hidden comments
// and comments of banned users are included only if includeHidden is set.
func (s *Store) ListComments(ctx context.Context, projectID int64, includeHidden bool) ([]*Comment, error) {
	q := commentSelect + " WHERE c.project_id = ?"
	if !includeHidden {
		q += " AND c.hidden_at IS NULL AND u.banned_at IS NULL"
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY c.created_at, c.id", projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CommentByID returns a single comment.
func (s *Store) CommentByID(ctx context.Context, id int64) (*Comment, error) {
	return scanComment(s.db.QueryRowContext(ctx, commentSelect+" WHERE c.id = ?", id))
}

// CreateComment adds a comment to a project.
func (s *Store) CreateComment(ctx context.Context, projectID, userID int64, body string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO comments (project_id, user_id, body, created_at) VALUES (?, ?, ?, ?)",
		projectID, userID, body, fmtTime(s.now()))
	return err
}

// CountUserCommentsSince counts comments by a user since t (rate limiting).
func (s *Store) CountUserCommentsSince(ctx context.Context, userID int64, t time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM comments WHERE user_id = ? AND created_at >= ?", userID, fmtTime(t)).Scan(&n)
	return n, err
}

// CountUserProjectsSince counts projects created by a user since t.
func (s *Store) CountUserProjectsSince(ctx context.Context, userID int64, t time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM projects WHERE owner_id = ? AND created_at >= ?", userID, fmtTime(t)).Scan(&n)
	return n, err
}

// DeleteComment removes a comment.
func (s *Store) DeleteComment(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM comments WHERE id = ?", id)
	return err
}

// SetCommentHidden hides or unhides a comment (moderation).
func (s *Store) SetCommentHidden(ctx context.Context, id int64, hidden bool) error {
	var at any
	if hidden {
		at = fmtTime(s.now())
	}
	_, err := s.db.ExecContext(ctx, "UPDATE comments SET hidden_at = ? WHERE id = ?", at, id)
	return err
}
