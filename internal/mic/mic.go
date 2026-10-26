// Package mic says whether the microphone is recording right now, which on
// this laptop almost always means a call (Meet, Zoom, Discord). It reads the
// ALSA capture streams' status files: no audio is ever touched. PipeWire
// suspends a device a few seconds after the last app stops recording, so
// "RUNNING" tracks real use closely.
package mic

import (
	"os"
	"path/filepath"
	"strings"
)

// InUse reports whether any capture stream is running.
func InUse() bool { return InUseUnder("/proc/asound") }

func InUseUnder(root string) bool {
	files, _ := filepath.Glob(filepath.Join(root, "card*", "pcm*c", "sub*", "status"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err == nil && strings.Contains(string(b), "state: RUNNING") {
			return true
		}
	}
	return false
}
