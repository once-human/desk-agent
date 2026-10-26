// Package scan builds the proposed sites.toml (from browser history) and
// apps.toml (from .desktop files). Privacy rules, enforced here:
//
//   - history databases are read from a private copy, never in place;
//   - Firefox is queried through moz_places.rev_host, so no URL or title is
//     even selected; Chromium has no host column, so URLs are selected but
//     reduced to a host immediately and never stored, returned or logged;
//   - only host names (and counts) leave this package, and they stay on the
//     machine;
//   - adult hosts are dropped before anything is counted or listed.
package scan

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: keeps the binary CGO-free

	"github.com/once-human/desk-agent/internal/hostname"
)

// Source is one browser profile's history database.
type Source struct {
	Browser string // display name
	Kind    string // "firefox" or "chromium"
	Path    string
}

// Discover finds history databases under home.
func Discover(home string) []Source {
	var out []Source
	add := func(browser, kind string, patterns ...string) {
		for _, p := range patterns {
			matches, _ := filepath.Glob(p)
			sort.Strings(matches)
			for _, m := range matches {
				if st, err := os.Stat(m); err == nil && !st.IsDir() {
					out = append(out, Source{browser, kind, m})
				}
			}
		}
	}
	h := func(rel ...string) string { return filepath.Join(append([]string{home}, rel...)...) }
	add("Firefox", "firefox",
		h(".mozilla/firefox/*/places.sqlite"),
		h("snap/firefox/common/.mozilla/firefox/*/places.sqlite"),
		h(".var/app/org.mozilla.firefox/.mozilla/firefox/*/places.sqlite"))
	add("Zen", "firefox", h(".zen/*/places.sqlite"), h(".var/app/app.zen_browser.zen/.zen/*/places.sqlite"))
	add("LibreWolf", "firefox", h(".librewolf/*/places.sqlite"), h(".var/app/io.gitlab.librewolf-community/.librewolf/*/places.sqlite"))
	add("Floorp", "firefox", h(".floorp/*/places.sqlite"))
	add("Waterfox", "firefox", h(".waterfox/*/places.sqlite"))
	for _, c := range []struct{ name, dir string }{
		{"Chromium", ".config/chromium"},
		{"Google Chrome", ".config/google-chrome"},
		{"Brave", ".config/BraveSoftware/Brave-Browser"},
		{"Vivaldi", ".config/vivaldi"},
		{"Microsoft Edge", ".config/microsoft-edge"},
		{"Thorium", ".config/thorium"},
		{"Chromium (flatpak)", ".var/app/org.chromium.Chromium/config/chromium"},
	} {
		add(c.name, "chromium", h(c.dir, "Default", "History"), h(c.dir, "Profile *", "History"))
	}
	return out
}

// HostCount is how often a host was visited and (Chromium only) how long it
// was open.
type HostCount struct {
	Visits  int
	Seconds float64
}

// Score ranks hosts: a visit is worth about a minute, plus recorded time.
func (c HostCount) Score() float64 { return float64(c.Visits) + c.Seconds/60 }

// Counts reads src (from a private copy) and returns normalised-host ->
// counts for visits since the given time. It returns only host names.
func Counts(src Source, since time.Time) (map[string]HostCount, error) {
	dir, err := os.MkdirTemp("", "desk-agent-scan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	cp := filepath.Join(dir, "db")
	if err := copyDB(src.Path, cp); err != nil {
		return nil, fmt.Errorf("copy %s: %w", src.Browser, err)
	}
	db, err := sql.Open("sqlite", "file:"+cp+"?_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	out := map[string]HostCount{}
	add := func(host string, visits int, secs float64) {
		h, ok := hostname.Normalise(host)
		if !ok {
			return
		}
		c := out[h]
		c.Visits += visits
		c.Seconds += secs
		out[h] = c
	}
	switch src.Kind {
	case "firefox":
		// rev_host only: no URL, no title.
		rows, err := db.Query(`SELECT p.rev_host, COUNT(*)
			FROM moz_historyvisits v JOIN moz_places p ON p.id = v.place_id
			WHERE v.visit_date >= ? AND p.rev_host IS NOT NULL AND p.rev_host != '' AND p.rev_host != '.'
			GROUP BY p.rev_host`, since.UnixMicro())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src.Browser, err)
		}
		defer rows.Close()
		for rows.Next() {
			var rev string
			var n int
			if err := rows.Scan(&rev, &n); err != nil {
				return nil, err
			}
			add(hostname.FromRevHost(rev), n, 0)
		}
		return out, rows.Err()
	case "chromium":
		// Chromium stores no host column. The URL is read into a local
		// variable, reduced to a host at once, and dropped.
		const epochDelta = 11644473600 // seconds from 1601 to 1970
		cutoff := (since.Unix() + epochDelta) * 1_000_000
		rows, err := db.Query(`SELECT u.url, COUNT(*), COALESCE(SUM(v.visit_duration), 0)
			FROM visits v JOIN urls u ON u.id = v.url
			WHERE v.visit_time >= ?
			GROUP BY u.id`, cutoff)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src.Browser, err)
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			var n int
			var dur int64
			if err := rows.Scan(&raw, &n, &dur); err != nil {
				return nil, err
			}
			if h, ok := hostname.FromURL(raw); ok {
				add(h, n, float64(dur)/1e6)
			}
			raw = ""
		}
		return out, rows.Err()
	}
	return nil, fmt.Errorf("unknown history kind %q", src.Kind)
}

// copyDB copies the database and its WAL/SHM side files so the browser's lock
// and recent, not-yet-checkpointed visits are both handled.
func copyDB(src, dst string) error {
	if err := copyFile(src, dst); err != nil {
		return err
	}
	for _, sfx := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(src + sfx); err == nil {
			if err := copyFile(src+sfx, dst+sfx); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
