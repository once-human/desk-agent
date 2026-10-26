package scan

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/once-human/desk-agent/internal/adult"
	"github.com/once-human/desk-agent/internal/catalog"
)

func mustExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// firefoxDB builds a places.sqlite WITHOUT url/title columns. If Counts ever
// selected them the query would fail, which proves it reads rev_host only.
func firefoxDB(t *testing.T, path string, visits map[string]int, when time.Time) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	mustExec(t, db, `CREATE TABLE moz_places (id INTEGER PRIMARY KEY, rev_host TEXT)`)
	mustExec(t, db, `CREATE TABLE moz_historyvisits (id INTEGER PRIMARY KEY, place_id INTEGER, visit_date INTEGER)`)
	id := 0
	for rev, n := range visits {
		id++
		mustExec(t, db, `INSERT INTO moz_places (id, rev_host) VALUES (?, ?)`, id, rev)
		for i := 0; i < n; i++ {
			mustExec(t, db, `INSERT INTO moz_historyvisits (place_id, visit_date) VALUES (?, ?)`, id, when.UnixMicro())
		}
	}
	// an old visit that must be excluded by the time window
	id++
	mustExec(t, db, `INSERT INTO moz_places (id, rev_host) VALUES (?, ?)`, id, "dlo.ld.")
	mustExec(t, db, `INSERT INTO moz_historyvisits (place_id, visit_date) VALUES (?, ?)`, id, when.AddDate(-2, 0, 0).UnixMicro())
	return db
}

func TestFirefoxCountsReadsWALAndNeverUrls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "places.sqlite")
	now := time.Now()
	db := firefoxDB(t, path, map[string]int{
		"moc.buhtig.":          5,
		"moc.elpmaxe.www.":     2, // www. is stripped
		"tsohlacol.":           9, // localhost: rejected
		"":                     3, // file:// pages have no rev_host
		"emoh.tenretni.":       1,
		"oi.ppa-tnenet.ecila.": 4,
	}, now)
	defer db.Close()
	// The rows live in places.sqlite-wal (the connection is still open): the
	// scan must copy the side file too or it would see nothing.
	if _, err := os.Stat(path + "-wal"); err != nil {
		t.Fatalf("fixture has no WAL file, test is not testing what it claims: %v", err)
	}
	got, err := Counts(Source{"Firefox", "firefox", path}, now.AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if got["github.com"].Visits != 5 || got["example.com"].Visits != 2 || got["alice.tenent-app.io"].Visits != 4 {
		t.Fatalf("counts: %+v", got)
	}
	for _, bad := range []string{"localhost", "", "old.ld", "internet.home"} {
		if _, ok := got[bad]; ok {
			t.Errorf("%q should have been filtered", bad)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %d hosts: %+v", len(got), got)
	}
}

const chromeEpoch = 11644473600

func chromiumDB(t *testing.T, path string, rows []struct {
	url    string
	visits int
	durUS  int64
}, when time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	mustExec(t, db, `CREATE TABLE urls (id INTEGER PRIMARY KEY, url TEXT, title TEXT, visit_count INTEGER)`)
	mustExec(t, db, `CREATE TABLE visits (id INTEGER PRIMARY KEY, url INTEGER, visit_time INTEGER, visit_duration INTEGER)`)
	ts := (when.Unix() + chromeEpoch) * 1_000_000
	for i, r := range rows {
		mustExec(t, db, `INSERT INTO urls (id, url, title, visit_count) VALUES (?, ?, 'SECRET TITLE', ?)`, i+1, r.url, r.visits)
		for j := 0; j < r.visits; j++ {
			mustExec(t, db, `INSERT INTO visits (url, visit_time, visit_duration) VALUES (?, ?, ?)`, i+1, ts, r.durUS)
		}
	}
	mustExec(t, db, `INSERT INTO urls (id, url, title, visit_count) VALUES (999, 'https://old.example.org/', '', 1)`)
	mustExec(t, db, `INSERT INTO visits (url, visit_time, visit_duration) VALUES (999, ?, 0)`, (when.AddDate(-2, 0, 0).Unix()+chromeEpoch)*1_000_000)
}

func TestChromiumCountsReduceUrlsToHosts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "History")
	now := time.Now()
	chromiumDB(t, path, []struct {
		url    string
		visits int
		durUS  int64
	}{
		{"https://github.com/once-human/private-repo?tab=secret", 3, 60_000_000},
		{"https://github.com/other/page#frag", 2, 0},
		{"https://www.Example.org:8443/a/b", 1, 0},
		{"http://localhost:3000/x", 7, 0},
		{"file:///home/me/secret.pdf", 4, 0},
		{"chrome://settings", 4, 0},
		{"https://user:pass@docs.google.com/document/d/SECRET/edit", 1, 0},
	}, now)
	got, err := Counts(Source{"Chromium", "chromium", path}, now.AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if gh := got["github.com"]; gh.Visits != 5 || gh.Seconds != 180 {
		t.Errorf("github.com = %+v, want 5 visits / 180 s (two URLs merged)", gh)
	}
	if got["example.org"].Visits != 1 || got["docs.google.com"].Visits != 1 {
		t.Errorf("counts: %+v", got)
	}
	if len(got) != 3 {
		t.Errorf("unexpected hosts: %+v", got)
	}
	for h := range got {
		if strings.ContainsAny(h, "/?#@") || strings.Contains(h, "secret") {
			t.Errorf("a URL fragment leaked into a host key: %q", h)
		}
	}
}

func TestCountsLeavesTheOriginalUntouchedAndCleansUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "History")
	now := time.Now()
	chromiumDB(t, path, []struct {
		url    string
		visits int
		durUS  int64
	}{{"https://github.com/x", 1, 0}}, now)
	before, _ := os.ReadFile(path)
	tmpBefore, _ := filepath.Glob(filepath.Join(os.TempDir(), "desk-agent-scan-*"))
	if _, err := Counts(Source{"Chromium", "chromium", path}, now.AddDate(0, 0, -30)); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("the original database was modified")
	}
	tmpAfter, _ := filepath.Glob(filepath.Join(os.TempDir(), "desk-agent-scan-*"))
	if len(tmpAfter) > len(tmpBefore) {
		t.Errorf("temporary copy not removed: %v", tmpAfter)
	}
}

func TestDiscover(t *testing.T) {
	home := t.TempDir()
	touch := func(rel string) {
		p := filepath.Join(home, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	touch(".mozilla/firefox/abc.default-release/places.sqlite")
	touch(".mozilla/firefox/def.dev-edition/places.sqlite")
	touch(".zen/xyz.Default/places.sqlite")
	touch(".config/chromium/Default/History")
	touch(".config/chromium/Profile 2/History")
	touch(".config/BraveSoftware/Brave-Browser/Default/History")
	touch(".config/google-chrome/Default/Cookies") // not a history file
	got := Discover(home)
	byBrowser := map[string]int{}
	for _, s := range got {
		byBrowser[s.Browser]++
	}
	want := map[string]int{"Firefox": 2, "Zen": 1, "Chromium": 2, "Brave": 1}
	if fmt.Sprint(byBrowser) != fmt.Sprint(want) {
		t.Errorf("discovered %v, want %v", byBrowser, want)
	}
}

func TestProposeRanksFiltersAndMarksGuesses(t *testing.T) {
	counts := map[string]HostCount{
		"github.com":          {Visits: 500},
		"gist.github.com":     {Visits: 20},                // folds into github.com
		"docs.google.com":     {Visits: 100},               // seed key kept specific
		"monkeytype.com":      {Visits: 80},                // unknown -> other
		"learn.example.edu":   {Visits: 70},                // guess: learning
		"my-store.example.in": {Visits: 60},                // guess: shopping
		"gstatic.com":         {Visits: 9999},              // noise
		"pornhub.com":         {Visits: 3000},              // adult: dropped + counted
		"free-nudes.net":      {Visits: 10},                // adult
		"alice.tenant-app.io": {Visits: 40, Seconds: 6000}, // 40 + 100 min
		"already.example.org": {Visits: 30},                // user already has it
	}
	existing := map[string]catalog.SiteEntry{"already.example.org": {Name: "Mine", Category: "tool"}}
	props, dropped := Propose(counts, existing, adult.New(), 0)
	if dropped != 2 {
		t.Errorf("adult dropped = %d, want 2", dropped)
	}
	var order []string
	byHost := map[string]Proposal{}
	for _, p := range props {
		order = append(order, p.Host)
		byHost[p.Host] = p
	}
	if order[0] != "github.com" || byHost["github.com"].Visits != 520 {
		t.Errorf("order/merge: %v %+v", order, byHost["github.com"])
	}
	if byHost["github.com"].Source != "seed" || byHost["github.com"].Category != "code" {
		t.Errorf("github: %+v", byHost["github.com"])
	}
	if p := byHost["docs.google.com"]; p.Source != "seed" || p.Name != "Google Docs" {
		t.Errorf("docs.google.com: %+v", p)
	}
	if p := byHost["monkeytype.com"]; p.Source != "unknown" || p.Category != "other" || p.Name != "Monkeytype" {
		t.Errorf("monkeytype: %+v", p)
	}
	if p := byHost["example.edu"]; p.Source != "guess" || p.Category != "learning" {
		t.Errorf("example.edu: %+v", p)
	}
	if p := byHost["tenant-app.io"]; p.Minutes != 100 {
		t.Errorf("tenant-app.io: %+v", p)
	}
	for _, bad := range []string{"gstatic.com", "pornhub.com", "free-nudes.net", "already.example.org"} {
		if _, ok := byHost[bad]; ok {
			t.Errorf("%q must not be proposed", bad)
		}
	}
	if len(props) > 0 && props[0].Host != "github.com" {
		t.Errorf("ranking: %v", order)
	}
	// top limits the list after ranking
	top2, _ := Propose(counts, existing, adult.New(), 2)
	if len(top2) != 2 || top2[0].Host != "github.com" {
		t.Errorf("top=2: %+v", top2)
	}
}

func TestGuessCategory(t *testing.T) {
	cases := map[string]string{
		"somebank.com": "", "fastmail.example": "", "webmail.example.org": "mail",
		"my-notes.example.io": "docs", "learn.example.org": "learning", "uni.ac.in": "learning",
		"something.ai": "ai", "indiestream.tv": "stream", "photoshare.com": "design",
		"ycombinator.com": "", "example.dev": "", "monkeytype.com": "",
	}
	for host, want := range cases {
		if got := guessCategory(host); got != want {
			t.Errorf("guessCategory(%q) = %q, want %q", host, got, want)
		}
	}
}

func TestRenderSitesRoundTripsAndKeepsExisting(t *testing.T) {
	props := []Proposal{
		{Host: "github.com", Name: "GitHub", Category: "code", Source: "seed", Visits: 520, Minutes: 3},
		{Host: "monkeytype.com", Name: `Monkey "type" \ x`, Category: "other", Source: "unknown", Visits: 80},
		{Host: "x.example.edu", Name: "Ünï\x00code", Category: "learning", Source: "guess", Visits: 7},
	}
	existing := "# my notes\n[\"mine.example.org\"]\nname = \"Mine\"\ncategory = \"tool\"\n"
	out := RenderSites(existing, props, time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC))
	m, err := catalog.ParseSites([]byte(out))
	if err != nil {
		t.Fatalf("rendered file does not parse: %v\n%s", err, out)
	}
	if len(m) != 4 || m["mine.example.org"].Name != "Mine" || m["monkeytype.com"].Name != `Monkey "type" \ x` {
		t.Errorf("round trip: %+v", m)
	}
	if !strings.HasPrefix(out, "# my notes\n") {
		t.Error("existing text must be kept verbatim at the top")
	}
	if !strings.Contains(out, "GUESS") || !strings.Contains(out, "UNKNOWN") {
		t.Error("guesses must be marked")
	}
	// Fresh file gets the explanatory header and still parses.
	fresh := RenderSites("", props, time.Now())
	if _, err := catalog.ParseSites([]byte(fresh)); err != nil || !strings.Contains(fresh, "Categories:") {
		t.Errorf("fresh render: %v", err)
	}
	// Nothing new: file unchanged apart from a trailing newline.
	if RenderSites(existing, nil, time.Now()) != existing {
		t.Error("an empty proposal must not alter an existing file")
	}
}
