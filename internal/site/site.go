// Package site turns a browser-reported host into the sample's site field,
// applying the privacy rules in one place.
package site

import (
	"github.com/once-human/desk-agent/internal/adult"
	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/hostname"
	"github.com/once-human/desk-agent/internal/sample"
)

// Resolve returns the site to report for rawHost, or nil for "say nothing":
//
//   - not a plain public host name (an IP, localhost, a URL, junk): nil;
//   - adult by heuristics or blocklist, checked before any catalog lookup so
//     no sites.toml entry can override it: nil;
//   - an entry with hide = true: nil;
//   - category "private": reported as an anonymous private site, no host;
//   - otherwise the catalog entry, or for unknown hosts the registrable
//     domain with a friendly name and category "other".
//
// The reported host is the catalog key (or the registrable domain), never the
// full host, so tenant or user subdomains are not exposed.
func Resolve(rawHost string, sites *catalog.Sites, ad *adult.Filter) *sample.Site {
	host, ok := hostname.Normalise(rawHost)
	if !ok {
		return nil
	}
	reg := hostname.Registrable(host)
	if ad.Host(host) || ad.Host(reg) {
		return nil
	}
	if e, key, found := sites.Lookup(host); found {
		if e.Hide {
			return nil
		}
		if e.Category == "private" {
			return &sample.Site{Host: "private", Name: "Private", Category: "private"}
		}
		name := e.Name
		if name == "" {
			name = hostname.Friendly(key)
		}
		return &sample.Site{Host: key, Name: name, Category: e.Category}
	}
	return &sample.Site{Host: reg, Name: hostname.Friendly(reg), Category: "other"}
}
