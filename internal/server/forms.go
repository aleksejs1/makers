package server

import (
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aleksejs1/makers/internal/social"
	"github.com/aleksejs1/makers/internal/store"
	"github.com/aleksejs1/makers/internal/video"
)

// Enumerations used by project forms and filters. Labels live in the
// locale files under "<group>.<value>".
var (
	Statuses     = []string{"idea", "building", "launched", "paused", "closed"}
	Categories   = []string{"ai", "devtools", "productivity", "business", "fintech", "ecommerce", "edtech", "health", "media", "social", "gaming", "govtech", "hardware", "other"}
	Pricings     = []string{"free", "freemium", "paid"}
	LookingFor   = []string{"cofounder", "users", "feedback", "investors", "team"}
	ProductLangs = []string{"lv", "en", "ru", "lt", "et", "de", "other"}
	ContentLangs = []string{"lv", "en", "ru"}
	Cities       = []string{"Rīga", "Daugavpils", "Liepāja", "Jelgava", "Jūrmala", "Ventspils", "Rēzekne", "Valmiera", "Ogre", "Cēsis", "Remote"}
)

// Field limits.
const (
	maxName        = 60
	maxTagline     = 140
	maxDescription = 5000
	maxURL         = 300
	maxCity        = 40
	maxTag         = 30
	maxTags        = 10
	maxLinks       = 6
	maxComment     = 2000
	maxBio         = 280
	maxUserName    = 60
)

// formErrors maps a field name to an i18n error key.
type formErrors map[string]string

func (e formErrors) Has(field string) bool { return e[field] != "" }

func (e formErrors) Get(field string) string { return e[field] }

// projectForm is the template data of the project editor.
type projectForm struct {
	In        store.ProjectInput
	LinksStr  string
	TechStr   string
	AIStr     string
	Errors    formErrors
	Project   *store.Project // nil when creating
	TechHints []string
	AIHints   []string
}

func parseProjectForm(r *http.Request) projectForm {
	f := r.PostForm
	in := store.ProjectInput{
		Name:         clean(f.Get("name")),
		Tagline:      clean(f.Get("tagline")),
		Description:  multiline(f.Get("description")),
		URL:          normalizeURL(f.Get("url")),
		GitHubURL:    normalizeURL(f.Get("github_url")),
		VideoURL:     strings.TrimSpace(f.Get("video_url")),
		LaunchDate:   strings.TrimSpace(f.Get("launch_date")),
		Status:       f.Get("status"),
		Category:     f.Get("category"),
		ContentLang:  f.Get("content_lang"),
		ProductLangs: pick(f["product_langs"], ProductLangs),
		LookingFor:   pick(f["looking_for"], LookingFor),
		Pricing:      f.Get("pricing"),
		IsOpenSource: f.Get("is_open_source") == "1",
		City:         clean(f.Get("city")),
	}
	pf := projectForm{In: in, LinksStr: f.Get("links"), TechStr: f.Get("tech"), AIStr: f.Get("ai"), Errors: formErrors{}}
	pf.In.Links = parseLinks(pf.LinksStr)
	pf.In.Tech = parseTags(pf.TechStr)
	pf.In.AI = parseTags(pf.AIStr)
	pf.validate()
	return pf
}

func (pf *projectForm) validate() {
	in, e := &pf.In, pf.Errors
	switch n := utf8.RuneCountInString(in.Name); {
	case n == 0:
		e["name"] = "form.error.required"
	case n > maxName:
		e["name"] = "form.error.too_long"
	}
	switch n := utf8.RuneCountInString(in.Tagline); {
	case n == 0:
		e["tagline"] = "form.error.required"
	case n > maxTagline:
		e["tagline"] = "form.error.too_long"
	}
	switch n := utf8.RuneCountInString(in.Description); {
	case n == 0:
		e["description"] = "form.error.required"
	case n > maxDescription:
		e["description"] = "form.error.too_long"
	}
	if in.URL == "" {
		e["url"] = "form.error.required"
	} else if !validURL(in.URL) {
		e["url"] = "form.error.url"
	}
	if in.GitHubURL != "" && !validURL(in.GitHubURL) {
		e["github_url"] = "form.error.url"
	}
	if in.VideoURL != "" && video.EmbedURL(in.VideoURL) == "" {
		e["video_url"] = "form.error.video"
		// Usually a profile or a video on an unsupported platform: point
		// the maker to the links field instead.
		if social.Platform(normalizeURL(in.VideoURL)) != "" {
			e["video_url"] = "form.error.video_social"
		}
	}
	switch {
	case len(in.Links) > maxLinks:
		e["links"] = "form.error.too_many_links"
	case !all(in.Links, validURL):
		e["links"] = "form.error.links"
	}
	if in.LaunchDate != "" {
		t, err := time.Parse(time.DateOnly, in.LaunchDate)
		if err != nil || t.Year() < 1990 || t.Year() > 2100 {
			e["launch_date"] = "form.error.date"
		}
	}
	if !oneOf(in.Status, Statuses) {
		e["status"] = "form.error.required"
	}
	if !oneOf(in.Category, Categories) {
		e["category"] = "form.error.required"
	}
	if !oneOf(in.ContentLang, ContentLangs) {
		e["content_lang"] = "form.error.required"
	}
	if in.Pricing != "" && !oneOf(in.Pricing, Pricings) {
		in.Pricing = ""
	}
	if utf8.RuneCountInString(in.City) > maxCity {
		e["city"] = "form.error.too_long"
	}
	if len(in.Tech) > maxTags {
		e["tech"] = "form.error.too_many_tags"
	}
	if len(in.AI) > maxTags {
		e["ai"] = "form.error.too_many_tags"
	}
}

func projectToForm(p *store.Project) projectForm {
	return projectForm{
		In:       p.ProjectInput,
		LinksStr: strings.Join(p.Links, "\n"),
		TechStr:  strings.Join(p.Tech, ", "),
		AIStr:    strings.Join(p.AI, ", "),
		Errors:   formErrors{},
		Project:  p,
	}
}

// clean trims a single-line value, collapses inner whitespace and drops
// invalid UTF-8.
func clean(s string) string { return strings.Join(strings.Fields(strings.ToValidUTF8(s, "")), " ") }

// multiline normalizes a multi-line text value.
func multiline(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\r\n", "\n"))
}

// normalizeURL trims a URL and adds https:// when the scheme is missing.
func normalizeURL(s string) string {
	s = strings.TrimSpace(s)
	if s != "" && !strings.Contains(s, "://") {
		s = "https://" + s
	}
	return s
}

func validURL(s string) bool {
	if len(s) > maxURL {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") &&
		strings.Contains(u.Hostname(), ".") && u.User == nil
}

// parseLinks splits whitespace-separated URLs (normally one per line),
// normalizing and de-duplicating them.
func parseLinks(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range strings.Fields(strings.ToValidUTF8(s, "")) {
		l = normalizeURL(l)
		if seen[strings.ToLower(l)] {
			continue
		}
		seen[strings.ToLower(l)] = true
		out = append(out, l)
	}
	return out
}

func all(list []string, ok func(string) bool) bool {
	for _, s := range list {
		if !ok(s) {
			return false
		}
	}
	return true
}

func oneOf(s string, list []string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// pick keeps the allowed values from vals, in the canonical order.
func pick(vals, allowed []string) []string {
	var out []string
	for _, a := range allowed {
		if oneOf(a, vals) {
			out = append(out, a)
		}
	}
	return out
}

// parseTags splits a comma-separated list, trimming and de-duplicating it
// case-insensitively. Overlong tags are cut.
func parseTags(s string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.Split(s, ",") {
		t = clean(strings.TrimPrefix(strings.TrimSpace(t), "#"))
		if r := []rune(t); len(r) > maxTag {
			t = strings.TrimSpace(string(r[:maxTag]))
		}
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}
