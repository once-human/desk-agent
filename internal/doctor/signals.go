package doctor

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/once-human/desk-agent/internal/battery"
	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/control"
	"github.com/once-human/desk-agent/internal/hypr"
	"github.com/once-human/desk-agent/internal/idle"
	"github.com/once-human/desk-agent/internal/logind"
	"github.com/once-human/desk-agent/internal/mpris"
)

func init() {
	Checks = append(Checks,
		Check{"agent", checkAgent},
		Check{"hyprland focus", checkHyprland},
		Check{"idle", checkIdle},
		Check{"logind", checkLogind},
		Check{"media (mpris)", checkMedia},
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
