// Package idle tracks keyboard/mouse inactivity with the Wayland
// ext-idle-notify-v1 protocol (the same one hypridle uses). It is event
// driven: the compositor tells us when input stops and resumes.
//
// Known behaviour: ext_idle_notifier_v1 respects idle inhibitors, so while an
// app holds one (a fullscreen video, a call) you are reported as active even
// without touching the keyboard.
package idle

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rajveermalviya/go-wayland/wayland/client"
	idlenotify "github.com/rajveermalviya/go-wayland/wayland/staging/ext-idle-notify-v1"
)

// Event is one notification from the compositor.
type Event struct {
	// Idle is true when the seat has been idle for at least Threshold, false
	// when input resumed.
	Idle      bool
	Threshold time.Duration
}

// State folds events into "how long has the user been away".
type State struct {
	idleSince time.Time // zero while the user is active
}

// Apply records ev, which happened at now.
func (s *State) Apply(ev Event, now time.Time) {
	if !ev.Idle {
		s.idleSince = time.Time{}
		return
	}
	since := now.Add(-ev.Threshold)
	// Keep the earliest estimate: a longer threshold firing must not move
	// the start of the idle period later.
	if s.idleSince.IsZero() || since.Before(s.idleSince) {
		s.idleSince = since
	}
}

// IdleFor is how long input has been absent, or 0 if the user is active.
// Precision is limited to the registered thresholds.
func (s *State) IdleFor(now time.Time) time.Duration {
	if s.idleSince.IsZero() {
		return 0
	}
	if d := now.Sub(s.idleSince); d > 0 {
		return d
	}
	return 0
}

// Thresholds returns the notification timeouts to register: a few coarse
// steps so idleS is meaningful, always including the active window.
func Thresholds(activeWindow time.Duration) []time.Duration {
	set := map[time.Duration]bool{10 * time.Second: true, 60 * time.Second: true, activeWindow: true}
	var out []time.Duration
	for d := range set {
		if d > 0 {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// WaylandSocket finds the compositor socket: $WAYLAND_DISPLAY if it exists,
// else the newest wayland-* socket in $XDG_RUNTIME_DIR (a systemd user
// service often has no WAYLAND_DISPLAY).
func WaylandSocket() (string, error) {
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" {
		return "", errors.New("XDG_RUNTIME_DIR not set")
	}
	if d := os.Getenv("WAYLAND_DISPLAY"); d != "" {
		p := d
		if !filepath.IsAbs(p) {
			p = filepath.Join(rt, d)
		}
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	matches, _ := filepath.Glob(filepath.Join(rt, "wayland-*"))
	var best string
	var bestMod time.Time
	for _, m := range matches {
		if strings.HasSuffix(m, ".lock") {
			continue
		}
		st, err := os.Stat(m)
		if err != nil || st.Mode()&os.ModeSocket == 0 {
			continue
		}
		if best == "" || st.ModTime().After(bestMod) {
			best, bestMod = m, st.ModTime()
		}
	}
	if best == "" {
		return "", errors.New("no wayland socket found")
	}
	return best, nil
}

// ErrUnsupported means the compositor lacks ext-idle-notify-v1.
var ErrUnsupported = errors.New("compositor does not support ext-idle-notify-v1")

// Watch registers one idle notification per threshold, calls onReady once they
// are armed, then calls onEvent for each idled/resumed. It blocks until ctx ends or the connection drops.
func Watch(ctx context.Context, log *slog.Logger, thresholds []time.Duration, onReady func(), onEvent func(Event)) error {
	sock, err := WaylandSocket()
	if err != nil {
		return err
	}
	display, wctx, seat, notifier, err := connect(sock, log)
	if err != nil {
		return err
	}
	defer wctx.Close()
	go func() { <-ctx.Done(); wctx.Close() }()
	_ = display

	for _, th := range thresholds {
		th := th
		n, err := notifier.GetIdleNotification(uint32(th/time.Millisecond), seat)
		if err != nil {
			return err
		}
		n.SetIdledHandler(func(idlenotify.IdleNotificationIdledEvent) {
			onEvent(Event{Idle: true, Threshold: th})
		})
		n.SetResumedHandler(func(idlenotify.IdleNotificationResumedEvent) {
			onEvent(Event{Idle: false, Threshold: th})
		})
	}
	onReady()
	for ctx.Err() == nil {
		if err := wctx.Dispatch(); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("wayland connection lost: %w", err)
		}
	}
	return nil
}

// connect dials the compositor and binds wl_seat and ext_idle_notifier_v1.
func connect(sock string, log *slog.Logger) (*client.Display, *client.Context, *client.Seat, *idlenotify.IdleNotifier, error) {
	display, err := client.Connect(sock)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("wayland connect: %w", err)
	}
	wctx := display.Context()
	registry, err := display.GetRegistry()
	if err != nil {
		wctx.Close()
		return nil, nil, nil, nil, err
	}
	var (
		seat     *client.Seat
		notifier *idlenotify.IdleNotifier
	)
	registry.SetGlobalHandler(func(e client.RegistryGlobalEvent) {
		switch e.Interface {
		case "wl_seat":
			if seat == nil {
				seat = client.NewSeat(wctx)
				if err := registry.Bind(e.Name, e.Interface, 1, seat); err != nil {
					log.Warn("bind wl_seat failed", "err", err)
					seat = nil
				}
			}
		case "ext_idle_notifier_v1":
			if notifier == nil {
				notifier = idlenotify.NewIdleNotifier(wctx)
				if err := registry.Bind(e.Name, e.Interface, 1, notifier); err != nil {
					log.Warn("bind ext_idle_notifier_v1 failed", "err", err)
					notifier = nil
				}
			}
		}
	})
	if err := roundtrip(display, wctx); err != nil {
		wctx.Close()
		return nil, nil, nil, nil, err
	}
	if notifier == nil {
		wctx.Close()
		return nil, nil, nil, nil, ErrUnsupported
	}
	if seat == nil {
		wctx.Close()
		return nil, nil, nil, nil, errors.New("compositor offered no wl_seat")
	}
	return display, wctx, seat, notifier, nil
}

// Probe is used by doctor: it checks that the compositor offers
// ext-idle-notify-v1 without registering any notification.
func Probe() error {
	sock, err := WaylandSocket()
	if err != nil {
		return err
	}
	_, wctx, _, _, err := connect(sock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	return wctx.Close()
}

func roundtrip(display *client.Display, wctx *client.Context) error {
	cb, err := display.Sync()
	if err != nil {
		return err
	}
	done := false
	cb.SetDoneHandler(func(client.CallbackDoneEvent) { done = true })
	for !done {
		if err := wctx.Dispatch(); err != nil {
			return err
		}
	}
	return nil
}
