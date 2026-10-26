package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"os"
	"path/filepath"

	"github.com/once-human/desk-agent/internal/adult"
	"github.com/once-human/desk-agent/internal/battery"
	"github.com/once-human/desk-agent/internal/bridge"
	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/claude"
	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/control"
	"github.com/once-human/desk-agent/internal/hypr"
	"github.com/once-human/desk-agent/internal/idle"
	"github.com/once-human/desk-agent/internal/logind"
	"github.com/once-human/desk-agent/internal/mpris"
	"github.com/once-human/desk-agent/internal/nativehost"
	"github.com/once-human/desk-agent/internal/paths"
)

func init() {
	Checks = append(Checks,
		Check{"agent", checkAgent},
		Check{"hyprland focus", checkHyprland},
		Check{"idle", checkIdle},
		Check{"logind", checkLogind},
		Check{"media (mpris)", checkMedia},
		Check{"claude code hooks", checkClaudeHooks},
		Check{"browser extension", checkBrowserExtension},
		Check{"sites & apps", checkCatalogs},
		Check{"battery", checkBattery},
	)
}

func checkAgent(_ context.Context, _ config.Config) Result {
	if reply, err := control.Notify("ping"); err == nil && reply == "pong" {
		return Result{OK, "running"}
	}
	return Result{Warn, "not running (start it: systemctl --user start desk-agent)"}
}

// checkHyprland reports the focused app id only, never the window title.
func checkHyprland(_ context.Context, _ config.Config) Result {
	dir, err := hypr.Instance()
	if err != nil {
		return Result{Fail, err.Error()}
	}
	f, err := hypr.Query(dir)
	if err != nil {
		return Result{Fail, "cannot query the command socket: " + err.Error()}
	}
	if f.Class == "" {
		return Result{OK, "connected, no window focused right now"}
	}
	return Result{OK, fmt.Sprintf("connected, focused app id %q", f.Class)}
}

func checkIdle(_ context.Context, _ config.Config) Result {
	err := idle.Probe()
	switch {
	case err == nil:
		return Result{OK, "ext-idle-notify-v1 available"}
	case errors.Is(err, idle.ErrUnsupported):
		return Result{Warn, "compositor lacks ext-idle-notify-v1: idleS will be omitted and idle never detected"}
	default:
		return Result{Warn, err.Error()}
	}
}

func checkLogind(_ context.Context, _ config.Config) Result {
	closed, err := logind.Probe()
	if err != nil {
		return Result{Warn, "no suspend/lid/shutdown handling: " + err.Error()}
	}
	if closed {
		return Result{OK, "connected (lid is closed)"}
	}
	return Result{OK, "connected (lid open or no lid switch)"}
}

func checkBattery(_ context.Context, cfg config.Config) Result {
	if !cfg.Battery {
		return Result{Skip, "disabled in config"}
	}
	b := battery.Read()
	if b == nil {
		return Result{Skip, "no battery found"}
	}
	return Result{OK, fmt.Sprintf("%d%%, charging=%v", b.Pct, b.Charging)}
}

// checkMedia lists players by id and kind only; it never reads metadata.
func checkMedia(_ context.Context, cfg config.Config) Result {
	apps, _ := catalog.LoadApps(cfg.AppsFile)
	players, err := mpris.Probe(apps)
	if err != nil {
		return Result{Warn, "session bus unavailable: " + err.Error()}
	}
	if len(players) == 0 {
		return Result{OK, "session bus ok, no players running"}
	}
	return Result{OK, strings.Join(players, "; ")}
}

func checkClaudeHooks(_ context.Context, _ config.Config) Result {
	n, total, err := claude.HooksInstalled(claude.SettingsPath())
	if err != nil {
		return Result{Warn, err.Error()}
	}
	if n == total {
		return Result{OK, fmt.Sprintf("%d/%d hooks present in %s", n, total, claude.SettingsPath())}
	}
	return Result{Warn, fmt.Sprintf("%d/%d hooks in %s: run `desk-agent install-hooks`", n, total, claude.SettingsPath())}
}

// checkBrowserExtension says whether the native-messaging manifests exist and,
// if the agent is running, which browsers' extensions are connected right now.
func checkBrowserExtension(_ context.Context, _ config.Config) Result {
	home, _ := os.UserHomeDir()
	var have []string
	for _, t := range nativehost.Targets(home) {
		if _, err := os.Stat(filepath.Join(t.Dir, nativehost.HostName+".json")); err == nil {
			have = append(have, t.Browser)
		}
	}
	if len(have) == 0 {
		return Result{Warn, "native host not installed: run `desk-agent install-native-host`, then load the extension (extension/README.md). Without it no site is reported"}
	}
	fams, err := bridge.Query(paths.BrowserSocket())
	if err != nil {
		return Result{Warn, "manifest for " + strings.Join(have, ", ") + "; agent not running, cannot tell if an extension is connected"}
	}
	if len(fams) == 0 {
		return Result{Warn, "manifest for " + strings.Join(have, ", ") + ", but no extension is connected (is it loaded and enabled?)"}
	}
	return Result{OK, "connected: " + strings.Join(fams, ", ") + " (manifest for " + strings.Join(have, ", ") + ")"}
}

// checkCatalogs validates sites.toml / apps.toml and reports the adult filter.
func checkCatalogs(_ context.Context, cfg config.Config) Result {
	var notes []string
	level := OK
	for _, f := range []struct {
		name, path, scanCmd string
		parse               func([]byte) (int, error)
	}{
		{"sites.toml", cfg.SitesFile, "scan-history", func(b []byte) (int, error) { m, err := catalog.ParseSites(b); return len(m), err }},
		{"apps.toml", cfg.AppsFile, "scan-apps", func(b []byte) (int, error) { m, err := catalog.ParseApps(b); return len(m), err }},
	} {
		data, err := os.ReadFile(f.path)
		if os.IsNotExist(err) {
			notes = append(notes, f.name+" missing (built-in seed only; run `desk-agent "+f.scanCmd+"`)")
			continue
		}
		if err != nil {
			return Result{Fail, err.Error()}
		}
		n, err := f.parse(data)
		if err != nil {
			return Result{Fail, f.name + ": " + err.Error()}
		}
		notes = append(notes, fmt.Sprintf("%s %d entries", f.name, n))
	}
	if ad, err := adult.Load(cfg.AdultBlocklistFile); err != nil {
		level = Warn
		notes = append(notes, "adult blocklist unreadable: "+err.Error())
	} else if ad.Len() > 0 {
		notes = append(notes, fmt.Sprintf("adult filter: heuristics + %d blocklisted hosts", ad.Len()))
	} else {
		notes = append(notes, "adult filter: built-in heuristics (no blocklist file)")
	}
	return Result{level, strings.Join(notes, "; ")}
}
