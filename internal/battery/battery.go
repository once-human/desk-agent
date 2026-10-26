// Package battery reads /sys/class/power_supply.
package battery

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/once-human/desk-agent/internal/sample"
)

// Parse turns the raw sysfs strings into a reading. It returns nil if the
// capacity is unreadable.
func Parse(capacity, status string) *sample.Battery {
	pct, err := strconv.Atoi(strings.TrimSpace(capacity))
	if err != nil || pct < 0 || pct > 100 {
		return nil
	}
	switch strings.TrimSpace(status) {
	case "Charging", "Full":
		return &sample.Battery{Pct: pct, Charging: true}
	default:
		return &sample.Battery{Pct: pct, Charging: false}
	}
}

// Read returns the first battery found, or nil on desktops.
func Read() *sample.Battery { return ReadFrom("/sys/class/power_supply") }

// ReadFrom is Read with a configurable root (for tests).
func ReadFrom(root string) *sample.Battery {
	dirs, _ := filepath.Glob(filepath.Join(root, "*"))
	for _, d := range dirs {
		if t, err := os.ReadFile(filepath.Join(d, "type")); err != nil || strings.TrimSpace(string(t)) != "Battery" {
			continue
		}
		capb, err := os.ReadFile(filepath.Join(d, "capacity"))
		if err != nil {
			continue
		}
		st, _ := os.ReadFile(filepath.Join(d, "status"))
		if b := Parse(string(capb), string(st)); b != nil {
			return b
		}
	}
	return nil
}
