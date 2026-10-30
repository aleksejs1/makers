// Package ui embeds templates, static assets and translations.
package ui

import "embed"

// FS contains the templates, static and locales directories.
//
//go:embed templates static locales
var FS embed.FS
