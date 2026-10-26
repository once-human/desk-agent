// Package tomledit makes small, comment-preserving edits to the TOML files
// desk-agent owns (sites.toml, apps.toml): set or remove one `["key"]` table.
//
// It is text surgery, not a TOML library. Callers must re-parse the result
// with the real parser and check it before writing it anywhere.
package tomledit

import (
	"regexp"
	"strings"
)

// Field is one `key = value` line; Value must already be TOML-encoded.
type Field struct {
	Key   string
	Value string
}

// Quote returns s as a TOML basic string, dropping control characters.
func Quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r < 0x20 || r == 0x7f:
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

var header = regexp.MustCompile(`^\s*\[\s*(?:"([^"]*)"|'([^']*)'|([A-Za-z0-9_-]+))\s*\]\s*(?:#.*)?$`)
var anyHeader = regexp.MustCompile(`^\s*\[\[?`)

// span finds the lines [start, end] of the table named key: from its header
// to its last non-blank, non-comment line. ok is false if there is none.
func span(lines []string, key string) (start, end int, ok bool) {
	start = -1
	for i, l := range lines {
		if m := header.FindStringSubmatch(l); m != nil {
			name := m[1] + m[2] + m[3]
			if strings.EqualFold(name, key) {
				start = i
				break
			}
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	end = start
	for i := start + 1; i < len(lines); i++ {
		if anyHeader.MatchString(lines[i]) {
			break
		}
		t := strings.TrimSpace(lines[i])
		if t != "" && !strings.HasPrefix(t, "#") {
			end = i
		}
	}
	return start, end, true
}

func block(key string, fields []Field) []string {
	out := []string{"[" + Quote(key) + "]"}
	for _, f := range fields {
		out = append(out, f.Key+" = "+f.Value)
	}
	return out
}

// SetTable replaces the table for key, or appends one. Comments and other
// tables are left exactly as they were.
func SetTable(src, key string, fields []Field) string {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	if strings.TrimSpace(src) == "" {
		lines = nil
	}
	if start, end, ok := span(lines, key); ok {
		out := append([]string{}, lines[:start]...)
		out = append(out, block(key, fields)...)
		out = append(out, lines[end+1:]...)
		return strings.Join(out, "\n") + "\n"
	}
	out := lines
	if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
		out = append(out, "")
	}
	out = append(out, block(key, fields)...)
	return strings.Join(out, "\n") + "\n"
}

// RemoveTable deletes the table for key (and its blank separator line).
func RemoveTable(src, key string) string {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	start, end, ok := span(lines, key)
	if !ok {
		return src
	}
	out := append([]string{}, lines[:start]...)
	rest := lines[end+1:]
	// Drop one blank line that separated this table from the previous one.
	if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" && (len(rest) == 0 || strings.TrimSpace(rest[0]) == "") {
		out = out[:len(out)-1]
	}
	out = append(out, rest...)
	res := strings.Join(out, "\n")
	if strings.TrimSpace(res) == "" {
		return ""
	}
	return strings.TrimRight(res, "\n") + "\n"
}

// SetKey sets a top-level `key = value` line: it replaces an existing one,
// else uncomments the first commented example, else appends. Only for keys
// above the first table header.
func SetKey(src, key, value string) string {
	lines := strings.Split(strings.TrimRight(src, "\n"), "\n")
	if strings.TrimSpace(src) == "" {
		lines = nil
	}
	active := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=`)
	commented := regexp.MustCompile(`^\s*#\s*` + regexp.QuoteMeta(key) + `\s*=`)
	for i, l := range lines {
		if anyHeader.MatchString(l) {
			break
		}
		if active.MatchString(l) {
			lines[i] = key + " = " + value
			return strings.Join(lines, "\n") + "\n"
		}
	}
	for i, l := range lines {
		if anyHeader.MatchString(l) {
			break
		}
		if commented.MatchString(l) {
			lines[i] = key + " = " + value
			return strings.Join(lines, "\n") + "\n"
		}
	}
	// Append before the first table header, if any, so it stays top-level.
	for i, l := range lines {
		if anyHeader.MatchString(l) {
			out := append(append(append([]string{}, lines[:i]...), key+" = "+value, ""), lines[i:]...)
			return strings.Join(out, "\n") + "\n"
		}
	}
	lines = append(lines, key+" = "+value)
	return strings.Join(lines, "\n") + "\n"
}
