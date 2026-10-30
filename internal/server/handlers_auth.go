package server

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/aleksejs1/makers/internal/auth"
	"github.com/aleksejs1/makers/internal/mail"
	"github.com/aleksejs1/makers/internal/store"
)

// Login code limits. Codes are bound to the requesting browser (see
// loginNonce), so nobody else can guess, burn or replace a user's code; the
// per-address limit only protects the mailbox from flooding.
//
// Brute force: an attacker can only guess codes they requested themselves,
// at most otpPerEmailPerHour codes × CodeMaxAttempts guesses per hour out of
// 10^8 codes — about 1.3% per year of non-stop attack on one address.
var otpLimits = store.OTPLimits{Cooldown: time.Minute, PerHour: 30}

type loginData struct {
	Email  string
	Next   string
	Error  string // i18n key
	Notice string // i18n key of an informational message
	Sent   bool   // code was sent; show the code form
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request, v *View) error {
	next := safeNext(r.URL.Query().Get("next"), v.URL("/"))
	if v.User != nil {
		http.Redirect(w, r, next, http.StatusSeeOther)
		return nil
	}
	return s.renderLogin(w, http.StatusOK, v, loginData{Next: next})
}

func (s *Server) renderLogin(w http.ResponseWriter, status int, v *View, d loginData) error {
	v.Title = v.T("login.title")
	v.NoIndex = true
	v.Data = d
	return s.render(w, status, "login", v)
}

// loginNonce returns the browser's login nonce, creating the cookie if it
// is missing or malformed.
func (s *Server) loginNonce(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(auth.LoginCookieName); err == nil && len(c.Value) == 43 {
		return c.Value
	}
	nonce := auth.NewToken()
	http.SetCookie(w, &http.Cookie{
		Name: auth.LoginCookieName, Value: nonce, Path: "/", MaxAge: int(auth.LoginCookieTTL.Seconds()),
		HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
	return nonce
}

func (s *Server) loginSend(w http.ResponseWriter, r *http.Request, v *View) error {
	if err := r.ParseForm(); err != nil {
		return err
	}
	d := loginData{Email: r.PostForm.Get("email"), Next: safeNext(r.PostForm.Get("next"), v.URL("/"))}
	if r.PostForm.Get("homepage") != "" { // honeypot
		d.Sent = true
		return s.renderLogin(w, http.StatusOK, v, d)
	}
	email, ok := auth.NormalizeEmail(d.Email)
	if !ok {
		d.Error = "login.error.email"
		return s.renderLogin(w, http.StatusUnprocessableEntity, v, d)
	}
	d.Email = email

	if !s.limiter.allow("otp-ip:"+s.limitKey(r), 10, time.Hour) {
		d.Error = "error.rate_limited"
		return s.renderLogin(w, http.StatusTooManyRequests, v, d)
	}

	nonceHash := auth.HashToken(s.loginNonce(w, r))
	code := auth.NewCode()
	id, err := s.store.CreateOTP(r.Context(), email, nonceHash,
		auth.HashCode(s.cfg.Secret, email, code), auth.CodeTTL, otpLimits)
	switch {
	case errors.Is(err, store.ErrOTPCooldown):
		// This browser just got a code; it is in the mailbox.
		d.Sent, d.Notice = true, "login.already_sent"
		return s.renderLogin(w, http.StatusOK, v, d)
	case errors.Is(err, store.ErrOTPLimit):
		d.Error = "error.rate_limited"
		return s.renderLogin(w, http.StatusTooManyRequests, v, d)
	case err != nil:
		return err
	}

	msg := mail.Message{
		To:      email,
		Subject: v.T("email.otp.subject", "Code", auth.FormatCode(code)),
		Body:    v.T("email.otp.body", "Code", auth.FormatCode(code), "Minutes", int(auth.CodeTTL.Minutes()), "BaseURL", s.cfg.BaseURL),
	}
	if err := s.mail.Send(r.Context(), msg); err != nil {
		s.log.Error("send login code", "err", err)
		// An undelivered code must neither count against the limits nor
		// replace a code the user may already have.
		if err := s.store.DeleteOTP(r.Context(), id); err != nil {
			return err
		}
		d.Error = "login.error.send"
		return s.renderLogin(w, http.StatusBadGateway, v, d)
	}
	if err := s.store.ActivateOTP(r.Context(), id); err != nil {
		return err
	}
	d.Sent = true
	return s.renderLogin(w, http.StatusOK, v, d)
}

func (s *Server) loginVerify(w http.ResponseWriter, r *http.Request, v *View) error {
	if err := r.ParseForm(); err != nil {
		return err
	}
	d := loginData{Sent: true, Next: safeNext(r.PostForm.Get("next"), v.URL("/"))}
	email, ok := auth.NormalizeEmail(r.PostForm.Get("email"))
	if !ok {
		return s.renderLogin(w, http.StatusUnprocessableEntity, v, loginData{Next: d.Next, Error: "login.error.email"})
	}
	d.Email = email
	code := auth.NormalizeCode(r.PostForm.Get("code"))

	if !s.limiter.allow("verify-ip:"+s.limitKey(r), 30, time.Hour) {
		d.Error = "error.rate_limited"
		return s.renderLogin(w, http.StatusTooManyRequests, v, d)
	}
	expired := loginData{Email: email, Next: d.Next, Error: "login.error.expired"}
	c, err := r.Cookie(auth.LoginCookieName)
	if err != nil {
		expired.Error = "login.error.browser"
		return s.renderLogin(w, http.StatusUnprocessableEntity, v, expired)
	}
	otp, err := s.store.ActiveOTP(r.Context(), email, auth.HashToken(c.Value))
	if errors.Is(err, store.ErrNotFound) {
		return s.renderLogin(w, http.StatusUnprocessableEntity, v, expired)
	}
	if err != nil {
		return err
	}
	// Reserve the attempt before checking the code (see ClaimOTPAttempt).
	attempt, ok, err := s.store.ClaimOTPAttempt(r.Context(), otp.ID, auth.CodeMaxAttempts)
	if err != nil {
		return err
	}
	if !ok {
		return s.renderLogin(w, http.StatusUnprocessableEntity, v, expired)
	}
	if len(code) != auth.CodeLength || !auth.CheckCode(s.cfg.Secret, email, code, otp.CodeHash) {
		if attempt >= auth.CodeMaxAttempts {
			return s.renderLogin(w, http.StatusUnprocessableEntity, v, expired)
		}
		d.Error = "login.error.code"
		return s.renderLogin(w, http.StatusUnprocessableEntity, v, d)
	}
	if used, err := s.store.UseOTP(r.Context(), otp.ID); err != nil {
		return err
	} else if !used {
		return s.renderLogin(w, http.StatusUnprocessableEntity, v, expired)
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.LoginCookieName, Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})

	u, err := s.store.UserByEmail(r.Context(), email)
	isNew := false
	if errors.Is(err, store.ErrNotFound) {
		u, err = s.store.CreateUser(r.Context(), email)
		isNew = true
	}
	if err != nil {
		return err
	}
	if u.BannedAt != nil {
		return s.renderLogin(w, http.StatusForbidden, v, loginData{Next: d.Next, Error: "login.error.banned"})
	}

	token := auth.NewToken()
	if err := s.store.CreateSession(r.Context(), auth.HashToken(token), u.ID, auth.SessionTTL); err != nil {
		return err
	}
	s.setSessionCookie(w, token)
	if isNew {
		s.setFlash(w, "settings.welcome")
		http.Redirect(w, r, v.URL("/settings?next="+url.QueryEscape(d.Next)), http.StatusSeeOther)
		return nil
	}
	http.Redirect(w, r, d.Next, http.StatusSeeOther)
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, v *View) error {
	if hash, ok := r.Context().Value(tokenKey).(string); ok {
		if err := s.store.DeleteSession(r.Context(), hash); err != nil {
			return err
		}
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, v.URL("/"), http.StatusSeeOther)
	return nil
}

// requireUser redirects anonymous visitors to the login page and reports
// whether the request may continue.
func requireUser(w http.ResponseWriter, r *http.Request, v *View) bool {
	if v.User != nil {
		return true
	}
	next := v.URL(v.Path)
	if r.Method == http.MethodGet {
		if r.URL.RawQuery != "" {
			next += "?" + r.URL.RawQuery
		}
	} else {
		// After login, return to the page the form was on.
		next = v.URL("/")
		if ref, err := url.Parse(r.Referer()); err == nil && ref.Host == r.Host {
			next = safeNext(ref.RequestURI(), next)
		}
	}
	redirect(w, r, v.URL("/login?next="+url.QueryEscape(next)))
	return false
}
