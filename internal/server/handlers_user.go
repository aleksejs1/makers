package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/aleksejs1/makers/internal/slug"
	"github.com/aleksejs1/makers/internal/store"
)

type profileData struct {
	Profile  *store.User
	Projects []*store.Project
	IsSelf   bool
}

func (s *Server) profile(w http.ResponseWriter, r *http.Request, v *View) error {
	u, err := s.store.UserByHandle(r.Context(), r.PathValue("handle"))
	if err != nil {
		return err
	}
	isSelf := v.User != nil && v.User.ID == u.ID
	isAdmin := v.User != nil && v.User.IsAdmin
	if u.BannedAt != nil && !isAdmin {
		return errNotFound
	}
	if u.Handle != r.PathValue("handle") {
		http.Redirect(w, r, v.URL("/u/"+u.Handle), http.StatusMovedPermanently)
		return nil
	}
	projects, _, err := s.store.ListProjects(r.Context(), store.ListParams{
		OwnerID: u.ID, Limit: 200, ViewerID: v.UserID(), IncludeHidden: isSelf || isAdmin,
	})
	if err != nil {
		return err
	}
	v.Title = u.Name + " (@" + u.Handle + ")"
	v.Description = u.Bio
	v.OGType = "profile"
	v.NoIndex = len(projects) == 0
	v.Data = profileData{Profile: u, Projects: projects, IsSelf: isSelf}
	return s.render(w, http.StatusOK, "profile", v)
}

type settingsData struct {
	Form   store.User
	Errors formErrors
	Next   string
}

func (s *Server) settingsForm(w http.ResponseWriter, r *http.Request, v *View) error {
	if !requireUser(w, r, v) {
		return nil
	}
	return s.renderSettings(w, http.StatusOK, v, settingsData{
		Form: *v.User, Errors: formErrors{}, Next: safeNext(r.URL.Query().Get("next"), ""),
	})
}

func (s *Server) renderSettings(w http.ResponseWriter, status int, v *View, d settingsData) error {
	v.Title = v.T("settings.title")
	v.NoIndex = true
	v.Data = d
	return s.render(w, status, "settings", v)
}

func (s *Server) settingsSave(w http.ResponseWriter, r *http.Request, v *View) error {
	if !requireUser(w, r, v) {
		return nil
	}
	if err := r.ParseForm(); err != nil {
		return err
	}
	f := r.PostForm
	u := *v.User
	u.Name = clean(f.Get("display_name"))
	u.Handle = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(f.Get("handle")), "@"))
	u.Bio = multiline(f.Get("bio"))
	u.Website = normalizeURL(f.Get("website"))
	u.GitHub = normalizeURL(f.Get("github"))
	u.Social = normalizeURL(f.Get("social"))
	d := settingsData{Form: u, Errors: formErrors{}, Next: safeNext(f.Get("next"), "")}

	switch n := utf8.RuneCountInString(u.Name); {
	case n == 0:
		d.Errors["display_name"] = "form.error.required"
	case n > maxUserName:
		d.Errors["display_name"] = "form.error.too_long"
	}
	if !slug.ValidHandle(u.Handle) {
		d.Errors["handle"] = "settings.error.handle"
	}
	if utf8.RuneCountInString(u.Bio) > maxBio {
		d.Errors["bio"] = "form.error.too_long"
	}
	for field, val := range map[string]string{"website": u.Website, "github": u.GitHub, "social": u.Social} {
		if val != "" && !validURL(val) {
			d.Errors[field] = "form.error.url"
		}
	}
	if len(d.Errors) > 0 {
		return s.renderSettings(w, http.StatusUnprocessableEntity, v, d)
	}
	err := s.store.UpdateProfile(r.Context(), &u)
	if errors.Is(err, store.ErrHandleTaken) {
		d.Errors["handle"] = "settings.error.handle_taken"
		return s.renderSettings(w, http.StatusUnprocessableEntity, v, d)
	}
	if err != nil {
		return err
	}
	s.setFlash(w, "settings.saved")
	http.Redirect(w, r, safeNext(d.Next, v.URL("/u/"+u.Handle)), http.StatusSeeOther)
	return nil
}

func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request, v *View) error {
	if !requireUser(w, r, v) {
		return nil
	}
	if err := s.store.DeleteUserSessions(r.Context(), v.User.ID); err != nil {
		return err
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, v.URL("/"), http.StatusSeeOther)
	return nil
}

func (s *Server) accountDelete(w http.ResponseWriter, r *http.Request, v *View) error {
	if !requireUser(w, r, v) {
		return nil
	}
	if err := r.ParseForm(); err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(r.PostForm.Get("confirm")), "@"), v.User.Handle) {
		return s.renderSettings(w, http.StatusUnprocessableEntity, v, settingsData{
			Form: *v.User, Errors: formErrors{"delete": "settings.delete.mismatch"},
		})
	}
	if err := s.store.DeleteUser(r.Context(), v.User.ID); err != nil {
		return err
	}
	s.log.Info("account deleted", "user_id", strconv.FormatInt(v.User.ID, 10))
	s.clearSessionCookie(w)
	s.setFlash(w, "settings.deleted")
	http.Redirect(w, r, v.URL("/"), http.StatusSeeOther)
	return nil
}
