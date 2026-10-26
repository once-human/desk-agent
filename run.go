package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"

	"github.com/once-human/desk-agent/internal/battery"
	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/control"
	"github.com/once-human/desk-agent/internal/engine"
	"github.com/once-human/desk-agent/internal/hypr"
	"github.com/once-human/desk-agent/internal/idle"
	"github.com/once-human/desk-agent/internal/logind"
	"github.com/once-human/desk-agent/internal/mpris"
	"github.com/once-human/desk-agent/internal/paths"
	"github.com/once-human/desk-agent/internal/queue"
	"github.com/once-human/desk-agent/internal/sender"
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

	go hypr.Watch(ctx, log, eng.SetFocus)

	go func() {
		w := mpris.NewWatcher(apps, log, eng.SetMedia)
		if err := w.Run(ctx); err != nil && ctx.Err() == nil {
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
