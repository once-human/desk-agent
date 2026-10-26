package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/once-human/desk-agent/internal/adult"
	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/paths"
	"github.com/once-human/desk-agent/internal/review"
	"github.com/once-human/desk-agent/internal/scan"
	"github.com/once-human/desk-agent/internal/seenapps"
)

func readOptional(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// cmdScanHistory reads browser history (from copies), proposes sites.toml rows
// and opens the file for review. It prints counts only: no URL, title or host
// ever goes to the terminal or a log.
func cmdScanHistory(args []string) error {
	fs := flag.NewFlagSet("scan-history", flag.ContinueOnError)
	top := fs.Int("top", 200, "how many of the most used hosts to propose")
	days := fs.Int("days", 180, "how far back to look")
	only := fs.String("browser", "", "only this browser (e.g. firefox, chromium, brave)")
	db := fs.String("db", "", "read this history database instead of discovering profiles")
	kind := fs.String("kind", "", "with --db: firefox or chromium")
	noEdit := fs.Bool("no-edit", false, "write the proposal without the review step (guesses stay unreviewed)")
	dry := fs.Bool("dry-run", false, "show counts and write nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return err
	}
	ad, err := adult.Load(cfg.AdultBlocklistFile)
	if err != nil {
		return fmt.Errorf("adult blocklist: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	var srcs []scan.Source
	if *db != "" {
		if *kind != "firefox" && *kind != "chromium" {
			return errors.New("--db needs --kind firefox|chromium")
		}
		srcs = []scan.Source{{Browser: *kind, Kind: *kind, Path: *db}}
	} else {
		for _, s := range scan.Discover(home) {
			if *only == "" || strings.Contains(strings.ToLower(s.Browser), strings.ToLower(*only)) {
				srcs = append(srcs, s)
			}
		}
	}
	if len(srcs) == 0 {
		return errors.New("no browser history found (looked for Firefox-family and Chromium-family profiles under your home directory; use --db and --kind to point at one)")
	}

	since := time.Now().AddDate(0, 0, -*days)
	merged := map[string]scan.HostCount{}
	read := 0
	for _, s := range srcs {
		counts, err := scan.Counts(s, since)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping one %s profile: %v\n", s.Browser, err)
			continue
		}
		read++
		for h, c := range counts {
			m := merged[h]
			m.Visits += c.Visits
			m.Seconds += c.Seconds
			merged[h] = m
		}
	}
	if read == 0 {
		return errors.New("could not read any history database")
	}

	existingText, err := readOptional(cfg.SitesFile)
	if err != nil {
		return err
	}
	existing, err := catalog.ParseSites([]byte(existingText))
	if err != nil {
		return fmt.Errorf("your current %s has a problem, fix it first: %w", cfg.SitesFile, err)
	}
	props, adultDropped := scan.Propose(merged, existing, ad, *top)

	guesses := 0
	for _, p := range props {
		if p.Source != "seed" {
			guesses++
		}
	}
	fmt.Printf("Read %d history database(s), last %d days.\n", read, *days)
	fmt.Printf("%d distinct hosts; %d proposed (%d from the built-in seed, %d guessed); %d adult host(s) excluded.\n",
		len(merged), len(props), len(props)-guesses, guesses, adultDropped)
	if *dry {
		return nil
	}
	if len(props) == 0 {
		fmt.Println("Nothing new to add.")
		return nil
	}

	out := scan.RenderSites(existingText, props, time.Now())
	validate := func(b []byte) error {
		m, err := catalog.ParseSites(b)
		if err != nil {
			return err
		}
		for k := range m {
			if ad.Host(k) {
				return fmt.Errorf("%q matches the adult filter: such hosts are never listed", k)
			}
		}
		return nil
	}
	if err := review.Edit(cfg.SitesFile, out, validate, review.Options{NoEdit: *noEdit}); err != nil {
		return err
	}
	fmt.Println("Saved", cfg.SitesFile)
	if *noEdit {
		fmt.Println("Note: --no-edit skipped the review. Rows marked GUESS/UNKNOWN are unreviewed.")
	}
	fmt.Println("Restart the agent to apply it:  systemctl --user restart desk-agent")
	return nil
}

// cmdScanApps proposes apps.toml rows for window classes that were actually
// focused, using installed .desktop files, then opens the file for review.
func cmdScanApps(args []string) error {
	fs := flag.NewFlagSet("scan-apps", flag.ContinueOnError)
	noEdit := fs.Bool("no-edit", false, "write the proposal without the review step")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return err
	}
	seen, err := seenapps.Read(paths.SeenApps())
	if err != nil {
		return err
	}
	if len(seen) == 0 {
		return errors.New("no focused apps recorded yet: let the agent run for a while (desk-agent run), then try again")
	}
	counts := make(map[string]int, len(seen))
	for k, v := range seen {
		counts[k] = v.Count
	}
	known, err := catalog.LoadApps(cfg.AppsFile)
	if err != nil {
		return fmt.Errorf("your current %s has a problem, fix it first: %w", cfg.AppsFile, err)
	}
	existingText, err := readOptional(cfg.AppsFile)
	if err != nil {
		return err
	}
	existing, err := catalog.ParseApps([]byte(existingText))
	if err != nil {
		return fmt.Errorf("your current %s has a problem, fix it first: %w", cfg.AppsFile, err)
	}
	home, _ := os.UserHomeDir()
	props := scan.ProposeApps(counts, known, existing, scan.LoadDesktops(scan.DesktopDirs(home)))
	fmt.Printf("%d focused app(s) recorded; %d new to propose.\n", len(counts), len(props))
	if len(props) == 0 {
		fmt.Println("Nothing new to add.")
		return nil
	}
	out := scan.RenderApps(existingText, props, time.Now())
	validate := func(b []byte) error { _, err := catalog.ParseApps(b); return err }
	if err := review.Edit(cfg.AppsFile, out, validate, review.Options{NoEdit: *noEdit}); err != nil {
		return err
	}
	fmt.Println("Saved", cfg.AppsFile)
	fmt.Println("Restart the agent to apply it:  systemctl --user restart desk-agent")
	return nil
}
