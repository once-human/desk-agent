package mpris

import (
	"testing"
	"time"
)

func TestPlayerIDAndBrowserDetection(t *testing.T) {
	if got := PlayerID("org.mpris.MediaPlayer2.firefox.instance_1_23"); got != "firefox" {
		t.Fatal(got)
	}
	if got := PlayerID("org.mpris.MediaPlayer2.spotify"); got != "spotify" {
		t.Fatal(got)
	}
	for _, id := range []string{"firefox", "chromium", "brave", "zen", "plasma-browser-integration"} {
		if !IsBrowser(id, "") {
			t.Fatalf("%s should be a browser", id)
		}
	}
	if IsBrowser("spotify", "") || IsBrowser("mpv", "") {
		t.Fatal("native players misclassified")
	}
	if !IsBrowser("someweirdname", "browser") {
		t.Fatal("desktop-entry category should win")
	}
}

func TestIsYouTubeURL(t *testing.T) {
	yes := []string{"https://www.youtube.com/watch?v=abc", "https://youtu.be/abc", "https://m.youtube.com/x", "https://music.youtube.com/watch?v=1"}
	no := []string{"https://example.com/youtube.com", "https://notyoutube.com/", "", "::bad", "https://vimeo.com/1"}
	for _, u := range yes {
		if !IsYouTubeURL(u) {
			t.Fatalf("%q should match", u)
		}
	}
	for _, u := range no {
		if IsYouTubeURL(u) {
			t.Fatalf("%q must not match", u)
		}
	}
}

func TestNativePlayerSendsTitleArtistState(t *testing.T) {
	tr := NewTracker()
	m := tr.Update(Player{Bus: "b1", ID: "spotify", Status: "Playing", Title: "Song", Artist: "Band"})
	if m == nil || m.Source != "spotify" || m.State != "playing" || m.Title == nil || *m.Title != "Song" || *m.Artist != "Band" {
		t.Fatalf("%+v", m)
	}
}

func TestBrowserMediaNeverCarriesTitle(t *testing.T) {
	tr := NewTracker()
	// even if a bug put a title on a browser player, it is dropped for browser sources
	m := tr.Update(Player{Bus: "b", ID: "firefox", Browser: true, Status: "Playing", Title: "LEAK", Artist: "LEAK"})
	if m == nil || m.Source != "browser" || m.Title != nil || m.Artist != nil {
		t.Fatalf("%+v", m)
	}
}

func TestYouTubeSourceNoTitleAndCountsVideos(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	tr := NewTracker()
	tr.Now = func() time.Time { return now }
	p := Player{Bus: "b", ID: "firefox", Browser: true, YouTube: true, Status: "Playing", TrackKey: "aaa", Title: "LEAK"}
	m := tr.Update(p)
	if m.Source != "youtube" || m.Title != nil || m.Artist != nil || m.Videos == nil || *m.Videos != 1 {
		t.Fatalf("%+v", m)
	}
	// metadata refresh of the same video must not count again
	p.Title = "changed title only"
	if m = tr.Update(p); *m.Videos != 1 {
		t.Fatalf("recount on same video: %d", *m.Videos)
	}
	p.TrackKey = "bbb"
	if m = tr.Update(p); *m.Videos != 2 {
		t.Fatalf("want 2 got %d", *m.Videos)
	}
	p.TrackKey = "ccc"
	if m = tr.Update(p); *m.Videos != 3 {
		t.Fatalf("want 3 got %d", *m.Videos)
	}
}

func TestYouTubeSessionResetsAfterGap(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	tr := NewTracker()
	tr.Now = func() time.Time { return now }
	p := Player{Bus: "b", ID: "firefox", Browser: true, YouTube: true, Status: "Playing", TrackKey: "a"}
	tr.Update(p)
	p.TrackKey = "b"
	tr.Update(p) // 2 videos
	p.Status = "Paused"
	tr.Update(p)

	now = now.Add(10 * time.Minute) // short break: same session
	p.Status, p.TrackKey = "Playing", "c"
	if m := tr.Update(p); *m.Videos != 3 {
		t.Fatalf("same session expected 3 got %d", *m.Videos)
	}
	p.Status = "Paused"
	tr.Update(p)

	now = now.Add(21 * time.Minute) // long break: new session
	p.Status, p.TrackKey = "Playing", "d"
	if m := tr.Update(p); *m.Videos != 1 {
		t.Fatalf("new session expected 1 got %d", *m.Videos)
	}
}

func TestExtensionAudibleHintMakesBrowserMediaYouTube(t *testing.T) {
	tr := NewTracker()
	tr.Update(Player{Bus: "b", ID: "firefox", Browser: true, Status: "Playing", TrackKey: "k"})
	if m := tr.Media(); m.Source != "browser" {
		t.Fatalf("%+v", m)
	}
	m := tr.SetYouTubeAudible(true)
	if m.Source != "youtube" || m.Videos == nil || *m.Videos != 1 || m.Title != nil {
		t.Fatalf("%+v", m)
	}
	if m = tr.SetYouTubeAudible(false); m.Source != "browser" {
		t.Fatalf("%+v", m)
	}
}

func TestPlayingBeatsPausedAndStoppedIsIgnored(t *testing.T) {
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	tr := NewTracker()
	tr.Now = func() time.Time { return now }
	tr.Update(Player{Bus: "a", ID: "spotify", Status: "Paused", Title: "A"})
	now = now.Add(time.Minute)
	tr.Update(Player{Bus: "b", ID: "mpv", Status: "Stopped", Title: "B"})
	if m := tr.Media(); m.Source != "spotify" || m.State != "paused" {
		t.Fatalf("%+v", m)
	}
	now = now.Add(time.Minute)
	tr.Update(Player{Bus: "c", ID: "vlc", Status: "Playing", Title: "C"})
	if m := tr.Media(); m.Source != "vlc" {
		t.Fatalf("%+v", m)
	}
	if m := tr.Remove("c"); m.Source != "spotify" {
		t.Fatalf("%+v", m)
	}
	tr.Remove("a")
	if tr.Media() != nil {
		t.Fatal("expected nil media")
	}
}

func TestTrackKeyIsSaltedAndOpaque(t *testing.T) {
	a, b := NewSalt(), NewSalt()
	k1 := MakeTrackKey(a, "/track/1", "https://youtube.com/watch?v=SECRETID")
	k2 := MakeTrackKey(b, "/track/1", "https://youtube.com/watch?v=SECRETID")
	if k1 == k2 || k1 == "" {
		t.Fatal("salt must change the key")
	}
	if MakeTrackKey(a, "", "") != "" {
		t.Fatal("no identifiers -> empty key")
	}
}
