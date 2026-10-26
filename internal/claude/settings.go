package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// hookEvents maps each Claude Code event we use to the hook kind it fires.
// Tool events refresh "working" so long turns stay green; Stop/SessionEnd
// end it. UserPromptSubmit/Stop do not support matchers.
var hookEvents = []struct {
	Event   string
	Kind    string
	Matcher bool
}{
	{"UserPromptSubmit", "start", false},
	{"PreToolUse", "start", true},
	{"PostToolUse", "start", true},
	{"Stop", "stop", false},
	{"SessionEnd", "stop", false},
}

// SettingsPath is ~/.claude/settings.json (or $CLAUDE_CONFIG_DIR/settings.json).
func SettingsPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// object is a JSON object that remembers key order, so merging hooks does not
// reshuffle the rest of the user's settings.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func newObject() *object { return &object{vals: map[string]json.RawMessage{}} }

func parseObject(b []byte) (*object, error) {
	o := newObject()
	dec := json.NewDecoder(bytes.NewReader(b))
	t, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil, errors.New("not a JSON object")
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, err
		}
		k := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		o.set(k, raw)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *object) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, kk := range o.keys {
		if kk == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

func (o *object) marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, buf.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// quote makes a path safe inside a shell command line.
func quote(p string) string {
	if p != "" && !strings.ContainsAny(p, " \t\n'\"\\$`!&;|<>()*?[]{}~#") {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

func hookCommand(bin, kind string) string { return quote(bin) + " hook " + kind }

// isOurs recognises a hook command this tool installed.
func isOurs(cmd string) bool {
	return strings.Contains(cmd, "desk-agent") && (strings.HasSuffix(cmd, " hook start") || strings.HasSuffix(cmd, " hook stop"))
}

func groupHasOurs(raw json.RawMessage) bool {
	var g struct {
		Hooks []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(raw, &g) != nil {
		return false
	}
	for _, h := range g.Hooks {
		if isOurs(h.Command) {
			return true
		}
	}
	return false
}

func readSettings(path string) (*object, bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return newObject(), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return newObject(), true, nil
	}
	o, err := parseObject(b)
	if err != nil {
		return nil, true, fmt.Errorf("%s is not valid JSON, refusing to touch it: %w", path, err)
	}
	return o, true, nil
}

func writeSettings(path string, existed bool, o *object) error {
	out, err := o.marshal()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if existed {
		bak := path + ".desk-agent.bak"
		if _, err := os.Stat(bak); errors.Is(err, os.ErrNotExist) {
			if src, err := os.Open(path); err == nil {
				if dst, err := os.OpenFile(bak, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600); err == nil {
					_, _ = io.Copy(dst, src)
					dst.Close()
				}
				src.Close()
			}
		}
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// InstallHooks merges desk-agent's hooks into settings.json at path without
// touching anything else, and is idempotent. It returns the events it added.
func InstallHooks(path, bin string) ([]string, error) {
	root, existed, err := readSettings(path)
	if err != nil {
		return nil, err
	}
	hooks := newObject()
	if raw, ok := root.vals["hooks"]; ok {
		if hooks, err = parseObject(raw); err != nil {
			return nil, fmt.Errorf("%s: \"hooks\" is not an object: %w", path, err)
		}
	}
	var added []string
	for _, he := range hookEvents {
		var groups []json.RawMessage
		if raw, ok := hooks.vals[he.Event]; ok {
			if err := json.Unmarshal(raw, &groups); err != nil {
				return nil, fmt.Errorf("%s: hooks.%s is not an array: %w", path, he.Event, err)
			}
		}
		have := false
		for _, g := range groups {
			if groupHasOurs(g) {
				have = true
			}
		}
		if have {
			continue
		}
		g := map[string]interface{}{"hooks": []map[string]interface{}{{
			"type": "command", "command": hookCommand(bin, he.Kind), "timeout": 5,
		}}}
		if he.Matcher {
			g["matcher"] = "*"
		}
		gb, _ := json.Marshal(g)
		groups = append(groups, gb)
		ab, _ := json.Marshal(groups)
		hooks.set(he.Event, ab)
		added = append(added, he.Event)
	}
	if len(added) == 0 {
		return nil, nil
	}
	hb, err := hooks.marshal()
	if err != nil {
		return nil, err
	}
	root.set("hooks", json.RawMessage(bytes.TrimSpace(hb)))
	return added, writeSettings(path, existed, root)
}

// UninstallHooks removes only the hooks InstallHooks added.
func UninstallHooks(path string) (int, error) {
	root, existed, err := readSettings(path)
	if err != nil || !existed {
		return 0, err
	}
	raw, ok := root.vals["hooks"]
	if !ok {
		return 0, nil
	}
	hooks, err := parseObject(raw)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, k := range append([]string(nil), hooks.keys...) {
		var groups []json.RawMessage
		if json.Unmarshal(hooks.vals[k], &groups) != nil {
			continue
		}
		var keep []json.RawMessage
		for _, g := range groups {
			if !groupHasOurs(g) {
				keep = append(keep, g)
				continue
			}
			// drop our handlers; keep the group if foreign handlers remain
			var m map[string]json.RawMessage
			if json.Unmarshal(g, &m) != nil {
				keep = append(keep, g)
				continue
			}
			var hs []json.RawMessage
			_ = json.Unmarshal(m["hooks"], &hs)
			var left []json.RawMessage
			for _, h := range hs {
				var c struct {
					Command string `json:"command"`
				}
				_ = json.Unmarshal(h, &c)
				if isOurs(c.Command) {
					removed++
				} else {
					left = append(left, h)
				}
			}
			if len(left) > 0 {
				m["hooks"], _ = json.Marshal(left)
				ng, _ := json.Marshal(m)
				keep = append(keep, ng)
			}
		}
		if len(keep) == 0 {
			hooks.del(k)
		} else {
			hooks.vals[k], _ = json.Marshal(keep)
		}
	}
	if removed == 0 {
		return 0, nil
	}
	if len(hooks.keys) == 0 {
		root.del("hooks")
	} else {
		hb, _ := hooks.marshal()
		root.set("hooks", json.RawMessage(bytes.TrimSpace(hb)))
	}
	return removed, writeSettings(path, existed, root)
}

// HooksInstalled reports which of our events are present (for doctor).
func HooksInstalled(path string) (present, total int, err error) {
	root, existed, err := readSettings(path)
	if err != nil || !existed {
		return 0, len(hookEvents), err
	}
	hooks, err := parseObject(root.vals["hooks"])
	if err != nil {
		return 0, len(hookEvents), nil
	}
	for _, he := range hookEvents {
		var groups []json.RawMessage
		_ = json.Unmarshal(hooks.vals[he.Event], &groups)
		for _, g := range groups {
			if groupHasOurs(g) {
				present++
				break
			}
		}
	}
	return present, len(hookEvents), nil
}
