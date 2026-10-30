package video

import "testing"

func TestEmbedURL(t *testing.T) {
	tests := map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=10":            "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ":                                "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
		"https://youtube.com/shorts/dQw4w9WgXcQ":                      "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
		"https://vimeo.com/76979871":                                  "https://player.vimeo.com/video/76979871?dnt=1",
		"https://www.loom.com/share/0123456789abcdef0123456789abcdef": "https://www.loom.com/embed/0123456789abcdef0123456789abcdef",
		"https://evil.example/watch?v=dQw4w9WgXcQ":                    "",
		"javascript:alert(1)":                                         "",
		"https://www.youtube.com/watch?v=bad\"id":                     "",
	}
	for in, want := range tests {
		if got := EmbedURL(in); got != want {
			t.Errorf("EmbedURL(%q) = %q, want %q", in, got, want)
		}
	}
}
