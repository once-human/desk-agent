package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/once-human/desk-agent/internal/adult"
	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/seenapps"
)

func tmp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.toml")
	if content != "" {
		os.WriteFile(p, []byte(content), 0o600)
	}
	return p
}

func str(s string) *string { return &s }
func bl(b bool) *bool      { return &b }

func TestSetSiteCreateEditHideRemove(t *testing.T) {
	p := tmp(t, "# my notes\n")
	ad := adult.New()
	if err := SetSite(p, "https://x.com/y", SiteUpdate{}, ad); err == nil {
		t.Error("a URL must be rejected")
	}
	if err := SetSite(p, "Monkeytype.com", SiteUpdate{Category: str("learning")}, ad); err != nil {
		t.Fatal(err)
	}
	rows, _ := ListSites(p)
	var mt *SiteRow
	for i := range rows {
		if rows[i].Host == "monkeytype.com" {
			mt = &rows[i]
		}
	}
	if mt == nil || mt.Category != "learning" || mt.Name != "Monkeytype" || mt.Source != "user" {
		t.Fatalf("%+v", mt)
	}
	// Editing a seed host starts from its seed name.
	if err := SetSite(p, "github.com", SiteUpdate{Category: str("tool")}, ad); err != nil {
		t.Fatal(err)
	}
	user, _ := catalog.ParseSites(mustRead(t, p))
	if user["github.com"].Name != "GitHub" || user["github.com"].Category != "tool" {
		t.Errorf("%+v", user["github.com"])
	}
	// hide
	if err := SetSite(p, "monkeytype.com", SiteUpdate{Hide: bl(true)}, ad); err != nil {
		t.Fatal(err)
	}
	user, _ = catalog.ParseSites(mustRead(t, p))
	if !user["monkeytype.com"].Hide || user["monkeytype.com"].Category != "learning" {
		t.Errorf("hide must keep the other fields: %+v", user["monkeytype.com"])
	}
	if !strings.HasPrefix(string(mustRead(t, p)), "# my notes") {
		t.Error("the user's comment was lost")
	}
	// bad category is rejected and nothing changes
	before := mustRead(t, p)
	if err := SetSite(p, "a.com", SiteUpdate{Category: str("nonsense")}, ad); err == nil {
		t.Error("expected a category error")
	}
	if string(mustRead(t, p)) != string(before) {
		t.Error("file changed on error")
	}
	if err := RemoveSite(p, "monkeytype.com"); err != nil {
		t.Fatal(err)
	}
	user, _ = catalog.ParseSites(mustRead(t, p))
	if _, ok := user["monkeytype.com"]; ok || len(user) != 1 {
		t.Errorf("%+v", user)
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAdultHostsCannotBeListed(t *testing.T) {
	p := tmp(t, "")
	if err := SetSite(p, "pornhub.com", SiteUpdate{Name: str("x")}, adult.New()); err == nil {
		t.Error("adult host accepted")
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("nothing should be written")
	}
	applied, skipped, err := ImportSites(p, []SiteRow{
		{Host: "pornhub.com", Name: "x", Category: "video"},
		{Host: "ok.example.org", Name: "Ok", Category: "tool"},
		{Host: "bad category.org", Name: "x", Category: "tool"},
		{Host: "x.example.org", Name: "x", Category: "nonsense"},
	}, adult.New())
	if err != nil || applied != 1 || skipped != 3 {
		t.Fatalf("applied=%d skipped=%d err=%v", applied, skipped, err)
	}
	if user, _ := catalog.ParseSites(mustRead(t, p)); len(user) != 1 || user["ok.example.org"].Name != "Ok" {
		t.Errorf("%+v", user)
	}
}

func TestBrokenFileIsNeverOverwritten(t *testing.T) {
	p := tmp(t, "this is [ not toml")
	if err := SetSite(p, "a.com", SiteUpdate{}, nil); err == nil || !strings.Contains(err.Error(), "fix it first") {
		t.Errorf("err = %v", err)
	}
	if string(mustRead(t, p)) != "this is [ not toml" {
		t.Error("a broken file must be left alone")
	}
}

func TestListAppsMergesSeedUserAndSeen(t *testing.T) {
	apps := tmp(t, "[\"obsidian\"]\nname = \"Obsidian\"\ncategory = \"editor\"\n")
	seenPath := filepath.Join(t.TempDir(), "seen.json")
	s := seenapps.Open(seenPath)
	for i := 0; i < 5; i++ {
		s.Note("kitty")
	}
	s.Note("mystery-app")
	s.Note("obsidian")
	s.Flush()
	rows, err := ListApps(apps, seenPath)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]AppRow{}
	for _, r := range rows {
		by[r.Class] = r
	}
	if by["kitty"].Source != "seed" || by["kitty"].Count != 5 || rows[0].Class != "kitty" {
		t.Errorf("kitty: %+v first=%v", by["kitty"], rows[0])
	}
	if by["obsidian"].Source != "user" || by["obsidian"].Count != 1 {
		t.Errorf("%+v", by["obsidian"])
	}
	if r := by["mystery-app"]; r.Source != "seen" || r.Name != "Mystery App" || r.Category != "other" {
		t.Errorf("%+v", r)
	}
}

func TestSetAndRemoveApp(t *testing.T) {
	p := tmp(t, "")
	if err := SetApp(p, "Obsidian", AppUpdate{Name: str("Obsidian"), Category: str("editor")}); err != nil {
		t.Fatal(err)
	}
	if err := SetApp(p, "kitty", AppUpdate{Category: str("editor")}); err != nil { // seed override keeps its name
		t.Fatal(err)
	}
	user, _ := catalog.ParseApps(mustRead(t, p))
	if user["kitty"].Name != "Kitty" || user["kitty"].Category != "editor" || user["obsidian"].Name != "Obsidian" {
		t.Errorf("%+v", user)
	}
	if err := SetApp(p, "x", AppUpdate{Category: str("nope")}); err == nil {
		t.Error("bad category accepted")
	}
	if err := SetApp(p, "has space", AppUpdate{}); err == nil {
		t.Error("bad class accepted")
	}
	if err := RemoveApp(p, "obsidian"); err != nil {
		t.Fatal(err)
	}
	if user, _ = catalog.ParseApps(mustRead(t, p)); len(user) != 1 {
		t.Errorf("%+v", user)
	}
	applied, skipped, err := ImportApps(p, []AppRow{{Class: "a", Name: "A", Category: "chat"}, {Class: "b", Name: "B", Category: "zzz"}})
	if err != nil || applied != 1 || skipped != 1 {
		t.Fatalf("%d %d %v", applied, skipped, err)
	}
}
