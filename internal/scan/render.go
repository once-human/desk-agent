package scan

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/tomledit"
)

func tomlString(s string) string { return tomledit.Quote(s) }

const sitesHeader = `# desk-agent sites.toml
#
# host -> { name, category }. A key also matches all its subdomains, and the
# most specific key wins. Anything not listed is shown by its friendly name
# with category "other".
#
# Categories: %s
#
# Add  hide = true  to a host to report nothing for it.
# Adult sites never need an entry: they are filtered out and never named.
#
# Rows marked "guess" or "unknown" were not confident. Fix or delete them.
`

// RenderSites returns the file to open for review. existing (the user's current
// sites.toml text, possibly empty) is kept verbatim; new proposals are
// appended under a dated heading, so a re-scan only asks the user to look at
// what is new.
func RenderSites(existing string, props []Proposal, now time.Time) string {
	var b strings.Builder
	if strings.TrimSpace(existing) == "" {
		fmt.Fprintf(&b, sitesHeader, strings.Join(catalog.SiteCategories, " "))
	} else {
		b.WriteString(strings.TrimRight(existing, "\n"))
		b.WriteString("\n")
	}
	if len(props) == 0 {
		return b.String()
	}
	fmt.Fprintf(&b, "\n# ---- scan-history %s: %d new host(s), most used first ----\n", now.Format("2006-01-02"), len(props))
	for _, p := range props {
		note := p.Source
		if p.Source != "seed" {
			note = "GUESS"
			if p.Source == "unknown" {
				note = "UNKNOWN, check me"
			}
		}
		stats := fmt.Sprintf("%d visits", p.Visits)
		if p.Minutes > 0 {
			stats += fmt.Sprintf(", ~%d min", p.Minutes)
		}
		fmt.Fprintf(&b, "\n[%s] # %s; %s\nname = %s\ncategory = %s\n", tomlString(p.Host), note, stats, tomlString(p.Name), tomlString(p.Category))
	}
	return b.String()
}

// AppProposal is one proposed apps.toml row.
type AppProposal struct {
	Class    string `json:"class"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Source   string `json:"source"` // "desktop", "guess" or "unknown"
	Count    int    `json:"count"`
}

const appsHeader = `# desk-agent apps.toml
#
# window class -> { name, category }. Keys are lower-case Hyprland window
# classes (run "hyprctl activewindow" to see one).
#
# Categories the site understands: editor, ide, terminal, chat, design,
# browser, media, other. Use "other" for anything else; it is still shown by
# name.
#
# Rows marked "guess" or "unknown" were not confident. Fix or delete them.
`

// RenderApps is RenderSites for apps.toml.
func RenderApps(existing string, props []AppProposal, now time.Time) string {
	var b strings.Builder
	if strings.TrimSpace(existing) == "" {
		b.WriteString(appsHeader)
	} else {
		b.WriteString(strings.TrimRight(existing, "\n"))
		b.WriteString("\n")
	}
	if len(props) == 0 {
		return b.String()
	}
	sort.SliceStable(props, func(i, j int) bool { return props[i].Count > props[j].Count })
	fmt.Fprintf(&b, "\n# ---- scan-apps %s: %d new app(s), most used first ----\n", now.Format("2006-01-02"), len(props))
	for _, p := range props {
		note := "from .desktop file"
		if p.Source == "guess" {
			note = "GUESS from .desktop categories"
		} else if p.Source == "unknown" {
			note = "UNKNOWN, no .desktop file found"
		}
		fmt.Fprintf(&b, "\n[%s] # %s; focused %d times\nname = %s\ncategory = %s\n", tomlString(p.Class), note, p.Count, tomlString(p.Name), tomlString(p.Category))
	}
	return b.String()
}
