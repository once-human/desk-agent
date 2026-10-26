package sample

import (
	"strings"
	"testing"
	"time"
)

func TestFormatFloorsToInterval(t *testing.T) {
	at := time.Date(2026, 10, 26, 10, 0, 47, 0, time.UTC)
	if got := Format(at, 30*time.Second); got != "2026-10-26T10:00:30Z" {
		t.Fatalf("got %s", got)
	}
	if got := Format(at.In(time.FixedZone("IST", 19800)), 30*time.Second); got != "2026-10-26T10:00:30Z" {
		t.Fatalf("zone not normalised: %s", got)
	}
}

func TestSignatureIgnoresTimeIdleBattery(t *testing.T) {
	a := Sample{T: "2026-10-26T10:00:00Z", Active: true, IdleS: Int(1), Battery: &Battery{Pct: 80}, Repo: "x"}
	b := Sample{T: "2026-10-26T10:00:30Z", Active: true, IdleS: Int(40), Battery: &Battery{Pct: 79}, Repo: "x"}
	if a.Signature() != b.Signature() {
		t.Fatal("t/idleS/battery must not change the signature")
	}
	b.Repo = "y"
	if a.Signature() == b.Signature() {
		t.Fatal("repo change must change the signature")
	}
}

func TestFinalIsMinimal(t *testing.T) {
	s := Final(time.Date(2026, 10, 26, 10, 0, 5, 0, time.UTC), 30*time.Second)
	if s.Active || s.App != nil || s.Media != nil {
		t.Fatalf("%+v", s)
	}
	if !strings.Contains(s.Signature(), `"active":false`) {
		t.Fatal(s.Signature())
	}
}
