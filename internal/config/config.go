// Package config loads ~/.config/desk-agent/config.toml and the site key.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/once-human/desk-agent/internal/paths"
)

const DefaultEndpoint = "https://www.onkaryaglewad.in/api/ingest/desk"

// Config is the on-disk configuration.
type Config struct {
	// Endpoint is the ingest URL samples are POSTed to.
	Endpoint string `toml:"endpoint"`
	// IntervalS is the heartbeat while the laptop is active.
	IntervalS int `toml:"interval_s"`
	// IdleIntervalS is the heartbeat while idle.
	IdleIntervalS int `toml:"idle_interval_s"`
	// UploadEveryS is how often collected samples are uploaded. Sampling stays
	// at interval_s; this only batches the uploads so the site's database can
	// sleep in between. 0 uploads every sample as it happens.
	UploadEveryS int `toml:"upload_every_s"`
	// PushMode is how eagerly samples reach the site: "realtime" (every sample
	// at once), "default" (calls and other big changes at once, the rest
	// batched every upload_every_s), "15min" or "30min" (everything batched,
	// calls included). See PushPolicy.
	PushMode string `toml:"push_mode"`
	// ActiveWindowS is how recent the last input must be for active=true.
	ActiveWindowS int `toml:"active_window_s"`
	// Private sends presence only (active/idle/lid): no app, site, repo,
	// Claude state, media or battery.
	Private bool `toml:"private"`
	// HideRepos are repo names that are never reported.
	HideRepos []string `toml:"hide_repos"`
	// Battery turns the optional battery reading on.
	Battery bool `toml:"battery"`
	// SitesFile / AppsFile override the default category maps.
	SitesFile string `toml:"sites_file"`
	AppsFile  string `toml:"apps_file"`
	// AdultBlocklistFile is an optional extra host blocklist (one host per
	// line, or hosts-file format).
	AdultBlocklistFile string `toml:"adult_blocklist_file"`
}

// Default returns the built-in defaults.
func Default() Config {
	return Config{
		Endpoint:      DefaultEndpoint,
		IntervalS:     30,
		IdleIntervalS: 60,
		UploadEveryS:  900,
		PushMode:      PushDefault,
		ActiveWindowS: 180,
		Battery:       true,
	}
}

// Load reads path. A missing file yields defaults with no error so a fresh
// install still runs; doctor reports it.
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			c.fillPaths()
			return c, nil
		}
		return c, err
	}
	if _, err := toml.Decode(string(data), &c); err != nil {
		return Default(), fmt.Errorf("parse %s: %w", path, err)
	}
	c.fillPaths()
	return c, c.Validate()
}

func (c *Config) fillPaths() {
	c.SitesFile = paths.Expand(c.SitesFile)
	c.AppsFile = paths.Expand(c.AppsFile)
	c.AdultBlocklistFile = paths.Expand(c.AdultBlocklistFile)
	if c.SitesFile == "" {
		c.SitesFile = paths.SitesFile()
	}
	if c.AppsFile == "" {
		c.AppsFile = paths.AppsFile()
	}
	if c.AdultBlocklistFile == "" {
		// Used only if the file exists; a missing file is fine.
		c.AdultBlocklistFile = filepath.Join(paths.ConfigDir(), "adult-hosts.txt")
	}
}

// Validate rejects values that would make the agent misbehave.
func (c *Config) Validate() error {
	if !strings.HasPrefix(c.Endpoint, "https://") && !strings.HasPrefix(c.Endpoint, "http://127.0.0.1") && !strings.HasPrefix(c.Endpoint, "http://localhost") {
		return fmt.Errorf("endpoint must be https:// (got %q)", c.Endpoint)
	}
	if c.IntervalS < 5 {
		return fmt.Errorf("interval_s must be >= 5 (got %d)", c.IntervalS)
	}
	if c.IdleIntervalS < c.IntervalS {
		return fmt.Errorf("idle_interval_s (%d) must be >= interval_s (%d)", c.IdleIntervalS, c.IntervalS)
	}
	if c.UploadEveryS != 0 && (c.UploadEveryS < 30 || c.UploadEveryS > 3600) {
		return fmt.Errorf("upload_every_s must be 0 or between 30 and 3600 (got %d)", c.UploadEveryS)
	}
	switch c.PushMode {
	case PushRealtime, PushDefault, Push15, Push30:
	default:
		return fmt.Errorf("push_mode must be realtime, default, 15min or 30min (got %q)", c.PushMode)
	}
	if c.ActiveWindowS < 30 {
		return fmt.Errorf("active_window_s must be >= 30 (got %d)", c.ActiveWindowS)
	}
	return nil
}

// Push modes.
const (
	PushRealtime = "realtime"
	PushDefault  = "default"
	Push15       = "15min"
	Push30       = "30min"
)

// PushPolicy turns push_mode into what the sender needs: how long samples are
// held between uploads (0 = send each at once) and whether big changes (a call
// starting or ending, you sitting down or walking away) skip the wait.
func (c Config) PushPolicy() (every time.Duration, live bool) {
	switch c.PushMode {
	case PushRealtime:
		return 0, false
	case Push15:
		return 15 * time.Minute, false
	case Push30:
		return 30 * time.Minute, false
	}
	return time.Duration(c.UploadEveryS) * time.Second, true
}

// HidesRepo reports whether name is listed in hide_repos (case-insensitive).
func (c *Config) HidesRepo(name string) bool {
	for _, h := range c.HideRepos {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}

// DefaultTOML is written by `desk-agent init` when no config exists.
const DefaultTOML = `# desk-agent configuration

# Where samples are sent.
endpoint = "` + DefaultEndpoint + `"

# Heartbeat in seconds while you are active / idle. Changes (new app, new
# site, lid closed...) are sent immediately regardless.
interval_s = 30
idle_interval_s = 60

# How often collected samples are uploaded, in seconds. Samples are still
# taken every interval_s; they are sent in one batch. A longer gap lets the
# site's free database sleep between uploads. 0 uploads each sample at once.
upload_every_s = 900

# How eagerly samples are pushed to the site:
#   "realtime"  every sample the moment it is taken (for demos; keeps the
#               site's database awake, so the agent drops back to "default"
#               after two hours)
#   "default"   calls and other big changes at once, everything else batched
#               every upload_every_s
#   "15min"     everything batched every 15 minutes, calls included
#   "30min"     everything batched every 30 minutes, calls included
# The final sample before suspend or shutdown always goes out straight away.
push_mode = "default"

# Input within this many seconds counts as "active".
active_window_s = 180

# private = true sends presence only (active / idle / lid). No app, site,
# repo, Claude state, media or battery.
private = false

# Repo names that must never be reported.
hide_repos = []

# Report battery percent and charging state.
battery = true

# Category maps (generated by "desk-agent scan-history" / "desk-agent scan-apps").
# sites_file = "~/.config/desk-agent/sites.toml"
# apps_file  = "~/.config/desk-agent/apps.toml"

# Optional extra host blocklist for adult sites (one host per line).
# adult_blocklist_file = "~/.config/desk-agent/adult-hosts.txt"
`

// Key returns the site key. DESK_AGENT_KEY wins over the key file. The
// second return value says where it came from.
func Key() (key, source string, err error) {
	if v := strings.TrimSpace(os.Getenv("DESK_AGENT_KEY")); v != "" {
		return v, "DESK_AGENT_KEY", nil
	}
	data, err := os.ReadFile(paths.KeyFile())
	if err != nil {
		return "", "", fmt.Errorf("no site key: set DESK_AGENT_KEY or run `desk-agent set-key` (%w)", err)
	}
	k := strings.TrimSpace(string(data))
	if k == "" {
		return "", "", fmt.Errorf("%s is empty", paths.KeyFile())
	}
	return k, paths.KeyFile(), nil
}

// KeyFileModeProblem returns a description if the key file is readable by
// anyone but the owner, or "" if fine / absent.
func KeyFileModeProblem() string {
	st, err := os.Stat(paths.KeyFile())
	if err != nil {
		return ""
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Sprintf("%s is mode %04o, should be 0600 (chmod 600 %s)", paths.KeyFile(), st.Mode().Perm(), paths.KeyFile())
	}
	return ""
}

// WriteKey stores key with mode 0600, creating the config dir (0700).
func WriteKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("empty key")
	}
	if err := paths.EnsureDir(paths.ConfigDir()); err != nil {
		return err
	}
	tmp := paths.KeyFile() + ".tmp"
	if err := os.WriteFile(tmp, []byte(key+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, paths.KeyFile())
}

// Init creates the config directory and a default config.toml if missing.
// It never touches an existing config or key.
func Init() (created bool, err error) {
	if err := paths.EnsureDir(paths.ConfigDir()); err != nil {
		return false, err
	}
	if err := paths.EnsureDir(paths.StateDir()); err != nil {
		return false, err
	}
	if _, err := os.Stat(paths.ConfigFile()); err == nil {
		return false, nil
	}
	return true, os.WriteFile(paths.ConfigFile(), []byte(DefaultTOML), 0o644)
}
