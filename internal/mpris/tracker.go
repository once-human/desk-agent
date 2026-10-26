// Package mpris turns MPRIS media-player state into the sample's media field,
// applying the privacy rules:
//
//   - Browser players: title and artist are never extracted, stored or sent.
//   - YouTube (detected from the player's URL host, or from the browser
//     extension reporting an audible YouTube tab): source "youtube", no
//     title/artist, plus a count of videos played in the current session.
//   - Native music players: title, artist and state are sent.
package mpris

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/once-human/desk-agent/internal/sample"
)

// SessionGap is how long YouTube must be silent before the next video starts
// a new session (and resets the counter).
const SessionGap = 20 * time.Minute

// browserKeywords identify browser-backed MPRIS players by bus-name id.
var browserKeywords = []string{
	"firefox", "chromium", "chrome", "brave", "zen", "librewolf", "floorp", "vivaldi",
	"edge", "opera", "waterfox", "thorium", "tor-browser", "mullvad", "plasma-browser",
}

// PlayerID derives a short id from an MPRIS bus name:
// "org.mpris.MediaPlayer2.firefox.instance_1_23" -> "firefox".
func PlayerID(busName string) string {
	id := strings.TrimPrefix(busName, "org.mpris.MediaPlayer2.")
	if i := strings.Index(id, ".instance"); i >= 0 {
		id = id[:i]
	}
	return strings.ToLower(id)
}

// IsBrowser decides whether a player is a browser. desktopCategory is the
// app catalog's category for the player's DesktopEntry (may be empty).
func IsBrowser(id, desktopCategory string) bool {
	if desktopCategory == "browser" {
		return true
	}
	for _, k := range browserKeywords {
		if strings.Contains(id, k) {
			return true
		}
	}
	return false
}

// IsYouTubeURL reports whether rawURL's host is YouTube. Only the host is
// ever looked at.
func IsYouTubeURL(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	for _, p := range []string{"www.", "m.", "music."} {
		h = strings.TrimPrefix(h, p)
	}
	return h == "youtube.com" || h == "youtu.be" || h == "youtube-nocookie.com"
}

// IsYouTubeArt reports whether a cover-art URL is served by YouTube's image
// hosts (i.ytimg.com and friends).
func IsYouTubeArt(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "ytimg.com" || strings.HasSuffix(h, ".ytimg.com") || strings.HasSuffix(h, ".ggpht.com")
}

// Player is what the D-Bus layer reports for one player. For browsers,
// Title and Artist are always empty: they are never extracted.
type Player struct {
	Bus     string // bus name, the map key
	ID      string
	Browser bool
	Status  string // "Playing", "Paused", "Stopped"
	// TrackKey is an opaque, salted hash of trackid+url, used only to notice
	// that the track changed. It is not reversible and not persisted.
	TrackKey string
	YouTube  bool // player URL host is YouTube
	Title    string
	Artist   string
	Changed  time.Time
}

// NewSalt returns a random per-process salt for TrackKey.
func NewSalt() []byte {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b
}

// MakeTrackKey hashes the identifiers with salt.
func MakeTrackKey(salt []byte, trackID, rawURL string) string {
	if trackID == "" && rawURL == "" {
		return ""
	}
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(trackID))
	h.Write([]byte{0})
	h.Write([]byte(rawURL))
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// ytSession counts videos in a YouTube viewing session.
type ytSession struct {
	videos   int
	lastKey  string
	lastPlay time.Time
	playing  bool
}

func (y *ytSession) observe(now time.Time, key string, playing bool) {
	if !playing {
		if y.playing {
			y.lastPlay = now
		}
		y.playing = false
		return
	}
	if y.videos > 0 && !y.playing && now.Sub(y.lastPlay) > SessionGap {
		*y = ytSession{} // a new session
	}
	if y.videos == 0 || key != y.lastKey {
		y.videos++
		y.lastKey = key
	}
	y.playing = true
	y.lastPlay = now
}

// Tracker holds all players and the YouTube session. Safe for concurrent use.
type Tracker struct {
	Now func() time.Time

	mu       sync.Mutex
	players  map[string]*Player
	yt       ytSession
	ytAudibl bool // extension says an audible, non-private YouTube tab exists
}

func NewTracker() *Tracker {
	return &Tracker{Now: time.Now, players: map[string]*Player{}}
}

// Update records the latest state of a player and returns the media to report.
func (t *Tracker) Update(p Player) *sample.Media {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.Now()
	if old, ok := t.players[p.Bus]; ok && old.Status == p.Status && old.TrackKey == p.TrackKey && old.Title == p.Title && old.Artist == p.Artist {
		p.Changed = old.Changed
	} else {
		p.Changed = now
	}
	t.players[p.Bus] = &p
	t.observeLocked(&p, now)
	return t.mediaLocked()
}

// Remove drops a player that left the bus.
func (t *Tracker) Remove(bus string) *sample.Media {
	t.mu.Lock()
	defer t.mu.Unlock()
	if p, ok := t.players[bus]; ok {
		if t.isYouTubeLocked(p) {
			t.yt.observe(t.Now(), "", false)
		}
		delete(t.players, bus)
	}
	return t.mediaLocked()
}

// SetYouTubeAudible is fed by the browser extension: true while a non-private
// tab on a YouTube host is making sound.
func (t *Tracker) SetYouTubeAudible(v bool) *sample.Media {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ytAudibl = v
	now := t.Now()
	for _, p := range t.players {
		t.observeLocked(p, now)
	}
	return t.mediaLocked()
}

func (t *Tracker) isYouTubeLocked(p *Player) bool {
	if !p.Browser {
		return false
	}
	return p.YouTube || (t.ytAudibl && p.Status == "Playing")
}

func (t *Tracker) observeLocked(p *Player, now time.Time) {
	if t.isYouTubeLocked(p) {
		t.yt.observe(now, p.TrackKey, p.Status == "Playing")
	}
}

// Media returns what to report right now, or nil.
func (t *Tracker) Media() *sample.Media {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.mediaLocked()
}

func (t *Tracker) mediaLocked() *sample.Media {
	var best *Player
	for _, p := range t.players {
		if p.Status != "Playing" && p.Status != "Paused" {
			continue
		}
		if best == nil || better(p, best) {
			best = p
		}
	}
	if best == nil {
		return nil
	}
	// One video often shows up twice (the browser's own player and a
	// browser-integration plugin); only one of them may carry the URL. If any
	// browser player knows it's YouTube and is in the same state, it's that.
	if best.Browser && !t.isYouTubeLocked(best) {
		for _, p := range t.players {
			if p.Browser && p.Status == best.Status && t.isYouTubeLocked(p) {
				best = p
				break
			}
		}
	}
	state := strings.ToLower(best.Status)
	switch {
	case t.isYouTubeLocked(best):
		return &sample.Media{Player: best.ID, State: state, Source: "youtube", Videos: sample.Int(t.yt.videos)}
	case best.Browser:
		return &sample.Media{Player: best.ID, State: state, Source: "browser"}
	default:
		m := &sample.Media{Player: best.ID, State: state, Source: best.ID}
		if best.Title != "" {
			m.Title = sample.Str(best.Title)
		}
		if best.Artist != "" {
			m.Artist = sample.Str(best.Artist)
		}
		return m
	}
}

// better prefers a playing player over a paused one, then the most recently
// changed.
func better(a, b *Player) bool {
	if (a.Status == "Playing") != (b.Status == "Playing") {
		return a.Status == "Playing"
	}
	return a.Changed.After(b.Changed)
}
