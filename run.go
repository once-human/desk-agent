package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/once-human/desk-agent/internal/adult"
	"github.com/once-human/desk-agent/internal/battery"
	"github.com/once-human/desk-agent/internal/bridge"
	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/claude"
	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/control"
	"github.com/once-human/desk-agent/internal/engine"
	"github.com/once-human/desk-agent/internal/gitrepo"
	"github.com/once-human/desk-agent/internal/hypr"
	"github.com/once-human/desk-agent/internal/idle"
	"github.com/once-human/desk-agent/internal/logind"
	"github.com/once-human/desk-agent/internal/mpris"
	"github.com/once-human/desk-agent/internal/paths"
	"github.com/once-human/desk-agent/internal/proctree"
	"github.com/once-human/desk-agent/internal/queue"
	"github.com/once-human/desk-agent/internal/sample"
	"github.com/once-human/desk-agent/internal/seenapps"
	"github.com/once-human/desk-agent/internal/sender"
	"github.com/once-human/desk-agent/internal/site"
)

// cmdRun is what the systemd service executes.
func cmdRun(ctx context.Context) error {
	level := slog.LevelInfo
	if os.Getenv("DESK_AGENT_DEBUG") != "" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return err
	}
	apps, err := catalog.LoadApps(cfg.AppsFile)
	if err != nil {
		log.Warn("apps.toml unreadable, using built-in seed", "err", err)
	}
	sites, err := catalog.LoadSites(cfg.SitesFile)
	if err != nil {
		log.Warn("sites.toml unreadable, using built-in seed", "err", err)
	}
	ad, err := adult.Load(cfg.AdultBlocklistFile)
	if err != nil {
		log.Warn("adult blocklist unreadable, using built-in heuristics only", "err", err)
	}
	if p := config.KeyFileModeProblem(); p != "" {
		log.Warn(p)
	}
	if err := paths.EnsureDir(paths.StateDir()); err != nil {
		return err
	}
	if err := paths.EnsureDir(paths.RuntimeDir()); err != nil {
		return err
	}

	snd := sender.New(cfg.Endpoint, "desk-agent/"+version, func() (string, error) {
		k, _, err := config.Key()
		return k, err
	}, queue.New(paths.QueueFile()), log)
	go snd.Run(ctx)

	eng := engine.New(cfg, apps, snd, log)
	eng.ReadBattery = battery.Read
	if control.PausedFlag() {
		eng.SetPaused(true)
		log.Info("starting paused (flag file present)")
	}

	go func() {
		if err := control.Serve(ctx, log, eng); err != nil {
			log.Warn("control socket unavailable; pause/resume will apply on next restart", "err", err)
		}
	}()

	// Repo of the focused terminal/editor: resolved when focus changes and
	// again before every heartbeat (cd-ing between repos raises no event).
	resolveRepo := func(f hypr.Focus) string {
		if f.Class == "" || f.PID <= 0 {
			return ""
		}
		cat := apps.Lookup(f.Class).Category
		if cat != "terminal" && cat != "editor" && cat != "ide" {
			return ""
		}
		return proctree.Proc{}.RepoFor(f.PID, cat != "terminal")
	}

	// Browser site: the extension reports a hostname; it is only used while a
	// browser is the focused app, and resolved through the catalog and the
	// adult filter (a match reports nothing).
	br := bridge.NewServer(log)
	refreshSite := func() {
		f := eng.Focus()
		if apps.Lookup(f.Class).Category != "browser" {
			eng.SetSite(nil)
			return
		}
		host, ok := br.Host(f.Class)
		if !ok {
			eng.SetSite(nil)
			return
		}
		eng.SetSite(site.Resolve(host, sites, ad))
	}

	// Never let an adult site or brand appear in a native player's track text.
	setMedia := func(m *sample.Media) { eng.SetMedia(ad.ScrubMedia(m)) }
	mw := mpris.NewWatcher(apps, log, setMedia)
	br.OnChange = func() {
		refreshSite()
		mw.SetYouTubeAudible(br.YouTubeAudible())
	}
	go func() {
		if err := br.Run(ctx, paths.BrowserSocket()); err != nil && ctx.Err() == nil {
			log.Warn("browser bridge unavailable; sites will not be reported", "err", err)
		}
	}()

	seen := seenapps.Open(paths.SeenApps())
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = seen.Flush()
				return
			case <-t.C:
				_ = seen.Flush()
			}
		}
	}()

	eng.OnHeartbeat = func() {
		eng.SetFocusRepo(resolveRepo(eng.Focus()))
		refreshSite()
	}
	go hypr.Watch(ctx, log, func(f hypr.Focus) {
		seen.Note(f.Class)
		eng.SetFocusAndRepo(f, resolveRepo(f))
		refreshSite()
	})

	go func() {
		w := &claude.Watcher{Path: paths.ClaudeState(), OnChange: func(working bool, cwd string) {
			eng.SetClaude(working, gitrepo.Name(cwd))
		}}
		if err := w.Run(ctx); err != nil && ctx.Err() == nil {
			log.Warn("claude state watcher stopped", "err", err)
		}
	}()

	go func() {
		if err := mw.Run(ctx); err != nil && ctx.Err() == nil {
			log.Warn("media detection unavailable", "err", err)
		}
	}()

	go func() {
		th := idle.Thresholds(time.Duration(cfg.ActiveWindowS) * time.Second)
		wait := 5 * time.Second
		for ctx.Err() == nil {
			err := idle.Watch(ctx, log, th, eng.IdleReady, eng.IdleEvent)
			eng.IdleLost()
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, idle.ErrUnsupported) {
				log.Warn("idle detection unavailable: compositor lacks ext-idle-notify-v1; idleS will be omitted")
				return
			}
			log.Warn("idle watcher stopped, retrying", "err", err, "in", wait)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			if wait *= 2; wait > time.Minute {
				wait = time.Minute
			}
		}
	}()

	go func() {
		err := logind.Run(ctx, log, logind.Events{
			OnSleep:    eng.Sleep,
			OnResume:   eng.Resume,
			OnShutdown: eng.Shutdown,
			OnLid:      eng.SetLid,
		})
		if err != nil && ctx.Err() == nil {
			log.Warn("logind unavailable: no suspend/lid/shutdown handling", "err", err)
		}
	}()

	log.Info("desk-agent running", "version", version, "endpoint", cfg.Endpoint, "interval_s", cfg.IntervalS, "private", cfg.Private)
	eng.Run(ctx)
	return nil
}
