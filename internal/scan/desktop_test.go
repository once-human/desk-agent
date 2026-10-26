package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/once-human/desk-agent/internal/catalog"
)

func writeDesktop(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopMatchAndPropose(t *testing.T) {
	dir := t.TempDir()
	writeDesktop(t, dir, "org.gnome.Loupe.desktop", "[Desktop Entry]\nName=Image Viewer\nExec=loupe %U\nCategories=GNOME;GTK;Graphics;Viewer;\n")
	writeDesktop(t, dir, "obsidian.desktop", "[Desktop Entry]\nName=Obsidian\nExec=env FOO=1 /opt/Obsidian/obsidian --no-sandbox %U\nStartupWMClass=obsidian\nCategories=Office;\n[Desktop Action x]\nName=Ignored\n")
	writeDesktop(t, dir, "foot.desktop", "[Desktop Entry]\nName=Foot\nExec=foot\nCategories=System;TerminalEmulator;\n")
	writeDesktop(t, dir, "weird.desktop", "[Desktop Entry]\nName=Weird App\nExec=wrd\nStartupWMClass=WeirdClass\nCategories=Development;\n")
	writeDesktop(t, dir, "junk.desktop", "not a desktop file")
	ds := LoadDesktops([]string{dir})
	if len(ds) != 4 {
		t.Fatalf("loaded %d desktop files", len(ds))
	}
	if d, ok := Match("org.gnome.loupe", ds); !ok || d.Name != "Image Viewer" {
		t.Errorf("match by file name: %+v %v", d, ok)
	}
	if d, ok := Match("weirdclass", ds); !ok || d.Name != "Weird App" {
		t.Errorf("match by StartupWMClass is case-insensitive: %+v %v", d, ok)
	}
	if d, ok := Match("obsidian", ds); !ok || d.ExecBase != "obsidian" {
		t.Errorf("exec with env prefix: %+v", d)
	}

	known, _ := catalog.LoadApps("")
	props := ProposeApps(map[string]int{
		"org.gnome.Loupe": 12, "obsidian": 40, "weirdclass": 3, "mystery-app": 2,
		"kitty": 99, // already in the seed
		"foot":  5,  // seed too
	}, known, map[string]catalog.Entry{"obsidian": {Name: "Mine", Category: "other"}}, ds)
	by := map[string]AppProposal{}
	for _, p := range props {
		by[p.Class] = p
	}
	if len(by) != 3 {
		t.Fatalf("proposals: %+v", by)
	}
	if p := by["org.gnome.loupe"]; p.Category != "design" || p.Name != "Image Viewer" || p.Source != "desktop" {
		t.Errorf("loupe: %+v", p)
	}
	if p := by["weirdclass"]; p.Category != "editor" {
		t.Errorf("Development category -> editor: %+v", p)
	}
	if p := by["mystery-app"]; p.Source != "unknown" || p.Category != "other" || p.Name != "Mystery App" {
		t.Errorf("mystery: %+v", p)
	}
	out := RenderApps("", props, time.Now())
	if _, err := catalog.ParseApps([]byte(out)); err != nil {
		t.Fatalf("apps render does not parse: %v\n%s", err, out)
	}
	if !strings.Contains(out, "UNKNOWN") {
		t.Error("unknown apps must be marked")
	}
}

func TestAppCategory(t *testing.T) {
	cases := []struct {
		cats []string
		want string
		ok   bool
	}{
		{[]string{"System", "TerminalEmulator"}, "terminal", true},
		{[]string{"Network", "WebBrowser"}, "browser", true},
		{[]string{"Development", "IDE"}, "ide", true},
		{[]string{"Utility", "TextEditor"}, "editor", true},
		{[]string{"Network", "InstantMessaging"}, "chat", true},
		{[]string{"AudioVideo", "Player"}, "media", true},
		{[]string{"Office"}, "", false},
	}
	for _, c := range cases {
		got, ok := AppCategory(c.cats)
		if got != c.want || ok != c.ok {
			t.Errorf("AppCategory(%v) = %q,%v want %q,%v", c.cats, got, ok, c.want, c.ok)
		}
	}
}
