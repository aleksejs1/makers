package server

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/aleksejs1/makers/internal/i18n"
	"github.com/aleksejs1/makers/ui"
)

func localeKeys(t *testing.T, lang string) map[string]bool {
	t.Helper()
	data, err := fs.ReadFile(ui.FS, "locales/"+lang+".toml")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := toml.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s.toml: %v", lang, err)
	}
	keys := map[string]bool{}
	for k := range m {
		keys[k] = true
	}
	return keys
}

// TestLocalesHaveSameKeys ensures every translation defines exactly the
// same messages as the English one.
func TestLocalesHaveSameKeys(t *testing.T) {
	en := localeKeys(t, "en")
	for _, lang := range i18n.Langs {
		keys := localeKeys(t, lang)
		var missing, extra []string
		for k := range en {
			if !keys[k] {
				missing = append(missing, k)
			}
		}
		for k := range keys {
			if !en[k] {
				extra = append(extra, k)
			}
		}
		sort.Strings(missing)
		sort.Strings(extra)
		if len(missing) > 0 {
			t.Errorf("%s.toml is missing: %s", lang, strings.Join(missing, ", "))
		}
		if len(extra) > 0 {
			t.Errorf("%s.toml has keys not in en.toml: %s", lang, strings.Join(extra, ", "))
		}
	}
}

// TestUsedKeysExist checks that every message referenced from templates and
// handlers is defined.
func TestUsedKeysExist(t *testing.T) {
	en := localeKeys(t, "en")
	used := map[string]bool{}

	tmplKey := regexp.MustCompile(`\.TN? "([a-z0-9_.]+)"`)
	fieldKey := regexp.MustCompile(`\((?:field|selectField|tagsField) "([a-z_]+)"`)
	err := fs.WalkDir(ui.FS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(ui.FS, p)
		if err != nil {
			return err
		}
		for _, m := range tmplKey.FindAllStringSubmatch(string(b), -1) {
			used[m[1]] = true
		}
		for _, m := range fieldKey.FindAllStringSubmatch(string(b), -1) {
			used["field."+m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	goKey := regexp.MustCompile(`"((?:form|login|error|settings|project|email)\.[a-z0-9_.]+)"`)
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range goKey.FindAllStringSubmatch(string(b), -1) {
			used[m[1]] = true
		}
	}

	for prefix, values := range map[string][]string{
		"status.": Statuses, "category.": Categories, "pricing.": Pricings,
		"looking.": LookingFor, "lang.": append(append([]string{}, ProductLangs...), ContentLangs...),
	} {
		for _, v := range values {
			used[prefix+v] = true
		}
	}
	for _, code := range []string{"403", "404", "413", "429", "500"} {
		used["error."+code+".title"] = true
		used["error."+code+".text"] = true
	}

	var missing []string
	for k := range used {
		if !en[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("keys used but not defined in en.toml: %s", strings.Join(missing, "\n  "))
	}
}
