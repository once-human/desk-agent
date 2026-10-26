package sysstat

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRead(t *testing.T) {
	d := t.TempDir()
	write(t, d, "stat", "cpu  100 0 100 800 0 0 0 0 0 0\n")
	write(t, d, "meminfo", "MemTotal:       16000000 kB\nMemAvailable:    4000000 kB\nSwapTotal:       8388608 kB\nSwapFree:        7340032 kB\n")
	write(t, d, "self/status", "Name:\tx\nVmRSS:\t   12288 kB\n")
	write(t, d, "self/stat", "1 (desk agent) S 0 0 0 0 0 0 0 0 0 0 5 5 0 0\n")
	write(t, d, "uptime", "7200.5 100.0\n")
	r := &Reader{Root: d}
	s := r.Read()
	if s == nil || s.Mem != 75 || s.AgentMB != 12 || s.UptimeH != 2 || s.SwapTotalGB != 8 || s.SwapGB != 1 {
		t.Fatalf("first read: %+v", s)
	}
	write(t, d, "stat", "cpu  200 0 200 900 0 0 0 0 0 0\n") // 300 ticks, 100 idle
	s = r.Read()
	if s.CPU != 67 {
		t.Fatalf("cpu = %d, want 67", s.CPU)
	}
}
