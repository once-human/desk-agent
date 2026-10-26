package nativehost

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func frame(s string) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.NativeEndian, uint32(len(s)))
	b.WriteString(s)
	return b.Bytes()
}

func TestFamilyFromArgs(t *testing.T) {
	if FamilyFromArgs([]string{"chrome-extension://abcdefgh/"}) != "chromium" {
		t.Error("chromium origin not recognised")
	}
	if FamilyFromArgs([]string{"/home/me/.mozilla/native-messaging-hosts/desk_agent.json", "desk-agent@example"}) != "firefox" {
		t.Error("firefox args not recognised")
	}
	if FamilyFromArgs(nil) != "firefox" {
		t.Error("default should be firefox")
	}
}

func TestConvert(t *testing.T) {
	line, err := Convert("firefox", []byte(`{"host":"GitHub.com","youtubeAudible":true}`))
	if err != nil || string(line) != `{"b":"firefox","h":"github.com","yt":true}`+"\n" {
		t.Fatalf("got %q, %v", line, err)
	}
	// URL smuggled in the host field becomes empty.
	line, err = Convert("chromium", []byte(`{"host":"https://example.com/a/b?c=d","youtubeAudible":false}`))
	if err != nil || !strings.Contains(string(line), `"h":""`) {
		t.Fatalf("URL not blanked: %q, %v", line, err)
	}
	for _, bad := range []string{
		`{"host":"a.com","youtubeAudible":false,"title":"Secret Doc"}`,
		`{"host":"a.com","youtubeAudible":false,"url":"https://a.com/x"}`,
		`{"host":"a.com","tabs":["x"]}`,
		`[]`, `{`, ``,
	} {
		if _, err := Convert("firefox", []byte(bad)); err == nil {
			t.Errorf("Convert accepted %q", bad)
		}
	}
}

func TestReadFrameLimits(t *testing.T) {
	if _, err := ReadFrame(bytes.NewReader(frame(`{"host":"a.com"}`))); err != nil {
		t.Fatal(err)
	}
	var huge bytes.Buffer
	binary.Write(&huge, binary.NativeEndian, uint32(10_000_000))
	if _, err := ReadFrame(&huge); err == nil {
		t.Error("an oversized frame length must be rejected before allocating")
	}
	var zero bytes.Buffer
	binary.Write(&zero, binary.NativeEndian, uint32(0))
	if _, err := ReadFrame(&zero); err == nil {
		t.Error("zero-length frame must be rejected")
	}
	if _, err := ReadFrame(bytes.NewReader([]byte{1, 2})); err == nil {
		t.Error("truncated header must error")
	}
}

func TestRunForwardsOnlyCleanLines(t *testing.T) {
	dir, _ := os.MkdirTemp("", "nh")
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		b, _ := io.ReadAll(c)
		got <- string(b)
	}()

	var in bytes.Buffer
	in.Write(frame(`{"host":"a.com","youtubeAudible":false,"title":"LEAK"}`)) // dropped
	in.Write(frame(`{"host":"github.com","youtubeAudible":false}`))
	in.Write(frame(`garbage`)) // dropped
	in.Write(frame(`{"host":"","youtubeAudible":true}`))
	if err := Run(&in, sock, "chromium"); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		want := `{"b":"chromium","h":"github.com","yt":false}` + "\n" + `{"b":"chromium","h":"","yt":true}` + "\n"
		if s != want {
			t.Fatalf("forwarded:\n%q\nwant:\n%q", s, want)
		}
		if strings.Contains(s, "LEAK") {
			t.Fatal("leaked a field")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing forwarded")
	}
}

func TestRunSurvivesMissingAgent(t *testing.T) {
	var in bytes.Buffer
	in.Write(frame(`{"host":"github.com","youtubeAudible":false}`))
	if err := Run(&in, "/nonexistent/agent.sock", "firefox"); err != nil {
		t.Fatalf("a missing agent must not crash the host: %v", err)
	}
}
