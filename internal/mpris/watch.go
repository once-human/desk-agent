package mpris

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/once-human/desk-agent/internal/catalog"
	"github.com/once-human/desk-agent/internal/sample"
)

const (
	busPrefix = "org.mpris.MediaPlayer2."
	objPath   = dbus.ObjectPath("/org/mpris/MediaPlayer2")
	rootIface = "org.mpris.MediaPlayer2"
	playIface = "org.mpris.MediaPlayer2.Player"
)

// Watcher follows every MPRIS player on the session bus.
type Watcher struct {
	Apps    *catalog.Apps
	Log     *slog.Logger
	OnMedia func(*sample.Media)

	Tracker *Tracker
	salt    []byte

	mu     sync.Mutex
	owners map[string]string // unique bus name -> well-known MPRIS name
	meta   map[string]*Player
}

func NewWatcher(apps *catalog.Apps, log *slog.Logger, onMedia func(*sample.Media)) *Watcher {
	return &Watcher{
		Apps: apps, Log: log, OnMedia: onMedia,
		Tracker: NewTracker(), salt: NewSalt(),
		owners: map[string]string{}, meta: map[string]*Player{},
	}
}

// SetYouTubeAudible forwards the browser extension's audible-tab hint.
func (w *Watcher) SetYouTubeAudible(v bool) { w.OnMedia(w.Tracker.SetYouTubeAudible(v)) }

// Run blocks until ctx ends. It returns an error if the session bus is
// unavailable.
func (w *Watcher) Run(ctx context.Context) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("session bus: %w", err)
	}
	defer conn.Close()

	if err := conn.AddMatchSignal(
		dbus.WithMatchSender("org.freedesktop.DBus"),
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
	); err != nil {
		return err
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(objPath),
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
	); err != nil {
		return err
	}
	sigs := make(chan *dbus.Signal, 32)
	conn.Signal(sigs)
	defer conn.RemoveSignal(sigs)

	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return err
	}
	for _, n := range names {
		if strings.HasPrefix(n, busPrefix) {
			w.add(conn, n)
		}
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case s, ok := <-sigs:
			if !ok {
				return errors.New("session bus connection closed")
			}
			switch s.Name {
			case "org.freedesktop.DBus.NameOwnerChanged":
				if len(s.Body) < 3 {
					continue
				}
				name, _ := s.Body[0].(string)
				newOwner, _ := s.Body[2].(string)
				if !strings.HasPrefix(name, busPrefix) {
					continue
				}
				if newOwner == "" {
					w.remove(name)
				} else {
					w.add(conn, name)
				}
			case "org.freedesktop.DBus.Properties.PropertiesChanged":
				w.onProps(conn, s)
			}
		}
	}
}

func (w *Watcher) add(conn *dbus.Conn, name string) {
	var owner string
	if err := conn.BusObject().Call("org.freedesktop.DBus.GetNameOwner", 0, name).Store(&owner); err != nil {
		return
	}
	w.mu.Lock()
	w.owners[owner] = name
	w.mu.Unlock()
	w.refresh(conn, name)
}

func (w *Watcher) remove(name string) {
	w.mu.Lock()
	for u, n := range w.owners {
		if n == name {
			delete(w.owners, u)
		}
	}
	delete(w.meta, name)
	w.mu.Unlock()
	w.OnMedia(w.Tracker.Remove(name))
}

// refresh re-reads a whole player (used on appearance).
func (w *Watcher) refresh(conn *dbus.Conn, name string) {
	obj := conn.Object(name, objPath)
	var all map[string]dbus.Variant
	if err := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, playIface).Store(&all); err != nil {
		return
	}
	entry := ""
	if v, err := obj.GetProperty(rootIface + ".DesktopEntry"); err == nil {
		entry, _ = v.Value().(string)
	}
	id := PlayerID(name)
	cat := ""
	if entry != "" {
		cat = w.Apps.Lookup(entry).Category
	}
	p := &Player{Bus: name, ID: id, Browser: IsBrowser(id, cat)}
	w.mu.Lock()
	w.meta[name] = p
	w.mu.Unlock()
	w.apply(name, all)
}

func (w *Watcher) onProps(conn *dbus.Conn, s *dbus.Signal) {
	if len(s.Body) < 2 {
		return
	}
	if iface, _ := s.Body[0].(string); iface != playIface {
		return
	}
	changed, _ := s.Body[1].(map[string]dbus.Variant)
	w.mu.Lock()
	name := w.owners[s.Sender]
	w.mu.Unlock()
	if name == "" {
		return
	}
	w.apply(name, changed)
}

// apply merges changed properties into the player and pushes the result.
func (w *Watcher) apply(name string, props map[string]dbus.Variant) {
	w.mu.Lock()
	p := w.meta[name]
	if p == nil {
		w.mu.Unlock()
		return
	}
	if v, ok := props["PlaybackStatus"]; ok {
		p.Status, _ = v.Value().(string)
	}
	if v, ok := props["Metadata"]; ok {
		md, _ := v.Value().(map[string]dbus.Variant)
		w.readMetadata(p, md)
	}
	cp := *p
	w.mu.Unlock()
	w.OnMedia(w.Tracker.Update(cp))
}

// readMetadata extracts only what each player kind is allowed to expose.
// For browsers that is the track id and URL (to spot YouTube and track
// changes); xesam:title and xesam:artist are not looked at. The D-Bus message
// itself carries them, but they are never copied out of it.
func (w *Watcher) readMetadata(p *Player, md map[string]dbus.Variant) {
	trackID := stringOf(md["mpris:trackid"])
	if p.Browser {
		rawURL := stringOf(md["xesam:url"])
		p.YouTube = IsYouTubeURL(rawURL)
		p.TrackKey = MakeTrackKey(w.salt, trackID, rawURL)
		p.Title, p.Artist = "", ""
		return
	}
	p.TrackKey = MakeTrackKey(w.salt, trackID, "")
	p.Title = stringOf(md["xesam:title"])
	p.Artist = stringOf(md["xesam:artist"])
}

// stringOf flattens the string-ish MPRIS value types.
func stringOf(v dbus.Variant) string {
	switch x := v.Value().(type) {
	case string:
		return x
	case dbus.ObjectPath:
		return string(x)
	case []string:
		return strings.Join(x, ", ")
	}
	return ""
}

// Probe is used by doctor: it lists players as "id (browser|native, status)"
// without reading any metadata.
func Probe(apps *catalog.Apps) ([]string, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return nil, err
	}
	var out []string
	for _, n := range names {
		if !strings.HasPrefix(n, busPrefix) {
			continue
		}
		obj := conn.Object(n, objPath)
		status := "?"
		if v, err := obj.GetProperty(playIface + ".PlaybackStatus"); err == nil {
			status, _ = v.Value().(string)
		}
		cat := ""
		if v, err := obj.GetProperty(rootIface + ".DesktopEntry"); err == nil {
			if e, _ := v.Value().(string); e != "" {
				cat = apps.Lookup(e).Category
			}
		}
		id := PlayerID(n)
		kind := "native"
		if IsBrowser(id, cat) {
			kind = "browser"
		}
		out = append(out, fmt.Sprintf("%s (%s, %s)", id, kind, strings.ToLower(status)))
	}
	return out, nil
}
