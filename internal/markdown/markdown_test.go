package markdown

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	tests := []struct {
		in       string
		contains []string
		absent   []string
	}{
		{"**bold** and _em_", []string{"<strong>bold</strong>", "<em>em</em>"}, nil},
		{"<script>alert(1)</script>", nil, []string{"<script"}},
		{"[x](javascript:alert(1))", nil, []string{"javascript:"}},
		{"![img](https://evil.example/pixel.png)", nil, []string{"<img"}},
		{"https://makers.lv", []string{`href="https://makers.lv"`, `rel="nofollow noopener"`, `target="_blank"`}, nil},
	}
	for _, tt := range tests {
		got := string(Render(tt.in))
		for _, c := range tt.contains {
			if !strings.Contains(got, c) {
				t.Errorf("Render(%q) = %q, want it to contain %q", tt.in, got, c)
			}
		}
		for _, a := range tt.absent {
			if strings.Contains(got, a) {
				t.Errorf("Render(%q) = %q, must not contain %q", tt.in, got, a)
			}
		}
	}
}
