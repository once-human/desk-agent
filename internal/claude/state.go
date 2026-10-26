// Package claude tracks whether Claude Code is working, from hook events.
//
// Privacy: hooks receive the user's prompt and tool inputs on stdin. Only
// session_id and cwd are decoded; everything else is discarded by the JSON
// decoder and nothing is logged.
package claude

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Window is how long after the last "start" event Claude counts as working
// if no "stop" arrives.
const Window = 90 * time.Second

// pruneAfter drops sessions that have been silent for a day.
const pruneAfter = 24 * time.Hour

// HookInput is the subset of Claude Code's hook payload we decode.
type HookInput struct {
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
}

// ParseHookInput reads a hook payload, capped at 8 MiB. Malformed input
// yields an empty HookInput rather than an error: a hook must never fail.
func ParseHookInput(r io.Reader) HookInput {
	var in HookInput
	_ = json.NewDecoder(io.LimitReader(r, 8<<20)).Decode(&in)
	return in
}

type Session struct {
	CWD     string `json:"cwd"`
	TS      int64  `json:"ts"` // unix seconds of the last event
	Working bool   `json:"working"`
}

// State is the on-disk file ($XDG_RUNTIME_DIR/desk-agent/claude.json).
type State struct {
	Sessions map[string]Session `json:"sessions"`
}

// Apply folds one hook event into the state. kind is "start" or "stop".
func (st *State) Apply(kind string, in HookInput, now time.Time) {
	if st.Sessions == nil {
		st.Sessions = map[string]Session{}
	}
	id := in.SessionID
	if id == "" {
		id = "default"
	}
	switch kind {
	case "start":
		s := st.Sessions[id]
		if in.CWD != "" {
			s.CWD = in.CWD
		}
		s.TS, s.Working = now.Unix(), true
		st.Sessions[id] = s
	case "stop":
		if in.HookEventName == "SessionEnd" {
			delete(st.Sessions, id)
			break
		}
		s := st.Sessions[id]
		if in.CWD != "" {
			s.CWD = in.CWD
		}
		s.TS, s.Working = now.Unix(), false
		st.Sessions[id] = s
	}
	for k, s := range st.Sessions {
		if now.Sub(time.Unix(s.TS, 0)) > pruneAfter {
			delete(st.Sessions, k)
		}
	}
}

// Evaluate reports whether any session is working and, if so, the cwd of the
// most recently active one and when that stops being true without new events.
func (st State) Evaluate(now time.Time) (working bool, cwd string, expires time.Time) {
	var best int64
	for _, s := range st.Sessions {
		if !s.Working || now.Sub(time.Unix(s.TS, 0)) > Window {
			continue
		}
		if s.TS >= best {
			best, cwd, working = s.TS, s.CWD, true
		}
	}
	if working {
		expires = time.Unix(best, 0).Add(Window)
	}
	return working, cwd, expires
}

// Read loads the state file; a missing or unreadable file is empty state.
func Read(path string) State {
	var st State
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	return st
}

// Record applies an event to the file under an exclusive lock, so several
// Claude sessions can fire hooks concurrently.
func Record(path, kind string, in HookInput, now time.Time) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	st := Read(path)
	st.Apply(kind, in, now)
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic: watchers see IN_MOVED_TO
}
