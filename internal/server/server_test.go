package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/aleksejs1/makers/internal/auth"
	"github.com/aleksejs1/makers/internal/config"
	"github.com/aleksejs1/makers/internal/db"
	"github.com/aleksejs1/makers/internal/mail"
	"github.com/aleksejs1/makers/internal/store"
	"github.com/aleksejs1/makers/ui"
)

type captureMail struct {
	mu   sync.Mutex
	msgs []mail.Message
	fail bool // simulate a delivery failure
}

func (c *captureMail) Send(_ context.Context, m mail.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("smtp down")
	}
	c.msgs = append(c.msgs, m)
	return nil
}

func (c *captureMail) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.msgs)
}

func (c *captureMail) setFail(fail bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fail = fail
}

func (c *captureMail) last() mail.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.msgs[len(c.msgs)-1]
}

type env struct {
	t    *testing.T
	ts   *httptest.Server
	mail *captureMail
	db   *sql.DB
	srv  *Server
}

func newEnv(t *testing.T, args ...string) *env {
	t.Helper()
	sqlDB, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	cfg, err := config.Load(append([]string{"-env", "dev", "-secret", "test-secret-0123456789", "-admin-emails", "admin@makers.lv"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	m := &captureMail{}
	srv, err := New(Options{
		Config: cfg, Store: store.New(sqlDB), Mail: m, UI: ui.FS, Version: "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &env{t: t, ts: ts, mail: m, db: sqlDB, srv: srv}
}

// client is a browser-like client with its own cookie jar that does not
// follow redirects.
type client struct {
	e *env
	c *http.Client
}

func (e *env) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{e: e, c: &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type resp struct {
	status   int
	body     string
	location string
}

func (c *client) do(method, path string, form url.Values, headers ...string) resp {
	c.e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, c.e.ts.URL+path, body)
	if err != nil {
		c.e.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := c.c.Do(req)
	if err != nil {
		c.e.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{status: res.StatusCode, body: string(b), location: res.Header.Get("Location")}
}

func (c *client) get(path string, headers ...string) resp { return c.do("GET", path, nil, headers...) }

func (c *client) post(path string, form url.Values, headers ...string) resp {
	return c.do("POST", path, form, headers...)
}

var codeRe = regexp.MustCompile(`\b(\d{4} \d{4})\b`)

func (c *client) login(email string) resp {
	c.e.t.Helper()
	r := c.post("/lv/login", url.Values{"email": {email}, "next": {"/lv/"}})
	if r.status != http.StatusOK || !strings.Contains(r.body, `name="code"`) {
		c.e.t.Fatalf("login: status %d", r.status)
	}
	code := codeRe.FindStringSubmatch(c.e.mail.last().Body)[1]
	return c.post("/lv/login/verify", url.Values{"email": {email}, "code": {code}, "next": {"/lv/"}})
}

func expect(t *testing.T, r resp, status int, contains ...string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d; location=%q body:\n%.2000s", r.status, status, r.location, r.body)
	}
	for _, s := range contains {
		if !strings.Contains(r.body, s) {
			t.Fatalf("response does not contain %q:\n%.3000s", s, r.body)
		}
	}
}

func TestLanguageRedirect(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	if r := c.get("/"); r.location != "/lv/" {
		t.Errorf("default: location %q", r.location)
	}
	if r := c.get("/", "Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8"); r.location != "/ru/" {
		t.Errorf("ru: location %q", r.location)
	}
	if r := c.get("/", "Accept-Language", "de-DE,en;q=0.5"); r.location != "/en/" {
		t.Errorf("de,en: location %q", r.location)
	}
	expect(t, c.get("/en/"), 200, "What makers in Latvia are building")
	// The visited language is remembered.
	if r := c.get("/", "Accept-Language", "ru"); r.location != "/en/" {
		t.Errorf("cookie: location %q", r.location)
	}
	expect(t, c.get("/lv/"), 200, "Ko veido radītāji Latvijā", `hreflang="ru"`)
	expect(t, c.get("/ru/"), 200, "Что создают мейкеры в Латвии")
	expect(t, c.get("/lv/nope"), 404, "Lapa nav atrasta")
}

func TestLoginFlow(t *testing.T) {
	e := newEnv(t)
	c := e.client()

	expect(t, c.post("/lv/login", url.Values{"email": {"not-an-email"}}), 422, "derīgu e-pasta")
	expect(t, c.post("/en/login", url.Values{"email": {"Jane@Example.com"}}), 200, "jane@example.com")
	m := e.mail.last()
	if m.To != "jane@example.com" || !strings.Contains(m.Subject, "login code") {
		t.Fatalf("unexpected mail %+v", m)
	}
	code := codeRe.FindStringSubmatch(m.Body)[1]
	if !strings.Contains(m.Subject, code) {
		t.Fatalf("subject %q lacks the formatted code", m.Subject)
	}
	expect(t, c.post("/en/login/verify", url.Values{"email": {"jane@example.com"}, "code": {wrongCode(code)}}), 422, "Wrong code")

	r := c.post("/en/login/verify", url.Values{"email": {"jane@example.com"}, "code": {"  " + code + "-"}, "next": {"/en/"}})
	if r.status != 303 || !strings.HasPrefix(r.location, "/en/settings") {
		t.Fatalf("verify: %d %q", r.status, r.location)
	}
	settings := c.get("/en/settings")
	expect(t, settings, 200, "Welcome to makers.lv", `value="maker-`)
	if strings.Contains(settings.body, `value="jane`) {
		t.Fatal("default handle or name is derived from the email")
	}
	// Code cannot be reused.
	expect(t, c.post("/en/login/verify", url.Values{"email": {"jane@example.com"}, "code": {code}}), 422)

	r = c.post("/en/settings", url.Values{"display_name": {"Jane Doe"}, "handle": {"jane-doe"}, "bio": {"Builder"}})
	if r.status != 303 || r.location != "/en/u/jane-doe" {
		t.Fatalf("settings: %d %q %s", r.status, r.location, r.body)
	}
	expect(t, c.get("/en/u/jane-doe"), 200, "Jane Doe", "@jane-doe")

	c.post("/en/logout", nil)
	if r := c.get("/en/settings"); r.status != 303 || !strings.Contains(r.location, "/en/login") {
		t.Fatalf("after logout: %d %q", r.status, r.location)
	}
}

func TestBruteForceBurnsCode(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.post("/lv/login", url.Values{"email": {"a@b.lv"}})
	code := codeRe.FindStringSubmatch(e.mail.last().Body)[1]
	for range 5 {
		c.post("/lv/login/verify", url.Values{"email": {"a@b.lv"}, "code": {wrongCode(code)}})
	}
	expect(t, c.post("/lv/login/verify", url.Values{"email": {"a@b.lv"}, "code": {code}}), 422, "derīgums ir beidzies")
}

func sampleProject(name string) url.Values {
	return url.Values{
		"name":          {name},
		"tagline":       {"Finds the best kebab in Rīga"},
		"description":   {"**Bold** claims. <script>alert(1)</script>"},
		"url":           {"kebab.example.lv"},
		"github_url":    {"https://github.com/example/kebab"},
		"video_url":     {"https://youtu.be/dQw4w9WgXcQ"},
		"launch_date":   {"2026-05-01"},
		"status":        {"launched"},
		"category":      {"ai"},
		"content_lang":  {"en"},
		"product_langs": {"lv", "en"},
		"looking_for":   {"users"},
		"pricing":       {"free"},
		"city":          {"Rīga"},
		"tech":          {"Go, htmx, go, SQLite"},
		"ai":            {"Claude Code"},
	}
}

func TestProjectLifecycle(t *testing.T) {
	e := newEnv(t)
	owner := e.client()
	owner.login("owner@makers.lv")

	// Validation errors are shown.
	bad := sampleProject("")
	bad.Set("video_url", "https://example.com/video")
	expect(t, owner.post("/en/new", bad), 422, "This field is required.", "YouTube, Vimeo or Loom")

	r := owner.post("/en/new", sampleProject("Kebab Radar"))
	if r.status != 303 || r.location != "/en/p/kebab-radar" {
		t.Fatalf("create: %d %q\n%s", r.status, r.location, r.body)
	}
	page := owner.get("/en/p/kebab-radar")
	expect(t, page, 200, "Kebab Radar", "<strong>Bold</strong>", "youtube-nocookie.com/embed/dQw4w9WgXcQ",
		"https://kebab.example.lv", "Your project is live!")
	if strings.Contains(page.body, "<script>alert") {
		t.Fatal("unsanitized HTML in description")
	}
	// Duplicate tags are collapsed.
	if strings.Count(page.body, `?tech=Go"`)+strings.Count(page.body, `?tech=go"`) != 1 {
		t.Error("expected tag Go exactly once")
	}

	// Second project with the same name gets a new slug.
	if r := owner.post("/en/new", sampleProject("Kebab Radar")); r.location != "/en/p/kebab-radar-2" {
		t.Fatalf("second slug: %q", r.location)
	}

	anon := e.client()
	expect(t, anon.get("/en/"), 200, "Kebab Radar", "2 projects")
	expect(t, anon.get("/lv/"), 200, "2 projekti")
	expect(t, anon.get("/ru/"), 200, "2 проекта")
	expect(t, anon.get("/en/?q=keba"), 200, "Kebab Radar")
	expect(t, anon.get("/en/?q=pizza"), 200, "Nothing matches")
	expect(t, anon.get("/en/?tech=sqlite"), 200, "Kebab Radar")
	expect(t, anon.get("/en/?category=health"), 200, "Nothing matches")
	partial := anon.get("/en/?sort=popular", "HX-Request", "true", "HX-Target", "projects")
	expect(t, partial, 200, `id="projects"`)
	if strings.Contains(partial.body, "<html") {
		t.Error("htmx partial contains the layout")
	}

	// Anonymous users are sent to login.
	if r := anon.get("/en/new"); r.status != 303 || !strings.Contains(r.location, "/en/login?next=") {
		t.Fatalf("anon new: %d %q", r.status, r.location)
	}

	// Another user cannot edit.
	other := e.client()
	other.login("other@makers.lv")
	expect(t, other.get("/en/p/kebab-radar/edit"), 403)
	expect(t, other.post("/en/p/kebab-radar/edit", sampleProject("Hacked")), 403)

	// Owner edits.
	upd := sampleProject("Kebab Radar Pro")
	upd.Set("tech", "Go")
	if r := owner.post("/en/p/kebab-radar/edit", upd); r.status != 303 || r.location != "/en/p/kebab-radar" {
		t.Fatalf("update: %d %q", r.status, r.location)
	}
	expect(t, anon.get("/en/p/kebab-radar"), 200, "Kebab Radar Pro")
	if r := anon.get("/en/?tech=sqlite"); strings.Contains(r.body, "Kebab Radar Pro") {
		t.Error("removed tag still matches")
	}

	// Delete requires confirmation.
	expect(t, owner.post("/en/p/kebab-radar-2/delete", url.Values{}), 422, "Tick the box")
	if r := owner.post("/en/p/kebab-radar-2/delete", url.Values{"confirm": {"1"}}); r.status != 303 {
		t.Fatalf("delete: %d", r.status)
	}
	expect(t, anon.get("/en/p/kebab-radar-2"), 404)
}

func TestLikesAndComments(t *testing.T) {
	e := newEnv(t)
	owner := e.client()
	owner.login("owner@makers.lv")
	owner.post("/en/new", sampleProject("Kebab Radar"))

	fan := e.client()
	fan.login("fan@makers.lv")
	hx := []string{"HX-Request", "true"}

	r := fan.post("/en/p/kebab-radar/like", url.Values{}, hx...)
	expect(t, r, 200, "is-liked", "<span>1</span>")
	r = fan.post("/en/p/kebab-radar/like", url.Values{}, hx...)
	expect(t, r, 200, "<span>0</span>")
	if strings.Contains(r.body, "is-liked") {
		t.Fatal("like was not toggled off")
	}
	fan.post("/en/p/kebab-radar/like", url.Values{})

	anon := e.client()
	if r := anon.post("/en/p/kebab-radar/like", url.Values{}, hx...); r.status != 204 {
		t.Fatalf("anon like: %d", r.status)
	}

	expect(t, fan.post("/en/p/kebab-radar/comments", url.Values{"body": {"  "}}, hx...), 200, "This field is required.")
	r = fan.post("/en/p/kebab-radar/comments", url.Values{"body": {"Great <b>work</b>!"}}, hx...)
	expect(t, r, 200, "1 comment", "Great &lt;b&gt;work&lt;/b&gt;!")

	expect(t, anon.get("/en/p/kebab-radar"), 200, "Great &lt;b&gt;work")
	expect(t, anon.get("/en/?sort=popular"), 200, "Kebab Radar")

	// Find the comment id and check permissions on delete.
	id := regexp.MustCompile(`/comments/(\d+)/delete`).FindStringSubmatch(r.body)[1]
	expect(t, owner.post("/en/comments/"+id+"/delete", url.Values{"slug": {"kebab-radar"}}), 403)
	r = fan.post("/en/comments/"+id+"/delete", url.Values{"slug": {"kebab-radar"}}, hx...)
	expect(t, r, 200, "0 comments")
}

func TestModeration(t *testing.T) {
	e := newEnv(t)
	owner := e.client()
	owner.login("owner@makers.lv")
	owner.post("/en/new", sampleProject("Spam Project"))
	owner.post("/en/settings", url.Values{"display_name": {"Spammer"}, "handle": {"spammer"}})

	admin := e.client()
	admin.login("admin@makers.lv")
	anon := e.client()

	expect(t, owner.post("/en/admin/projects/spam-project/hide", nil), 403)
	admin.post("/en/admin/projects/spam-project/hide", nil)
	expect(t, anon.get("/en/p/spam-project"), 404)
	expect(t, owner.get("/en/p/spam-project"), 200, "hidden by a moderator")
	expect(t, anon.get("/en/"), 200, "0 projects")
	admin.post("/en/admin/projects/spam-project/hide", nil)
	expect(t, anon.get("/en/p/spam-project"), 200)

	admin.post("/en/admin/users/spammer/ban", nil)
	expect(t, anon.get("/en/p/spam-project"), 404)
	expect(t, anon.get("/en/u/spammer"), 404)
	// The banned user's session is gone and they cannot log back in.
	if r := owner.get("/en/settings"); r.status != 303 {
		t.Fatalf("banned session still valid: %d", r.status)
	}
	expect(t, owner.login("owner@makers.lv"), 403, "konts ir bloķēts")
}

func TestAccountDeletion(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.login("bye@makers.lv")
	c.post("/en/settings", url.Values{"display_name": {"Bye"}, "handle": {"bye"}})
	c.post("/en/new", sampleProject("Short Lived"))
	expect(t, c.post("/en/settings/delete", url.Values{"confirm": {"wrong"}}), 422, "The username doesn&#39;t match")
	if r := c.post("/en/settings/delete", url.Values{"confirm": {"bye"}}); r.status != 303 {
		t.Fatalf("delete account: %d", r.status)
	}
	expect(t, e.client().get("/en/p/short-lived"), 404)
	expect(t, e.client().get("/en/?q=short"), 200, "Nothing matches")
}

func TestCrossOriginPostRejected(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.login("x@makers.lv")
	r := c.post("/en/settings/logout-all", url.Values{}, "Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example")
	if r.status != http.StatusForbidden {
		t.Fatalf("cross-origin POST: %d", r.status)
	}
}

func TestServiceEndpoints(t *testing.T) {
	e := newEnv(t)
	owner := e.client()
	owner.login("owner@makers.lv")
	owner.post("/en/new", sampleProject("Kebab Radar"))
	c := e.client()

	expect(t, c.get("/healthz"), 200, "ok")
	expect(t, c.get("/robots.txt"), 200, "Sitemap: ")
	expect(t, c.get("/sitemap.xml"), 200, "/lv/p/kebab-radar", `hreflang="ru"`)
	expect(t, c.get("/feed.xml?lang=en"), 200, "<title>Kebab Radar</title>", "/en/p/kebab-radar")
	expect(t, c.get("/static/css/app.css"), 200, "--accent")
	expect(t, c.get("/en/about"), 200, "About makers.lv")
	expect(t, c.get("/ru/privacy"), 200, "Приватность")
}

func mailedCode(t *testing.T, e *env) string {
	t.Helper()
	return codeRe.FindStringSubmatch(e.mail.last().Body)[1]
}

func wrongCode(code string) string {
	if auth.NormalizeCode(code) == "00000000" {
		return "11111111"
	}
	return "00000000"
}

// Concurrent guesses must not get more than CodeMaxAttempts tries.
func TestConcurrentGuessesCannotExceedAttemptLimit(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.post("/en/login", url.Values{"email": {"victim@makers.lv"}})
	code := mailedCode(t, e)

	const n = 15
	results := make(chan resp, n)
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			results <- c.post("/en/login/verify",
				url.Values{"email": {"victim@makers.lv"}, "code": {wrongCode(code)}})
		})
	}
	wg.Wait()
	close(results)
	wrong := 0
	for r := range results {
		if strings.Contains(r.body, "Wrong code") {
			wrong++
		}
	}
	// Attempts 1–4 report a wrong code, the 5th burns it, the rest are rejected.
	if wrong != 4 {
		t.Fatalf("%d requests got to check a guess and continue, want 4", wrong)
	}
	expect(t, c.post("/en/login/verify", url.Values{"email": {"victim@makers.lv"}, "code": {code}}), 422, "expired")
}

// Code requests and guesses by a stranger must not lock the owner out.
func TestCodeRequestsByOthersDoNotLockOutOwner(t *testing.T) {
	e := newEnv(t, "-trusted-proxies", "127.0.0.1")
	victim := e.client()
	expect(t, victim.post("/en/login", url.Values{"email": {"victim@makers.lv"}}, "X-Forwarded-For", "198.51.100.7"),
		200, `name="code"`)
	victimCode := mailedCode(t, e)

	// The attacker requests codes for the same address from other browsers
	// and guesses wildly; their codes are separate from the victim's.
	for i := range 15 {
		attacker := e.client()
		ip := fmt.Sprintf("203.0.113.%d", i+1)
		if r := attacker.post("/en/login", url.Values{"email": {"victim@makers.lv"}}, "X-Forwarded-For", ip); r.status != 200 {
			t.Fatalf("attacker request %d: status %d", i, r.status)
		}
		for range auth.CodeMaxAttempts {
			attacker.post("/en/login/verify", url.Values{"email": {"victim@makers.lv"}, "code": {wrongCode(victimCode)}},
				"X-Forwarded-For", ip)
		}
	}

	r := victim.post("/en/login/verify", url.Values{"email": {"victim@makers.lv"}, "code": {victimCode}},
		"X-Forwarded-For", "198.51.100.7")
	if r.status != 303 {
		t.Fatalf("victim could not log in: %d %.500s", r.status, r.body)
	}
}

// A code whose email could not be delivered must not count or block.
func TestUndeliveredCodeIsDiscarded(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	e.mail.setFail(true)
	expect(t, c.post("/en/login", url.Values{"email": {"a@makers.lv"}}), 502, "try again")
	e.mail.setFail(false)
	// No cooldown applies: the failed code was removed.
	expect(t, c.post("/en/login", url.Values{"email": {"a@makers.lv"}}), 200, "We sent an 8-digit code")
	if r := c.post("/en/login/verify", url.Values{"email": {"a@makers.lv"}, "code": {mailedCode(t, e)}}); r.status != 303 {
		t.Fatalf("verify: %d", r.status)
	}
}

func TestHugePageNumber(t *testing.T) {
	e := newEnv(t)
	owner := e.client()
	owner.login("owner@makers.lv")
	owner.post("/en/new", sampleProject("Kebab Radar"))
	c := e.client()
	for _, q := range []string{"?page=9223372036854775807", "?page=2", "?page=5&sort=popular"} {
		r := c.get("/en/" + q)
		want := "/en/"
		if strings.Contains(q, "sort") {
			want = "/en/?sort=popular"
		}
		if r.status != 303 || r.location != want {
			t.Errorf("%s: %d %q, want redirect to %q", q, r.status, r.location, want)
		}
	}
	expect(t, c.get("/en/?page=1"), 200, "Kebab Radar")
}

func TestSafeNext(t *testing.T) {
	ok := []string{"/", "/en/p/x", "/en/?q=a&tech=Go", "/en/p/x#comments"}
	bad := []string{"", "https://evil.com", "//evil.com", "/\t/evil.com", "/\n/evil.com",
		"/\\evil.com", "/%09/evil.com\x7f", "javascript:alert(1)", "en/p/x"}
	for _, s := range ok {
		if got := safeNext(s, "FB"); got != s {
			t.Errorf("safeNext(%q) = %q, want it accepted", s, got)
		}
	}
	for _, s := range bad {
		if got := safeNext(s, "FB"); got != "FB" {
			t.Errorf("safeNext(%q) = %q, want fallback", s, got)
		}
	}
}

func TestLoginNextCannotRedirectOffSite(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.login("x@makers.lv")
	r := c.get("/en/login?next=/%09/evil.com")
	if r.status != 303 || r.location != "/en/" {
		t.Fatalf("logged-in redirect: %d %q, want /en/", r.status, r.location)
	}
	expect(t, c.get("/en/settings?next=/%09/evil.com"), 200, `name="next" value=""`)
}

// Failed guesses by a stranger must not stop the owner from logging in.
func TestBogusVerifiesDoNotLockOutOwner(t *testing.T) {
	e := newEnv(t, "-trusted-proxies", "127.0.0.1")
	for i := range 25 {
		e.client().post("/en/login/verify", url.Values{"email": {"victim@makers.lv"}, "code": {"00000000"}},
			"X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
	}
	victim := e.client()
	expect(t, victim.post("/en/login", url.Values{"email": {"victim@makers.lv"}}, "X-Forwarded-For", "198.51.100.7"),
		200, `name="code"`)
	r := victim.post("/en/login/verify", url.Values{"email": {"victim@makers.lv"}, "code": {mailedCode(t, e)}},
		"X-Forwarded-For", "198.51.100.7")
	if r.status != 303 {
		t.Fatalf("owner could not log in: %d", r.status)
	}
}

func TestAIFilterIsSeparateFromTech(t *testing.T) {
	e := newEnv(t)
	owner := e.client()
	owner.login("owner@makers.lv")
	p := sampleProject("Only AI Tag")
	p.Set("tech", "Go")
	p.Set("ai", "OpenAI")
	owner.post("/en/new", p)
	c := e.client()
	expect(t, c.get("/en/?tech=OpenAI"), 200, "Nothing matches")
	r := c.get("/en/?ai=OpenAI")
	expect(t, r, 200, "Only AI Tag", `name="ai" value="OpenAI"`)
	expect(t, c.get("/en/p/only-ai-tag"), 200, `?ai=OpenAI`)
}

func TestFragmentCaching(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/en/", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Target", "projects")
	res, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if v := res.Header.Get("Vary"); !strings.Contains(v, "HX-Target") {
		t.Errorf("Vary = %q, want HX-Target", v)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("fragment Cache-Control = %q, want no-store", cc)
	}
}

func TestCodeIsBoundToRequestingBrowser(t *testing.T) {
	e := newEnv(t)
	owner := e.client()
	owner.post("/en/login", url.Values{"email": {"a@makers.lv"}})
	code := mailedCode(t, e)

	// Someone who learns the code cannot use it from another browser.
	expect(t, e.client().post("/en/login/verify", url.Values{"email": {"a@makers.lv"}, "code": {code}}),
		422, "same browser")
	thief := e.client()
	thief.post("/en/login", url.Values{"email": {"a@makers.lv"}}) // own nonce, own code
	expect(t, thief.post("/en/login/verify", url.Values{"email": {"a@makers.lv"}, "code": {code}}), 422, "Wrong code")

	if r := owner.post("/en/login/verify", url.Values{"email": {"a@makers.lv"}, "code": {code}}); r.status != 303 {
		t.Fatalf("owner: %d", r.status)
	}
}

func TestResendFromSameBrowserIsThrottled(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.post("/en/login", url.Values{"email": {"a@makers.lv"}})
	expect(t, c.post("/en/login", url.Values{"email": {"a@makers.lv"}}), 200, `name="code"`, "recently")
	if n := e.mail.count(); n != 1 {
		t.Fatalf("%d emails, want 1", n)
	}
}

// Parallel requests must not exceed the per-address hourly cap.
func TestParallelCodeRequestsRespectCap(t *testing.T) {
	e := newEnv(t, "-trusted-proxies", "127.0.0.1")
	var wg sync.WaitGroup
	for i := range 45 {
		wg.Go(func() {
			e.client().post("/en/login", url.Values{"email": {"victim@makers.lv"}},
				"X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i+1))
		})
	}
	wg.Wait()
	if n := e.mail.count(); n != otpLimits.PerHour {
		t.Fatalf("%d emails sent, want exactly %d", n, otpLimits.PerHour)
	}
}

func TestClientIP(t *testing.T) {
	e := newEnv(t, "-trusted-proxies", "10.0.0.1, 172.16.0.0/12")
	tests := []struct {
		remote, xff, want string
	}{
		{"203.0.113.5:1234", "1.2.3.4", "203.0.113.5"},                // untrusted peer: header ignored
		{"10.0.0.1:1234", "198.51.100.7", "198.51.100.7"},             // trusted proxy
		{"10.0.0.1:1234", "1.2.3.4, 198.51.100.7", "198.51.100.7"},    // client-supplied prefix ignored
		{"10.0.0.1:1234", "198.51.100.7, 172.16.5.5", "198.51.100.7"}, // chain of trusted proxies
		{"10.0.0.1:1234", "", "10.0.0.1"},                             // no header
		{"10.0.0.1:1234", "garbage", "10.0.0.1"},                      // malformed header
		{"[2001:db8::1]:1234", "", "2001:db8::1"},                     // IPv6 peer
	}
	for _, tt := range tests {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tt.remote
		if tt.xff != "" {
			r.Header.Set("X-Forwarded-For", tt.xff)
		}
		if got := e.srv.clientIP(r); got != tt.want {
			t.Errorf("remote=%s xff=%q: got %s, want %s", tt.remote, tt.xff, got, tt.want)
		}
	}
	// IPv6 clients share a limit per /64.
	a := httptest.NewRequest(http.MethodGet, "/", nil)
	a.RemoteAddr = "[2001:db8:1:2::1]:1"
	b := httptest.NewRequest(http.MethodGet, "/", nil)
	b.RemoteAddr = "[2001:db8:1:2:ffff::9]:1"
	if e.srv.limitKey(a) != e.srv.limitKey(b) {
		t.Error("addresses in one /64 got different limit keys")
	}
}

func TestDatabaseErrorKeepsSession(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	c.login("a@makers.lv")
	e.db.Close()
	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/en/about", nil)
	res, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	for _, ck := range res.Cookies() {
		if ck.Name == auth.CookieName && ck.MaxAge < 0 {
			t.Fatal("session cookie cleared because of a database error")
		}
	}
}

func headers(t *testing.T, c *client, path string) http.Header {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.e.ts.URL+path, nil)
	res, err := c.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode >= 400 {
		res.Header.Set("X-Test-Status", fmt.Sprint(res.StatusCode))
	}
	return res.Header
}

func TestPrivacyAndTransportHeaders(t *testing.T) {
	e := newEnv(t)
	c := e.client()
	if cc := headers(t, c, "/en/").Get("Cache-Control"); cc != "" {
		t.Errorf("anonymous page Cache-Control = %q, want none", cc)
	}
	c.login("a@makers.lv")
	if cc := headers(t, c, "/en/settings").Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("settings Cache-Control = %q", cc)
	}
	if h := headers(t, c, "/en/").Get("Strict-Transport-Security"); h != "" {
		t.Errorf("HSTS sent over http base URL: %q", h)
	}
	if h := headers(t, c, "/static/").Get("X-Test-Status"); h != "404" {
		t.Errorf("/static/ directory listing status %q, want 404", h)
	}
	if h := headers(t, c, "/static/css/").Get("X-Test-Status"); h != "404" {
		t.Errorf("/static/css/ directory listing status %q, want 404", h)
	}

	https := newEnv(t, "-base-url", "https://makers.lv")
	if h := headers(t, https.client(), "/en/").Get("Strict-Transport-Security"); !strings.HasPrefix(h, "max-age=") {
		t.Errorf("HSTS = %q with an https base URL", h)
	}
}

func TestSitemapIsCapped(t *testing.T) {
	e := newEnv(t)
	owner := e.client()
	owner.login("owner@makers.lv")
	for i := range 3 {
		owner.post("/en/new", sampleProject(fmt.Sprint("Project ", i)))
	}
	old := maxSitemapURLs
	maxSitemapURLs = 7
	t.Cleanup(func() { maxSitemapURLs = old })
	r := e.client().get("/sitemap.xml")
	if n := strings.Count(r.body, "<url>"); n > 7 {
		t.Fatalf("sitemap has %d URLs, cap is 7", n)
	}
}

func TestProjectLinks(t *testing.T) {
	e := newEnv(t)
	owner := e.client()
	owner.login("owner@makers.lv")

	// A TikTok account in the video field points the maker to the links field.
	bad := sampleProject("Kebab Radar")
	bad.Set("video_url", "https://www.tiktok.com/@kebabradar")
	expect(t, owner.post("/en/new", bad), 422, "Only a single YouTube, Vimeo or Loom video")

	bad = sampleProject("Kebab Radar")
	bad.Set("links", "https://ok.example.lv\njavascript:alert(1)")
	expect(t, owner.post("/en/new", bad), 422, "Each line must be a valid http(s) link.")

	bad.Set("links", "a.lv b.lv c.lv d.lv e.lv f.lv g.lv")
	expect(t, owner.post("/en/new", bad), 422, "Use at most 6 links.")

	p := sampleProject("Kebab Radar")
	p.Set("links", "https://www.tiktok.com/@kebabradar\r\n\r\ninstagram.com/kebab\nhttps://www.tiktok.com/@kebabradar\nkebab.example.com/press")
	if r := owner.post("/en/new", p); r.status != 303 {
		t.Fatalf("create: %d\n%s", r.status, r.body)
	}
	page := owner.get("/en/p/kebab-radar")
	expect(t, page, 200, "Elsewhere", `href="https://www.tiktok.com/@kebabradar"`, ">TikTok<",
		`href="https://instagram.com/kebab"`, ">Instagram<", ">kebab.example.com/press<")
	if n := strings.Count(page.body, ">TikTok<"); n != 1 {
		t.Errorf("TikTok link shown %d times, want 1", n)
	}

	// The editor shows the saved links one per line; clearing them works.
	expect(t, owner.get("/en/p/kebab-radar/edit"), 200,
		"https://www.tiktok.com/@kebabradar\nhttps://instagram.com/kebab\nhttps://kebab.example.com/press</textarea>")
	p.Set("links", "")
	if r := owner.post("/en/p/kebab-radar/edit", p); r.status != 303 {
		t.Fatalf("edit: %d\n%s", r.status, r.body)
	}
	if body := owner.get("/en/p/kebab-radar").body; strings.Contains(body, "Elsewhere") {
		t.Error("links still shown after clearing them")
	}
}
