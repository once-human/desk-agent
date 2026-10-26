// Package adult decides whether a host (or a piece of text) must never be
// named. A match means "send nothing about it": the site is reported as
// null, and scan-history leaves the host out of sites.toml entirely.
//
// Two layers:
//
//   - built-in heuristics on host keywords (no host list is shipped);
//   - an optional blocklist file (one host per line, or hosts-file format).
//
// The heuristics deliberately over-block rather than under-block. A false
// positive just means a site shows as "Browsing"; a false negative would
// name an adult site on a public page.
package adult

import (
	"bufio"
	"github.com/once-human/desk-agent/internal/sample"
	"io"
	"os"
	"sort"
	"strings"
)

// strong tokens are blocked as a substring anywhere in the host.
var strong = []string{
	"porn", "xxx", "xvideos", "xnxx", "xhamster", "redtube", "youporn", "tube8",
	"onlyfans", "fansly", "chaturbate", "stripchat", "livejasmin", "bongacams",
	"cam4", "myfreecams", "hentai", "brazzers", "spankbang", "rule34", "e621",
	"erotic", "camgirl", "nsfw", "sexcam", "xfree", "playboy", "bangbros",
	"naughtyamerica", "realitykings", "fapello", "thothub", "motherless",
}

// weak tokens are blocked only as a whole label or hyphen-separated part of a
// label ("sex.com", "free-nudes.net"), so "essex.ac.uk" and "adultswim.com"
// are untouched.
var weak = map[string]bool{
	"sex": true, "sexy": true, "adult": true, "adults": true, "nude": true, "nudes": true,
	"cam": true, "cams": true, "escort": true, "escorts": true, "milf": true, "boobs": true,
	"tits": true, "fap": true, "lewd": true, "nsfw": true, "porno": true, "slut": true, "slutty": true,
}

// textTokens are the subset of strong tokens that are safe to match inside
// free text (a track or artist name). Short ones such as "cam4" or "e621"
// are left out to avoid hitting ordinary words.
var textTokens = []string{
	"porn", "xxx", "xvideos", "xnxx", "xhamster", "redtube", "youporn", "onlyfans",
	"chaturbate", "hentai", "brazzers", "spankbang", "nsfw", "camgirl", "playboy",
}

// Filter holds the optional blocklist.
type Filter struct {
	hosts map[string]bool
}

// New returns a Filter with only the built-in heuristics.
func New() *Filter { return &Filter{hosts: map[string]bool{}} }

// Load reads an optional blocklist file. A missing file is fine.
func Load(path string) (*Filter, error) {
	f := New()
	if path == "" {
		return f, nil
	}
	fh, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return f, err
	}
	defer fh.Close()
	return f, f.Read(fh)
}

// Read adds hosts from r. Accepted line forms: "host", "0.0.0.0 host",
// "127.0.0.1 host", with "#" comments.
func (f *Filter) Read(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		var h string
		switch len(fields) {
		case 1:
			h = fields[0]
		case 2:
			if fields[0] == "0.0.0.0" || fields[0] == "127.0.0.1" || fields[0] == "::" || fields[0] == "::1" {
				h = fields[1]
			}
		}
		h = strings.ToLower(strings.TrimSuffix(h, "."))
		if h == "" || h == "localhost" || h == "0.0.0.0" || strings.ContainsAny(h, "/:@ ") {
			continue
		}
		f.hosts[strings.TrimPrefix(h, "www.")] = true
	}
	return sc.Err()
}

// Len is the number of blocklist entries loaded (heuristics not counted).
func (f *Filter) Len() int { return len(f.hosts) }

// Host reports whether host must never be named. host should already be
// lower-case; it is lower-cased again to be safe.
func (f *Filter) Host(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return false
	}
	for _, t := range strong {
		if strings.Contains(host, t) {
			return true
		}
	}
	for _, label := range strings.Split(host, ".") {
		for _, part := range strings.Split(label, "-") {
			if weak[part] {
				return true
			}
		}
	}
	// Blocklist: exact host or any parent domain.
	for h := host; h != ""; {
		if f.hosts[h] {
			return true
		}
		i := strings.IndexByte(h, '.')
		if i < 0 {
			break
		}
		h = h[i+1:]
	}
	return false
}

// Text reports whether free text (a track title, an artist) mentions an
// adult site or brand. It is intentionally narrower than Host.
func (f *Filter) Text(s string) bool {
	s = strings.ToLower(s)
	for _, t := range textTokens {
		if strings.Contains(s, t) {
			return true
		}
	}
	return false
}

// Hosts returns the loaded blocklist entries, sorted. Used to write a
// normalised copy of a downloaded list.
func (f *Filter) Hosts() []string {
	out := make([]string, 0, len(f.hosts))
	for h := range f.hosts {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// ScrubMedia returns m with the track title and artist removed when either
// mentions an adult site or brand. The player, state and source are kept. m is
// never modified; a copy is returned when something was removed.
func (f *Filter) ScrubMedia(m *sample.Media) *sample.Media {
	if m == nil {
		return nil
	}
	if (m.Title != nil && f.Text(*m.Title)) || (m.Artist != nil && f.Text(*m.Artist)) {
		c := *m
		c.Title, c.Artist = nil, nil
		return &c
	}
	return m
}
