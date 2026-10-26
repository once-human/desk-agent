// Package catalog maps raw app ids and hostnames to friendly names and
// categories. The maps are data (apps.toml / sites.toml); the built-in seed is
// only a starting point.
package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Entry is a friendly name plus a category.
type Entry struct {
	Name     string `toml:"name"`
	Category string `toml:"category"`
}

// Apps resolves Hyprland window classes.
type Apps struct {
	m map[string]Entry
}

// seedApps covers common Linux desktop apps. apps.toml entries win.
var seedApps = map[string]Entry{
	// terminals
	"kitty": {"Kitty", "terminal"}, "alacritty": {"Alacritty", "terminal"}, "foot": {"Foot", "terminal"},
	"footclient": {"Foot", "terminal"}, "wezterm": {"WezTerm", "terminal"}, "org.wezfurlong.wezterm": {"WezTerm", "terminal"},
	"com.mitchellh.ghostty": {"Ghostty", "terminal"}, "ghostty": {"Ghostty", "terminal"},
	"org.kde.konsole": {"Konsole", "terminal"}, "konsole": {"Konsole", "terminal"},
	"gnome-terminal-server": {"GNOME Terminal", "terminal"}, "xterm": {"XTerm", "terminal"}, "tilix": {"Tilix", "terminal"},
	// editors / IDEs
	"code": {"VS Code", "editor"}, "code-oss": {"Code - OSS", "editor"}, "vscodium": {"VSCodium", "editor"},
	"cursor": {"Cursor", "editor"}, "dev.zed.zed": {"Zed", "editor"}, "zed": {"Zed", "editor"},
	"neovide": {"Neovide", "editor"}, "emacs": {"Emacs", "editor"}, "sublime_text": {"Sublime Text", "editor"},
	"jetbrains-studio": {"Android Studio", "ide"}, "jetbrains-idea": {"IntelliJ IDEA", "ide"},
	"jetbrains-pycharm": {"PyCharm", "ide"}, "jetbrains-webstorm": {"WebStorm", "ide"}, "jetbrains-clion": {"CLion", "ide"},
	// browsers
	"firefox": {"Firefox", "browser"}, "org.mozilla.firefox": {"Firefox", "browser"}, "zen": {"Zen", "browser"},
	"zen-browser": {"Zen", "browser"}, "zen-alpha": {"Zen", "browser"}, "librewolf": {"LibreWolf", "browser"},
	"floorp": {"Floorp", "browser"}, "chromium": {"Chromium", "browser"}, "google-chrome": {"Chrome", "browser"},
	"brave-browser": {"Brave", "browser"}, "vivaldi-stable": {"Vivaldi", "browser"}, "microsoft-edge": {"Edge", "browser"},
	// chat
	"discord": {"Discord", "chat"}, "vesktop": {"Discord", "chat"}, "slack": {"Slack", "chat"},
	"org.telegram.desktop": {"Telegram", "chat"}, "telegramdesktop": {"Telegram", "chat"}, "signal": {"Signal", "chat"},
	"element": {"Element", "chat"}, "whatsapp-for-linux": {"WhatsApp", "chat"},
	// design
	"gimp": {"GIMP", "design"}, "org.inkscape.inkscape": {"Inkscape", "design"}, "inkscape": {"Inkscape", "design"},
	"krita": {"Krita", "design"}, "blender": {"Blender", "design"}, "figma-linux": {"Figma", "design"},
	// media
	"mpv": {"mpv", "media"}, "vlc": {"VLC", "media"}, "spotify": {"Spotify", "media"}, "com.obsproject.studio": {"OBS", "media"},
}

// LoadApps reads apps.toml (a missing file is fine) on top of the seed.
func LoadApps(path string) (*Apps, error) {
	a := &Apps{m: make(map[string]Entry, len(seedApps))}
	for k, v := range seedApps {
		a.m[k] = v
	}
	if path == "" {
		return a, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return a, nil
		}
		return a, err
	}
	user := map[string]Entry{}
	if _, err := toml.Decode(string(data), &user); err != nil {
		return a, err
	}
	for k, v := range user {
		a.m[strings.ToLower(k)] = v
	}
	return a, nil
}

// Lookup resolves a window class. Unknown classes get a prettified name and
// category "other" rather than being hidden.
func (a *Apps) Lookup(class string) Entry {
	class = strings.ToLower(strings.TrimSpace(class))
	if e, ok := a.m[class]; ok {
		return fill(e, class)
	}
	// jetbrains-<product>-<hash> style classes: try the first two segments.
	if strings.HasPrefix(class, "jetbrains-") {
		parts := strings.SplitN(class, "-", 3)
		if len(parts) >= 2 {
			if e, ok := a.m["jetbrains-"+parts[1]]; ok {
				return fill(e, class)
			}
		}
	}
	return Entry{Name: Prettify(class), Category: "other"}
}

func fill(e Entry, class string) Entry {
	if e.Name == "" {
		e.Name = Prettify(class)
	}
	if e.Category == "" {
		e.Category = "other"
	}
	return e
}

// Prettify turns an app id into a display name: the last segment of a
// reverse-DNS id, separators to spaces, capitalised.
func Prettify(id string) string {
	id = strings.TrimSpace(id)
	if i := strings.LastIndex(id, "."); i >= 0 && i < len(id)-1 && strings.Count(id, ".") >= 2 {
		id = id[i+1:]
	}
	id = strings.NewReplacer("-", " ", "_", " ").Replace(id)
	words := strings.Fields(id)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// Has reports whether class resolves to a real entry (not the "unknown app"
// fallback).
func (a *Apps) Has(class string) bool {
	class = strings.ToLower(strings.TrimSpace(class))
	if _, ok := a.m[class]; ok {
		return true
	}
	if strings.HasPrefix(class, "jetbrains-") {
		parts := strings.SplitN(class, "-", 3)
		if len(parts) >= 2 {
			_, ok := a.m["jetbrains-"+parts[1]]
			return ok
		}
	}
	return false
}

// AppCategories are the categories the site understands for apps.
var AppCategories = []string{"editor", "ide", "terminal", "chat", "design", "browser", "media", "other"}

// ParseApps decodes and validates an apps.toml document.
func ParseApps(data []byte) (map[string]Entry, error) {
	raw := map[string]Entry{}
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		return nil, fmt.Errorf("unknown field %q (allowed: name, category)", und[0].String())
	}
	out := make(map[string]Entry, len(raw))
	for k, v := range raw {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" || strings.ContainsAny(key, "/ ") {
			return nil, fmt.Errorf("%q is not a window class", k)
		}
		if v.Category == "" {
			v.Category = "other"
		}
		ok := false
		for _, c := range AppCategories {
			if c == v.Category {
				ok = true
			}
		}
		if !ok {
			return nil, fmt.Errorf("%s: unknown category %q (use one of %s)", k, v.Category, strings.Join(AppCategories, ", "))
		}
		out[key] = v
	}
	return out, nil
}
