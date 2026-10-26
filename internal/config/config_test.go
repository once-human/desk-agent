package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/once-human/desk-agent/internal/paths"
)

func sandbox(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("HOME", d)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(d, "cfg"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(d, "state"))
	t.Setenv("DESK_AGENT_KEY", "")
	return d
}

func TestMissingConfigGivesDefaults(t *testing.T) {
	sandbox(t)
	c, err := Load(paths.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	if c.Endpoint != DefaultEndpoint || c.IntervalS != 30 || c.IdleIntervalS != 60 || c.ActiveWindowS != 180 || !c.Battery || c.Private {
		t.Errorf("defaults: %+v", c)
	}
	if c.SitesFile != paths.SitesFile() || c.AppsFile != paths.AppsFile() || !strings.HasSuffix(c.AdultBlocklistFile, "adult-hosts.txt") {
		t.Errorf("default paths: %+v", c)
	}
}

func TestDefaultTOMLParsesToDefaults(t *testing.T) {
	d := sandbox(t)
	p := filepath.Join(d, "c.toml")
	os.WriteFile(p, []byte(DefaultTOML), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatalf("the config written by `init` must load: %v", err)
	}
	def := Default()
	if c.Endpoint != def.Endpoint || c.IntervalS != def.IntervalS || c.IdleIntervalS != def.IdleIntervalS || c.ActiveWindowS != def.ActiveWindowS || c.Battery != def.Battery {
		t.Errorf("init config differs from defaults: %+v", c)
	}
}

func TestOverridesAndTildeExpansion(t *testing.T) {
	d := sandbox(t)
	p := filepath.Join(d, "c.toml")
	os.WriteFile(p, []byte(`
interval_s = 15
idle_interval_s = 90
private = true
hide_repos = ["Secret-Project"]
sites_file = "~/x/sites.toml"
adult_blocklist_file = "~/x/adult.txt"
`), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.IntervalS != 15 || c.IdleIntervalS != 90 || !c.Private {
		t.Errorf("%+v", c)
	}
	if c.SitesFile != filepath.Join(d, "x/sites.toml") || c.AdultBlocklistFile != filepath.Join(d, "x/adult.txt") {
		t.Errorf("~ not expanded: %q %q", c.SitesFile, c.AdultBlocklistFile)
	}
	if !c.HidesRepo("secret-project") || c.HidesRepo("other") {
		t.Error("hide_repos must match case-insensitively")
	}
}

func TestValidation(t *testing.T) {
	d := sandbox(t)
	bad := map[string]string{
		"http endpoint":   `endpoint = "http://example.com/x"`,
		"tiny interval":   `interval_s = 1`,
		"idle < interval": "interval_s = 60\nidle_interval_s = 30",
		"tiny active":     `active_window_s = 5`,
		"not toml":        `interval_s = [`,
	}
	for name, body := range bad {
		p := filepath.Join(d, "bad.toml")
		os.WriteFile(p, []byte(body), 0o600)
		if _, err := Load(p); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	for _, ok := range []string{`endpoint = "http://127.0.0.1:8080/x"`, `endpoint = "http://localhost:3000/x"`} {
		p := filepath.Join(d, "ok.toml")
		os.WriteFile(p, []byte(ok), 0o600)
		if _, err := Load(p); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
}

func TestKeyHandling(t *testing.T) {
	sandbox(t)
	if _, _, err := Key(); err == nil {
		t.Fatal("no key anywhere must be an error")
	}
	if err := WriteKey("  s3cret-value \n"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(paths.KeyFile())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode: %v %v", st, err)
	}
	if dst, _ := os.Stat(paths.ConfigDir()); dst.Mode().Perm() != 0o700 {
		t.Errorf("config dir mode %v", dst.Mode().Perm())
	}
	k, src, err := Key()
	if err != nil || k != "s3cret-value" || src != paths.KeyFile() {
		t.Errorf("Key() = %q,%q,%v", k, src, err)
	}
	t.Setenv("DESK_AGENT_KEY", "from-env")
	if k, src, _ := Key(); k != "from-env" || src != "DESK_AGENT_KEY" {
		t.Errorf("env override: %q %q", k, src)
	}
	if KeyFileModeProblem() != "" {
		t.Error("0600 must not be flagged")
	}
	os.Chmod(paths.KeyFile(), 0o644)
	if p := KeyFileModeProblem(); !strings.Contains(p, "0600") {
		t.Errorf("0644 must be flagged: %q", p)
	}
	if err := WriteKey("   "); err == nil {
		t.Error("an empty key must be rejected")
	}
}

func TestInitNeverOverwrites(t *testing.T) {
	sandbox(t)
	created, err := Init()
	if err != nil || !created {
		t.Fatalf("first init: %v %v", created, err)
	}
	os.WriteFile(paths.ConfigFile(), []byte("interval_s = 20\n"), 0o644)
	WriteKey("keep-me")
	created, err = Init()
	if err != nil || created {
		t.Fatalf("second init: %v %v", created, err)
	}
	if b, _ := os.ReadFile(paths.ConfigFile()); string(b) != "interval_s = 20\n" {
		t.Error("init overwrote an existing config")
	}
	if k, _, _ := Key(); k != "keep-me" {
		t.Error("init touched the key")
	}
}
