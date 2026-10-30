package slug

import (
	"strings"
	"testing"
)

func TestMake(t *testing.T) {
	tests := []struct{ in, want string }{
		{"My Project", "my-project"},
		{"  Šis ir Mans Ļoti Labs Projekts!  ", "sis-ir-mans-loti-labs-projekts"},
		{"Привет, мир", "privet-mir"},
		{"Объявления", "obyavleniya"},
		{"AI + Go = ❤", "ai-go"},
		{"---", ""},
		{"Rīga 2026", "riga-2026"},
	}
	for _, tt := range tests {
		if got := Make(tt.in, 60); got != tt.want {
			t.Errorf("Make(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := Make("aaaa bbbb", 5); got != "aaaa" {
		t.Errorf("truncation: got %q", got)
	}
}

func TestValidHandle(t *testing.T) {
	valid := []string{"aleks", "a-b-c", "user123", "abc"}
	invalid := []string{"ab", "-abc", "abc-", "a--b", "ABC", "admin", "with space", "āboliņš"}
	for _, h := range valid {
		if !ValidHandle(h) {
			t.Errorf("ValidHandle(%q) = false, want true", h)
		}
	}
	for _, h := range invalid {
		if ValidHandle(h) {
			t.Errorf("ValidHandle(%q) = true, want false", h)
		}
	}
}

func TestRandomHandle(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		h := RandomHandle()
		if !ValidHandle(h) || !strings.HasPrefix(h, "maker-") {
			t.Fatalf("invalid handle %q", h)
		}
		seen[h] = true
	}
	if len(seen) < 95 {
		t.Fatalf("only %d distinct handles out of 100", len(seen))
	}
}
