// Package engine folds every signal into one picture of the laptop and
// decides when a sample is worth sending: immediately on change (debounced),
// otherwise on a heartbeat timer.
package engine

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/hypr"
	"github.com/once-human/desk-agent/internal/idle"
	"github.com/once-human/desk-agent/internal/sample"
)

// Debounce is the minimum gap between change-triggered samples.
const Debounce = 2 * time.Second

// Sink receives finished samples.
type Sink interface {
	Send(ctx context.Context, s sample.Sample)
	SendFinal(s sample.Sample, timeout time.Duration)
}

// Engine is safe for concurrent use; every signal source calls its setters
// from its own goroutine.
type Engine struct {
	Cfg         config.Config
	Apps        *catalog.Apps
	Sink        Sink
	Log         *slog.Logger
	Now         func() time.Time
	ReadBattery func() *sample.Battery
	// ReadSys reports machine load and the agent's own footprint.
	ReadSys func() *sample.Sys
	// OnHeartbeat runs just before each heartbeat sample (used to re-resolve
	// the focused repo, which changes without any event when you cd).
	OnHeartbeat func()

	mu        sync.Mutex
	focus     hypr.Focus
	idle      idle.State
	haveIdle  bool
	lid       bool
	asleep    bool
	paused    bool
	forceNext bool

	// Filled in by the later signal sources.
	claudeOn   bool
	claudeRepo string
	focusRepo  string
	site       *sample.Site
	media      *sample.Media

	lastSig  string
	lastEmit time.Time
	lastAct  bool
	dirty    chan struct{}

	history []sample.Sample // the most recent emitted samples, newest last

	// Focus dwell: seconds per window class inside the current sample bucket.
	dwellBucket string
	dwell       map[string]float64
	focusSince  time.Time
}

// accrueLocked credits the time since the last change to the focused class,
// inside the current bucket (the sample's truncated time). Samples in the same
// bucket replace each other on the site, so each one carries the bucket's
// running totals.
func (e *Engine) accrueLocked(now time.Time) {
	bucket := sample.Format(now, e.interval())
	if bucket != e.dwellBucket {
		e.dwellBucket = bucket
		e.dwell = map[string]float64{}
		if start, err := time.Parse("2006-01-02T15:04:05Z", bucket); err == nil && e.focusSince.Before(start) {
			e.focusSince = start
		}
	}
	if e.focusSince.IsZero() {
		e.focusSince = now
		return
	}
	if e.focus.Class != "" && !e.lid && !e.asleep {
		e.dwell[e.focus.Class] += now.Sub(e.focusSince).Seconds()
	}
	e.focusSince = now
}

// HistoryCap bounds how many emitted samples are remembered in memory.
const HistoryCap = 240

// New returns an Engine. Now and ReadBattery default to the real ones.
func New(cfg config.Config, apps *catalog.Apps, sink Sink, log *slog.Logger) *Engine {
	return &Engine{
		Cfg: cfg, Apps: apps, Sink: sink, Log: log,
		Now:   time.Now,
		dirty: make(chan struct{}, 1),
	}
}

func (e *Engine) poke() {
	select {
	case e.dirty <- struct{}{}:
	default:
	}
}

// interval requires e.mu to be held.
func (e *Engine) interval() time.Duration { return time.Duration(e.Cfg.IntervalS) * time.Second }

// Reconfigure swaps in a freshly loaded config and app catalog without a
// restart. The endpoint is owned by the sender and is not changed here.
func (e *Engine) Reconfigure(cfg config.Config, apps *catalog.Apps) {
	e.mu.Lock()
	e.Cfg = cfg
	if apps != nil {
		e.Apps = apps
	}
	e.forceNext = true // what is reported may have changed (private, hide_repos...)
	e.mu.Unlock()
	e.poke()
}

// Private reports the current private-mode setting.
func (e *Engine) Private() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Cfg.Private
}

// Paused reports whether sending is paused.
func (e *Engine) Paused() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.paused
}

// Asleep reports whether the machine is suspended or shutting down.
func (e *Engine) Asleep() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.asleep
}

// History returns a copy of the recently emitted samples, oldest first.
func (e *Engine) History() []sample.Sample {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sample.Sample(nil), e.history...)
}

// ---- setters (one per signal) ----

func (e *Engine) SetFocus(f hypr.Focus) {
	e.mu.Lock()
	e.accrueLocked(e.Now())
	changed := e.focus != f
	e.focus = f
	e.mu.Unlock()
	if changed {
		e.poke()
	}
}

// IdleReady marks that an idle source is connected, so idleS is meaningful.
func (e *Engine) IdleReady() {
	e.mu.Lock()
	e.haveIdle = true
	e.mu.Unlock()
}

// IdleLost marks the idle source as gone (idleS is omitted, state reset).
func (e *Engine) IdleLost() {
	e.mu.Lock()
	e.haveIdle = false
	e.idle = idle.State{}
	e.mu.Unlock()
	e.poke()
}

func (e *Engine) IdleEvent(ev idle.Event) {
	e.mu.Lock()
	e.idle.Apply(ev, e.Now())
	e.mu.Unlock()
	e.poke()
}

func (e *Engine) SetLid(closed bool) {
	e.mu.Lock()
	changed := e.lid != closed
	e.lid = closed
	e.mu.Unlock()
	if changed {
		e.poke()
	}
}

func (e *Engine) SetClaude(working bool, repo string) {
	e.mu.Lock()
	changed := e.claudeOn != working || e.claudeRepo != repo
	e.claudeOn, e.claudeRepo = working, repo
	e.mu.Unlock()
	if changed {
		e.poke()
	}
}

// SetFocusAndRepo updates focus and its repo together, so no sample ever
// pairs the new app with the previous app's repo.
func (e *Engine) SetFocusAndRepo(f hypr.Focus, repo string) {
	e.mu.Lock()
	changed := e.focus != f || e.focusRepo != repo
	e.focus, e.focusRepo = f, repo
	e.mu.Unlock()
	if changed {
		e.poke()
	}
}

// SetFocusRepo sets the repo derived from the focused terminal/editor.
func (e *Engine) SetFocusRepo(repo string) {
	e.mu.Lock()
	changed := e.focusRepo != repo
	e.focusRepo = repo
	e.mu.Unlock()
	if changed {
		e.poke()
	}
}

func (e *Engine) SetSite(s *sample.Site) {
	e.mu.Lock()
	e.site = s
	e.mu.Unlock()
	e.poke()
}

func (e *Engine) SetMedia(m *sample.Media) {
	e.mu.Lock()
	e.media = m
	e.mu.Unlock()
	e.poke()
}

// Focus returns the current focus (used by the repo and site resolvers).
func (e *Engine) Focus() hypr.Focus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.focus
}

// ---- lifecycle ----

// Sleep sends the final "not active" sample and goes silent until Resume.
// It blocks (up to 4 s) so the packet leaves before the machine suspends.
func (e *Engine) Sleep() { e.goQuiet() }

// Shutdown is Sleep for power-off; there is no Resume.
func (e *Engine) Shutdown() { e.goQuiet() }

func (e *Engine) goQuiet() {
	e.mu.Lock()
	if e.asleep {
		e.mu.Unlock()
		return
	}
	e.asleep = true
	send := !e.paused
	final := sample.Final(e.Now(), e.interval())
	e.mu.Unlock()
	if send {
		e.Sink.SendFinal(final, 4*time.Second)
	}
}

// Resume sends a fresh sample right away.
func (e *Engine) Resume() {
	e.mu.Lock()
	e.asleep = false
	e.idle = idle.State{} // the user just woke the machine
	e.forceNext = true
	e.mu.Unlock()
	e.poke()
}

// SetPaused implements the kill switch. Pausing sends one final "not
// active" sample, then nothing until resumed.
func (e *Engine) SetPaused(p bool) {
	e.mu.Lock()
	if e.paused == p {
		e.mu.Unlock()
		return
	}
	e.paused = p
	var final *sample.Sample
	if p && !e.asleep {
		f := sample.Final(e.Now(), e.interval())
		final = &f
	}
	if !p {
		e.forceNext = true
	}
	e.mu.Unlock()
	if final != nil {
		e.Sink.SendFinal(*final, 3*time.Second)
	}
	if !p {
		e.poke()
	}
}

// ---- building samples ----

// Build returns the sample describing right now.
func (e *Engine) Build() sample.Sample {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.buildLocked()
}

func (e *Engine) buildLocked() sample.Sample {
	now := e.Now()
	idleFor := e.idle.IdleFor(now)
	window := time.Duration(e.Cfg.ActiveWindowS) * time.Second
	active := !e.lid && !e.asleep && idleFor < window

	s := sample.Sample{T: sample.Format(now, e.interval()), Active: active}
	if e.haveIdle {
		s.IdleS = sample.Int(int(idleFor / time.Second))
	}
	if e.lid {
		return s // lid closed: nothing else is meaningful
	}
	if e.Cfg.Private {
		return s // presence only
	}

	if e.focus.Class != "" {
		ent := e.Apps.Lookup(e.focus.Class)
		s.App = &sample.App{ID: e.focus.Class, Name: ent.Name, Category: ent.Category}
		if ent.Category == "browser" && e.site != nil {
			site := *e.site
			s.Site = &site
		}
	}
	repo := e.focusRepo
	if repo == "" && e.claudeOn {
		repo = e.claudeRepo
	}
	if repo != "" && !e.Cfg.HidesRepo(repo) {
		s.Repo = repo
	}
	s.Claude = sample.Bool(e.claudeOn)
	if e.media != nil {
		m := *e.media
		s.Media = &m
	}
	if e.Cfg.Battery && e.ReadBattery != nil {
		s.Battery = e.ReadBattery()
	}
	e.accrueLocked(now)
	s.Focus = e.focusItemsLocked()
	if e.ReadSys != nil {
		s.Sys = e.ReadSys()
	}
	return s
}

// ---- main loop ----

// Run sends the first sample, then reacts to changes and heartbeats until
// ctx ends.
func (e *Engine) Run(ctx context.Context) {
	hb := time.NewTimer(time.Hour)
	var debT *time.Timer
	var debC <-chan time.Time

	reset := func() {
		e.mu.Lock()
		d := e.interval()
		if !e.lastAct {
			d = time.Duration(e.Cfg.IdleIntervalS) * time.Second
		}
		e.mu.Unlock()
		hb.Reset(d)
	}

	e.emit(ctx, true)
	reset()
	for {
		select {
		case <-ctx.Done():
			hb.Stop()
			if debT != nil {
				debT.Stop()
			}
			return
		case <-e.dirty:
			if debC != nil {
				continue // an emit is already scheduled
			}
			e.mu.Lock()
			wait := Debounce - e.Now().Sub(e.lastEmit)
			e.mu.Unlock()
			if wait > 0 {
				debT = time.NewTimer(wait)
				debC = debT.C
				continue
			}
			if e.emit(ctx, false) {
				reset()
			}
		case <-debC:
			debC, debT = nil, nil
			if e.emit(ctx, false) {
				reset()
			}
		case <-hb.C:
			if e.OnHeartbeat != nil {
				e.OnHeartbeat()
			}
			e.emit(ctx, true)
			reset()
		}
	}
}

// emit builds and sends a sample and reports whether it sent one. Without
// force it only sends if something that matters changed since the last one.
// The heartbeat timer is only restarted after a real send, so no-op wake-ups
// can never starve it.
func (e *Engine) emit(ctx context.Context, force bool) bool {
	e.mu.Lock()
	if e.paused || e.asleep {
		e.mu.Unlock()
		return false
	}
	s := e.buildLocked()
	sig := s.Signature()
	if e.forceNext {
		force, e.forceNext = true, false
	}
	if !force && sig == e.lastSig {
		e.mu.Unlock()
		return false
	}
	e.lastSig, e.lastEmit, e.lastAct = sig, e.Now(), s.Active
	e.history = append(e.history, s)
	if len(e.history) > HistoryCap {
		e.history = append(e.history[:0], e.history[len(e.history)-HistoryCap:]...)
	}
	e.mu.Unlock()
	e.Sink.Send(ctx, s)
	return true
}

// focusItemsLocked is the bucket's dwell, longest first, at most four apps;
// anything under a second is noise from flicking past a workspace.
func (e *Engine) focusItemsLocked() []sample.FocusItem {
	out := make([]sample.FocusItem, 0, len(e.dwell))
	for class, s := range e.dwell {
		if s < 1 {
			continue
		}
		ent := e.Apps.Lookup(class)
		out = append(out, sample.FocusItem{ID: class, Name: ent.Name, Category: ent.Category, S: int(s + 0.5)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].S > out[j].S })
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}
