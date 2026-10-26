package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		"unknown push":    `push_mode = "turbo"`,
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

func TestApplyEdit(t *testing.T) {
	out, err := ApplyEdit(DefaultTOML, "private", []string{"true"})
	if err != nil || !strings.Contains(out, "\nprivate = true\n") {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "# desk-agent configuration") {
		t.Error("comments must survive an edit")
	}
	if err := ValidateText(out); err != nil {
		t.Errorf("edited config invalid: %v", err)
	}
	out, err = ApplyEdit(out, "hide_repos", []string{"secret one", "other"})
	if err != nil || !strings.Contains(out, `hide_repos = ["secret one", "other"]`) {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err = ApplyEdit(out, "hide_repos", nil); err != nil || !strings.Contains(out, "hide_repos = []") {
		t.Fatalf("clearing a list: %v\n%s", err, out)
	}
	bad := []struct {
		key  string
		args []string
	}{
		{"interval_s", []string{"1"}},       // below the minimum
		{"interval_s", []string{"abc"}},     // not a number
		{"private", []string{"maybe"}},      // not a bool
		{"endpoint", []string{"https://x"}}, // not settable
		{"nope", []string{"x"}},
		{"interval_s", []string{"1", "2"}},
		{"idle_interval_s", []string{"10"}}, // below interval_s (30)
	}
	for _, b := range bad {
		if _, err := ApplyEdit(DefaultTOML, b.key, b.args); err == nil {
			t.Errorf("%s %v should be rejected", b.key, b.args)
		}
	}
}

func TestSetWritesAtomicallyAndCreatesFromDefault(t *testing.T) {
	sandbox(t)
	if err := Set(paths.ConfigFile(), "battery", []string{"false"}); err != nil {
		t.Fatal(err)
	}
	c, err := Load(paths.ConfigFile())
	if err != nil || c.Battery {
		t.Fatalf("%+v %v", c, err)
	}
	if err := Set(paths.ConfigFile(), "interval_s", []string{"2"}); err == nil {
		t.Error("an invalid value must not be written")
	}
	if c2, _ := Load(paths.ConfigFile()); c2.IntervalS != 30 {
		t.Error("the file changed despite the error")
	}
	m := c.Map()
	if _, leaked := m["key"]; leaked || m["hide_repos"] == nil {
		t.Errorf("Map: %v", m)
	}
}

func TestPushPolicy(t *testing.T) {
	d := sandbox(t)
	cases := []struct {
		mode  string
		every time.Duration
		live  bool
	}{
		{PushRealtime, 0, false},
		{PushDefault, 15 * time.Minute, true},
		{Push15, 15 * time.Minute, false},
		{Push30, 30 * time.Minute, false},
	}
	for _, c := range cases {
		p := filepath.Join(d, "c.toml")
		os.WriteFile(p, []byte(`push_mode = "`+c.mode+`"`), 0o600)
		cfg, err := Load(p)
		if err != nil {
			t.Fatalf("%s: %v", c.mode, err)
		}
		every, live := cfg.PushPolicy()
		if every != c.every || live != c.live {
			t.Errorf("%s: got %v/%v, want %v/%v", c.mode, every, live, c.every, c.live)
		}
	}
	// A config written before push_mode existed behaves as default, and the
	// mode is settable and reported.
	def, _ := Load(filepath.Join(d, "missing.toml"))
	if def.PushMode != PushDefault {
		t.Errorf("missing config: push_mode = %q", def.PushMode)
	}
	if _, err := ApplyEdit(DefaultTOML, "push_mode", []string{"30min"}); err != nil {
		t.Errorf("setting push_mode: %v", err)
	}
	if _, err := ApplyEdit(DefaultTOML, "push_mode", []string{"turbo"}); err == nil {
		t.Error("an unknown push_mode was accepted")
	}
	m := def.Map()
	if m["push_mode"] != PushDefault || m["upload_every_s"] != 900 {
		t.Errorf("Map() = %v", m)
	}
}
