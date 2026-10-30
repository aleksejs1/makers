package server

import (
	"net/http"
	"strconv"
)

func requireAdmin(v *View) error {
	if v.User == nil || !v.User.IsAdmin {
		return errForbidden
	}
	return nil
}

func (s *Server) adminProjectHide(w http.ResponseWriter, r *http.Request, v *View) error {
	if err := requireAdmin(v); err != nil {
		return err
	}
	p, err := s.store.ProjectBySlug(r.Context(), r.PathValue("slug"), 0)
	if err != nil {
		return err
	}
	if err := s.store.SetProjectHidden(r.Context(), p.ID, p.HiddenAt == nil); err != nil {
		return err
	}
	s.log.Info("admin: project visibility changed", "admin", v.User.Handle, "project", p.Slug, "hidden", p.HiddenAt == nil)
	http.Redirect(w, r, v.URL("/p/"+p.Slug), http.StatusSeeOther)
	return nil
}

func (s *Server) adminCommentHide(w http.ResponseWriter, r *http.Request, v *View) error {
	if err := requireAdmin(v); err != nil {
		return err
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, err := s.store.CommentByID(r.Context(), id)
	if err != nil {
		return err
	}
	if err := s.store.SetCommentHidden(r.Context(), c.ID, c.HiddenAt == nil); err != nil {
		return err
	}
	s.log.Info("admin: comment visibility changed", "admin", v.User.Handle, "comment", c.ID, "hidden", c.HiddenAt == nil)
	return s.backToProjectComments(w, r, v, c.ProjectID)
}

func (s *Server) adminUserBan(w http.ResponseWriter, r *http.Request, v *View) error {
	if err := requireAdmin(v); err != nil {
		return err
	}
	u, err := s.store.UserByHandle(r.Context(), r.PathValue("handle"))
	if err != nil {
		return err
	}
	if u.ID == v.User.ID || s.cfg.IsAdmin(u.Email) {
		return errForbidden
	}
	if err := s.store.SetBanned(r.Context(), u.ID, u.BannedAt == nil); err != nil {
		return err
	}
	s.log.Info("admin: user ban changed", "admin", v.User.Handle, "user", u.Handle, "banned", u.BannedAt == nil)
	http.Redirect(w, r, v.URL("/u/"+u.Handle), http.StatusSeeOther)
	return nil
}
