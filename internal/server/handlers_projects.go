package server

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/aleksejs1/makers/internal/store"
)

const (
	pageSize = 30
	maxPage  = 10_000 // keeps offsets far from integer overflow
)

type homeData struct {
	Projects   []*store.Project
	Total      int
	Filters    homeFilters
	Page       int
	HasNext    bool
	TechHints  []string
	Categories []string
	Statuses   []string
}

type homeFilters struct {
	Q, Category, Status, Tech, AI, Sort string
}

// QueryString encodes the filters with an optional page number.
func (f homeFilters) QueryString(page int) string {
	q := url.Values{}
	for k, v := range map[string]string{"q": f.Q, "category": f.Category, "status": f.Status, "tech": f.Tech, "ai": f.AI} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if f.Sort != store.SortNew {
		q.Set("sort", f.Sort)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// Active reports whether any filter is set.
func (f homeFilters) Active() bool {
	return f.Q != "" || f.Category != "" || f.Status != "" || f.Tech != "" || f.AI != ""
}

// WithoutAI returns the filters with the AI tool filter removed.
func (f homeFilters) WithoutAI() homeFilters {
	f.AI = ""
	return f
}

func (s *Server) home(w http.ResponseWriter, r *http.Request, v *View) error {
	q := r.URL.Query()
	f := homeFilters{Q: clean(q.Get("q")), Category: q.Get("category"), Status: q.Get("status"), Tech: clean(q.Get("tech")), AI: clean(q.Get("ai")), Sort: q.Get("sort")}
	if !oneOf(f.Category, Categories) {
		f.Category = ""
	}
	if !oneOf(f.Status, Statuses) {
		f.Status = ""
	}
	if f.Sort != store.SortPopular && f.Sort != store.SortLaunch {
		f.Sort = store.SortNew
	}
	page, _ := strconv.Atoi(q.Get("page"))
	page = min(max(page, 1), maxPage)

	projects, total, err := s.store.ListProjects(r.Context(), store.ListParams{
		Query: f.Q, Category: f.Category, Status: f.Status, Tech: f.Tech, AI: f.AI, Sort: f.Sort,
		Limit: pageSize, Offset: (page - 1) * pageSize, ViewerID: v.UserID(),
	})
	if err != nil {
		return err
	}
	if last := max(1, (total+pageSize-1)/pageSize); page > last {
		redirect(w, r, v.URL("/")+f.QueryString(last))
		return nil
	}
	d := homeData{
		Projects: projects, Total: total, Filters: f, Page: page,
		HasNext: page*pageSize < total, Categories: Categories, Statuses: Statuses,
	}
	// The same URL serves a full page or an htmx fragment; caches must keep
	// them apart, and fragments are never cached (htmx history restores
	// would otherwise show a bare fragment).
	w.Header().Set("Vary", "HX-Request, HX-Target")
	if isHTMX(r) && r.Header.Get("HX-Target") == "projects" {
		w.Header().Set("Cache-Control", "no-store")
		return s.renderPartial(w, "project_list", pair(v, d))
	}
	if d.TechHints, err = s.store.PopularTags(r.Context(), store.TagTech, 30); err != nil {
		return err
	}
	v.Title = v.T("home.title")
	v.Description = v.T("home.description")
	v.NoIndex = f.Active() || page > 1
	v.Data = d
	return s.render(w, http.StatusOK, "home", v)
}

func pair(v *View, x any) Pair { return Pair{V: v, X: x} }

type projectData struct {
	Project  *store.Project
	Comments []*store.Comment
	Body     string    // comment draft after a validation error
	Error    string    // comment error key
	Now      time.Time // for "launching soon"
}

// loadProject returns the project for the slug path value if the viewer
// may see it.
func (s *Server) loadProject(r *http.Request, v *View) (*store.Project, error) {
	p, err := s.store.ProjectBySlug(r.Context(), r.PathValue("slug"), v.UserID())
	if err != nil {
		return nil, err
	}
	if (p.HiddenAt != nil || p.OwnerBanned) && !v.CanEdit(p.OwnerID) {
		return nil, errNotFound
	}
	return p, nil
}

func (s *Server) projectShow(w http.ResponseWriter, r *http.Request, v *View) error {
	p, err := s.loadProject(r, v)
	if err != nil {
		return err
	}
	comments, err := s.store.ListComments(r.Context(), p.ID, v.User != nil && v.User.IsAdmin)
	if err != nil {
		return err
	}
	v.Title = p.Name + " — " + p.Tagline
	v.Description = p.Tagline
	v.OGType = "article"
	v.NoIndex = p.HiddenAt != nil
	v.Data = projectData{Project: p, Comments: comments, Now: time.Now()}
	return s.render(w, http.StatusOK, "project", v)
}

func (s *Server) projectNew(w http.ResponseWriter, r *http.Request, v *View) error {
	if !requireUser(w, r, v) {
		return nil
	}
	f := projectForm{Errors: formErrors{}}
	f.In.Status = "building"
	f.In.ContentLang = v.Lang
	return s.renderProjectForm(w, r, http.StatusOK, v, f)
}

func (s *Server) renderProjectForm(w http.ResponseWriter, r *http.Request, status int, v *View, f projectForm) error {
	var err error
	if f.TechHints, err = s.store.PopularTags(r.Context(), store.TagTech, 100); err != nil {
		return err
	}
	if f.AIHints, err = s.store.PopularTags(r.Context(), store.TagAI, 50); err != nil {
		return err
	}
	if f.Project == nil {
		v.Title = v.T("project.new.title")
	} else {
		v.Title = v.T("project.edit.title", "Name", f.Project.Name)
	}
	v.NoIndex = true
	v.Data = f
	return s.render(w, status, "project_form", v)
}

func (s *Server) projectCreate(w http.ResponseWriter, r *http.Request, v *View) error {
	if !requireUser(w, r, v) {
		return nil
	}
	if err := r.ParseForm(); err != nil {
		return err
	}
	f := parseProjectForm(r)
	if r.PostForm.Get("homepage") != "" { // honeypot
		http.Redirect(w, r, v.URL("/"), http.StatusSeeOther)
		return nil
	}
	if len(f.Errors) > 0 {
		return s.renderProjectForm(w, r, http.StatusUnprocessableEntity, v, f)
	}
	n, err := s.store.CountUserProjectsSince(r.Context(), v.User.ID, time.Now().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	if n >= 10 && !v.User.IsAdmin {
		f.Errors["form"] = "error.rate_limited"
		return s.renderProjectForm(w, r, http.StatusTooManyRequests, v, f)
	}
	p, err := s.store.CreateProject(r.Context(), v.User.ID, f.In)
	if err != nil {
		return err
	}
	s.setFlash(w, "project.created")
	http.Redirect(w, r, v.URL("/p/"+p.Slug), http.StatusSeeOther)
	return nil
}

// editableProject loads a project the current user may edit.
func (s *Server) editableProject(w http.ResponseWriter, r *http.Request, v *View) (*store.Project, bool, error) {
	if !requireUser(w, r, v) {
		return nil, false, nil
	}
	p, err := s.loadProject(r, v)
	if err != nil {
		return nil, false, err
	}
	if !v.CanEdit(p.OwnerID) {
		return nil, false, errForbidden
	}
	return p, true, nil
}

func (s *Server) projectEdit(w http.ResponseWriter, r *http.Request, v *View) error {
	p, ok, err := s.editableProject(w, r, v)
	if !ok {
		return err
	}
	return s.renderProjectForm(w, r, http.StatusOK, v, projectToForm(p))
}

func (s *Server) projectUpdate(w http.ResponseWriter, r *http.Request, v *View) error {
	p, ok, err := s.editableProject(w, r, v)
	if !ok {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return err
	}
	f := parseProjectForm(r)
	f.Project = p
	if len(f.Errors) > 0 {
		return s.renderProjectForm(w, r, http.StatusUnprocessableEntity, v, f)
	}
	if err := s.store.UpdateProject(r.Context(), p.ID, f.In); err != nil {
		return err
	}
	s.setFlash(w, "project.saved")
	http.Redirect(w, r, v.URL("/p/"+p.Slug), http.StatusSeeOther)
	return nil
}

func (s *Server) projectDelete(w http.ResponseWriter, r *http.Request, v *View) error {
	p, ok, err := s.editableProject(w, r, v)
	if !ok {
		return err
	}
	if err := r.ParseForm(); err != nil {
		return err
	}
	if r.PostForm.Get("confirm") != "1" {
		f := projectToForm(p)
		f.Errors["delete"] = "project.delete.confirm_required"
		return s.renderProjectForm(w, r, http.StatusUnprocessableEntity, v, f)
	}
	if err := s.store.DeleteProject(r.Context(), p.ID); err != nil {
		return err
	}
	s.setFlash(w, "project.deleted")
	http.Redirect(w, r, v.URL("/u/"+p.OwnerHandle), http.StatusSeeOther)
	return nil
}
