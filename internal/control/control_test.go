package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/once-human/desk-agent/internal/paths"
	"github.com/once-human/desk-agent/internal/sample"
)

type fake struct {
	mu        sync.Mutex
	paused    bool
	reloads   int
	reloadErr error
	state     State
}

func (f *fake) SetPaused(p bool) { f.mu.Lock(); f.paused = p; f.mu.Unlock() }
func (f *fake) Reload() error    { f.mu.Lock(); defer f.mu.Unlock(); f.reloads++; return f.reloadErr }
func (f *fake) State() State     { f.mu.Lock(); defer f.mu.Unlock(); return f.state }

func serve(t *testing.T, h Handler) {
	t.Helper()
	dir, err := os.MkdirTemp("", "dactl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("XDG_STATE_HOME", dir)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go Serve(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), h)
	for i := 0; i < 100; i++ {
		if _, err := Notify("ping"); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not come up")
}

func TestPingStateReload(t *testing.T) {
	f := &fake{state: State{Version: "9.9", PID: 42, Sample: &sample.Sample{T: "2026-10-26T10:00:00Z", Active: true}, EndpointHost: "example.org"}}
	serve(t, f)

	if r, _ := Notify("ping"); r != "pong" {
		t.Fatalf("ping = %q", r)
	}
	r, err := Notify("state")
	if err != nil {
		t.Fatal(err)
	}
	var st State
	if err := json.Unmarshal([]byte(r), &st); err != nil || st.Version != "9.9" || st.PID != 42 || st.Sample == nil || !st.Sample.Active || st.EndpointHost != "example.org" {
		t.Fatalf("state: %v %+v", err, st)
	}

	if r, _ := Notify("reload"); r != "ok paused=false" || f.reloads != 1 {
		t.Fatalf("reload = %q reloads=%d", r, f.reloads)
	}
	// The pause flag is read on reload and handed to the agent.
	if err := os.MkdirAll(paths.StateDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(paths.PausedFile(), []byte("x"), 0o600)
	if r, _ := Notify("reload"); r != "ok paused=true" || !f.paused {
		t.Fatalf("reload with flag = %q paused=%v", r, f.paused)
	}
	os.Remove(paths.PausedFile())

	f.reloadErr = errors.New("sites.toml: line 3\nbad")
	r, _ = Notify("reload")
	if !strings.HasPrefix(r, "error ") || strings.Contains(r, "\n") {
		t.Fatalf("a failed reload must be one 'error' line: %q", r)
	}
	if r, _ := Notify("bogus"); !strings.HasPrefix(r, "error") {
		t.Fatalf("unknown command: %q", r)
	}
}

func TestSocketIsPrivate(t *testing.T) {
	serve(t, &fake{})
	fi, err := os.Stat(paths.CtlSocket())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("socket mode %v lets others in", fi.Mode().Perm())
	}
}

type flusher struct {
	fake
	flushed int
}

func (f *flusher) Flush() { f.mu.Lock(); f.flushed++; f.mu.Unlock() }

func TestFlushIsOptionalAndWorks(t *testing.T) {
	f := &flusher{}
	serve(t, f)
	if r, _ := Notify("flush"); r != "ok" || f.flushed != 1 {
		t.Fatalf("flush = %q flushed=%d", r, f.flushed)
	}
}

func TestFlushWithoutSupportIsHarmless(t *testing.T) {
	serve(t, &fake{})
	if r, _ := Notify("flush"); r != "ok" {
		t.Fatalf("flush = %q", r)
	}
}
