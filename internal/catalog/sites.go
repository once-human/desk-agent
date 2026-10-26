package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// SiteCategories are the categories sites.toml may use. The set is the union
// of what scan-history proposes and what the site understands.
var SiteCategories = []string{
	"code", "docs", "writing", "mail", "chat", "social", "video", "stream", "music",
	"design", "shopping", "news", "learning", "ai", "tool", "private", "other",
}

// ValidSiteCategory reports whether c is a known site category.
func ValidSiteCategory(c string) bool {
	for _, k := range SiteCategories {
		if k == c {
			return true
		}
	}
	return false
}

// SiteEntry is one sites.toml row. Hide sends nothing for the host.
type SiteEntry struct {
	Name     string `toml:"name"`
	Category string `toml:"category"`
	Hide     bool   `toml:"hide"`
}

// Sites resolves hostnames. Keys are hosts; a key matches itself and every
// subdomain, and the longest (most specific) key wins.
type Sites struct {
	m map[string]SiteEntry
}

// seedSites is a starting point only. sites.toml wins over it.
var seedSites = map[string]SiteEntry{
	// code
	"github.com": {"GitHub", "code", false}, "gitlab.com": {"GitLab", "code", false}, "bitbucket.org": {"Bitbucket", "code", false},
	"stackoverflow.com": {"Stack Overflow", "code", false}, "npmjs.com": {"npm", "code", false}, "crates.io": {"crates.io", "code", false},
	"pypi.org": {"PyPI", "code", false}, "codeberg.org": {"Codeberg", "code", false}, "codepen.io": {"CodePen", "code", false},
	"replit.com": {"Replit", "code", false}, "vercel.com": {"Vercel", "tool", false}, "netlify.com": {"Netlify", "tool", false},
	"console.cloud.google.com": {"Google Cloud", "tool", false}, "console.aws.amazon.com": {"AWS Console", "tool", false},
	"supabase.com": {"Supabase", "tool", false}, "neon.tech": {"Neon", "tool", false}, "render.com": {"Render", "tool", false},
	"firebase.google.com": {"Firebase", "tool", false}, "console.firebase.google.com": {"Firebase", "tool", false},
	// docs
	"docs.google.com": {"Google Docs", "docs", false}, "drive.google.com": {"Google Drive", "docs", false},
	"sheets.google.com": {"Google Sheets", "docs", false}, "notion.so": {"Notion", "docs", false}, "notion.site": {"Notion", "docs", false},
	"developer.mozilla.org": {"MDN", "docs", false}, "pkg.go.dev": {"Go Packages", "docs", false}, "docs.rs": {"docs.rs", "docs", false},
	"wiki.archlinux.org": {"Arch Wiki", "docs", false}, "readthedocs.io": {"Read the Docs", "docs", false},
	"wikipedia.org": {"Wikipedia", "docs", false}, "overleaf.com": {"Overleaf", "docs", false}, "obsidian.md": {"Obsidian", "docs", false},
	// writing
	"medium.com": {"Medium", "writing", false}, "substack.com": {"Substack", "writing", false}, "dev.to": {"DEV", "writing", false},
	"hashnode.com": {"Hashnode", "writing", false}, "wordpress.com": {"WordPress", "writing", false},
	// mail
	"mail.google.com": {"Gmail", "mail", false}, "outlook.live.com": {"Outlook", "mail", false}, "outlook.office.com": {"Outlook", "mail", false},
	"proton.me": {"Proton", "mail", false}, "mail.proton.me": {"Proton Mail", "mail", false},
	// chat
	"discord.com": {"Discord", "chat", false}, "web.whatsapp.com": {"WhatsApp", "chat", false}, "web.telegram.org": {"Telegram", "chat", false},
	"slack.com": {"Slack", "chat", false}, "app.slack.com": {"Slack", "chat", false}, "messenger.com": {"Messenger", "chat", false},
	"teams.microsoft.com": {"Teams", "chat", false}, "meet.google.com": {"Google Meet", "chat", false}, "zoom.us": {"Zoom", "chat", false},
	// social
	"x.com": {"X", "social", false}, "twitter.com": {"X", "social", false}, "reddit.com": {"Reddit", "social", false},
	"instagram.com": {"Instagram", "social", false}, "facebook.com": {"Facebook", "social", false}, "linkedin.com": {"LinkedIn", "social", false},
	"threads.net": {"Threads", "social", false}, "bsky.app": {"Bluesky", "social", false}, "mastodon.social": {"Mastodon", "social", false},
	"pinterest.com": {"Pinterest", "social", false}, "quora.com": {"Quora", "social", false},
	// video / stream
	"youtube.com": {"YouTube", "video", false}, "youtu.be": {"YouTube", "video", false}, "vimeo.com": {"Vimeo", "video", false},
	"twitch.tv": {"Twitch", "stream", false}, "netflix.com": {"Netflix", "stream", false}, "primevideo.com": {"Prime Video", "stream", false},
	"hotstar.com": {"Hotstar", "stream", false}, "disneyplus.com": {"Disney+", "stream", false}, "jiocinema.com": {"JioCinema", "stream", false},
	"hbomax.com": {"Max", "stream", false}, "crunchyroll.com": {"Crunchyroll", "stream", false},
	// music
	"open.spotify.com": {"Spotify", "music", false}, "spotify.com": {"Spotify", "music", false}, "music.youtube.com": {"YouTube Music", "music", false},
	"soundcloud.com": {"SoundCloud", "music", false}, "music.apple.com": {"Apple Music", "music", false}, "bandcamp.com": {"Bandcamp", "music", false},
	"gaana.com": {"Gaana", "music", false}, "jiosaavn.com": {"JioSaavn", "music", false},
	// design
	"figma.com": {"Figma", "design", false}, "dribbble.com": {"Dribbble", "design", false}, "behance.net": {"Behance", "design", false},
	"canva.com": {"Canva", "design", false}, "framer.com": {"Framer", "design", false}, "excalidraw.com": {"Excalidraw", "design", false},
	// shopping
	"amazon.in": {"Amazon", "shopping", false}, "amazon.com": {"Amazon", "shopping", false}, "flipkart.com": {"Flipkart", "shopping", false},
	"myntra.com": {"Myntra", "shopping", false}, "ebay.com": {"eBay", "shopping", false}, "aliexpress.com": {"AliExpress", "shopping", false},
	"swiggy.com": {"Swiggy", "shopping", false}, "zomato.com": {"Zomato", "shopping", false}, "blinkit.com": {"Blinkit", "shopping", false},
	// news
	"news.ycombinator.com": {"Hacker News", "news", false}, "nytimes.com": {"NYT", "news", false}, "bbc.com": {"BBC", "news", false},
	"bbc.co.uk": {"BBC", "news", false}, "thehindu.com": {"The Hindu", "news", false}, "techcrunch.com": {"TechCrunch", "news", false},
	"theverge.com": {"The Verge", "news", false}, "arstechnica.com": {"Ars Technica", "news", false}, "lobste.rs": {"Lobsters", "news", false},
	// learning
	"coursera.org": {"Coursera", "learning", false}, "udemy.com": {"Udemy", "learning", false}, "khanacademy.org": {"Khan Academy", "learning", false},
	"leetcode.com": {"LeetCode", "learning", false}, "freecodecamp.org": {"freeCodeCamp", "learning", false}, "edx.org": {"edX", "learning", false},
	"brilliant.org": {"Brilliant", "learning", false}, "nptel.ac.in": {"NPTEL", "learning", false},
	// ai
	"chatgpt.com": {"ChatGPT", "ai", false}, "openai.com": {"OpenAI", "ai", false}, "claude.ai": {"Claude", "ai", false},
	"gemini.google.com": {"Gemini", "ai", false}, "perplexity.ai": {"Perplexity", "ai", false}, "huggingface.co": {"Hugging Face", "ai", false},
	"aistudio.google.com": {"AI Studio", "ai", false}, "poe.com": {"Poe", "ai", false},
	// tools
	"google.com": {"Google", "tool", false}, "duckduckgo.com": {"DuckDuckGo", "tool", false}, "calendar.google.com": {"Google Calendar", "tool", false},
	"translate.google.com": {"Google Translate", "tool", false}, "maps.google.com": {"Google Maps", "tool", false},
	"regex101.com": {"regex101", "tool", false}, "excalidraw.dev": {"Excalidraw", "design", false},
}

// LoadSites reads sites.toml on top of the seed. A missing file is fine.
func LoadSites(path string) (*Sites, error) {
	s := &Sites{m: make(map[string]SiteEntry, len(seedSites))}
	for k, v := range seedSites {
		s.m[k] = v
	}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	user, err := ParseSites(data)
	if err != nil {
		return s, err
	}
	for k, v := range user {
		s.m[k] = v
	}
	return s, nil
}

// ParseSites decodes and validates a sites.toml document. Keys are lower-cased
// and stripped of a leading "www.". Unknown categories are rejected so a typo
// in the review step is caught instead of silently shown as "other".
func ParseSites(data []byte) (map[string]SiteEntry, error) {
	raw := map[string]SiteEntry{}
	md, err := toml.Decode(string(data), &raw)
	if err != nil {
		return nil, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		return nil, fmt.Errorf("unknown field %q (allowed: name, category, hide)", und[0].String())
	}
	out := make(map[string]SiteEntry, len(raw))
	for k, v := range raw {
		key := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(k)), "www.")
		if key == "" || strings.ContainsAny(key, "/: ") {
			return nil, fmt.Errorf("%q is not a host name", k)
		}
		if v.Category == "" {
			v.Category = "other"
		}
		if !ValidSiteCategory(v.Category) {
			return nil, fmt.Errorf("%s: unknown category %q (use one of %s)", k, v.Category, strings.Join(SiteCategories, ", "))
		}
		out[key] = v
	}
	return out, nil
}

// Lookup finds the most specific entry for host (an already-normalised host).
// key is the matched sites.toml key, which is what gets reported as the host
// so that deeper subdomains are never exposed.
func (s *Sites) Lookup(host string) (e SiteEntry, key string, ok bool) {
	for h := host; h != ""; {
		if e, found := s.m[h]; found {
			return e, h, true
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			break
		}
		h = h[i+1:]
	}
	return SiteEntry{}, "", false
}

// Has reports whether host already has an entry (seed or sites.toml).
func (s *Sites) Has(host string) bool { _, _, ok := s.Lookup(host); return ok }

// Keys returns all known hosts, sorted. Used by tests and scan-history.
func (s *Sites) Keys() []string {
	out := make([]string, 0, len(s.m))
	for k := range s.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Seed returns the built-in entry for host, if any (ignoring sites.toml).
func Seed(host string) (SiteEntry, string, bool) {
	return (&Sites{m: seedSites}).Lookup(host)
}

// MustEmptySites returns Sites with no entries at all (not even the seed).
// It exists for tests that need to prove behaviour for unknown hosts.
func MustEmptySites() *Sites { return &Sites{m: map[string]SiteEntry{}} }

// SeedSites returns a copy of the built-in site entries.
func SeedSites() map[string]SiteEntry {
	out := make(map[string]SiteEntry, len(seedSites))
	for k, v := range seedSites {
		out[k] = v
	}
	return out
}
