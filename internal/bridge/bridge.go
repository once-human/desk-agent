// Package bridge is the agent side of the browser extension link.
//
// The extension talks to `desk-agent native-host` (browser native messaging,
// stdio); each native-host process forwards validated one-line JSON messages
// to this unix socket. A message carries only:
//
//	{"b":"firefox|chromium","h":"<hostname or empty>","yt":<bool>}
//
// h is the hostname of the active tab of the focused, non-private browser
// window (empty when there is nothing to report); yt says whether a
// non-private YouTube tab is making sound. Unknown fields are rejected, so a
// URL, path or title cannot ride along.
package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/once-human/desk-agent/internal/hostname"
	"github.com/once-human/desk-agent/internal/paths"
)

// TTL is how long a report stays valid without a refresh. The extension
// re-sends every ~15-30 s while it is alive; if it dies or the browser is
// closed, the site disappears rather than going stale.
const TTL = 75 * time.Second

// MaxLine bounds a message. Real messages are under 100 bytes.
const MaxLine = 1024

// Wire is the message format between native-host and agent.
type Wire struct {
	Browser string `json:"b"`
	Host    string `json:"h"`
	YouTube bool   `json:"yt"`
}

// Decode parses and validates one line. Unknown fields, an unknown browser
// family, or a host that is not a plain host name are all rejected or blanked:
// a bad host becomes "" (nothing to report), never a passthrough.
func Decode(line []byte) (Wire, error) {
	var w Wire
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return Wire{}, err
	}
	if w.Browser != "firefox" && w.Browser != "chromium" {
		return Wire{}, errors.New("unknown browser family")
	}
	if w.Host != "" {
		h, ok := hostname.Normalise(w.Host)
		if !ok {
			h = ""
		}
		w.Host = h
	}
	return w, nil
}

// Family maps a window class to the browser family whose extension reports on
// it. Anything that is not a Gecko browser is treated as Chromium-family.
func Family(class string) string {
	c := strings.ToLower(class)
	for _, k := range []string{"firefox", "librewolf", "floorp", "waterfox", "zen", "mullvad", "tor-browser", "torbrowser"} {
		if strings.Contains(c, k) {
			return "firefox"
		}
	}
	return "chromium"
}

type report struct {
	w  Wire
	at time.Time
}

// Server accepts native-host connections and tracks their latest reports.
type Server struct {
	Log      *slog.Logger
	Now      func() time.Time
	OnChange func() // called (without locks held) when the view may have changed

	mu    sync.Mutex
	conns map[net.Conn]*report
}

func NewServer(log *slog.Logger) *Server {
	return &Server{Log: log, Now: time.Now, conns: map[net.Conn]*report{}}
}

// Update feeds a report in directly (used by tests and by serveConn).
func (s *Server) update(c net.Conn, w Wire) {
	s.mu.Lock()
	prev := s.conns[c]
	s.conns[c] = &report{w: w, at: s.Now()}
	changed := prev == nil || prev.w != w
	s.mu.Unlock()
	if changed && s.OnChange != nil {
		s.OnChange()
	}
}

func (s *Server) drop(c net.Conn) {
	s.mu.Lock()
	_, had := s.conns[c]
	delete(s.conns, c)
	s.mu.Unlock()
	if had && s.OnChange != nil {
		s.OnChange()
	}
}

// Host is the host to report while the focused app has window class class:
// the freshest non-empty host reported by an extension of the matching browser
// family. ok is false when there is nothing to report.
func (s *Server) Host(class string) (string, bool) {
	fam := Family(class)
	now := s.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var best *report
	for _, r := range s.conns {
		if r.w.Browser != fam || r.w.Host == "" || now.Sub(r.at) > TTL {
			continue
		}
		if best == nil || r.at.After(best.at) {
			best = r
		}
	}
	if best == nil {
		return "", false
	}
	return best.w.Host, true
}

// YouTubeAudible reports whether any fresh extension says a non-private
// YouTube tab is playing sound.
func (s *Server) YouTubeAudible() bool {
	now := s.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.conns {
		if r.w.YouTube && now.Sub(r.at) <= TTL {
			return true
		}
	}
	return false
}

// Connected lists the browser families with a live connection (for doctor).
func (s *Server) Connected() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, r := range s.conns {
		if !seen[r.w.Browser] {
			seen[r.w.Browser] = true
			out = append(out, r.w.Browser)
		}
	}
	return out
}

// Expire is called periodically so a silent extension's report is dropped
// from the view even when no new message arrives.
func (s *Server) Expire() bool {
	now := s.Now()
	changed := false
	s.mu.Lock()
	for _, r := range s.conns {
		if (r.w.Host != "" || r.w.YouTube) && now.Sub(r.at) > TTL {
			r.w.Host, r.w.YouTube = "", false
			changed = true
		}
	}
	s.mu.Unlock()
	if changed && s.OnChange != nil {
		s.OnChange()
	}
	return changed
}

// Run listens on path until ctx ends. The socket is created mode 0600 inside
// the 0700 runtime directory.
func (s *Server) Run(ctx context.Context, path string) error {
	if err := paths.EnsureDir(paths.RuntimeDir()); err != nil {
		return err
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	_ = os.Chmod(path, 0o600)
	go func() { <-ctx.Done(); ln.Close(); os.Remove(path) }()
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.Expire()
			}
		}
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.serveConn(c)
	}
}

func (s *Server) serveConn(c net.Conn) {
	defer c.Close()
	defer s.drop(c)
	r := bufio.NewReaderSize(c, MaxLine+1)
	for {
		line, err := r.ReadSlice('\n')
		if err != nil {
			return // EOF, or a line longer than the buffer: hang up
		}
		if bytes.Equal(bytes.TrimSpace(line), []byte("?")) {
			// Status query used by `desk-agent doctor`: which browser
			// families currently have a live extension connection.
			fmt.Fprintf(c, "%s\n", strings.Join(s.Connected(), ","))
			continue
		}
		w, derr := Decode(bytes.TrimSpace(line))
		if derr != nil {
			if s.Log != nil {
				s.Log.Debug("browser message rejected", "err", derr)
			}
			continue
		}
		s.update(c, w)
	}
}

// Query asks a running agent which browser families are connected. It is
// what doctor uses. An error means the agent (or its bridge) is not running.
func Query(path string) ([]string, error) {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintln(c, "?"); err != nil {
		return nil, err
	}
	reply, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		return nil, err
	}
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return nil, nil
	}
	return strings.Split(reply, ","), nil
}
