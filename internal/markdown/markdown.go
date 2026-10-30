// Package markdown renders user-written Markdown into sanitized HTML.
package markdown

import (
	"bytes"
	"html/template"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

var (
	md = goldmark.New(goldmark.WithExtensions(
		extension.Linkify, extension.Strikethrough, extension.Table,
	))

	// policy allows basic formatting and links. Images are not allowed:
	// remote images would leak visitors' IP addresses to third parties.
	policy = func() *bluemonday.Policy {
		p := bluemonday.NewPolicy()
		p.AllowStandardURLs()
		p.AllowURLSchemes("http", "https", "mailto")
		p.AllowAttrs("href").OnElements("a")
		p.RequireNoFollowOnLinks(true)
		p.AddTargetBlankToFullyQualifiedLinks(true)
		p.AllowElements("p", "br", "hr", "strong", "em", "del", "code", "pre", "blockquote",
			"ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6",
			"table", "thead", "tbody", "tr", "th", "td")
		return p
	}()
)

// Render converts Markdown to safe HTML. Raw HTML in the input is escaped
// by goldmark and the output is sanitized again with bluemonday.
func Render(src string) template.HTML {
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(src)) //nolint:gosec // escaped
	}
	return template.HTML(policy.SanitizeBytes(buf.Bytes())) //nolint:gosec // sanitized by bluemonday
}
