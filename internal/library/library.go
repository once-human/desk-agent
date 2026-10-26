// Package library lists and edits the user's sites.toml and apps.toml for the
// CLI and the GUI. Edits keep comments and other rows exactly as they were,
// and are validated with the real parsers before anything is written.
package library

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/once-human/desk-agent/internal/adult"
	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/hostname"
	"github.com/once-human/desk-agent/internal/review"
	"github.com/once-human/desk-agent/internal/seenapps"
	"github.com/once-human/desk-agent/internal/tomledit"
)

// SiteRow is one site as the UI shows it.
type SiteRow struct {
	Host     string `json:"host"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Hide     bool   `json:"hide"`
	// Source is "user" (in sites.toml) or "seed" (built in, not overridden).
	Source string `json:"source"`
}

// SiteUpdate holds the fields to change; nil means keep.
type SiteUpdate struct {
	Name     *string `json:"name,omitempty"`
	Category *string `json:"category,omitempty"`
	Hide     *bool   `json:"hide,omitempty"`
}

func readText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// ListSites returns the built-in sites overlaid with the user's, sorted with
// the user's rows first.
func ListSites(path string) ([]SiteRow, error) {
	text, err := readText(path)
	if err != nil {
		return nil, err
	}
	user, err := catalog.ParseSites([]byte(text))
	if err != nil {
		return nil, err
	}
	var rows []SiteRow
	for h, e := range user {
		rows = append(rows, SiteRow{h, nameOr(e.Name, h), e.Category, e.Hide, "user"})
	}
	for h, e := range catalog.SeedSites() {
		if _, over := user[h]; !over {
			rows = append(rows, SiteRow{h, e.Name, e.Category, false, "seed"})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Source != rows[j].Source {
			return rows[i].Source == "user"
		}
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return rows, nil
}

func nameOr(n, host string) string {
	if n != "" {
		return n
	}
	return hostname.Friendly(host)
}

// siteKey normalises user input to a catalog key and refuses adult hosts.
func siteKey(raw string, ad *adult.Filter) (string, error) {
	h, ok := hostname.Normalise(strings.TrimSpace(raw))
	if !ok {
		return "", fmt.Errorf("%q is not a host name (use something like github.com)", raw)
	}
	if ad != nil && (ad.Host(h) || ad.Host(hostname.Registrable(h))) {
		return "", errors.New("that host matches the adult filter: such hosts are never listed")
	}
	return h, nil
}

func writeText(path, text string, parse func([]byte) error) error {
	if err := parse([]byte(text)); err != nil {
		return err
	}
	return review.WriteAtomic(path, []byte(text))
}

func parseSites(b []byte) error { _, err := catalog.ParseSites(b); return err }
func parseApps(b []byte) error  { _, err := catalog.ParseApps(b); return err }

func siteFields(name, category string, hide bool) []tomledit.Field {
	f := []tomledit.Field{{Key: "name", Value: tomledit.Quote(name)}, {Key: "category", Value: tomledit.Quote(category)}}
	if hide {
		f = append(f, tomledit.Field{Key: "hide", Value: "true"})
	}
	return f
}

func applySite(text string, user map[string]catalog.SiteEntry, host string, u SiteUpdate) (string, error) {
	cur, have := user[host]
	if !have {
		if e, _, ok := catalog.Seed(host); ok {
			cur = e
		} else {
			cur = catalog.SiteEntry{Name: hostname.Friendly(host), Category: "other"}
		}
	}
	if u.Name != nil {
		cur.Name = strings.TrimSpace(*u.Name)
	}
	if u.Category != nil {
		cur.Category = *u.Category
	}
	if u.Hide != nil {
		cur.Hide = *u.Hide
	}
	if cur.Name == "" {
		cur.Name = hostname.Friendly(host)
	}
	if !catalog.ValidSiteCategory(cur.Category) {
		return "", fmt.Errorf("unknown category %q (use one of %s)", cur.Category, strings.Join(catalog.SiteCategories, ", "))
	}
	return tomledit.SetTable(text, host, siteFields(cur.Name, cur.Category, cur.Hide)), nil
}

// SetSite creates or updates one site row.
func SetSite(path, rawHost string, u SiteUpdate, ad *adult.Filter) error {
	host, err := siteKey(rawHost, ad)
	if err != nil {
		return err
	}
	text, err := readText(path)
	if err != nil {
		return err
	}
	user, err := catalog.ParseSites([]byte(text))
	if err != nil {
		return fmt.Errorf("your current sites.toml has a problem, fix it first: %w", err)
	}
	out, err := applySite(text, user, host, u)
	if err != nil {
		return err
	}
	return writeText(path, out, parseSites)
}

// RemoveSite deletes the user's row for host (a built-in row simply reappears).
func RemoveSite(path, rawHost string) error {
	host, ok := hostname.Normalise(strings.TrimSpace(rawHost))
	if !ok {
		return fmt.Errorf("%q is not a host name", rawHost)
	}
	text, err := readText(path)
	if err != nil {
		return err
	}
	if _, err := catalog.ParseSites([]byte(text)); err != nil {
		return fmt.Errorf("your current sites.toml has a problem, fix it first: %w", err)
	}
	return writeText(path, tomledit.RemoveTable(text, host), parseSites)
}

// ImportSites adds many rows at once (the GUI's reviewed scan results). Rows
// for adult or invalid hosts are skipped; the count of applied rows is returned.
func ImportSites(path string, rows []SiteRow, ad *adult.Filter) (applied, skipped int, err error) {
	text, err := readText(path)
	if err != nil {
		return 0, 0, err
	}
	user, err := catalog.ParseSites([]byte(text))
	if err != nil {
		return 0, 0, fmt.Errorf("your current sites.toml has a problem, fix it first: %w", err)
	}
	for _, r := range rows {
		host, kerr := siteKey(r.Host, ad)
		if kerr != nil || !catalog.ValidSiteCategory(r.Category) {
			skipped++
			continue
		}
		name, cat, hide := r.Name, r.Category, r.Hide
		next, aerr := applySite(text, user, host, SiteUpdate{Name: &name, Category: &cat, Hide: &hide})
		if aerr != nil {
			skipped++
			continue
		}
		text = next
		user[host] = catalog.SiteEntry{Name: nameOr(name, host), Category: cat, Hide: hide}
		applied++
	}
	if applied == 0 {
		return 0, skipped, nil
	}
	return applied, skipped, writeText(path, text, parseSites)
}

// ---- apps ----

// AppRow is one app (window class) as the UI shows it.
type AppRow struct {
	Class    string `json:"class"`
	Name     string `json:"name"`
	Category string `json:"category"`
	// Source is "user", "seed", or "seen" (focused but in neither list).
	Source string `json:"source"`
	// Count is how often the class was focused, from the local seen-apps record.
	Count int `json:"count"`
}

// AppUpdate holds the fields to change; nil means keep.
type AppUpdate struct {
	Name     *string `json:"name,omitempty"`
	Category *string `json:"category,omitempty"`
}

func validAppCategory(c string) bool {
	for _, k := range catalog.AppCategories {
		if k == c {
			return true
		}
	}
	return false
}

// ListApps merges the built-in apps, the user's apps.toml and the classes
// that have actually been focused, most used first.
func ListApps(path, seenPath string) ([]AppRow, error) {
	text, err := readText(path)
	if err != nil {
		return nil, err
	}
	user, err := catalog.ParseApps([]byte(text))
	if err != nil {
		return nil, err
	}
	seen, err := seenapps.Read(seenPath)
	if err != nil {
		seen = map[string]seenapps.Seen{}
	}
	rows := map[string]*AppRow{}
	for c, e := range catalog.SeedApps() {
		rows[c] = &AppRow{Class: c, Name: e.Name, Category: e.Category, Source: "seed"}
	}
	for c, e := range user {
		n := e.Name
		if n == "" {
			n = catalog.Prettify(c)
		}
		rows[c] = &AppRow{Class: c, Name: n, Category: e.Category, Source: "user"}
	}
	for c, s := range seen {
		k := strings.ToLower(c)
		if r, ok := rows[k]; ok {
			r.Count += s.Count
		} else {
			rows[k] = &AppRow{Class: k, Name: catalog.Prettify(k), Category: "other", Source: "seen", Count: s.Count}
		}
	}
	out := make([]AppRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func applyApp(text string, user map[string]catalog.Entry, class string, u AppUpdate) (string, error) {
	cur, have := user[class]
	if !have {
		if e, ok := catalog.SeedApps()[class]; ok {
			cur = e
		} else {
			cur = catalog.Entry{Name: catalog.Prettify(class), Category: "other"}
		}
	}
	if u.Name != nil {
		cur.Name = strings.TrimSpace(*u.Name)
	}
	if u.Category != nil {
		cur.Category = *u.Category
	}
	if cur.Name == "" {
		cur.Name = catalog.Prettify(class)
	}
	if !validAppCategory(cur.Category) {
		return "", fmt.Errorf("unknown category %q (use one of %s)", cur.Category, strings.Join(catalog.AppCategories, ", "))
	}
	return tomledit.SetTable(text, class, []tomledit.Field{
		{Key: "name", Value: tomledit.Quote(cur.Name)}, {Key: "category", Value: tomledit.Quote(cur.Category)},
	}), nil
}

func appKey(raw string) (string, error) {
	k := strings.ToLower(strings.TrimSpace(raw))
	if k == "" || strings.ContainsAny(k, "/ ") {
		return "", fmt.Errorf("%q is not a window class", raw)
	}
	return k, nil
}

// SetApp creates or updates one app row.
func SetApp(path, rawClass string, u AppUpdate) error {
	class, err := appKey(rawClass)
	if err != nil {
		return err
	}
	text, err := readText(path)
	if err != nil {
		return err
	}
	user, err := catalog.ParseApps([]byte(text))
	if err != nil {
		return fmt.Errorf("your current apps.toml has a problem, fix it first: %w", err)
	}
	out, err := applyApp(text, user, class, u)
	if err != nil {
		return err
	}
	return writeText(path, out, parseApps)
}

// RemoveApp deletes the user's row for class.
func RemoveApp(path, rawClass string) error {
	class, err := appKey(rawClass)
	if err != nil {
		return err
	}
	text, err := readText(path)
	if err != nil {
		return err
	}
	if _, err := catalog.ParseApps([]byte(text)); err != nil {
		return fmt.Errorf("your current apps.toml has a problem, fix it first: %w", err)
	}
	return writeText(path, tomledit.RemoveTable(text, class), parseApps)
}

// ImportApps adds many rows at once.
func ImportApps(path string, rows []AppRow) (applied, skipped int, err error) {
	text, err := readText(path)
	if err != nil {
		return 0, 0, err
	}
	user, err := catalog.ParseApps([]byte(text))
	if err != nil {
		return 0, 0, fmt.Errorf("your current apps.toml has a problem, fix it first: %w", err)
	}
	for _, r := range rows {
		class, kerr := appKey(r.Class)
		if kerr != nil || !validAppCategory(r.Category) {
			skipped++
			continue
		}
		name, cat := r.Name, r.Category
		next, aerr := applyApp(text, user, class, AppUpdate{Name: &name, Category: &cat})
		if aerr != nil {
			skipped++
			continue
		}
		text = next
		user[class] = catalog.Entry{Name: name, Category: cat}
		applied++
	}
	if applied == 0 {
		return 0, skipped, nil
	}
	return applied, skipped, writeText(path, text, parseApps)
}
