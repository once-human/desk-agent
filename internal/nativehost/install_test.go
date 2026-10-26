package nativehost

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifest(t *testing.T) {
	data, err := Manifest("chromium", "/home/u/.local/share/desk-agent/native-host")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["name"] != "desk_agent" || m["type"] != "stdio" {
		t.Errorf("bad manifest: %v", m)
	}
	if o := m["allowed_origins"].([]any); len(o) != 1 || o[0] != "chrome-extension://"+ChromiumExtensionID+"/" {
		t.Errorf("origins: %v", o)
	}
	if _, has := m["allowed_extensions"]; has {
		t.Error("chromium manifest must not carry allowed_extensions")
	}

	data, _ = Manifest("firefox", "/x/native-host")
	json.Unmarshal(data, &m)
	if e := m["allowed_extensions"].([]any); len(e) != 1 || e[0] != GeckoExtensionID {
		t.Errorf("extensions: %v", e)
	}

	if _, err := Manifest("firefox", "relative/path"); err == nil {
		t.Error("relative host path must be rejected")
	}
	if _, err := Manifest("safari", "/x"); err == nil {
		t.Error("unknown family must be rejected")
	}
}

// The pinned Chromium id must match the key in the extension manifest,
// otherwise the browser would refuse to talk to the host.
func TestChromiumIDMatchesPinnedKey(t *testing.T) {
	raw, err := os.ReadFile("../../extension/manifest.chromium.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &m); err != nil || m.Key == "" {
		t.Fatalf("no key in manifest: %v", err)
	}
	if got := ChromiumIDFromKey(m.Key); got != ChromiumExtensionID {
		t.Fatalf("manifest key hashes to %s but ChromiumExtensionID is %s", got, ChromiumExtensionID)
	}
}

func TestFirefoxIDMatchesManifest(t *testing.T) {
	raw, err := os.ReadFile("../../extension/manifest.firefox.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"`+GeckoExtensionID+`"`) {
		t.Fatalf("firefox manifest does not contain %s", GeckoExtensionID)
	}
}

func TestInstallOnlyForInstalledBrowsersAndRemove(t *testing.T) {
	home := t.TempDir()
	data := filepath.Join(home, ".local/share/desk-agent")
	os.MkdirAll(filepath.Join(home, ".mozilla"), 0o755)
	os.MkdirAll(filepath.Join(home, ".config/chromium"), 0o755)

	written, err := Install(home, data, "/usr/local/bin/desk-agent", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 {
		t.Fatalf("wrote %v, want firefox + chromium only", written)
	}
	for _, p := range written {
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			t.Errorf("%s missing", p)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config/google-chrome")); err == nil {
		t.Error("created a directory for an uninstalled browser")
	}
	// idempotent
	if w2, err := Install(home, data, "/usr/local/bin/desk-agent", false); err != nil || len(w2) != 2 {
		t.Fatalf("second install: %v %v", w2, err)
	}

	// The wrapper must exec the binary with the native-host subcommand and
	// pass the browser's arguments through.
	wrapper := filepath.Join(data, "native-host")
	fake := filepath.Join(t.TempDir(), "it's a binary")
	os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\"\n"), 0o755)
	if _, err := WriteWrapper(data, fake); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(wrapper, "chrome-extension://abc/").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "native-host chrome-extension://abc/" {
		t.Fatalf("wrapper output %q, %v", out, err)
	}
	if st, _ := os.Stat(wrapper); st.Mode().Perm() != 0o755 {
		t.Errorf("wrapper mode %v", st.Mode().Perm())
	}

	removed, _ := Remove(home, data)
	if len(removed) != 3 {
		t.Fatalf("removed %v", removed)
	}
}

func TestInstallAllCreatesEveryDir(t *testing.T) {
	home := t.TempDir()
	written, err := Install(home, filepath.Join(home, "d"), "/bin/true", true)
	if err != nil || len(written) != len(Targets(home)) {
		t.Fatalf("%v %v", written, err)
	}
}
