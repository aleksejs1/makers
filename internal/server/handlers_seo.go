package server

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"time"

	"github.com/aleksejs1/makers/internal/i18n"
	"github.com/aleksejs1/makers/internal/store"
)

func (s *Server) staticPage(name string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request, v *View) error {
		v.Title = v.T(name + ".title")
		return s.render(w, http.StatusOK, name, v)
	}
}

func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "User-agent: *\nDisallow: /*/login\nDisallow: /*/settings\nDisallow: /*/new\n\nSitemap: %s/sitemap.xml\n", s.cfg.BaseURL)
}

type sitemapURL struct {
	Loc     string        `xml:"loc"`
	LastMod string        `xml:"lastmod,omitempty"`
	Links   []sitemapLink `xml:"xhtml:link"`
}

type sitemapLink struct {
	Rel      string `xml:"rel,attr"`
	Hreflang string `xml:"hreflang,attr"`
	Href     string `xml:"href,attr"`
}

// maxSitemapURLs is the sitemap protocol's limit per file. Beyond it a
// sitemap index would be needed; until then the newest entries are cut.
var maxSitemapURLs = 50_000

func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.PublicProjectSlugs(r.Context())
	if err != nil {
		s.log.Error("sitemap", "err", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	handles, err := s.store.AllUserHandles(r.Context())
	if err != nil {
		s.log.Error("sitemap", "err", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	var urls []sitemapURL
	add := func(path string, mod time.Time) {
		if len(urls)+len(i18n.Langs) > maxSitemapURLs {
			return
		}
		var links []sitemapLink
		for _, l := range i18n.Langs {
			links = append(links, sitemapLink{"alternate", l, s.cfg.BaseURL + "/" + l + path})
		}
		for _, l := range i18n.Langs {
			u := sitemapURL{Loc: s.cfg.BaseURL + "/" + l + path, Links: links}
			if !mod.IsZero() {
				u.LastMod = mod.Format(time.DateOnly)
			}
			urls = append(urls, u)
		}
	}
	add("/", time.Time{})
	add("/about", time.Time{})
	for _, p := range projects {
		add("/p/"+p.Slug, p.UpdatedAt)
	}
	for _, h := range handles {
		add("/u/"+h, time.Time{})
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	_ = enc.Encode(struct {
		XMLName xml.Name     `xml:"urlset"`
		NS      string       `xml:"xmlns,attr"`
		XHTML   string       `xml:"xmlns:xhtml,attr"`
		URLs    []sitemapURL `xml:"url"`
	}{NS: "http://www.sitemaps.org/schemas/sitemap/0.9", XHTML: "http://www.w3.org/1999/xhtml", URLs: urls})
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Description string `xml:"description"`
	PubDate     string `xml:"pubDate"`
}

// feed serves an RSS feed of the newest projects. The language is taken
// from ?lang= and defaults to Latvian.
func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	if !i18n.Supported(lang) {
		lang = i18n.Default
	}
	projects, _, err := s.store.ListProjects(r.Context(), store.ListParams{Sort: store.SortNew, Limit: 30})
	if err != nil {
		s.log.Error("feed", "err", err)
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	items := make([]rssItem, 0, len(projects))
	for _, p := range projects {
		link := s.cfg.BaseURL + "/" + lang + "/p/" + p.Slug
		items = append(items, rssItem{
			Title: p.Name, Link: link, GUID: link, Description: p.Tagline,
			PubDate: p.CreatedAt.Format(time.RFC1123Z),
		})
	}
	type channel struct {
		Title       string    `xml:"title"`
		Link        string    `xml:"link"`
		Description string    `xml:"description"`
		Language    string    `xml:"language"`
		Items       []rssItem `xml:"item"`
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	_ = xml.NewEncoder(w).Encode(struct {
		XMLName xml.Name `xml:"rss"`
		Version string   `xml:"version,attr"`
		Channel channel  `xml:"channel"`
	}{Version: "2.0", Channel: channel{
		Title:       "makers.lv",
		Link:        s.cfg.BaseURL + "/" + lang + "/",
		Description: s.i18n.T(lang, "home.description"),
		Language:    lang,
		Items:       items,
	}})
}
