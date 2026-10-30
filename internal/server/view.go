package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
	_ "time/tzdata" // embedded zone data: the production image has no zoneinfo
	"unicode"

	"github.com/aleksejs1/makers/internal/i18n"
	"github.com/aleksejs1/makers/internal/markdown"
	"github.com/aleksejs1/makers/internal/store"
	"github.com/aleksejs1/makers/internal/video"
)

// riga is the time zone used to display timestamps, loaded once.
var riga = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Riga")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// View is the data passed to every template.
type View struct {
	s *Server
	r *http.Request

	Lang        string
	Path        string // request path without the language prefix
	User        *store.User
	Title       string
	Description string
	OGType      string
	NoIndex     bool
	Flash       string // i18n key of a one-time message
	Data        any
}

func (s *Server) newView(r *http.Request, lang string) *View {
	p := r.URL.Path
	if rest, ok := strings.CutPrefix(p, "/"+lang); ok && (rest == "" || rest[0] == '/') {
		p = rest
	}
	if p == "" {
		p = "/"
	}
	return &View{s: s, r: r, Lang: lang, Path: p, User: currentUser(r), OGType: "website"}
}

// T translates a message for the view's language.
func (v *View) T(id string, args ...any) string { return v.s.i18n.T(v.Lang, id, args...) }

// UserID returns the current user's id, or 0 for anonymous visitors.
func (v *View) UserID() int64 {
	if v.User == nil {
		return 0
	}
	return v.User.ID
}

// Has reports whether a translation exists for id.
func (v *View) Has(id string) bool { return v.T(id) != id }

// TN translates a message with plural forms.
func (v *View) TN(id string, n int, args ...any) string { return v.s.i18n.TN(v.Lang, id, n, args...) }

// URL prefixes a path with the current language.
func (v *View) URL(p string) string { return "/" + v.Lang + p }

// LangURL returns the current page in another language. After a form
// submission it points to the language's home page.
func (v *View) LangURL(lang string) string {
	if v.r.Method != http.MethodGet {
		return "/" + lang + "/"
	}
	u := "/" + lang + v.Path
	if v.r.URL.RawQuery != "" {
		u += "?" + v.r.URL.RawQuery
	}
	return u
}

// Abs returns an absolute URL for a path.
func (v *View) Abs(p string) string { return v.s.cfg.BaseURL + p }

// Canonical is the absolute URL of the current page (without query).
func (v *View) Canonical() string { return v.Abs(v.URL(v.Path)) }

// Langs lists the supported interface languages.
func (v *View) Langs() []string { return i18n.Langs }

// Version is the application version.
func (v *View) Version() string { return v.s.version }

// Date formats a date in Riga time for the view's language.
func (v *View) Date(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.In(riga)
	if v.Lang == "en" {
		return t.Format("2 Jan 2006")
	}
	return t.Format("02.01.2006")
}

// DateTime formats a timestamp for the view's language.
func (v *View) DateTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return v.Date(t) + " " + t.In(riga).Format("15:04")
}

// Query returns a query parameter of the current request.
func (v *View) Query(key string) string { return v.r.URL.Query().Get(key) }

// CanEdit reports whether the current user owns or moderates a resource.
func (v *View) CanEdit(ownerID int64) bool {
	return v.User != nil && (v.User.ID == ownerID || v.User.IsAdmin)
}

// formField describes a form input for the field_* partials.
type formField struct {
	Name     string
	Value    string
	Type     string
	Max      int
	Required bool
	Prefix   string   // i18n prefix for select option labels
	Options  []string // select options
	Hints    []string // tag suggestions
}

// Pair bundles the view with an item for partial templates.
type Pair struct {
	V *View
	X any
}

type templates struct {
	pages    map[string]*template.Template
	partials *template.Template
}

func loadTemplates(fsys fs.FS, funcs template.FuncMap) (*templates, error) {
	base, err := template.New("").Funcs(funcs).ParseFS(fsys, "templates/layout.html", "templates/partials/*.html")
	if err != nil {
		return nil, err
	}
	pages, err := fs.Glob(fsys, "templates/pages/*.html")
	if err != nil {
		return nil, err
	}
	t := &templates{pages: map[string]*template.Template{}, partials: base}
	for _, p := range pages {
		clone, err := base.Clone()
		if err != nil {
			return nil, err
		}
		if _, err := clone.ParseFS(fsys, p); err != nil {
			return nil, err
		}
		t.pages[strings.TrimSuffix(path.Base(p), ".html")] = clone
	}
	return t, nil
}

func hashAssets(static fs.FS) (map[string]string, error) {
	out := map[string]string{}
	err := fs.WalkDir(static, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(static, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		out[p] = hex.EncodeToString(sum[:])[:12]
		return nil
	})
	return out, err
}

func assetPath(hashes map[string]string, name string) string {
	if h, ok := hashes[name]; ok {
		return "/static/" + name + "?h=" + h
	}
	return "/static/" + name
}

func (s *Server) assetURL(name string) string { return assetPath(s.hashes, name) }

func (s *Server) staticHandler() http.Handler {
	files := http.FileServerFS(s.assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.hashes[r.URL.Path]; !ok { // only files, no directory listings
			http.NotFound(w, r)
			return
		}
		if h := r.URL.Query().Get("h"); h != "" && h == s.hashes[r.URL.Path] {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		files.ServeHTTP(w, r)
	})
}

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"asset":    s.assetURL,
		"pair":     pair,
		"markdown": markdown.Render,
		"embed":    video.EmbedURL,
		"host":     displayHost,
		"initials": initials,
		"avatar":   avatarClass,
		"oneOf":    oneOf,
		"field": func(name, value string, maxLen int, required bool, typ string) formField {
			return formField{Name: name, Value: value, Max: maxLen, Required: required, Type: typ}
		},
		"selectField": func(name, value, prefix string, options []string, required bool) formField {
			return formField{Name: name, Value: value, Prefix: prefix, Options: options, Required: required}
		},
		"tagsField": func(name, value string, hints []string) formField {
			return formField{Name: name, Value: value, Hints: hints}
		},
		"categories":   func() []string { return Categories },
		"statuses":     func() []string { return Statuses },
		"pricings":     func() []string { return Pricings },
		"lookingFor":   func() []string { return LookingFor },
		"productLangs": func() []string { return ProductLangs },
		"contentLangs": func() []string { return ContentLangs },
		"cities":       func() []string { return Cities },
		"join":         strings.Join,
		"add":          func(a, b int) int { return a + b },
		"first":        func(n int, list []string) []string { return list[:min(n, len(list))] },
	}
}

// render writes a full page.
func (s *Server) render(w http.ResponseWriter, status int, page string, v *View) error {
	t, ok := s.tmpl.pages[page]
	if !ok {
		return fmt.Errorf("unknown page %q", page)
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", v); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, err := buf.WriteTo(w)
	return err
}

// renderPartial writes a single named template (for htmx responses).
func (s *Server) renderPartial(w http.ResponseWriter, name string, data any) error {
	var buf bytes.Buffer
	if err := s.tmpl.partials.ExecuteTemplate(&buf, name, data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := buf.WriteTo(w)
	return err
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, v *View, status int) {
	if isHTMX(r) {
		http.Error(w, http.StatusText(status), status)
		return
	}
	v.Title = v.T(fmt.Sprintf("error.%d.title", status))
	v.NoIndex = true
	v.Data = status
	if err := s.render(w, status, "error", v); err != nil {
		s.log.Error("render error page", "err", err)
		http.Error(w, http.StatusText(status), status)
	}
}

// initials returns up to two uppercase initials of a name.
func initials(name string) string {
	var out []rune
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out = append(out, unicode.ToUpper([]rune(w)[0]))
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// avatarClass picks one of the avatar color classes for a string.
func avatarClass(s string) string {
	h := fnv.New32a()
	h.Write([]byte(s))
	return fmt.Sprintf("av-%d", h.Sum32()%8)
}

// displayHost returns a short host+path for showing a link.
func displayHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	out := strings.TrimPrefix(u.Host, "www.") + strings.TrimSuffix(u.Path, "/")
	if r := []rune(out); len(r) > 40 {
		out = string(r[:39]) + "…"
	}
	return out
}
