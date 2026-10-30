// Package i18n loads interface translations and picks a language for a request.
package i18n

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/BurntSushi/toml"
	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// Langs lists supported interface languages; the first one is the default.
var Langs = []string{"lv", "en", "ru"}

// Default is the fallback language for new visitors.
const Default = "lv"

// Bundle holds all translations.
type Bundle struct {
	localizers map[string]*goi18n.Localizer
	matcher    language.Matcher
}

// Load reads locales/<lang>.toml for every supported language from fsys.
func Load(fsys fs.FS) (*Bundle, error) {
	b := goi18n.NewBundle(language.English)
	b.RegisterUnmarshalFunc("toml", toml.Unmarshal)
	tags := make([]language.Tag, 0, len(Langs))
	for _, l := range Langs {
		data, err := fs.ReadFile(fsys, "locales/"+l+".toml")
		if err != nil {
			return nil, err
		}
		if _, err := b.ParseMessageFileBytes(data, l+".toml"); err != nil {
			return nil, fmt.Errorf("locale %s: %w", l, err)
		}
		tags = append(tags, language.MustParse(l))
	}
	out := &Bundle{localizers: map[string]*goi18n.Localizer{}, matcher: language.NewMatcher(tags)}
	for _, l := range Langs {
		out.localizers[l] = goi18n.NewLocalizer(b, l, "en")
	}
	return out, nil
}

// Supported reports whether lang is a supported interface language.
func Supported(lang string) bool {
	for _, l := range Langs {
		if l == lang {
			return true
		}
	}
	return false
}

// Match picks the best supported language for an Accept-Language header,
// falling back to Default.
func (b *Bundle) Match(acceptLanguage string) string {
	if acceptLanguage == "" {
		return Default
	}
	prefs, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(prefs) == 0 {
		return Default
	}
	_, idx, conf := b.matcher.Match(prefs...)
	if conf == language.No {
		return Default
	}
	return Langs[idx]
}

// T translates a message. args are key/value pairs passed to the template.
func (b *Bundle) T(lang, id string, args ...any) string {
	return b.localize(lang, id, nil, args)
}

// TN translates a message with plural forms for count n. The count is
// available in the message as {{.Count}}.
func (b *Bundle) TN(lang, id string, n int, args ...any) string {
	return b.localize(lang, id, n, append(args, "Count", n))
}

func (b *Bundle) localize(lang, id string, count any, args []any) string {
	loc, ok := b.localizers[lang]
	if !ok {
		loc = b.localizers[Default]
	}
	var data map[string]any
	if len(args) > 0 {
		data = make(map[string]any, len(args)/2)
		for i := 0; i+1 < len(args); i += 2 {
			data[fmt.Sprint(args[i])] = args[i+1]
		}
	}
	s, err := loc.Localize(&goi18n.LocalizeConfig{MessageID: id, TemplateData: data, PluralCount: count})
	if err != nil && s == "" {
		return id
	}
	return strings.TrimSpace(s)
}
