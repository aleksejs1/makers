package server

import (
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"
)

func (s *Server) like(w http.ResponseWriter, r *http.Request, v *View) error {
	if !requireUser(w, r, v) {
		return nil
	}
	p, err := s.loadProject(r, v)
	if err != nil {
		return err
	}
	if !s.limiter.allow("like:"+strconv.FormatInt(v.User.ID, 10), 120, time.Hour) {
		return s.tooManyRequests(w, r, v)
	}
	if p.Liked, p.Likes, err = s.store.ToggleLike(r.Context(), v.User.ID, p.ID); err != nil {
		return err
	}
	if isHTMX(r) {
		return s.renderPartial(w, "like_button", pair(v, p))
	}
	http.Redirect(w, r, v.URL("/p/"+p.Slug), http.StatusSeeOther)
	return nil
}

func (s *Server) commentCreate(w http.ResponseWriter, r *http.Request, v *View) error {
	if !requireUser(w, r, v) {
		return nil
	}
	p, err := s.loadProject(r, v)
	if err != nil {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return err
	}
	body := multiline(r.PostForm.Get("body"))
	d := projectData{Project: p, Body: body, Now: time.Now()}
	switch n := utf8.RuneCountInString(body); {
	case n == 0:
		d.Error = "form.error.required"
	case n > maxComment:
		d.Error = "form.error.too_long"
	}
	if d.Error == "" && !v.User.IsAdmin {
		n, err := s.store.CountUserCommentsSince(r.Context(), v.User.ID, time.Now().Add(-time.Hour))
		if err != nil {
			return err
		}
		if n >= 20 {
			d.Error = "error.rate_limited"
		}
	}
	if d.Error == "" && r.PostForm.Get("homepage") == "" { // honeypot
		if err := s.store.CreateComment(r.Context(), p.ID, v.User.ID, body); err != nil {
			return err
		}
		d.Body = ""
	}
	if !isHTMX(r) && d.Error == "" {
		http.Redirect(w, r, v.URL("/p/"+p.Slug)+"#comments", http.StatusSeeOther)
		return nil
	}
	if d.Comments, err = s.store.ListComments(r.Context(), p.ID, v.User.IsAdmin); err != nil {
		return err
	}
	if isHTMX(r) {
		return s.renderPartial(w, "comments", pair(v, d))
	}
	v.Title = p.Name
	v.Data = d
	return s.render(w, http.StatusUnprocessableEntity, "project", v)
}

func (s *Server) commentDelete(w http.ResponseWriter, r *http.Request, v *View) error {
	if !requireUser(w, r, v) {
		return nil
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, err := s.store.CommentByID(r.Context(), id)
	if err != nil {
		return err
	}
	if !v.CanEdit(c.UserID) {
		return errForbidden
	}
	if err := s.store.DeleteComment(r.Context(), c.ID); err != nil {
		return err
	}
	return s.backToProjectComments(w, r, v, c.ProjectID)
}

// backToProjectComments re-renders the comment section for htmx, or
// redirects to the project page.
func (s *Server) backToProjectComments(w http.ResponseWriter, r *http.Request, v *View, projectID int64) error {
	slug := r.PostFormValue("slug")
	p, err := s.store.ProjectBySlug(r.Context(), slug, v.UserID())
	if err != nil || p.ID != projectID {
		redirect(w, r, v.URL("/"))
		return nil //nolint:nilerr // fall back to the home page
	}
	if !isHTMX(r) {
		http.Redirect(w, r, v.URL("/p/"+p.Slug)+"#comments", http.StatusSeeOther)
		return nil
	}
	comments, err := s.store.ListComments(r.Context(), p.ID, v.User != nil && v.User.IsAdmin)
	if err != nil {
		return err
	}
	return s.renderPartial(w, "comments", pair(v, projectData{Project: p, Comments: comments}))
}

func (s *Server) tooManyRequests(w http.ResponseWriter, r *http.Request, v *View) error {
	s.renderError(w, r, v, http.StatusTooManyRequests)
	return nil
}
