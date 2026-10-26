package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/once-human/desk-agent/internal/adult"
	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/control"
	"github.com/once-human/desk-agent/internal/library"
	"github.com/once-human/desk-agent/internal/paths"
)

// notifyReload tells a running agent to pick up the change now, and says so.
func notifyReload() {
	reply, err := control.Notify("reload")
	switch {
	case err != nil:
		fmt.Println("Saved. The agent is not running; it will use this when it starts.")
	case strings.HasPrefix(reply, "error"):
		fmt.Println("Saved, but the running agent rejected it and kept the old settings:", strings.TrimSpace(strings.TrimPrefix(reply, "error")))
	default:
		fmt.Println("Applied to the running agent.")
	}
}

func loadForEdit() (config.Config, *adult.Filter, error) {
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return cfg, nil, err
	}
	ad, err := adult.Load(cfg.AdultBlocklistFile)
	if err != nil {
		return cfg, nil, fmt.Errorf("adult blocklist: %w", err)
	}
	return cfg, ad, nil
}

func printJSON(v any) error { return json.NewEncoder(os.Stdout).Encode(v) }

// parseFlags lets flags appear after the positional argument, as in
// `sites set github.com --category code`.
func parseFlags(fs *flag.FlagSet, args []string) (rest []string, err error) {
	for {
		if err = fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return rest, nil
		}
		rest = append(rest, args[0])
		args = args[1:]
	}
}

func optStr(set map[string]bool, name, v string) *string {
	if set[name] {
		return &v
	}
	return nil
}

func visited(fs *flag.FlagSet) map[string]bool {
	m := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { m[f.Name] = true })
	return m
}

// cmdSites: sites list|set|remove|import
func cmdSites(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: desk-agent sites list [--json] | set HOST [--name N] [--category C] [--hide|--show] | remove HOST | import (JSON on stdin)")
	}
	cfg, ad, err := loadForEdit()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("sites list", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "machine-readable output")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		rows, err := library.ListSites(cfg.SitesFile)
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(map[string]any{"rows": rows, "categories": siteCategories()})
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "HOST\tNAME\tCATEGORY\tFROM")
		for _, r := range rows {
			cat := r.Category
			if r.Hide {
				cat += " (hidden)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Host, r.Name, cat, r.Source)
		}
		return w.Flush()
	case "set":
		fs := flag.NewFlagSet("sites set", flag.ContinueOnError)
		name := fs.String("name", "", "display name")
		cat := fs.String("category", "", "category")
		hide := fs.Bool("hide", false, "send nothing about this site (it reads as a plain browser tab)")
		show := fs.Bool("show", false, "undo --hide")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) != 1 {
			return errors.New("usage: desk-agent sites set HOST [--name N] [--category C] [--hide|--show]")
		}
		if *hide && *show {
			return errors.New("--hide and --show conflict")
		}
		set := visited(fs)
		u := library.SiteUpdate{Name: optStr(set, "name", *name), Category: optStr(set, "category", *cat)}
		if *hide || *show {
			h := *hide
			u.Hide = &h
		}
		if err := library.SetSite(cfg.SitesFile, rest[0], u, ad); err != nil {
			return err
		}
		fmt.Println("Saved", cfg.SitesFile)
		notifyReload()
		return nil
	case "remove":
		if len(args) != 2 {
			return errors.New("usage: desk-agent sites remove HOST")
		}
		if err := library.RemoveSite(cfg.SitesFile, args[1]); err != nil {
			return err
		}
		fmt.Println("Removed", args[1], "from", cfg.SitesFile)
		notifyReload()
		return nil
	case "import":
		var rows []library.SiteRow
		if err := decodeStdin(&rows); err != nil {
			return err
		}
		applied, skipped, err := library.ImportSites(cfg.SitesFile, rows, ad)
		if err != nil {
			return err
		}
		fmt.Printf("%d added or updated, %d skipped\n", applied, skipped)
		if applied > 0 {
			notifyReload()
		}
		return nil
	}
	return fmt.Errorf("unknown sites command %q", args[0])
}

// cmdApps: apps list|set|remove|import
func cmdApps(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: desk-agent apps list [--json] | set CLASS [--name N] [--category C] | remove CLASS | import (JSON on stdin)")
	}
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("apps list", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "machine-readable output")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		rows, err := library.ListApps(cfg.AppsFile, paths.SeenApps())
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(map[string]any{"rows": rows, "categories": appCategories()})
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "CLASS\tNAME\tCATEGORY\tFROM\tFOCUSED")
		for _, r := range rows {
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n", r.Class, r.Name, r.Category, r.Source, r.Count)
		}
		return w.Flush()
	case "set":
		fs := flag.NewFlagSet("apps set", flag.ContinueOnError)
		name := fs.String("name", "", "display name")
		cat := fs.String("category", "", "category")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) != 1 {
			return errors.New("usage: desk-agent apps set CLASS [--name N] [--category C]")
		}
		set := visited(fs)
		u := library.AppUpdate{Name: optStr(set, "name", *name), Category: optStr(set, "category", *cat)}
		if err := library.SetApp(cfg.AppsFile, rest[0], u); err != nil {
			return err
		}
		fmt.Println("Saved", cfg.AppsFile)
		notifyReload()
		return nil
	case "remove":
		if len(args) != 2 {
			return errors.New("usage: desk-agent apps remove CLASS")
		}
		if err := library.RemoveApp(cfg.AppsFile, args[1]); err != nil {
			return err
		}
		fmt.Println("Removed", args[1], "from", cfg.AppsFile)
		notifyReload()
		return nil
	case "import":
		var rows []library.AppRow
		if err := decodeStdin(&rows); err != nil {
			return err
		}
		applied, skipped, err := library.ImportApps(cfg.AppsFile, rows)
		if err != nil {
			return err
		}
		fmt.Printf("%d added or updated, %d skipped\n", applied, skipped)
		if applied > 0 {
			notifyReload()
		}
		return nil
	}
	return fmt.Errorf("unknown apps command %q", args[0])
}

func decodeStdin(v any) error {
	b, err := io.ReadAll(io.LimitReader(os.Stdin, 8<<20))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) == "" {
		return errors.New("expected a JSON array on stdin")
	}
	return json.Unmarshal(b, v)
}
