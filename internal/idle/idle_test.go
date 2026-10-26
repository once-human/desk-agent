package idle

import (
	"testing"
	"time"
)

func TestStateTracksIdleStartAndResume(t *testing.T) {
	var s State
	t0 := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	s.Apply(Event{Idle: true, Threshold: 10 * time.Second}, t0.Add(10*time.Second))
	if got := s.IdleFor(t0.Add(40 * time.Second)); got != 40*time.Second {
		t.Fatalf("got %v", got)
	}
	// a longer threshold firing must not shift the start later
	s.Apply(Event{Idle: true, Threshold: 60 * time.Second}, t0.Add(60*time.Second))
	if got := s.IdleFor(t0.Add(70 * time.Second)); got != 70*time.Second {
		t.Fatalf("got %v", got)
	}
	s.Apply(Event{Idle: false}, t0.Add(80*time.Second))
	if got := s.IdleFor(t0.Add(81 * time.Second)); got != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestThresholdsSortedAndIncludeWindow(t *testing.T) {
	got := Thresholds(3 * time.Minute)
	want := []time.Duration{10 * time.Second, time.Minute, 3 * time.Minute}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("%v", got)
	}
	if got := Thresholds(time.Minute); len(got) != 2 {
		t.Fatalf("dupes not removed: %v", got)
	}
}
