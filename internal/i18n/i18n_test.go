package i18n_test

import (
	"testing"

	"github.com/aleksejs1/makers/internal/i18n"
	"github.com/aleksejs1/makers/ui"
)

func TestPlurals(t *testing.T) {
	b, err := i18n.Load(ui.FS)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		lang string
		n    int
		want string
	}{
		{"en", 1, "1 project"}, {"en", 2, "2 projects"}, {"en", 0, "0 projects"},
		{"lv", 0, "0 projektu"}, {"lv", 1, "1 projekts"}, {"lv", 2, "2 projekti"},
		{"lv", 11, "11 projektu"}, {"lv", 21, "21 projekts"},
		{"ru", 1, "1 проект"}, {"ru", 3, "3 проекта"}, {"ru", 5, "5 проектов"},
		{"ru", 11, "11 проектов"}, {"ru", 22, "22 проекта"},
	}
	for _, tt := range tests {
		if got := b.TN(tt.lang, "home.count", tt.n); got != tt.want {
			t.Errorf("TN(%s, %d) = %q, want %q", tt.lang, tt.n, got, tt.want)
		}
	}
}

func TestMatch(t *testing.T) {
	b, err := i18n.Load(ui.FS)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"":                       "lv",
		"lv":                     "lv",
		"ru-RU,ru;q=0.9":         "ru",
		"en-GB,en;q=0.9":         "en",
		"de-DE":                  "lv",
		"de-DE,en;q=0.7,ru;q=.5": "en",
		"garbage;;;":             "lv",
	}
	for in, want := range tests {
		if got := b.Match(in); got != want {
			t.Errorf("Match(%q) = %q, want %q", in, got, want)
		}
	}
	if got := b.T("en", "no.such.key"); got != "no.such.key" {
		t.Errorf("missing key: %q", got)
	}
}
