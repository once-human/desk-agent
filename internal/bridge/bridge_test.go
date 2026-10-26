package bridge

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDecode(t *testing.T) {
	good := map[string]Wire{
		`{"b":"firefox","h":"GitHub.com","yt":false}`:             {"firefox", "github.com", false},
		`{"b":"chromium","h":"","yt":true}`:                       {"chromium", "", true},
		`{"b":"chromium","h":"www.Example.org:8443","yt":false}`:  {"chromium", "example.org", false},
		`{"b":"firefox","h":"https://example.com/secret","yt":0}`: {},
	}
	for in, want := range good {
		got, err := Decode([]byte(in))
		if want == (Wire{}) {
			if err == nil {
				t.Errorf("%s: expected an error (yt is not a bool)", in)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("Decode(%s) = %+v,%v want %+v", in, got, err, want)
		}
	}
	// A URL in the host field is blanked, never passed through.
	w, err := Decode([]byte(`{"b":"firefox","h":"https://example.com/private/path?x=1","yt":false}`))
	if err != nil || w.Host != "" {
		t.Errorf("URL in host: %+v, %v", w, err)
	}
	// Extra fields (a title, a URL) are rejected outright.
	for _, in := range []string{
		`{"b":"firefox","h":"a.com","yt":false,"title":"My secret doc"}`,
		`{"b":"firefox","h":"a.com","yt":false,"url":"https://a.com/x"}`,
		`{"b":"safari","h":"a.com","yt":false}`,
		`not json`,
		``,
	} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("Decode(%q) should fail", in)
		}
	}
}

func TestFamily(t *testing.T) {
	for class, want := range map[string]string{
		"firefox": "firefox", "org.mozilla.firefox": "firefox", "zen": "firefox", "zen-browser": "firefox",
		"librewolf": "firefox", "chromium": "chromium", "google-chrome": "chromium", "brave-browser": "chromium",
		"vivaldi-stable": "chromium", "": "chromium",
	} {
		if got := Family(class); got != want {
			t.Errorf("Family(%q) = %q, want %q", class, got, want)
		}
	}
}

type fakeConn struct{ net.Conn }

func TestServerViewAndTTL(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	s := NewServer(nil)
	s.Now = func() time.Time { return now }
	changes := 0
	s.OnChange = func() { changes++ }

	ff, ch := &fakeConn{}, &fakeConn{}
	s.update(ff, Wire{"firefox", "github.com", false})
	s.update(ch, Wire{"chromium", "docs.google.com", true})

	if h, ok := s.Host("firefox"); !ok || h != "github.com" {
		t.Errorf("firefox host = %q,%v", h, ok)
	}
	if h, ok := s.Host("google-chrome"); !ok || h != "docs.google.com" {
		t.Errorf("chrome host = %q,%v", h, ok)
	}
	if !s.YouTubeAudible() {
		t.Error("youtube audible should be true")
	}
	before := changes
	s.update(ff, Wire{"firefox", "github.com", false}) // heartbeat, same content
	if changes != before {
		t.Error("an identical heartbeat must not signal a change")
	}

	// A second Firefox profile that is not focused reports "" and must not
	// hide the real one; the freshest non-empty host wins.
	other := &fakeConn{}
	now = now.Add(time.Second)
	s.update(other, Wire{"firefox", "", false})
	if h, ok := s.Host("firefox"); !ok || h != "github.com" {
		t.Errorf("blank report masked the real one: %q,%v", h, ok)
	}

	// TTL: everything goes stale without refreshes.
	now = now.Add(TTL + time.Second)
	if _, ok := s.Host("firefox"); ok {
		t.Error("stale report still served")
	}
	if s.YouTubeAudible() {
		t.Error("stale youtube flag still served")
	}
	s.Expire()

	// Disconnect drops the report immediately.
	s.update(ff, Wire{"firefox", "example.org", false})
	s.drop(ff)
	if _, ok := s.Host("firefox"); ok {
		t.Error("dropped connection still served")
	}
}

func TestServerOverSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "br")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	t.Setenv("XDG_RUNTIME_DIR", dir)
	sock := filepath.Join(dir, "b.sock")

	s := NewServer(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, sock)
	var c net.Conn
	for i := 0; i < 50; i++ {
		if c, err = net.Dial("unix", sock); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(sock)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("socket mode = %v", st.Mode().Perm())
	}
	if fams, err := Query(sock); err != nil || len(fams) != 0 {
		t.Errorf("status before any report: %v %v", fams, err)
	}
	c.Write([]byte(`{"b":"firefox","h":"x.com","yt":false,"title":"leak"}` + "\n")) // rejected
	c.Write([]byte(`{"b":"firefox","h":"example.org","yt":false}` + "\n"))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h, ok := s.Host("firefox"); ok {
			if h != "example.org" {
				t.Fatalf("host = %q", h)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h, _ := s.Host("firefox"); h != "example.org" {
		t.Fatalf("message never arrived, host = %q", h)
	}
	if fams, err := Query(sock); err != nil || len(fams) != 1 || fams[0] != "firefox" {
		t.Errorf("status = %v, %v", fams, err)
	}
	c.Close()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := s.Host("firefox"); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("closing the connection did not clear the report")
}

func TestOversizedLineHangsUp(t *testing.T) {
	a, b := net.Pipe()
	s := NewServer(nil)
	done := make(chan struct{})
	go func() { s.serveConn(b); close(done) }()
	go func() {
		big := make([]byte, MaxLine*3)
		for i := range big {
			big[i] = 'a'
		}
		a.Write(big)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not hang up on an oversized line")
	}
}
