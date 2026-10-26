package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)

func TestParseHookInputIgnoresPromptAndSurvivesGarbage(t *testing.T) {
	in := ParseHookInput(strings.NewReader(`{"session_id":"s1","cwd":"/work/x","hook_event_name":"UserPromptSubmit","prompt":"MY SECRET PROMPT","transcript_path":"/t"}`))
	if in.SessionID != "s1" || in.CWD != "/work/x" {
		t.Fatalf("%+v", in)
	}
	b, _ := json.Marshal(in)
	if strings.Contains(string(b), "SECRET") {
		t.Fatal("prompt leaked into HookInput")
	}
	if got := ParseHookInput(strings.NewReader("not json")); got != (HookInput{}) {
		t.Fatalf("%+v", got)
	}
}

func TestStartStopAndNinetySecondRule(t *testing.T) {
	var st State
	st.Apply("start", HookInput{SessionID: "a", CWD: "/w/one"}, t0)
	if w, cwd, _ := st.Evaluate(t0.Add(30 * time.Second)); !w || cwd != "/w/one" {
		t.Fatal("should be working", w, cwd)
	}
	if w, _, _ := st.Evaluate(t0.Add(91 * time.Second)); w {
		t.Fatal("stale start must expire after 90s")
	}
	st.Apply("start", HookInput{SessionID: "a"}, t0.Add(100*time.Second))
	st.Apply("stop", HookInput{SessionID: "a", HookEventName: "Stop"}, t0.Add(110*time.Second))
	if w, _, _ := st.Evaluate(t0.Add(111 * time.Second)); w {
		t.Fatal("stop must end working")
	}
}

func TestMultipleSessionsWorkingIfAny(t *testing.T) {
	var st State
	st.Apply("start", HookInput{SessionID: "a", CWD: "/w/a"}, t0)
	st.Apply("start", HookInput{SessionID: "b", CWD: "/w/b"}, t0.Add(10*time.Second))
	st.Apply("stop", HookInput{SessionID: "b", HookEventName: "Stop"}, t0.Add(20*time.Second))
	w, cwd, _ := st.Evaluate(t0.Add(30 * time.Second))
	if !w || cwd != "/w/a" {
		t.Fatalf("session a is still working: %v %q", w, cwd)
	}
	st.Apply("stop", HookInput{SessionID: "a", HookEventName: "SessionEnd"}, t0.Add(40*time.Second))
	if w, _, _ := st.Evaluate(t0.Add(41 * time.Second)); w {
		t.Fatal("nothing should be working")
	}
	if _, ok := st.Sessions["a"]; ok {
		t.Fatal("SessionEnd must delete the session")
	}
	if _, ok := st.Sessions["b"]; !ok {
		t.Fatal("a plain Stop keeps the session (just not working)")
	}
}

func TestRecordIsAtomicAndPrivate(t *testing.T) {
	p := filepath.Join(t.TempDir(), "d", "claude.json")
	if err := Record(p, "start", HookInput{SessionID: "x", CWD: "/w/x"}, t0); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = Record(p, "start", HookInput{SessionID: string(rune('a' + i)), CWD: "/w"}, t0)
		}(i)
	}
	wg.Wait()
	if n := len(Read(p).Sessions); n != 21 {
		t.Fatalf("lost updates under concurrency: %d sessions", n)
	}
}

func TestStaleSessionsArePruned(t *testing.T) {
	var st State
	st.Apply("start", HookInput{SessionID: "old"}, t0)
	st.Apply("start", HookInput{SessionID: "new"}, t0.Add(25*time.Hour))
	if _, ok := st.Sessions["old"]; ok {
		t.Fatal("old session should be pruned")
	}
}

func TestWatcherFiresOnWriteAndOnExpiry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "claude.json")
	var mu sync.Mutex
	var seen []bool
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	w := &Watcher{Path: p, Now: clock, OnChange: func(working bool, _ string) {
		mu.Lock()
		seen = append(seen, working)
		mu.Unlock()
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	time.Sleep(150 * time.Millisecond)
	_ = Record(p, "start", HookInput{SessionID: "s", CWD: "/w"}, clock())
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	if len(seen) < 2 || !seen[len(seen)-1] {
		t.Fatalf("expected a working=true notification, got %v", seen)
	}
	mu.Unlock()
	_ = Record(p, "stop", HookInput{SessionID: "s", HookEventName: "Stop"}, clock())
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if seen[len(seen)-1] {
		t.Fatalf("expected working=false after stop, got %v", seen)
	}
}

// ---- settings.json merge ----

const userSettings = `{
  "model": "opus",
  "permissions": {"allow": ["Bash(ls)"]},
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/audit"}]}
    ]
  },
  "env": {"A": "1"}
}`

func TestInstallHooksMergesWithoutDisturbingUserSettings(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".claude", "settings.json")
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, []byte(userSettings), 0o644)

	added, err := InstallHooks(p, "/home/o/.local/bin/desk-agent")
	if err != nil || len(added) != 5 {
		t.Fatalf("%v %v", added, err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	// order of user keys is preserved
	if !(strings.Index(s, `"model"`) < strings.Index(s, `"permissions"`) && strings.Index(s, `"permissions"`) < strings.Index(s, `"hooks"`) && strings.Index(s, `"hooks"`) < strings.Index(s, `"env"`)) {
		t.Fatalf("key order changed:\n%s", s)
	}
	for _, want := range []string{"/usr/local/bin/audit", `"Bash(ls)"`, "/home/o/.local/bin/desk-agent hook start", "/home/o/.local/bin/desk-agent hook stop"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	var v map[string]interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal("result is not valid JSON:", err)
	}
	if _, err := os.Stat(p + ".desk-agent.bak"); err != nil {
		t.Fatal("expected a one-time backup")
	}
	// idempotent
	if added, err = InstallHooks(p, "/home/o/.local/bin/desk-agent"); err != nil || len(added) != 0 {
		t.Fatalf("second install should add nothing: %v %v", added, err)
	}
	if n, total, _ := HooksInstalled(p); n != 5 || total != 5 {
		t.Fatalf("%d/%d", n, total)
	}
}

func TestUninstallRemovesOnlyOurHooks(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(p, []byte(userSettings), 0o644)
	_, _ = InstallHooks(p, "/bin/desk-agent")
	n, err := UninstallHooks(p)
	if err != nil || n != 5 {
		t.Fatalf("%d %v", n, err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if strings.Contains(s, "desk-agent") || !strings.Contains(s, "/usr/local/bin/audit") || !strings.Contains(s, `"env"`) {
		t.Fatalf("uninstall damaged settings:\n%s", s)
	}
}

func TestInstallCreatesMissingFileAndRefusesInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "new", "settings.json")
	if _, err := InstallHooks(p, "/bin/desk-agent"); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`{ "hooks": oops`), 0o644)
	if _, err := InstallHooks(bad, "/bin/desk-agent"); err == nil {
		t.Fatal("must refuse to rewrite invalid JSON")
	}
	if b, _ := os.ReadFile(bad); string(b) != `{ "hooks": oops` {
		t.Fatal("invalid file was modified")
	}
}

func TestQuotePathsWithSpaces(t *testing.T) {
	if got := hookCommand("/home/my user/desk-agent", "start"); got != `'/home/my user/desk-agent' hook start` {
		t.Fatal(got)
	}
	if !isOurs(hookCommand("/home/my user/desk-agent", "stop")) {
		t.Fatal("quoted command must still be recognised")
	}
}
