// Package control is the tiny unix-socket channel `desk-agent pause|resume`
// uses to tell the running agent to re-read the pause flag immediately.
package control

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/once-human/desk-agent/internal/paths"
)

// Handler applies a state change; it is implemented by the agent.
type Handler interface {
	SetPaused(bool)
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
		log.Info("pause flag applied", "paused", p)
		fmt.Fprintf(c, "ok paused=%v\n", p)
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
	reply, err := bufio.NewReader(c).ReadString('\n')
	return strings.TrimSpace(reply), err
}
