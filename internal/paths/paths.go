// Package paths resolves the XDG locations desk-agent uses.
package paths

import (
	"os"
	"path/filepath"
)

const app = "desk-agent"

func home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}

func xdg(env, fallback string) string {
	if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
		return v
	}
	return filepath.Join(home(), fallback)
}

// ConfigDir is ~/.config/desk-agent.
func ConfigDir() string { return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), app) }

// StateDir is ~/.local/state/desk-agent (queue, pause flag).
func StateDir() string { return filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), app) }

// DataDir is ~/.local/share/desk-agent.
func DataDir() string { return filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), app) }

// RuntimeDir is $XDG_RUNTIME_DIR/desk-agent. It is per-user and mode 0700 on
// systemd systems, which is why the control and extension sockets live here.
func RuntimeDir() string {
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return filepath.Join(v, app)
	}
	return filepath.Join(os.TempDir(), app+"-"+itoa(os.Getuid()))
}

func ConfigFile() string { return filepath.Join(ConfigDir(), "config.toml") }
func KeyFile() string    { return filepath.Join(ConfigDir(), "key") }
func SitesFile() string  { return filepath.Join(ConfigDir(), "sites.toml") }
func AppsFile() string   { return filepath.Join(ConfigDir(), "apps.toml") }
func QueueFile() string  { return filepath.Join(StateDir(), "queue.jsonl") }
func PausedFile() string { return filepath.Join(StateDir(), "paused") }

func CtlSocket() string     { return filepath.Join(RuntimeDir(), "ctl.sock") }
func BrowserSocket() string { return filepath.Join(RuntimeDir(), "browser.sock") }
func ClaudeState() string   { return filepath.Join(RuntimeDir(), "claude.json") }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// EnsureDir creates dir (and parents) with mode 0700.
func EnsureDir(dir string) error { return os.MkdirAll(dir, 0o700) }
