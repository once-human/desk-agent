package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/once-human/desk-agent/internal/catalog"
)

// Desktop is the little we need from a .desktop file.
type Desktop struct {
	Name       string
	WMClass    string
	ExecBase   string
	FileBase   string
	Categories []string
}

// DesktopDirs lists where .desktop files live.
func DesktopDirs(home string) []string {
	return []string{
		filepath.Join(home, ".local/share/applications"),
		filepath.Join(home, ".local/share/flatpak/exports/share/applications"),
		"/usr/local/share/applications",
		"/usr/share/applications",
		"/var/lib/flatpak/exports/share/applications",
		"/var/lib/snapd/desktop/applications",
	}
}

// ParseDesktop parses the [Desktop Entry] group of a .desktop file.
func ParseDesktop(path string) (Desktop, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Desktop{}, false
	}
	defer f.Close()
	d := Desktop{FileBase: strings.TrimSuffix(filepath.Base(path), ".desktop")}
	in := false
	hidden := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			in = line == "[Desktop Entry]"
			continue
		}
		if !in || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "Name":
			d.Name = v
		case "StartupWMClass":
			d.WMClass = v
		case "Exec":
			fields := strings.Fields(v)
			for len(fields) > 0 && (fields[0] == "env" || strings.Contains(fields[0], "=")) {
				fields = fields[1:]
			}
			if len(fields) > 0 {
				d.ExecBase = filepath.Base(strings.Trim(fields[0], `"`))
			}
		case "Categories":
			for _, c := range strings.Split(v, ";") {
				if c != "" {
					d.Categories = append(d.Categories, c)
				}
			}
		case "NoDisplay", "Hidden":
			if v == "true" {
				hidden = true
			}
		}
	}
	_ = hidden // hidden entries can still be the app that was focused
	return d, d.Name != ""
}

// LoadDesktops reads every .desktop file in dirs; earlier dirs win.
func LoadDesktops(dirs []string) []Desktop {
	seen := map[string]bool{}
	var out []Desktop
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".desktop") || seen[e.Name()] {
				continue
			}
			if d, ok := ParseDesktop(filepath.Join(dir, e.Name())); ok {
				seen[e.Name()] = true
				out = append(out, d)
			}
		}
	}
	return out
}

// Match finds the desktop entry for a window class: by StartupWMClass first,
// then the file name, then the executable.
func Match(class string, ds []Desktop) (Desktop, bool) {
	c := strings.ToLower(class)
	for _, d := range ds {
		if d.WMClass != "" && strings.ToLower(d.WMClass) == c {
			return d, true
		}
	}
	for _, d := range ds {
		if strings.ToLower(d.FileBase) == c {
			return d, true
		}
	}
	for _, d := range ds {
		if d.ExecBase != "" && strings.ToLower(d.ExecBase) == c {
			return d, true
		}
	}
	return Desktop{}, false
}

// AppCategory maps freedesktop categories to the site's app categories.
// ok is false when nothing matched (the caller falls back to "other").
func AppCategory(cats []string) (string, bool) {
	has := map[string]bool{}
	for _, c := range cats {
		has[c] = true
	}
	switch {
	case has["TerminalEmulator"]:
		return "terminal", true
	case has["WebBrowser"]:
		return "browser", true
	case has["IDE"]:
		return "ide", true
	case has["TextEditor"]:
		return "editor", true
	case has["InstantMessaging"] || has["Chat"] || has["IRCClient"] || has["VideoConference"]:
		return "chat", true
	case has["Graphics"] || has["2DGraphics"] || has["3DGraphics"] || has["VectorGraphics"] || has["RasterGraphics"] || has["Photography"]:
		return "design", true
	case has["Audio"] || has["Video"] || has["AudioVideo"] || has["Player"] || has["Music"] || has["Recorder"]:
		return "media", true
	case has["Development"]:
		return "editor", true
	}
	return "", false
}

// ProposeApps builds rows for classes that have no entry yet. known is the
// resolver (seed + apps.toml) and existing the keys already in apps.toml; both
// are respected so nothing is duplicated.
func ProposeApps(seen map[string]int, known *catalog.Apps, existing map[string]catalog.Entry, ds []Desktop) []AppProposal {
	var out []AppProposal
	for class, n := range seen {
		key := strings.ToLower(class)
		if _, have := existing[key]; have {
			continue
		}
		if known != nil && known.Has(key) {
			continue
		}
		p := AppProposal{Class: key, Count: n}
		if d, ok := Match(key, ds); ok {
			p.Name = d.Name
			if cat, ok := AppCategory(d.Categories); ok {
				p.Category, p.Source = cat, "desktop"
			} else {
				p.Category, p.Source = "other", "guess"
			}
		} else {
			p.Name, p.Category, p.Source = catalog.Prettify(key), "other", "unknown"
		}
		out = append(out, p)
	}
	return out
}
