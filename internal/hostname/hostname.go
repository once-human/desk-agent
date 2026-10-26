// Package hostname turns whatever a browser or a history database hands us
// into a clean host, and a host into a friendly name. It only ever deals with
// host names: callers must never pass it more than that, and it rejects
// anything that looks like a URL.
package hostname

import (
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Normalise returns the lower-case host without "www.", a port, or a trailing
// dot. It reports false for anything that is not a plain public-looking
// DNS name: URLs (slash, query, fragment, userinfo), IP literals, single-label
// names like "localhost", and names with characters DNS does not allow.
func Normalise(raw string) (string, bool) {
	h := strings.ToLower(strings.TrimSpace(raw))
	if h == "" || len(h) > 253+6 {
		return "", false
	}
	// Strip a single ":port".
	if i := strings.LastIndexByte(h, ':'); i >= 0 {
		port := h[i+1:]
		if port == "" || len(port) > 5 || strings.ContainsRune(h[:i], ':') {
			return "", false
		}
		for _, c := range port {
			if c < '0' || c > '9' {
				return "", false
			}
		}
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	h = strings.TrimPrefix(h, "www.")
	if len(h) == 0 || len(h) > 253 {
		return "", false
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return "", false
	}
	for _, l := range labels {
		if l == "" || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return "", false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return "", false
			}
		}
	}
	// IPv4 literal: the last label is numeric (real TLDs never are).
	if last := labels[len(labels)-1]; last[0] >= '0' && last[0] <= '9' {
		return "", false
	}
	switch labels[len(labels)-1] {
	case "local", "localhost", "internal", "lan", "home", "intranet", "corp", "test", "invalid", "example":
		return "", false
	}
	return h, true
}

// Registrable returns the registrable domain (eTLD+1) of host, or host itself
// when it already is one or the suffix list has no answer.
func Registrable(host string) string {
	d, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return d
}

// Friendly turns a host into a display name: the first label of its
// registrable domain, split on hyphens and capitalised.
// "monkeytype.com" -> "Monkeytype", "my-site.co.uk" -> "My Site".
func Friendly(host string) string {
	d := Registrable(host)
	label := d
	if i := strings.IndexByte(d, '.'); i > 0 {
		label = d[:i]
	}
	words := strings.FieldsFunc(label, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	if len(words) == 0 {
		return d
	}
	return strings.Join(words, " ")
}

// FromRevHost converts Firefox's moz_places.rev_host ("moc.buhtig.") to a host
// ("github.com"). It is how scan-history reads Firefox without ever selecting
// a URL.
func FromRevHost(rev string) string {
	rev = strings.TrimSuffix(rev, ".")
	b := []byte(rev)
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	return string(b)
}

// FromURL extracts only the host from a URL string and normalises it. The URL
// is never retained or returned. ok is false for non-http(s) schemes.
func FromURL(u string) (string, bool) {
	i := strings.Index(u, "://")
	if i < 0 {
		return "", false
	}
	switch strings.ToLower(u[:i]) {
	case "http", "https":
	default:
		return "", false
	}
	rest := u[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	if k := strings.LastIndexByte(rest, '@'); k >= 0 {
		rest = rest[k+1:]
	}
	return Normalise(rest)
}
