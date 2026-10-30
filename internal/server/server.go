// Package server implements the HTTP interface of makers.lv.
package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/aleksejs1/makers/internal/config"
	"github.com/aleksejs1/makers/internal/i18n"
	"github.com/aleksejs1/makers/internal/mail"
	"github.com/aleksejs1/makers/internal/store"
)

// Server holds dependencies shared by all handlers.
type Server struct {
	cfg     *config.Config
	store   *store.Store
	mail    mail.Sender
	log     *slog.Logger
	i18n    *i18n.Bundle
	tmpl    *templates
	assets  fs.FS
	hashes  map[string]string // static file path -> content hash
	version string
	limiter *limiter
}

// Options configures New.
type Options struct {
	Config  *config.Config
	Store   *store.Store
	Mail    mail.Sender
	Logger  *slog.Logger
	UI      fs.FS // contains templates/, static/ and locales/
	Version string
}

// New creates a Server.
func New(o Options) (*Server, error) {
	bundle, err := i18n.Load(o.UI)
	if err != nil {
		return nil, err
	}
	static, err := fs.Sub(o.UI, "static")
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:     o.Config,
		store:   o.Store,
		mail:    o.Mail,
		log:     o.Logger,
		i18n:    bundle,
		assets:  static,
		version: o.Version,
		limiter: newLimiter(),
	}
	if s.hashes, err = hashAssets(static); err != nil {
		return nil, err
	}
	if s.tmpl, err = loadTemplates(o.UI, s.funcs()); err != nil {
		return nil, err
	}
	return s, nil
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.rootRedirect)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /robots.txt", s.robots)
	mux.HandleFunc("GET /sitemap.xml", s.sitemap)
	mux.HandleFunc("GET /feed.xml", s.feed)
	mux.Handle("GET /static/", http.StripPrefix("/static/", s.staticHandler()))
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.assetURL("favicon.svg"), http.StatusMovedPermanently)
	})

	for _, lang := range i18n.Langs {
		p := "/" + lang
		h := func(fn handlerFunc) http.Handler { return s.wrap(lang, fn) }

		mux.HandleFunc("GET "+p, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, p+"/", http.StatusMovedPermanently)
		})
		mux.Handle("GET "+p+"/{$}", h(s.home))
		mux.Handle("GET "+p+"/about", h(s.staticPage("about")))
		mux.Handle("GET "+p+"/privacy", h(s.staticPage("privacy")))

		mux.Handle("GET "+p+"/login", h(s.loginForm))
		mux.Handle("POST "+p+"/login", h(s.loginSend))
		mux.Handle("POST "+p+"/login/verify", h(s.loginVerify))
		mux.Handle("POST "+p+"/logout", h(s.logout))

		mux.Handle("GET "+p+"/new", h(s.projectNew))
		mux.Handle("POST "+p+"/new", h(s.projectCreate))
		mux.Handle("GET "+p+"/p/{slug}", h(s.projectShow))
		mux.Handle("GET "+p+"/p/{slug}/edit", h(s.projectEdit))
		mux.Handle("POST "+p+"/p/{slug}/edit", h(s.projectUpdate))
		mux.Handle("POST "+p+"/p/{slug}/delete", h(s.projectDelete))
		mux.Handle("POST "+p+"/p/{slug}/like", h(s.like))
		mux.Handle("POST "+p+"/p/{slug}/comments", h(s.commentCreate))
		mux.Handle("POST "+p+"/comments/{id}/delete", h(s.commentDelete))

		mux.Handle("GET "+p+"/u/{handle}", h(s.profile))
		mux.Handle("GET "+p+"/settings", h(s.settingsForm))
		mux.Handle("POST "+p+"/settings", h(s.settingsSave))
		mux.Handle("POST "+p+"/settings/logout-all", h(s.logoutAll))
		mux.Handle("POST "+p+"/settings/delete", h(s.accountDelete))

		mux.Handle("POST "+p+"/admin/projects/{slug}/hide", h(s.adminProjectHide))
		mux.Handle("POST "+p+"/admin/comments/{id}/hide", h(s.adminCommentHide))
		mux.Handle("POST "+p+"/admin/users/{handle}/ban", h(s.adminUserBan))
	}

	mux.Handle("/", s.wrap("", func(w http.ResponseWriter, r *http.Request, v *View) error {
		return errNotFound
	}))

	var h http.Handler = mux
	h = s.withSession(h)
	h = http.NewCrossOriginProtection().Handler(h)
	h = s.securityHeaders(h)
	h = s.withLogging(h)
	h = s.withRecover(h)
	return h
}

// RunMaintenance periodically removes expired data until ctx is done.
func (s *Server) RunMaintenance(ctx context.Context) {
	if err := s.store.Cleanup(ctx); err != nil && ctx.Err() == nil {
		s.log.Error("cleanup", "err", err)
	}
	db := time.NewTicker(time.Hour)
	defer db.Stop()
	limits := time.NewTicker(time.Minute)
	defer limits.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-limits.C:
			if n := s.limiter.cleanup(); n > 0 {
				s.log.Warn("rate limiter full; requests were not rate limited", "requests", n)
			}
		case <-db.C:
			if err := s.store.Cleanup(ctx); err != nil && ctx.Err() == nil {
				s.log.Error("cleanup", "err", err)
			}
		}
	}
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

// rootRedirect sends visitors to their language: a previously chosen one
// (cookie), else the browser's preference, else Latvian.
func (s *Server) rootRedirect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Vary", "Accept-Language, Cookie")
	http.Redirect(w, r, "/"+s.detectLang(r)+"/", http.StatusFound)
}

func (s *Server) detectLang(r *http.Request) string {
	if c, err := r.Cookie(langCookie); err == nil && i18n.Supported(c.Value) {
		return c.Value
	}
	return s.i18n.Match(r.Header.Get("Accept-Language"))
}
