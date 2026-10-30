package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/aleksejs1/makers/internal/slug"
)

// Tag kinds.
const (
	TagTech = "tech"
	TagAI   = "ai"
)

// ProjectInput holds the editable fields of a project.
type ProjectInput struct {
	Name         string
	Tagline      string
	Description  string
	URL          string
	GitHubURL    string
	VideoURL     string
	LaunchDate   string // YYYY-MM-DD or empty
	Status       string
	Category     string
	ContentLang  string
	ProductLangs []string
	LookingFor   []string
	Pricing      string
	IsOpenSource bool
	City         string
	Tech         []string
	AI           []string
}

// Project is a listed project with derived data.
type Project struct {
	ProjectInput
	ID          int64
	Slug        string
	OwnerID     int64
	OwnerHandle string
	OwnerName   string
	OwnerBanned bool
	HiddenAt    *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Likes       int
	Comments    int
	Liked       bool // by the viewing user
}

// Launch returns the launch date, or the zero time if unset.
func (p *Project) Launch() time.Time {
	t, _ := time.Parse(time.DateOnly, p.LaunchDate)
	return t
}

// Sort orders for ListProjects.
const (
	SortNew     = "new"
	SortPopular = "popular"
	SortLaunch  = "launch"
)

// ListParams filters and paginates ListProjects.
type ListParams struct {
	Query    string
	Category string
	Status   string
	Tech     string // technology tag
	AI       string // AI tool tag
	OwnerID  int64
	Sort     string
	Limit    int
	Offset   int
	ViewerID int64
	// IncludeHidden also returns hidden projects and projects of banned
	// owners (for owners and admins).
	IncludeHidden bool
}

const projectSelect = `SELECT p.id, p.slug, p.owner_id, u.handle, u.name, u.banned_at IS NOT NULL,
	p.name, p.tagline, p.description, p.url, p.github_url, p.video_url, p.launch_date,
	p.status, p.category, p.content_lang, p.product_langs, p.looking_for, p.pricing,
	p.is_open_source, p.city, p.hidden_at, p.created_at, p.updated_at,
	(SELECT COUNT(*) FROM likes l WHERE l.project_id = p.id) AS likes_count,
	(SELECT COUNT(*) FROM comments c JOIN users cu ON cu.id = c.user_id
	 WHERE c.project_id = p.id AND c.hidden_at IS NULL AND cu.banned_at IS NULL),
	EXISTS (SELECT 1 FROM likes l WHERE l.project_id = p.id AND l.user_id = ?)
	FROM projects p JOIN users u ON u.id = p.owner_id`

func scanProject(row scanner) (*Project, error) {
	var p Project
	var productLangs, lookingFor, created, updated string
	var hidden sql.NullString
	err := row.Scan(&p.ID, &p.Slug, &p.OwnerID, &p.OwnerHandle, &p.OwnerName, &p.OwnerBanned,
		&p.Name, &p.Tagline, &p.Description, &p.URL, &p.GitHubURL, &p.VideoURL, &p.LaunchDate,
		&p.Status, &p.Category, &p.ContentLang, &productLangs, &lookingFor, &p.Pricing,
		&p.IsOpenSource, &p.City, &hidden, &created, &updated,
		&p.Likes, &p.Comments, &p.Liked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.ProductLangs = splitList(productLangs)
	p.LookingFor = splitList(lookingFor)
	p.HiddenAt = parseNullTime(hidden)
	p.CreatedAt = parseTime(created)
	p.UpdatedAt = parseTime(updated)
	return &p, nil
}

// ProjectBySlug returns a project regardless of visibility; callers decide
// whether the viewer may see it.
func (s *Store) ProjectBySlug(ctx context.Context, slugStr string, viewerID int64) (*Project, error) {
	p, err := scanProject(s.db.QueryRowContext(ctx, projectSelect+" WHERE p.slug = ?", viewerID, slugStr))
	if err != nil {
		return nil, err
	}
	if err := s.loadTags(ctx, []*Project{p}); err != nil {
		return nil, err
	}
	return p, nil
}

// ListProjects returns a page of projects and the total number of matches.
func (s *Store) ListProjects(ctx context.Context, lp ListParams) ([]*Project, int, error) {
	var where []string
	var args []any
	if !lp.IncludeHidden {
		where = append(where, "p.hidden_at IS NULL", "u.banned_at IS NULL")
	}
	if lp.OwnerID != 0 {
		where = append(where, "p.owner_id = ?")
		args = append(args, lp.OwnerID)
	}
	if lp.Category != "" {
		where = append(where, "p.category = ?")
		args = append(args, lp.Category)
	}
	if lp.Status != "" {
		where = append(where, "p.status = ?")
		args = append(args, lp.Status)
	}
	for kind, name := range map[string]string{TagTech: lp.Tech, TagAI: lp.AI} {
		if name != "" {
			where = append(where, `p.id IN (SELECT pt.project_id FROM project_tags pt
				JOIN tags t ON t.id = pt.tag_id WHERE t.kind = ? AND t.name = ?)`)
			args = append(args, kind, name)
		}
	}
	if q := ftsQuery(lp.Query); q != "" {
		where = append(where, "p.id IN (SELECT rowid FROM projects_fts WHERE projects_fts MATCH ?)")
		args = append(args, q)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM projects p JOIN users u ON u.id = p.owner_id"+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	order := " ORDER BY p.created_at DESC, p.id DESC"
	switch lp.Sort {
	case SortPopular:
		order = " ORDER BY likes_count DESC, p.created_at DESC, p.id DESC"
	case SortLaunch:
		order = " ORDER BY p.launch_date = '', p.launch_date DESC, p.id DESC"
	}
	if lp.Limit <= 0 {
		lp.Limit = 30
	}
	q := projectSelect + cond + order + " LIMIT ? OFFSET ?"
	all := append([]any{lp.ViewerID}, args...)
	all = append(all, lp.Limit, lp.Offset)

	rows, err := s.db.QueryContext(ctx, q, all...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if err := s.loadTags(ctx, out); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// CreateProject inserts a project owned by ownerID with a unique slug.
func (s *Store) CreateProject(ctx context.Context, ownerID int64, in ProjectInput) (*Project, error) {
	base := slug.Make(in.Name, 50)
	if base == "" {
		base = "project"
	}
	now := fmtTime(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer rollback(tx)

	var id int64
	var slugStr string
	for i := 1; ; i++ {
		slugStr = base
		if i > 1 {
			slugStr = fmt.Sprintf("%s-%d", base, i)
		}
		if i > 100 {
			return nil, errors.New("could not allocate a unique slug")
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO projects (slug, owner_id, name, tagline, description,
			url, github_url, video_url, launch_date, status, category, content_lang, product_langs,
			looking_for, pricing, is_open_source, city, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			slugStr, ownerID, in.Name, in.Tagline, in.Description, in.URL, in.GitHubURL, in.VideoURL,
			in.LaunchDate, in.Status, in.Category, in.ContentLang, strings.Join(in.ProductLangs, ","),
			strings.Join(in.LookingFor, ","), in.Pricing, in.IsOpenSource, in.City, now, now)
		if isUniqueViolation(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		id, _ = res.LastInsertId()
		break
	}
	if err := saveTags(ctx, tx, id, in); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.ProjectBySlug(ctx, slugStr, ownerID)
}

// UpdateProject saves new field values. The slug stays stable.
func (s *Store) UpdateProject(ctx context.Context, id int64, in ProjectInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET name = ?, tagline = ?, description = ?,
		url = ?, github_url = ?, video_url = ?, launch_date = ?, status = ?, category = ?,
		content_lang = ?, product_langs = ?, looking_for = ?, pricing = ?, is_open_source = ?,
		city = ?, updated_at = ? WHERE id = ?`,
		in.Name, in.Tagline, in.Description, in.URL, in.GitHubURL, in.VideoURL, in.LaunchDate,
		in.Status, in.Category, in.ContentLang, strings.Join(in.ProductLangs, ","),
		strings.Join(in.LookingFor, ","), in.Pricing, in.IsOpenSource, in.City, fmtTime(s.now()), id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM project_tags WHERE project_id = ?", id); err != nil {
		return err
	}
	if err := saveTags(ctx, tx, id, in); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteProject removes a project and everything attached to it.
func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err := tx.ExecContext(ctx, "DELETE FROM projects_fts WHERE rowid = ?", id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM projects WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// SetProjectHidden hides or unhides a project (moderation).
func (s *Store) SetProjectHidden(ctx context.Context, id int64, hidden bool) error {
	var at any
	if hidden {
		at = fmtTime(s.now())
	}
	_, err := s.db.ExecContext(ctx, "UPDATE projects SET hidden_at = ? WHERE id = ?", at, id)
	return err
}

// PopularTags returns the most used tag names of a kind.
func (s *Store) PopularTags(ctx context.Context, kind string, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.name FROM tags t
		JOIN project_tags pt ON pt.tag_id = t.id JOIN projects p ON p.id = pt.project_id
		JOIN users u ON u.id = p.owner_id
		WHERE t.kind = ? AND p.hidden_at IS NULL AND u.banned_at IS NULL
		GROUP BY t.id ORDER BY COUNT(*) DESC, t.name LIMIT ?`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SitemapEntry is a public project URL for the sitemap.
type SitemapEntry struct {
	Slug      string
	UpdatedAt time.Time
}

// PublicProjectSlugs lists all visible projects for the sitemap.
func (s *Store) PublicProjectSlugs(ctx context.Context) ([]SitemapEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.slug, p.updated_at FROM projects p
		JOIN users u ON u.id = p.owner_id
		WHERE p.hidden_at IS NULL AND u.banned_at IS NULL ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SitemapEntry
	for rows.Next() {
		var e SitemapEntry
		var updated string
		if err := rows.Scan(&e.Slug, &updated); err != nil {
			return nil, err
		}
		e.UpdatedAt = parseTime(updated)
		out = append(out, e)
	}
	return out, rows.Err()
}

func saveTags(ctx context.Context, tx *sql.Tx, projectID int64, in ProjectInput) error {
	for kind, names := range map[string][]string{TagTech: in.Tech, TagAI: in.AI} {
		for _, name := range names {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO tags (kind, name) VALUES (?, ?) ON CONFLICT (kind, name) DO NOTHING", kind, name); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO project_tags (project_id, tag_id)
				SELECT ?, id FROM tags WHERE kind = ? AND name = ?`, projectID, kind, name); err != nil {
				return err
			}
		}
	}
	tags := strings.Join(append(append([]string{}, in.Tech...), in.AI...), " ")
	if _, err := tx.ExecContext(ctx, "DELETE FROM projects_fts WHERE rowid = ?", projectID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		"INSERT INTO projects_fts (rowid, name, tagline, description, tags) VALUES (?, ?, ?, ?, ?)",
		projectID, in.Name, in.Tagline, in.Description, tags)
	return err
}

func (s *Store) loadTags(ctx context.Context, ps []*Project) error {
	if len(ps) == 0 {
		return nil
	}
	byID := make(map[int64]*Project, len(ps))
	args := make([]any, len(ps))
	for i, p := range ps {
		byID[p.ID] = p
		args[i] = p.ID
	}
	//nolint:gosec // only "?" placeholders are concatenated
	rows, err := s.db.QueryContext(ctx, `SELECT pt.project_id, t.kind, t.name FROM project_tags pt
		JOIN tags t ON t.id = pt.tag_id WHERE pt.project_id IN (`+placeholders(len(ps))+`)
		ORDER BY t.name`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var kind, name string
		if err := rows.Scan(&id, &kind, &name); err != nil {
			return err
		}
		p := byID[id]
		if kind == TagAI {
			p.AI = append(p.AI, name)
		} else {
			p.Tech = append(p.Tech, name)
		}
	}
	return rows.Err()
}

// ftsQuery turns free text into a safe FTS5 prefix query.
func ftsQuery(q string) string {
	words := strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) > 8 {
		words = words[:8]
	}
	for i, w := range words {
		words[i] = `"` + w + `"*`
	}
	return strings.Join(words, " ")
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
