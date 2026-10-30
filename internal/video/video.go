// Package video turns links to supported video hosts into embeddable URLs.
package video

import (
	"net/url"
	"regexp"
	"strings"
)

// FrameSources lists the origins used for embeds (for the CSP header).
var FrameSources = []string{
	"https://www.youtube-nocookie.com",
	"https://player.vimeo.com",
	"https://www.loom.com",
}

var (
	youtubeID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	numericID = regexp.MustCompile(`^[0-9]{1,12}$`)
	loomID    = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

// EmbedURL returns the iframe URL for a YouTube, Vimeo or Loom link,
// or "" if the link is not recognized.
func EmbedURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")

	switch host {
	case "youtube.com", "m.youtube.com", "youtube-nocookie.com":
		id := ""
		switch {
		case len(parts) == 1 && parts[0] == "watch":
			id = u.Query().Get("v")
		case len(parts) == 2 && (parts[0] == "embed" || parts[0] == "shorts" || parts[0] == "live"):
			id = parts[1]
		}
		if youtubeID.MatchString(id) {
			return "https://www.youtube-nocookie.com/embed/" + id
		}
	case "youtu.be":
		if len(parts) == 1 && youtubeID.MatchString(parts[0]) {
			return "https://www.youtube-nocookie.com/embed/" + parts[0]
		}
	case "vimeo.com", "player.vimeo.com":
		id := parts[len(parts)-1]
		if numericID.MatchString(id) {
			return "https://player.vimeo.com/video/" + id + "?dnt=1"
		}
	case "loom.com":
		if len(parts) == 2 && (parts[0] == "share" || parts[0] == "embed") && loomID.MatchString(parts[1]) {
			return "https://www.loom.com/embed/" + parts[1]
		}
	}
	return ""
}
