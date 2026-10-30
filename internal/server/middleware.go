package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/aleksejs1/makers/internal/auth"
	"github.com/aleksejs1/makers/internal/i18n"
	"github.com/aleksejs1/makers/internal/store"
	"github.com/aleksejs1/makers/internal/video"
)

const (
	langCookie  = "makers_lang"
	flashCookie = "makers_flash"
	maxFormSize = 256 << 10
)

var (
	errNotFound  = errors.New("not found")
	errForbidden = errors.New("forbidden")
)

type ctxKey int

const (
	userKey ctxKey = iota
	tokenKey
)

// handlerFunc is a page handler; returned errors are rendered as error pages.
type handlerFunc func(w http.ResponseWriter, r *http.Request, v *View) error

// wrap adapts a handlerFunc for a language-prefixed route. An empty lang
// means the language is detected (used for the catch-all 404).
func (s *Server) wrap(lang string, fn handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := lang
		if lang == "" {
			lang = s.pathLang(r)
		} else if c, err := r.Cookie(langCookie); err != nil || c.Value != lang {
			http.SetCookie(w, &http.Cookie{
				Name: langCookie, Value: lang, Path: "/", MaxAge: 365 * 24 * 3600,
				HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
			})
		}
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, maxFormSize)
		}

		v := s.newView(r, lang)
		if c, err := r.Cookie(flashCookie); err == nil && !isHTMX(r) {
			v.Flash = c.Value
			http.SetCookie(w, &http.Cookie{
				Name: flashCookie, Path: "/", MaxAge: -1,
				HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
			})
		}

		// Pages for a logged-in user or answering a form may contain personal
		// data (e.g. the email on the settings page); keep them out of caches.
		if v.User != nil || r.Method != http.MethodGet {
			w.Header().Set("Cache-Control", "private, no-store")
		}
		err := fn(w, r, v)
		if err == nil {
			return
		}
		switch {
		case errors.Is(err, errNotFound), errors.Is(err, store.ErrNotFound):
			s.renderError(w, r, v, http.StatusNotFound)
		case errors.Is(err, errForbidden):
			s.renderError(w, r, v, http.StatusForbidden)
		default:
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				s.renderError(w, r, v, http.StatusRequestEntityTooLarge)
				return
			}
			s.log.Error("handler", "path", r.URL.Path, "err", err)
			s.renderError(w, r, v, http.StatusInternalServerError)
		}
	})
}

// pathLang takes the language from the first path segment if it is a
// supported one, otherwise detects it.
func (s *Server) pathLang(r *http.Request) string {
	seg, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if i18n.Supported(seg) {
		return seg
	}
	return s.detectLang(r)
}

// setFlash shows a one-time message (an i18n key) on the next page.
func (s *Server) setFlash(w http.ResponseWriter, key string) {
	http.SetCookie(w, &http.Cookie{
		Name: flashCookie, Value: key, Path: "/", MaxAge: 60,
		HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
}

// withSession resolves the session cookie into a user.
func (s *Server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.CookieName)
		if err != nil || c.Value == "" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		hash := auth.HashToken(c.Value)
		u, lastSeen, err := s.store.SessionUser(r.Context(), hash)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				s.clearSessionCookie(w)
			} else {
				// A temporary database problem must not log the user out.
				s.log.Error("session lookup", "err", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		u.IsAdmin = s.cfg.IsAdmin(u.Email)
		if time.Since(lastSeen) > time.Hour {
			if err := s.store.TouchSession(r.Context(), hash, auth.SessionTTL); err != nil {
				s.log.Error("touch session", "err", err)
			}
			s.setSessionCookie(w, c.Value)
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		ctx = context.WithValue(ctx, tokenKey, hash)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.CookieName, Value: token, Path: "/", MaxAge: int(auth.SessionTTL.Seconds()),
		HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.CookieName, Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
}

func currentUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	csp := "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; " +
		"frame-src " + strings.Join(video.FrameSources, " ") + "; " +
		"frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if s.cfg.SecureCookies() {
			h.Set("Strict-Transport-Security", "max-age=63072000")
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/healthz" {
			return
		}
		s.log.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", sw.status),
			slog.Duration("duration", time.Since(start)),
			slog.String("ip", s.clientIP(r)),
		)
	})
}

func (s *Server) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}
				s.log.Error("panic", "err", rec, "stack", string(debug.Stack()))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// clientIP returns the visitor's IP. X-Forwarded-For is only honoured when
// the direct peer is a configured trusted proxy; the header is then read
// from the right, skipping further trusted proxies, so values a client puts
// at the start of the header are ignored.
func (s *Server) clientIP(r *http.Request) string {
	peer := remoteAddr(r)
	if !s.trustedProxy(peer) {
		return peer.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		ip = ip.Unmap()
		if !s.trustedProxy(ip) {
			return ip.String()
		}
		peer = ip
	}
	return peer.String()
}

// limitKey returns the rate-limiting key for the visitor's address. IPv6
// clients are grouped by /64, the usual allocation for a single customer.
func (s *Server) limitKey(r *http.Request) string {
	ip, err := netip.ParseAddr(s.clientIP(r))
	if err != nil {
		return s.clientIP(r)
	}
	if ip.Is6() {
		p, _ := ip.Prefix(64)
		return p.String()
	}
	return ip.String()
}

func (s *Server) trustedProxy(ip netip.Addr) bool {
	for _, p := range s.cfg.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func remoteAddr(r *http.Request) netip.Addr {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.IPv4Unspecified()
	}
	return ap.Addr().Unmap()
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// redirect navigates to url; for htmx requests it asks htmx to do a full
// page navigation instead of swapping the response in.
func redirect(w http.ResponseWriter, r *http.Request, url string) {
	if isHTMX(r) {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// safeNext returns next if it is a path on this site, otherwise fallback.
// Browsers strip tabs and newlines from URLs and treat a backslash like
// "/", so such characters could turn "/<tab>/evil.com" into "//evil.com";
// they are rejected outright.
func safeNext(next, fallback string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsRune(next, '\\') {
		return fallback
	}
	for _, r := range next {
		if r < 0x20 || r == 0x7f {
			return fallback
		}
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return fallback
	}
	return next
}
