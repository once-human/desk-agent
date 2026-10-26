package mpris

import "testing"

func TestIsYouTubeArt(t *testing.T) {
	for in, want := range map[string]bool{
		"https://i.ytimg.com/vi/abc/hqdefault.jpg": true,
		"https://yt3.ggpht.com/x":                  true,
		"https://example.com/a.jpg":                false,
		"":                                         false,
	} {
		if got := IsYouTubeArt(in); got != want {
			t.Errorf("IsYouTubeArt(%q) = %v", in, got)
		}
	}
}
