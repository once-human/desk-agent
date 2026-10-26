package scan

import (
	"sort"
	"strings"

	"github.com/once-human/desk-agent/internal/adult"
	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/hostname"
)

// Proposal is one proposed sites.toml row.
type Proposal struct {
	Host     string `json:"host"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Source   string `json:"source"` // "seed", "guess" or "unknown"
	Visits   int    `json:"visits"`
	Minutes  int    `json:"minutes"`
}

// noise are infrastructure hosts that show up in history but are never a
// place the user "was".
var noise = map[string]bool{
	"doubleclick.net": true, "googlesyndication.com": true, "gstatic.com": true, "googleapis.com": true,
	"googleusercontent.com": true, "cloudfront.net": true, "akamaihd.net": true, "fbcdn.net": true,
	"googletagmanager.com": true, "google-analytics.com": true, "cloudflare.com": true, "akamaized.net": true,
}

// guessRules are checked in order against the host's labels. They are a
// starting point only; every row they produce is marked "guess".
var guessRules = []struct {
	cat    string
	tokens []string // matched as whole label parts or label prefixes
}{
	{"ai", []string{"ai", "gpt", "llm", "chatbot", "copilot"}},
	{"mail", []string{"mail", "webmail", "inbox", "postbox"}},
	{"chat", []string{"chat", "messenger", "messaging", "whatsapp", "telegram", "signal", "slack", "discord", "meet"}},
	{"code", []string{"git", "github", "gitlab", "code", "coding", "dev", "developer", "developers", "api", "npm", "stack", "sdk", "cli", "compiler"}},
	{"docs", []string{"docs", "doc", "documentation", "wiki", "manual", "reference", "readthedocs", "notes"}},
	{"learning", []string{"learn", "learning", "academy", "course", "courses", "edu", "school", "tutorial", "tutorials", "university", "class", "classroom", "nptel", "mooc"}},
	{"design", []string{"design", "figma", "canva", "dribbble", "behance", "draw", "paint", "photo", "photos", "icons", "fonts", "color", "colors", "palette"}},
	{"music", []string{"music", "radio", "song", "songs", "podcast", "spotify", "soundcloud", "lyrics", "fm"}},
	{"stream", []string{"stream", "streaming", "watch", "movies", "movie", "film", "films", "anime", "flix", "tv", "series", "hotstar", "cinema"}},
	{"video", []string{"video", "videos", "tube", "clips"}},
	{"shopping", []string{"shop", "shopping", "store", "cart", "buy", "mart", "bazaar", "deals", "offers", "market", "marketplace", "amazon", "flipkart"}},
	{"news", []string{"news", "times", "post", "herald", "daily", "tribune", "journal", "gazette"}},
	{"social", []string{"social", "forum", "forums", "community", "reddit", "twitter", "instagram", "facebook", "linkedin", "tiktok", "snapchat"}},
	{"writing", []string{"blog", "blogs", "write", "writing", "medium", "substack", "essay", "essays"}},
	{"tool", []string{"tool", "tools", "convert", "converter", "calculator", "generator", "online", "app", "dashboard", "console", "admin"}},
}

func guessCategory(host string) string {
	reg := hostname.Registrable(host)
	// Whole-token matching looks at every label except the TLD.
	var parts []string
	labels := strings.Split(host, ".")
	for _, label := range labels[:len(labels)-1] {
		parts = append(parts, strings.Split(label, "-")...)
	}
	// The TLD is only used for ".ai" and ".edu"/".ac.xx", never for the rest
	// (".dev", ".app" and ".io" tell you almost nothing).
	tld := host[strings.LastIndexByte(host, '.')+1:]
	if tld == "ai" {
		return "ai"
	}
	if tld == "edu" || strings.Contains("."+host+".", ".ac.") {
		return "learning"
	}
	first := reg
	if i := strings.IndexByte(reg, '.'); i > 0 {
		first = reg[:i]
	}
	for _, r := range guessRules {
		for _, tok := range r.tokens {
			for _, p := range parts {
				if p == tok {
					return r.cat
				}
			}
			// Prefix/suffix of the main label ("gitfoo", "foogit") for tokens
			// long enough not to hit by accident.
			if len(tok) >= 5 && (strings.HasPrefix(first, tok) || strings.HasSuffix(first, tok)) {
				return r.cat
			}
		}
	}
	return ""
}

// Propose turns host counts into ranked proposals. Adult hosts are dropped
// entirely, before ranking; the number dropped is returned (never the names).
// Hosts the user already has an entry for (existing, from sites.toml) are
// skipped so the proposal never duplicates a key. top limits the list.
func Propose(counts map[string]HostCount, existing map[string]catalog.SiteEntry, ad *adult.Filter, top int) (out []Proposal, adultDropped int) {
	type agg struct {
		HostCount
		key string
	}
	byKey := map[string]*agg{}
	for host, c := range counts {
		reg := hostname.Registrable(host)
		if ad.Host(host) || ad.Host(reg) {
			adultDropped++
			continue
		}
		if noise[reg] {
			continue
		}
		key := reg
		if _, k, ok := catalog.Seed(host); ok {
			key = k
		}
		if _, have := existing[key]; have {
			continue
		}
		a := byKey[key]
		if a == nil {
			a = &agg{key: key}
			byKey[key] = a
		}
		a.Visits += c.Visits
		a.Seconds += c.Seconds
	}
	aggs := make([]*agg, 0, len(byKey))
	for _, a := range byKey {
		aggs = append(aggs, a)
	}
	sort.Slice(aggs, func(i, j int) bool {
		si, sj := aggs[i].Score(), aggs[j].Score()
		if si != sj {
			return si > sj
		}
		return aggs[i].key < aggs[j].key
	})
	if top > 0 && len(aggs) > top {
		aggs = aggs[:top]
	}
	for _, a := range aggs {
		p := Proposal{Host: a.key, Visits: a.Visits, Minutes: int(a.Seconds / 60)}
		if e, _, ok := catalog.Seed(a.key); ok {
			p.Name, p.Category, p.Source = e.Name, e.Category, "seed"
		} else if cat := guessCategory(a.key); cat != "" {
			p.Name, p.Category, p.Source = hostname.Friendly(a.key), cat, "guess"
		} else {
			p.Name, p.Category, p.Source = hostname.Friendly(a.key), "other", "unknown"
		}
		out = append(out, p)
	}
	return out, adultDropped
}
