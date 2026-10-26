// Package sysstat reads how hard the laptop is working from /proc: whole
// machine CPU and RAM, and the agent's own memory and CPU. Everything is a
// few small file reads, done once per sample.
package sysstat

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/once-human/desk-agent/internal/sample"
)

// Reader keeps the previous CPU counters so each reading is a rate since the
// last one.
type Reader struct {
	Root string // "/proc", replaceable in tests

	mu        sync.Mutex
	lastTotal uint64
	lastIdle  uint64
	lastSelf  uint64
	lastAt    time.Time
	lastCPU   float64
	guiMB     float64
	guiAt     time.Time
}

func New() *Reader { return &Reader{Root: "/proc"} }

const clkTck = 100 // USER_HZ on every Linux the agent runs on

// Read returns a reading, or nil if /proc isn't readable.
func (r *Reader) Read() *sample.Sys {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	total, idle, ok := cpuTimes(filepath.Join(r.Root, "stat"))
	if !ok {
		return nil
	}
	s := &sample.Sys{}
	if r.lastTotal > 0 && total > r.lastTotal {
		dt := float64(total - r.lastTotal)
		di := float64(idle - r.lastIdle)
		s.CPU = clamp(int((1-di/dt)*100 + 0.5))
	}
	r.lastTotal, r.lastIdle = total, idle

	if memTotal, memAvail, swapTotal, swapFree, ok := memInfo(filepath.Join(r.Root, "meminfo")); ok && memTotal > 0 {
		used := memTotal - memAvail
		s.SwapTotalGB = round1(float64(swapTotal) / 1024 / 1024)
		s.SwapGB = round1(float64(swapTotal-swapFree) / 1024 / 1024)
		s.Mem = clamp(int(float64(used)/float64(memTotal)*100 + 0.5))
		s.MemGB = round1(float64(used) / 1024 / 1024)
		s.TotalGB = round1(float64(memTotal) / 1024 / 1024)
	}
	s.AgentMB = round1(float64(rssKB(filepath.Join(r.Root, "self", "status"))) / 1024)
	// The agent's own CPU is measured over at least ten seconds: a reading
	// taken a moment after the last one (focus changes force an extra sample)
	// would otherwise turn a few milliseconds of work into a huge percentage.
	if ticks, ok := selfTicks(filepath.Join(r.Root, "self", "stat")); ok {
		secs := now.Sub(r.lastAt).Seconds()
		switch {
		case r.lastAt.IsZero() || ticks < r.lastSelf:
			r.lastSelf, r.lastAt = ticks, now
		case secs >= 10:
			r.lastCPU = round1(float64(ticks-r.lastSelf) / clkTck / secs * 100)
			r.lastSelf, r.lastAt = ticks, now
		}
		s.AgentCPU = r.lastCPU
	}
	if b, err := os.ReadFile(filepath.Join(r.Root, "uptime")); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				s.UptimeH = round1(v / 3600)
			}
		}
	}
	// The settings app is looked for at most every five minutes (it means listing /proc).
	if now.Sub(r.guiAt) > 5*time.Minute {
		r.guiMB, r.guiAt = r.findGUI(), now
	}
	s.GuiMB = r.guiMB
	return s
}

func cpuTimes(path string) (total, idle uint64, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i == 3 || i == 4 { // idle, iowait
			idle += n
		}
	}
	return total, idle, true
}

func memInfo(path string) (total, avail, swapTotal, swapFree uint64, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0, 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		n, _ := strconv.ParseUint(f[1], 10, 64)
		switch f[0] {
		case "MemTotal:":
			total = n
		case "MemAvailable:":
			avail = n
		case "SwapTotal:":
			swapTotal = n
		case "SwapFree:":
			swapFree = n
		}
	}
	if swapFree > swapTotal {
		swapFree = swapTotal
	}
	return total, avail, swapTotal, swapFree, total > 0
}

func rssKB(path string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				n, _ := strconv.ParseUint(f[1], 10, 64)
				return n
			}
		}
	}
	return 0
}

// selfTicks is utime+stime from /proc/self/stat (fields 14 and 15; the
// command name in field 2 can contain spaces, so count from its closing paren).
func selfTicks(path string) (uint64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, false
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 13 {
		return 0, false
	}
	u, _ := strconv.ParseUint(f[11], 10, 64)
	st, _ := strconv.ParseUint(f[12], 10, 64)
	return u + st, true
}

func (r *Reader) findGUI() float64 {
	ents, err := os.ReadDir(r.Root)
	if err != nil {
		return 0
	}
	var kb uint64
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		cmd, err := os.ReadFile(filepath.Join(r.Root, e.Name(), "cmdline"))
		if err != nil || !strings.Contains(string(cmd), "deskagent_gui") {
			continue
		}
		kb += rssKB(filepath.Join(r.Root, e.Name(), "status"))
	}
	return round1(float64(kb) / 1024)
}

func clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }
