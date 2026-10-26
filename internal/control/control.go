// Package control is the small unix-socket channel to the running agent.
//
// Commands (one line in, one line out):
//
//	ping    -> "pong"
//	reload  -> re-read the pause flag, config.toml, sites.toml, apps.toml and
//	           the adult blocklist; "ok paused=<bool>" or "error <why>"
//	flush   -> save the focused-apps record to disk now; "ok"
//	state   -> one line of JSON (see State), used by `desk-agent status --json`
//	           and the GUI
//
// The socket lives in $XDG_RUNTIME_DIR/desk-agent (mode 0700), so only this
// user can reach it.
package control

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/once-human/desk-agent/internal/paths"
	"github.com/once-human/desk-agent/internal/sample"
	"github.com/once-human/desk-agent/internal/sender"
)

// Handler is implemented by the agent.
type Handler interface {
	SetPaused(bool)
	// Reload re-reads configuration and catalogs. On error the previous
	// configuration stays in force.
	Reload() error
	State() State
}

// Flusher is optionally implemented by a Handler to save in-memory records on
// request (the `flush` command).
type Flusher interface{ Flush() }

// State is what `state` returns: everything a UI needs, nothing sensitive
// beyond what the agent would send anyway (the sample is exactly what is, or
// would be, sent).
type State struct {
	Version string    `json:"version"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	Paused  bool      `json:"paused"`
	Private bool      `json:"private"`
	Asleep  bool      `json:"asleep"`
	// Sample is what would be sent right now; nil while paused or asleep.
	Sample *sample.Sample `json:"sample"`
	// Recent are the samples emitted lately, oldest first.
	Recent []sample.Sample `json:"recent"`
	Send   sender.Stats    `json:"send"`
	// Browsers are the browser families with a live extension connection.
	Browsers     []string `json:"browsers"`
	EndpointHost string   `json:"endpointHost"`
}

// PausedFlag reports whether the pause flag file exists.
func PausedFlag() bool {
	_, err := os.Stat(paths.PausedFile())
	return err == nil
}

// Serve listens on the control socket until ctx ends. The socket lives in
// $XDG_RUNTIME_DIR/desk-agent (mode 0700), so only this user can reach it.
func Serve(ctx context.Context, log *slog.Logger, h Handler) error {
	if err := paths.EnsureDir(paths.RuntimeDir()); err != nil {
		return err
	}
	sock := paths.CtlSocket()
	_ = os.Remove(sock)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	_ = os.Chmod(sock, 0o600)
	go func() { <-ctx.Done(); ln.Close(); os.Remove(sock) }()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveConn(c, log, h)
	}
}

func serveConn(c net.Conn, log *slog.Logger, h Handler) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(c).ReadString('\n')
	if err != nil && line == "" {
		return
	}
	switch strings.TrimSpace(line) {
	case "reload":
		p := PausedFlag()
		h.SetPaused(p)
		if err := h.Reload(); err != nil {
			log.Warn("reload failed, keeping the previous configuration", "err", err)
			fmt.Fprintf(c, "error %s\n", strings.ReplaceAll(err.Error(), "\n", " "))
			return
		}
		log.Info("reloaded", "paused", p)
		fmt.Fprintf(c, "ok paused=%v\n", p)
	case "state":
		b, err := json.Marshal(h.State())
		if err != nil {
			fmt.Fprintf(c, "error %v\n", err)
			return
		}
		fmt.Fprintf(c, "%s\n", b)
	case "flush":
		if f, ok := h.(Flusher); ok {
			f.Flush()
		}
		fmt.Fprintln(c, "ok")
	case "ping":
		fmt.Fprintln(c, "pong")
	default:
		fmt.Fprintln(c, "error unknown command")
	}
}

// Notify sends cmd to the running agent. It returns an error if no agent is
// listening, which callers treat as "nothing to notify".
func Notify(cmd string) (string, error) {
	c, err := net.DialTimeout("unix", paths.CtlSocket(), time.Second)
	if err != nil {
		return "", errors.New("agent is not running")
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(6 * time.Second))
	if _, err := fmt.Fprintln(c, cmd); err != nil {
		return "", err
	}
	reply, err := bufio.NewReaderSize(c, 1<<20).ReadString('\n')
	return strings.TrimSpace(reply), err
}
