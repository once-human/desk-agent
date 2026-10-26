package adult

import (
	"strings"
	"testing"

	"github.com/once-human/desk-agent/internal/sample"
)

func TestHostHeuristics(t *testing.T) {
	f := New()
	blocked := []string{
		"pornhub.com", "xvideos.com", "www.xnxx.com", "some-xxx-site.net", "onlyfans.com",
		"sex.com", "free-nudes.net", "cams.tv", "adult.example.org", "hentai-haven.org",
		"chaturbate.com", "rule34.xxx", "escort-delhi.in", "MILF.example.com",
	}
	for _, h := range blocked {
		if !f.Host(h) {
			t.Errorf("%q should be blocked", h)
		}
	}
	allowed := []string{
		"github.com", "essex.ac.uk", "sussex.ac.uk", "middlesex.edu", "adultswim.com",
		"classmates.com", "scunthorpe.gov.uk", "monkeytype.com", "camera-shop.in", "cambridge.org",
		"escorial.es", "accommodation.com", "", "docs.google.com",
	}
	for _, h := range allowed {
		if f.Host(h) {
			t.Errorf("%q must not be blocked", h)
		}
	}
}

func TestBlocklistFormats(t *testing.T) {
	f := New()
	err := f.Read(strings.NewReader(`
# comment
0.0.0.0 bad-example.org   # trailing comment
127.0.0.1 localhost
plain-example.net
0.0.0.0 0.0.0.0
https://not-a-host/path
`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Len() != 2 {
		t.Fatalf("Len = %d, want 2", f.Len())
	}
	for _, h := range []string{"bad-example.org", "www.bad-example.org", "cdn.bad-example.org", "plain-example.net"} {
		if !f.Host(h) {
			t.Errorf("%q should match the blocklist", h)
		}
	}
	if f.Host("example.org") || f.Host("other-example.net") {
		t.Error("blocklist must match by host or parent domain only")
	}
}

func TestMissingBlocklistFileIsFine(t *testing.T) {
	f, err := Load("/nonexistent/path/adult-hosts.txt")
	if err != nil || f == nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestText(t *testing.T) {
	f := New()
	if !f.Text("Best of PornHub mix") || !f.Text("nsfw playlist") {
		t.Error("expected adult text to match")
	}
	for _, s := range []string{"Bohemian Rhapsody", "Queen", "Essex Girls", "Cam'ron", ""} {
		if f.Text(s) {
			t.Errorf("%q must not match", s)
		}
	}
}

func TestScrubMedia(t *testing.T) {
	f := New()
	if f.ScrubMedia(nil) != nil {
		t.Error("nil stays nil")
	}
	ok := &sample.Media{Player: "spotify", State: "playing", Source: "spotify", Title: sample.Str("Bohemian Rhapsody"), Artist: sample.Str("Queen")}
	if f.ScrubMedia(ok) != ok {
		t.Error("clean media must pass through untouched")
	}
	bad := &sample.Media{Player: "spotify", State: "playing", Source: "spotify", Title: sample.Str("Best of pornhub"), Artist: sample.Str("Someone")}
	got := f.ScrubMedia(bad)
	if got.Title != nil || got.Artist != nil || got.Player != "spotify" || got.State != "playing" {
		t.Errorf("scrubbed: %+v", got)
	}
	if bad.Title == nil {
		t.Error("the input must not be modified")
	}
}
