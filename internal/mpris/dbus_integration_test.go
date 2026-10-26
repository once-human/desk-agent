//go:build dbustest

// Run with: go test -tags dbustest ./internal/mpris
// Needs dbus-daemon on PATH. Starts a private session bus with fake players.
package mpris

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/sample"
)

type fakePlayer struct {
	conn *dbus.Conn
	mu   sync.Mutex
	play map[string]dbus.Variant
	root map[string]dbus.Variant
}

func (f *fakePlayer) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if iface == playIface {
		return f.play, nil
	}
	return f.root, nil
}

func (f *fakePlayer) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.root
	if iface == playIface {
		m = f.play
	}
	return m[name], nil
}

func (f *fakePlayer) set(changed map[string]dbus.Variant) {
	f.mu.Lock()
	for k, v := range changed {
		f.play[k] = v
	}
	f.mu.Unlock()
	_ = f.conn.Emit(objPath, "org.freedesktop.DBus.Properties.PropertiesChanged", playIface, changed, []string{})
}

func newFake(t *testing.T, addr, name, desktop string, status string, md map[string]dbus.Variant) *fakePlayer {
	t.Helper()
	c, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakePlayer{conn: c,
		play: map[string]dbus.Variant{"PlaybackStatus": dbus.MakeVariant(status), "Metadata": dbus.MakeVariant(md)},
		root: map[string]dbus.Variant{"DesktopEntry": dbus.MakeVariant(desktop)}}
	if err := c.Export(f, objPath, "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatal(err)
	}
	if rep, err := c.RequestName(name, dbus.NameFlagDoNotQueue); err != nil || rep != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request name: %v %v", rep, err)
	}
	return f
}

func TestWatcherAgainstFakePlayers(t *testing.T) {
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Skip("dbus-daemon not available:", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	addr, _ := bufio.NewReader(out).ReadString('\n')
	addr = strings.TrimSpace(addr)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)

	var mu sync.Mutex
	var seen []*sample.Media
	apps, _ := catalog.LoadApps("")
	w := NewWatcher(apps, slog.New(slog.NewTextHandler(io.Discard, nil)), func(m *sample.Media) {
		mu.Lock()
		seen = append(seen, m)
		mu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()
	time.Sleep(300 * time.Millisecond)

	last := func() *sample.Media {
		time.Sleep(300 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		if len(seen) == 0 {
			return nil
		}
		return seen[len(seen)-1]
	}

	// native player: title and artist are sent
	sp := newFake(t, addr, "org.mpris.MediaPlayer2.spotify", "spotify", "Playing", map[string]dbus.Variant{
		"xesam:title": dbus.MakeVariant("Song"), "xesam:artist": dbus.MakeVariant([]string{"Band"}),
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/s/1")),
	})
	if m := last(); m == nil || m.Source != "spotify" || m.Title == nil || *m.Title != "Song" || *m.Artist != "Band" {
		t.Fatalf("native: %+v", m)
	}

	// browser playing YouTube: no title, counts videos; the secret title must never surface
	const secret = "SECRET TAB TITLE"
	ff := newFake(t, addr, "org.mpris.MediaPlayer2.firefox.instance_1_9", "firefox", "Playing", map[string]dbus.Variant{
		"xesam:title": dbus.MakeVariant(secret), "xesam:artist": dbus.MakeVariant([]string{secret}),
		"xesam:url":     dbus.MakeVariant("https://www.youtube.com/watch?v=AAA"),
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/firefox/1")),
	})
	m := last()
	if m == nil || m.Source != "youtube" || m.Videos == nil || *m.Videos != 1 || m.Title != nil || m.Artist != nil {
		t.Fatalf("youtube: %+v", m)
	}
	ff.set(map[string]dbus.Variant{"Metadata": dbus.MakeVariant(map[string]dbus.Variant{
		"xesam:title": dbus.MakeVariant(secret + " 2"), "xesam:url": dbus.MakeVariant("https://www.youtube.com/watch?v=BBB"),
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/firefox/2")),
	})})
	if m = last(); m == nil || m.Videos == nil || *m.Videos != 2 {
		t.Fatalf("second video: %+v", m)
	}

	// non-YouTube browser media
	ff.set(map[string]dbus.Variant{"Metadata": dbus.MakeVariant(map[string]dbus.Variant{
		"xesam:title": dbus.MakeVariant(secret + " 3"), "xesam:url": dbus.MakeVariant("https://example.org/video"),
		"mpris:trackid": dbus.MakeVariant(dbus.ObjectPath("/firefox/3")),
	})})
	if m = last(); m == nil || m.Source != "browser" || m.Videos != nil {
		t.Fatalf("browser: %+v", m)
	}

	// nothing anywhere in the output may contain the secret
	mu.Lock()
	b, _ := json.Marshal(seen)
	mu.Unlock()
	if strings.Contains(string(b), "SECRET") {
		t.Fatalf("browser title leaked into output: %s", b)
	}

	// the player quits: media falls back to the remaining one
	_, _ = ff.conn.ReleaseName("org.mpris.MediaPlayer2.firefox.instance_1_9")
	ff.conn.Close()
	if m = last(); m == nil || m.Source != "spotify" {
		t.Fatalf("after quit: %+v", m)
	}
	_ = sp
}
