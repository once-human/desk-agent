package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/once-human/desk-agent/internal/tomledit"
)

// Kind is the type of a config key.
type Kind int

const (
	KindString Kind = iota
	KindInt
	KindBool
	KindList
)

// Keys are the settable config keys. "endpoint" is deliberately settable only
// by hand-editing the file: a typo there would silently redirect where your
// activity is sent.
var Keys = map[string]Kind{
	"interval_s":           KindInt,
	"idle_interval_s":      KindInt,
	"upload_every_s":       KindInt,
	"active_window_s":      KindInt,
	"private":              KindBool,
	"battery":              KindBool,
	"hide_repos":           KindList,
	"sites_file":           KindString,
	"apps_file":            KindString,
	"adult_blocklist_file": KindString,
}

// KeyNames returns the settable keys, sorted.
func KeyNames() []string {
	out := make([]string, 0, len(Keys))
	for k := range Keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// FormatValue encodes command-line arguments as a TOML value for key.
func FormatValue(key string, args []string) (string, error) {
	kind, ok := Keys[key]
	if !ok {
		return "", fmt.Errorf("unknown or read-only key %q (settable: %s)", key, strings.Join(KeyNames(), ", "))
	}
	switch kind {
	case KindList:
		items := make([]string, 0, len(args))
		for _, a := range args {
			if strings.TrimSpace(a) != "" {
				items = append(items, tomledit.Quote(strings.TrimSpace(a)))
			}
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	}
	if len(args) != 1 {
		return "", fmt.Errorf("%s takes exactly one value", key)
	}
	v := strings.TrimSpace(args[0])
	switch kind {
	case KindInt:
		if _, err := strconv.Atoi(v); err != nil {
			return "", fmt.Errorf("%s must be a whole number, got %q", key, v)
		}
		return v, nil
	case KindBool:
		b, err := strconv.ParseBool(v)
		if err != nil {
			return "", fmt.Errorf("%s must be true or false, got %q", key, v)
		}
		return strconv.FormatBool(b), nil
	}
	return tomledit.Quote(v), nil
}

// ValidateText checks that src parses as a config and passes Validate.
func ValidateText(src string) error {
	c := Default()
	if _, err := toml.Decode(src, &c); err != nil {
		return err
	}
	c.fillPaths()
	return c.Validate()
}

// ApplyEdit returns src with key set from args, after checking the result
// still loads. It never returns invalid text.
func ApplyEdit(src, key string, args []string) (string, error) {
	val, err := FormatValue(key, args)
	if err != nil {
		return "", err
	}
	out := tomledit.SetKey(src, key, val)
	if err := ValidateText(out); err != nil {
		return "", fmt.Errorf("that value would make config.toml invalid: %w", err)
	}
	return out, nil
}

// Set edits the config file at path in place, keeping its comments. A missing
// file starts from the default config.
func Set(path, key string, args []string) error {
	data, err := os.ReadFile(path)
	src := string(data)
	if errors.Is(err, fs.ErrNotExist) {
		src = DefaultTOML
	} else if err != nil {
		return err
	}
	out, err := ApplyEdit(src, key, args)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Map is the effective configuration keyed by its TOML names, for `config
// show --json`. The site key is never part of it.
func (c Config) Map() map[string]any {
	hide := c.HideRepos
	if hide == nil {
		hide = []string{}
	}
	return map[string]any{
		"endpoint": c.Endpoint, "interval_s": c.IntervalS, "idle_interval_s": c.IdleIntervalS,
		"active_window_s": c.ActiveWindowS, "private": c.Private, "battery": c.Battery,
		"hide_repos": hide, "sites_file": c.SitesFile, "apps_file": c.AppsFile,
		"adult_blocklist_file": c.AdultBlocklistFile,
	}
}
