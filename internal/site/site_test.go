package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/once-human/desk-agent/internal/adult"
	"github.com/once-human/desk-agent/internal/catalog"
)

func load(t *testing.T, toml string) *catalog.Sites {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sites.toml")
	if err := os.WriteFile(p, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := catalog.LoadSites(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestResolve(t *testing.T) {
	sites := load(t, `
["monkeytype.com"]
name = "Monkeytype"
category = "learning"

["mybank.example.org"]
hide = true

["secret.example.net"]
category = "private"

["docs.example.com"]
name = "Example Docs"
category = "docs"
`)
	ad := adult.New()

	cases := []struct {
		in              string
		host, name, cat string // empty host = nil
	}{
		{"github.com", "github.com", "GitHub", "code"},
		{"gist.github.com", "github.com", "GitHub", "code"},
		{"docs.google.com", "docs.google.com", "Google Docs", "docs"},
		{"mail.google.com", "mail.google.com", "Gmail", "mail"},
		{"www.google.com", "google.com", "Google", "tool"},
		{"monkeytype.com", "monkeytype.com", "Monkeytype", "learning"},
		{"unknown-thing.dev", "unknown-thing.dev", "Unknown Thing", "other"},
		{"tenant42.some-saas.io", "some-saas.io", "Some Saas", "other"},
		{"deep.docs.example.com", "docs.example.com", "Example Docs", "docs"},
		{"mybank.example.org", "", "", ""},
		{"login.mybank.example.org", "", "", ""},
		{"secret.example.net", "private", "Private", "private"},
		{"pornhub.com", "", "", ""},
		{"localhost", "", "", ""},
		{"192.168.0.1", "", "", ""},
		{"https://github.com/x", "", "", ""},
		{"", "", "", ""},
	}
	for _, c := range cases {
		got := Resolve(c.in, sites, ad)
		if c.host == "" {
			if got != nil {
				t.Errorf("Resolve(%q) = %+v, want nil", c.in, got)
			}
			continue
		}
		if got == nil || got.Host != c.host || got.Name != c.name || got.Category != c.cat {
			t.Errorf("Resolve(%q) = %+v, want {%s %s %s}", c.in, got, c.host, c.name, c.cat)
		}
	}
	// github.io is a public suffix, so foo.github.io is its own site and
	// must not inherit the "github.com" entry.
	if got := Resolve("foo.github.io", sites, ad); got == nil || got.Host != "foo.github.io" || got.Category != "other" {
		t.Errorf("foo.github.io -> %+v", got)
	}
}

func TestAdultCannotBeOverriddenByCatalog(t *testing.T) {
	sites := load(t, "[\"pornhub.com\"]\nname = \"Shown\"\ncategory = \"video\"\n")
	if got := Resolve("pornhub.com", sites, adult.New()); got != nil {
		t.Fatalf("adult host reported: %+v", got)
	}
	bl := adult.New()
	_ = bl.Read(strings.NewReader("quietly-blocked.org\n"))
	if got := Resolve("quietly-blocked.org", catalog.MustEmptySites(), bl); got != nil {
		t.Fatalf("blocklisted host reported: %+v", got)
	}
}

func TestNeverReportsSubdomainsOrPaths(t *testing.T) {
	got := Resolve("alice-private-notes.some-saas.io", catalog.MustEmptySites(), adult.New())
	if got == nil || strings.Contains(got.Host, "alice") || strings.Contains(got.Name, "Alice") {
		t.Fatalf("leaked subdomain: %+v", got)
	}
}

func TestParseSitesRejectsMistakes(t *testing.T) {
	bad := []string{
		"[\"a.com\"]\ncategory = \"nonsense\"\n",
		"[\"a.com\"]\nnmae = \"typo\"\n",
		"[\"http://a.com/x\"]\nname = \"x\"\n",
		"not toml at all [",
	}
	for _, b := range bad {
		if _, err := catalog.ParseSites([]byte(b)); err == nil {
			t.Errorf("expected an error for %q", b)
		}
	}
}
