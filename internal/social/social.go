// Package social recognizes links to social networks and similar platforms.
package social

import (
	"net/url"
	"strings"
)

// platforms maps a host (without "www.") to the platform's display name.
var platforms = map[string]string{
	"tiktok.com":                "TikTok",
	"vm.tiktok.com":             "TikTok",
	"instagram.com":             "Instagram",
	"facebook.com":              "Facebook",
	"m.facebook.com":            "Facebook",
	"fb.com":                    "Facebook",
	"x.com":                     "X",
	"twitter.com":               "X",
	"linkedin.com":              "LinkedIn",
	"lv.linkedin.com":           "LinkedIn",
	"youtube.com":               "YouTube",
	"m.youtube.com":             "YouTube",
	"youtu.be":                  "YouTube",
	"threads.net":               "Threads",
	"threads.com":               "Threads",
	"bsky.app":                  "Bluesky",
	"t.me":                      "Telegram",
	"telegram.me":               "Telegram",
	"discord.gg":                "Discord",
	"discord.com":               "Discord",
	"producthunt.com":           "Product Hunt",
	"reddit.com":                "Reddit",
	"draugiem.lv":               "Draugiem",
	"apps.apple.com":            "App Store",
	"play.google.com":           "Google Play",
	"chromewebstore.google.com": "Chrome Web Store",
}

// Platform returns the display name of the platform a link points to,
// or "" for an unknown site.
func Platform(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return platforms[strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")]
}
