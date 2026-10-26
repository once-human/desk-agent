package engine

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/hypr"
	"github.com/once-human/desk-agent/internal/idle"
	"github.com/once-human/desk-agent/internal/sample"
)

type fakeSink struct {
	mu     sync.Mutex
	sent   []sample.Sample
	finals []sample.Sample
}

func (f *fakeSink) Send(_ context.Context, s sample.Sample) {
	f.mu.Lock()
	f.sent = append(f.sent, s)
	f.mu.Unlock()
}
func (f *fakeSink) SendFinal(s sample.Sample, _ time.Duration) {
	f.mu.Lock()
	f.finals = append(f.finals, s)
	f.mu.Unlock()
}

func newEng(t *testing.T, mut func(*config.Config)) (*Engine, *fakeSink, *time.Time) {
	t.Helper()
	cfg := config.Default()
	if mut != nil {
		mut(&cfg)
	}
	apps, _ := catalog.LoadApps("")
	sink := &fakeSink{}
	e := New(cfg, apps, sink, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, 10, 26, 10, 0, 7, 0, time.UTC)
	e.Now = func() time.Time { return now }
	return e, sink, &now
}

func TestBuildMapsFocusAndRepo(t *testing.T) {
	e, _, _ := newEng(t, nil)
	e.SetFocus(hypr.Focus{Class: "kitty", PID: 1})
	e.SetFocusRepo("1618")
	s := e.Build()
	if !s.Active || s.App == nil || s.App.Name != "Kitty" || s.App.Category != "terminal" || s.Repo != "1618" {
		t.Fatalf("%+v", s)
	}
	if s.T != "2026-10-26T10:00:00Z" {
		t.Fatalf("t=%s", s.T)
	}
}

func TestHiddenRepoIsNeverReported(t *testing.T) {
	e, _, _ := newEng(t, func(c *config.Config) { c.HideRepos = []string{"Secret-Repo"} })
	e.SetFocus(hypr.Focus{Class: "kitty"})
	e.SetFocusRepo("secret-repo")
	if r := e.Build().Repo; r != "" {
		t.Fatalf("leaked %q", r)
	}
	e.SetFocusRepo("")
	e.SetClaude(true, "secret-repo")
	if r := e.Build().Repo; r != "" {
		t.Fatalf("leaked via claude %q", r)
	}
}

func TestClaudeRepoUsedWhenNothingFocused(t *testing.T) {
	e, _, _ := newEng(t, nil)
	e.SetFocus(hypr.Focus{Class: "firefox"})
	e.SetClaude(true, "desk-agent")
	s := e.Build()
	if s.Repo != "desk-agent" || s.Claude == nil || !*s.Claude {
		t.Fatalf("%+v", s)
	}
}

func TestPrivateModeIsPresenceOnly(t *testing.T) {
	e, _, _ := newEng(t, func(c *config.Config) { c.Private = true })
	e.SetFocus(hypr.Focus{Class: "kitty"})
	e.SetFocusRepo("x")
	e.SetClaude(true, "x")
	e.SetMedia(&sample.Media{Player: "spotify", State: "playing", Source: "spotify", Title: sample.Str("t")})
	s := e.Build()
	if s.App != nil || s.Repo != "" || s.Claude != nil || s.Media != nil || s.Battery != nil {
		t.Fatalf("private mode leaked: %+v", s)
	}
}

func TestLidClosedAndIdleMeanNotActive(t *testing.T) {
	e, _, now := newEng(t, nil)
	e.IdleReady()
	e.SetFocus(hypr.Focus{Class: "kitty"})
	e.IdleEvent(idle.Event{Idle: true, Threshold: 180 * time.Second})
	*now = now.Add(5 * time.Second)
	s := e.Build()
	if s.Active || s.IdleS == nil || *s.IdleS < 180 || s.App == nil {
		t.Fatalf("idle: %+v", s)
	}
	e.IdleEvent(idle.Event{Idle: false})
	e.SetLid(true)
	s = e.Build()
	if s.Active || s.App != nil {
		t.Fatalf("lid closed should be minimal: %+v", s)
	}
}

func TestBrowserSiteOnlyForBrowsers(t *testing.T) {
	e, _, _ := newEng(t, nil)
	e.SetSite(&sample.Site{Host: "github.com", Name: "GitHub", Category: "code"})
	e.SetFocus(hypr.Focus{Class: "kitty"})
	if e.Build().Site != nil {
		t.Fatal("site must not be sent when a non-browser is focused")
	}
	e.SetFocus(hypr.Focus{Class: "firefox"})
	if e.Build().Site == nil {
		t.Fatal("site expected for a browser")
	}
}

func TestSleepSendsFinalOnceThenGoesQuiet(t *testing.T) {
	e, sink, _ := newEng(t, nil)
	e.Sleep()
	e.Sleep()
	if len(sink.finals) != 1 || sink.finals[0].Active {
		t.Fatalf("%+v", sink.finals)
	}
	if e.emit(context.Background(), true) {
		t.Fatal("must not send while asleep")
	}
	e.Resume()
	if !e.emit(context.Background(), false) {
		t.Fatal("resume must force an immediate sample")
	}
}

func TestPauseSendsFinalAndSuppressesEverything(t *testing.T) {
	e, sink, _ := newEng(t, nil)
	e.SetPaused(true)
	if len(sink.finals) != 1 {
		t.Fatalf("finals %d", len(sink.finals))
	}
	if e.emit(context.Background(), true) {
		t.Fatal("paused must send nothing")
	}
	e.SetPaused(false)
	if !e.emit(context.Background(), false) {
		t.Fatal("resume must force a sample")
	}
}

func TestReconfigureAppliesPrivateAndHideReposAtOnce(t *testing.T) {
	e, _, _ := newEng(t, nil)
	e.SetFocus(hypr.Focus{Class: "kitty"})
	e.SetFocusRepo("shh")
	if e.Build().Repo != "shh" || e.Private() {
		t.Fatal("precondition")
	}
	cfg := config.Default()
	cfg.HideRepos = []string{"shh"}
	e.Reconfigure(cfg, nil)
	if r := e.Build().Repo; r != "" {
		t.Fatalf("hidden repo leaked after reconfigure: %q", r)
	}
	cfg.Private = true
	e.Reconfigure(cfg, nil)
	if !e.Private() || e.Build().App != nil {
		t.Fatalf("private mode not applied: %+v", e.Build())
	}
}

func TestReconfigureForcesAFreshSample(t *testing.T) {
	e, sink, _ := newEng(t, nil)
	e.SetFocus(hypr.Focus{Class: "kitty"})
	ctx := context.Background()
	if !e.emit(ctx, false) {
		t.Fatal("first emit should send")
	}
	if e.emit(ctx, false) {
		t.Fatal("an unchanged sample must not be re-sent")
	}
	e.Reconfigure(config.Default(), nil)
	if !e.emit(ctx, false) {
		t.Fatal("reconfigure must force the next emit so a privacy change is not delayed")
	}
	if len(sink.sent) != 2 {
		t.Fatalf("sent %d", len(sink.sent))
	}
}

func TestHistoryIsBoundedAndCopied(t *testing.T) {
	e, _, now := newEng(t, nil)
	e.SetFocus(hypr.Focus{Class: "kitty"})
	for i := 0; i < HistoryCap+25; i++ {
		*now = now.Add(time.Minute)
		e.emit(context.Background(), true)
	}
	h := e.History()
	if len(h) != HistoryCap {
		t.Fatalf("len=%d want %d", len(h), HistoryCap)
	}
	if h[len(h)-1].T <= h[0].T {
		t.Fatal("history must be oldest first")
	}
	h[0].Repo = "mutated"
	if e.History()[0].Repo == "mutated" {
		t.Fatal("History must return a copy")
	}
}

func TestPausedAndAsleepAreReported(t *testing.T) {
	e, _, _ := newEng(t, nil)
	if e.Paused() || e.Asleep() {
		t.Fatal("start state")
	}
	e.SetPaused(true)
	if !e.Paused() {
		t.Fatal("paused")
	}
	e.SetPaused(false)
	e.Sleep()
	if !e.Asleep() {
		t.Fatal("asleep")
	}
	e.Resume()
	if e.Asleep() {
		t.Fatal("resume")
	}
}
