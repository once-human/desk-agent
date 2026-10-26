package main

import (
	"errors"
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/paths"
)

func siteCategories() []string { return catalog.SiteCategories }
func appCategories() []string  { return catalog.AppCategories }

// cmdConfig: config show [--json] | get KEY | set KEY VALUE... | keys
func cmdConfig(args []string) error {
	if len(args) == 0 {
		args = []string{"show"}
	}
	switch args[0] {
	case "show":
		fs := flag.NewFlagSet("config show", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "machine-readable output")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cfg, err := config.Load(paths.ConfigFile())
		if err != nil {
			return err
		}
		m := cfg.Map()
		if *asJSON {
			return printJSON(map[string]any{"path": paths.ConfigFile(), "values": m, "settable": config.KeyNames()})
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%-22s %v\n", k, m[k])
		}
		return nil
	case "get":
		if len(args) != 2 {
			return errors.New("usage: desk-agent config get KEY")
		}
		cfg, err := config.Load(paths.ConfigFile())
		if err != nil {
			return err
		}
		v, ok := cfg.Map()[args[1]]
		if !ok {
			return fmt.Errorf("unknown key %q", args[1])
		}
		if l, isList := v.([]string); isList {
			fmt.Println(strings.Join(l, "\n"))
		} else {
			fmt.Println(v)
		}
		return nil
	case "set":
		if len(args) < 2 {
			return errors.New("usage: desk-agent config set KEY VALUE   (lists take several values; none empties the list)")
		}
		if err := config.Set(paths.ConfigFile(), args[1], args[2:]); err != nil {
			return err
		}
		fmt.Println("Saved", paths.ConfigFile())
		notifyReload()
		return nil
	case "keys":
		for _, k := range config.KeyNames() {
			fmt.Println(k)
		}
		return nil
	}
	return fmt.Errorf("unknown config command %q", args[0])
}
