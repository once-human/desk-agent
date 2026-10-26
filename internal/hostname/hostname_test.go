package hostname

import "testing"

func TestNormalise(t *testing.T) {
	good := map[string]string{
		"GitHub.com":          "github.com",
		"www.example.org":     "example.org",
		"example.org.":        "example.org",
		"example.org:8443":    "example.org",
		"  docs.google.com  ": "docs.google.com",
		"xn--bcher-kva.ch":    "xn--bcher-kva.ch",
		"my-site.co.uk":       "my-site.co.uk",
	}
	for in, want := range good {
		got, ok := Normalise(in)
		if !ok || got != want {
			t.Errorf("Normalise(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	bad := []string{
		"", "localhost", "intranet", "192.168.1.5", "10.0.0.1:80", "[::1]", "::1", "fe80::1",
		"printer.local", "http://example.com", "example.com/path", "example.com?q=1",
		"user@example.com", "exa mple.com", "-bad.com", "bad-.com", "a..b.com", "exa_mple.com",
		"example.com:abc", "example.com:", "bücher.ch", "example.com#frag", "300.300.300.300",
	}
	for _, in := range bad {
		if got, ok := Normalise(in); ok {
			t.Errorf("Normalise(%q) = %q, want rejection", in, got)
		}
	}
}

func TestRegistrableAndFriendly(t *testing.T) {
	cases := []struct{ host, reg, friendly string }{
		{"monkeytype.com", "monkeytype.com", "Monkeytype"},
		{"docs.rs", "docs.rs", "Docs"},
		{"sub.deep.example.co.uk", "example.co.uk", "Example"},
		{"my-site.co.uk", "my-site.co.uk", "My Site"},
		{"foo.github.io", "foo.github.io", "Foo"},
		{"news.ycombinator.com", "ycombinator.com", "Ycombinator"},
	}
	for _, c := range cases {
		if got := Registrable(c.host); got != c.reg {
			t.Errorf("Registrable(%q) = %q, want %q", c.host, got, c.reg)
		}
		if got := Friendly(c.host); got != c.friendly {
			t.Errorf("Friendly(%q) = %q, want %q", c.host, got, c.friendly)
		}
	}
}

func TestFromRevHost(t *testing.T) {
	if got := FromRevHost("moc.buhtig."); got != "github.com" {
		t.Errorf("got %q", got)
	}
	if got := FromRevHost(""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestFromURLKeepsOnlyTheHost(t *testing.T) {
	cases := map[string]string{
		"https://github.com/once-human/desk-agent?tab=readme#top": "github.com",
		"http://User:pw@www.Example.org:8080/a/b":                 "example.org",
		"https://docs.google.com/document/d/SECRETID/edit":        "docs.google.com",
	}
	for in, want := range cases {
		got, ok := FromURL(in)
		if !ok || got != want {
			t.Errorf("FromURL(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"file:///home/me/x.pdf", "about:blank", "chrome://settings", "ftp://example.com/x", "example.com", "http://localhost:3000/x", "http://127.0.0.1/x"} {
		if got, ok := FromURL(in); ok {
			t.Errorf("FromURL(%q) = %q, want rejection", in, got)
		}
	}
}
