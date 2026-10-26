package tomledit

import (
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func parse(t *testing.T, s string) map[string]map[string]any {
	t.Helper()
	m := map[string]map[string]any{}
	if _, err := toml.Decode(s, &m); err != nil {
		t.Fatalf("result does not parse: %v\n%s", err, s)
	}
	return m
}

const doc = `# my notes at the top

["github.com"] # seed; 40 visits
name = "GitHub"
category = "code"

# ---- scan-history 2026-10-26: 2 new host(s) ----

["monkeytype.com"] # UNKNOWN, check me; 25 visits
name = "Monkeytype"
category = "other"

['single.quoted']
name = "Single"
`

func TestSetTableReplacesInPlaceKeepingComments(t *testing.T) {
	out := SetTable(doc, "monkeytype.com", []Field{{"name", Quote("Monkeytype")}, {"category", Quote("learning")}})
	m := parse(t, out)
	if m["monkeytype.com"]["category"] != "learning" || m["github.com"]["name"] != "GitHub" || m["single.quoted"]["name"] != "Single" {
		t.Errorf("%v", m)
	}
	for _, keep := range []string{"# my notes at the top", "# ---- scan-history 2026-10-26: 2 new host(s) ----", "# seed; 40 visits"} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %q:\n%s", keep, out)
		}
	}
	if strings.Contains(out, "UNKNOWN, check me") {
		t.Error("the stale review note on an edited row should be gone")
	}
	if strings.Count(out, "monkeytype.com") != 1 {
		t.Errorf("duplicate table:\n%s", out)
	}
}

func TestSetTableAppendsAndMatchesQuoteStyles(t *testing.T) {
	out := SetTable(doc, "new.example.org", []Field{{"name", Quote("New")}, {"hide", "true"}})
	m := parse(t, out)
	if m["new.example.org"]["hide"] != true || len(m) != 4 {
		t.Errorf("%v", m)
	}
	out = SetTable(out, "single.quoted", []Field{{"name", Quote("Changed")}})
	if parse(t, out)["single.quoted"]["name"] != "Changed" || strings.Count(out, "single.quoted") != 1 {
		t.Errorf("single-quoted header not matched:\n%s", out)
	}
	if got := parse(t, SetTable("", "a.com", []Field{{"name", Quote("A")}})); got["a.com"]["name"] != "A" {
		t.Errorf("empty source: %v", got)
	}
}

func TestRemoveTable(t *testing.T) {
	out := RemoveTable(doc, "github.com")
	m := parse(t, out)
	if _, ok := m["github.com"]; ok || len(m) != 2 {
		t.Errorf("%v\n%s", m, out)
	}
	if !strings.Contains(out, "# my notes at the top") || !strings.Contains(out, "# ---- scan-history") {
		t.Errorf("comments lost:\n%s", out)
	}
	if RemoveTable(doc, "nope.com") != doc {
		t.Error("removing a missing table must be a no-op")
	}
	only := "[\"a.com\"]\nname = \"A\"\n"
	if RemoveTable(only, "a.com") != "" {
		t.Error("removing the last table leaves an empty file")
	}
}

func TestQuote(t *testing.T) {
	if q := Quote("a \"b\" \\ c\x00\n"); q != `"a \"b\" \\ c"` {
		t.Errorf("%s", q)
	}
}

func TestSetKey(t *testing.T) {
	src := "# header\nendpoint = \"https://x/\"\ninterval_s = 30\n\n# private = true\nhide_repos = []\n"
	out := SetKey(src, "interval_s", "15")
	if !strings.Contains(out, "interval_s = 15\n") || strings.Contains(out, "interval_s = 30") {
		t.Errorf("replace:\n%s", out)
	}
	out = SetKey(src, "private", "true") // uncomments the example line
	if !strings.Contains(out, "\nprivate = true\n") || strings.Contains(out, "# private") {
		t.Errorf("uncomment:\n%s", out)
	}
	out = SetKey(src, "battery", "false")
	if !strings.HasSuffix(out, "battery = false\n") {
		t.Errorf("append:\n%s", out)
	}
	// With a table in the file the appended key must stay top-level.
	withTable := src + "\n[table]\nx = 1\n"
	out = SetKey(withTable, "battery", "false")
	var m map[string]any
	if _, err := toml.Decode(out, &m); err != nil || m["battery"] != false {
		t.Errorf("battery must be top-level: %v %v\n%s", m, err, out)
	}
}
