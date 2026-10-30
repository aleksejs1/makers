package auth

import "testing"

func TestCode(t *testing.T) {
	secret := []byte("0123456789abcdef")
	code := NewCode()
	if len(code) != CodeLength {
		t.Fatalf("code %q has wrong length", code)
	}
	h := HashCode(secret, "a@b.lv", code)
	if !CheckCode(secret, "a@b.lv", code, h) {
		t.Error("valid code rejected")
	}
	if CheckCode(secret, "other@b.lv", code, h) {
		t.Error("code accepted for another email")
	}
	if CheckCode([]byte("another-secret!!"), "a@b.lv", code, h) {
		t.Error("code accepted with another secret")
	}
}

func TestNormalizeEmail(t *testing.T) {
	tests := map[string]string{
		" Jane@Example.LV ": "jane@example.lv",
		"a.b+c@d.co":        "a.b+c@d.co",
		"no-at-sign":        "",
		"x@localhost":       "",
		"Jane <j@e.lv>":     "",
	}
	for in, want := range tests {
		got, ok := NormalizeEmail(in)
		if got != want || ok != (want != "") {
			t.Errorf("NormalizeEmail(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestFormatCode(t *testing.T) {
	if got := FormatCode("12345678"); got != "1234 5678" {
		t.Errorf("got %q", got)
	}
	if got := NormalizeCode(FormatCode("12345678")); got != "12345678" {
		t.Errorf("round trip: %q", got)
	}
}

func TestNormalizeCode(t *testing.T) {
	if got := NormalizeCode(" 123-456 "); got != "123456" {
		t.Errorf("got %q", got)
	}
}
