package social

import "testing"

func TestPlatform(t *testing.T) {
	tests := map[string]string{
		"https://www.tiktok.com/@kebabradar":             "TikTok",
		"https://www.tiktok.com/@kebabradar/video/72981": "TikTok",
		"https://INSTAGRAM.com/kebab":                    "Instagram",
		"https://twitter.com/kebab":                      "X",
		"https://www.youtube.com/@kebab":                 "YouTube",
		"https://www.producthunt.com/products/kebab":     "Product Hunt",
		"https://kebab.example.lv":                       "",
		"https://tiktok.com.evil.example/@x":             "",
		"not a url %zz":                                  "",
	}
	for in, want := range tests {
		if got := Platform(in); got != want {
			t.Errorf("Platform(%q) = %q, want %q", in, got, want)
		}
	}
}
