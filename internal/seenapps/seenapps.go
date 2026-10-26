// Package seenapps remembers, locally, which window classes have been focused.
// scan-apps uses it to build apps.toml for the apps that were really used.
// Only window classes (such as "kitty" or "org.mozilla.firefox") are stored,
// never titles, and the file never leaves the machine.
package seenapps

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Seen struct {
	Count int       `json:"count"`
	Last  time.Time `json:"last"`
}

type Store struct {
	Path string
	Now  func() time.Time

	mu    sync.Mutex
	m     map[string]*Seen
	dirty bool
}

// Open loads the store; a missing or unreadable file starts empty.
func Open(path string) *Store {
	s := &Store{Path: path, Now: time.Now, m: map[string]*Seen{}}
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &s.m)
		if s.m == nil {
			s.m = map[string]*Seen{}
		}
	}
	return s
}

// Note records that class was focused. Empty classes are ignored.
func (s *Store) Note(class string) {
	if class == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.m[class]
	if e == nil {
		e = &Seen{}
		s.m[class] = e
	}
	e.Count++
	e.Last = s.Now()
	s.dirty = true
}

// All returns a copy of everything seen.
func (s *Store) All() map[string]Seen {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Seen, len(s.m))
	for k, v := range s.m {
		out[k] = *v
	}
	return out
}

// Flush writes the store if anything changed (atomically, mode 0600).
func (s *Store) Flush() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	data, err := json.Marshal(s.m)
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Read loads a store file without keeping a handle (used by scan-apps).
func Read(path string) (map[string]Seen, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]Seen{}, nil
		}
		return nil, err
	}
	m := map[string]Seen{}
	return m, json.Unmarshal(data, &m)
}
