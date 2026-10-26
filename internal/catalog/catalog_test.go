package catalog

import "testing"

func TestSitesLookupMostSpecificWins(t *testing.T) {
	s := MustEmptySites()
	s.m["google.com"] = SiteEntry{Name: "Google", Category: "tool"}
	s.m["mail.google.com"] = SiteEntry{Name: "Gmail", Category: "mail"}
	cases := []struct{ host, key, name string }{
		{"google.com", "google.com", "Google"},
		{"www.google.com", "google.com", "Google"},
		{"mail.google.com", "mail.google.com", "Gmail"},
		{"a.b.mail.google.com", "mail.google.com", "Gmail"},
		{"notgoogle.com", "", ""},
		{"google.com.evil.example", "", ""}, // suffix match must respect label boundaries
	}
	for _, c := range cases {
		e, key, ok := s.Lookup(c.host)
		if (c.key == "") == ok || key != c.key || e.Name != c.name {
			t.Errorf("Lookup(%q) = %+v,%q,%v", c.host, e, key, ok)
		}
	}
}

func TestSeedIsConsistent(t *testing.T) {
	for host, e := range seedSites {
		if !ValidSiteCategory(e.Category) {
			t.Errorf("seed %s has invalid category %q", host, e.Category)
		}
		if e.Name == "" {
			t.Errorf("seed %s has no name", host)
		}
		if host != toLowerNoWWW(host) {
			t.Errorf("seed key %q must be lower-case without www.", host)
		}
	}
}

func toLowerNoWWW(h string) string {
	for i := 0; i < len(h); i++ {
		if h[i] >= 'A' && h[i] <= 'Z' {
			return "UPPER"
		}
	}
	if len(h) > 4 && h[:4] == "www." {
		return "WWW"
	}
	return h
}

func TestParseApps(t *testing.T) {
	m, err := ParseApps([]byte("[\"Kitty\"]\nname = \"Kitty\"\ncategory = \"terminal\"\n[\"x\"]\nname = \"X\"\n"))
	if err != nil || m["kitty"].Category != "terminal" || m["x"].Category != "other" {
		t.Fatalf("%+v %v", m, err)
	}
	for _, bad := range []string{"[\"a\"]\ncategory = \"nope\"\n", "[\"a\"]\nnmae = \"x\"\n", "["} {
		if _, err := ParseApps([]byte(bad)); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestAppsHas(t *testing.T) {
	a, _ := LoadApps("")
	if !a.Has("Kitty") || !a.Has("jetbrains-idea-abc123") || a.Has("definitely-unknown") {
		t.Error("Has() disagrees with the seed")
	}
}
