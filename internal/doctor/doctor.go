// Package doctor checks every signal source and prints what works.
package doctor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/paths"
)

type Level int

const (
	OK Level = iota
	Warn
	Fail
	Skip
)

func (l Level) String() string {
	switch l {
	case OK:
		return "ok  "
	case Warn:
		return "warn"
	case Fail:
		return "FAIL"
	default:
		return "skip"
	}
}

// Result is one check's outcome.
type Result struct {
	Level  Level
	Detail string
}

// Check is a named probe.
type Check struct {
	Name string
	Run  func(ctx context.Context, cfg config.Config) Result
}

// Checks is the registry; other files in this package append to it.
var Checks = []Check{
	{"config", checkConfig},
	{"site key", checkKey},
	{"endpoint", checkEndpoint},
}

// Run executes every check, prints a table to w and returns the number of
// FAIL results.
func Run(ctx context.Context, w io.Writer, version string) int {
	cfg, cfgErr := config.Load(paths.ConfigFile())
	fmt.Fprintf(w, "desk-agent %s doctor\n\n", version)
	fails := 0
	for _, c := range Checks {
		var r Result
		if c.Name == "config" && cfgErr != nil {
			r = Result{Fail, cfgErr.Error()}
		} else {
			cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			r = c.Run(cctx, cfg)
			cancel()
		}
		if r.Level == Fail {
			fails++
		}
		fmt.Fprintf(w, "  [%s] %-18s %s\n", r.Level, c.Name, r.Detail)
	}
	fmt.Fprintln(w)
	if fails == 0 {
		fmt.Fprintln(w, "Nothing is broken. Warnings are optional signals that are unavailable on this machine.")
	} else {
		fmt.Fprintf(w, "%d check(s) failed.\n", fails)
	}
	return fails
}

func checkConfig(_ context.Context, cfg config.Config) Result {
	if _, err := os.Stat(paths.ConfigFile()); err != nil {
		return Result{Warn, "no " + paths.ConfigFile() + " yet (defaults in use). Run `desk-agent init`."}
	}
	mode := "sending"
	if cfg.Private {
		mode = "PRIVATE mode (presence only)"
	}
	if _, err := os.Stat(paths.PausedFile()); err == nil {
		mode = "PAUSED (sends nothing)"
	}
	return Result{OK, fmt.Sprintf("%s, interval %ds, %s", paths.ConfigFile(), cfg.IntervalS, mode)}
}

func checkKey(_ context.Context, _ config.Config) Result {
	_, src, err := config.Key()
	if err != nil {
		return Result{Fail, err.Error()}
	}
	if p := config.KeyFileModeProblem(); p != "" && src != "DESK_AGENT_KEY" {
		return Result{Fail, p}
	}
	return Result{OK, "found in " + src}
}

// checkEndpoint posts an empty batch: it proves the site is reachable and the
// key is accepted without recording anything.
func checkEndpoint(ctx context.Context, cfg config.Config) Result {
	key, _, err := config.Key()
	if err != nil {
		return Result{Skip, "no key to test with"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.Endpoint, bytes.NewReader([]byte(`{"samples":[]}`)))
	if err != nil {
		return Result{Fail, err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Result{Fail, "cannot reach " + cfg.Endpoint + ": " + err.Error()}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return Result{Fail, "site rejected the key (401). Re-run `desk-agent set-key`."}
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return Result{OK, "reachable, key accepted (" + strings.TrimSpace(string(body)) + ")"}
	default:
		return Result{Warn, fmt.Sprintf("reachable but answered %d to an empty batch; key not verified", resp.StatusCode)}
	}
}

// UserAgent is set by main (desk-agent/<version>).
var UserAgent = "desk-agent/dev"
