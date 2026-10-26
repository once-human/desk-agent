// Package hypr follows window focus through Hyprland's IPC sockets.
//
// Privacy: only the window class (app id) and pid are ever decoded. The title
// field in Hyprland's replies and events is never parsed, stored or logged;
// the raw reply buffer is zeroed right after decoding.
package hypr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Focus is the currently focused window. Class is lower-cased; an empty Class
// means nothing is focused.
type Focus struct {
	Class string
	PID   int
}

// reply deliberately has no Title field: encoding/json skips unknown keys, so
// the title is discarded during decoding.
type reply struct {
	Class        string `json:"class"`
	InitialClass string `json:"initialClass"`
	PID          int    `json:"pid"`
}

// Instance locates the running Hyprland instance's socket directory. It
// honours HYPRLAND_INSTANCE_SIGNATURE but also works from a systemd user
// service, where that variable is usually missing, by picking the newest
// instance under $XDG_RUNTIME_DIR/hypr.
func Instance() (string, error) {
	base := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "hypr")
	if sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"); sig != "" {
		if d := filepath.Join(base, sig); exists(filepath.Join(d, ".socket2.sock")) {
			return d, nil
		}
	}
	return newestInstance(base)
}

func newestInstance(base string) (string, error) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return "", errors.New("no Hyprland runtime dir (is Hyprland running?)")
	}
	type cand struct {
		dir string
		mod time.Time
	}
	var cands []cand
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sock := filepath.Join(base, e.Name(), ".socket2.sock")
		if st, err := os.Stat(sock); err == nil {
			cands = append(cands, cand{filepath.Join(base, e.Name()), st.ModTime()})
		}
	}
	if len(cands) == 0 {
		return "", errors.New("no Hyprland instance socket found")
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	return cands[0].dir, nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// ParseFocusJSON turns a `j/activewindow` reply into a Focus and wipes b.
func ParseFocusJSON(b []byte) Focus {
	defer func() {
		for i := range b {
			b[i] = 0
		}
	}()
	var r reply
	if json.Unmarshal(b, &r) != nil {
		return Focus{}
	}
	class := r.Class
	if class == "" {
		class = r.InitialClass
	}
	return Focus{Class: strings.ToLower(strings.TrimSpace(class)), PID: r.PID}
}

// Query asks the command socket for the active window.
func Query(dir string) (Focus, error) {
	c, err := net.DialTimeout("unix", filepath.Join(dir, ".socket.sock"), 2*time.Second)
	if err != nil {
		return Focus{}, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write([]byte("j/activewindow")); err != nil {
		return Focus{}, err
	}
	b, err := io.ReadAll(io.LimitReader(c, 1<<16))
	if err != nil {
		return Focus{}, err
	}
	return ParseFocusJSON(b), nil
}

// IsFocusEvent reports whether an event line signals a focus change. Only the
// v2 event is used; the legacy `activewindow>>` event carries the title and is
// never looked at beyond its prefix.
func IsFocusEvent(line string) bool {
	return strings.HasPrefix(line, "activewindowv2>>")
}

// Watch calls onFocus once at connect and again after every focus change,
// until ctx ends. It reconnects with back-off if Hyprland goes away.
func Watch(ctx context.Context, log *slog.Logger, onFocus func(Focus)) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		dir, err := Instance()
		if err == nil {
			err = watchOnce(ctx, dir, onFocus)
			if err == nil || ctx.Err() != nil {
				return
			}
			backoff = 2 * time.Second
		}
		log.Debug("hyprland unavailable", "err", err)
		onFocus(Focus{})
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func watchOnce(ctx context.Context, dir string, onFocus func(Focus)) error {
	conn, err := net.Dial("unix", filepath.Join(dir, ".socket2.sock"))
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()

	if f, err := Query(dir); err == nil {
		onFocus(f)
	}
	r := bufio.NewReaderSize(conn, 4096)
	for {
		line, isPrefix, err := r.ReadLine()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		// Skip the rest of an over-long line (some events carry titles).
		for isPrefix {
			_, isPrefix, err = r.ReadLine()
			if err != nil {
				return err
			}
		}
		if !IsFocusEvent(string(line[:min(len(line), 16)])) {
			continue
		}
		if f, err := Query(dir); err == nil {
			onFocus(f)
		}
	}
}
