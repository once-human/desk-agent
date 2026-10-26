package seenapps

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNoteFlushReopen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", "seen.json")
	s := Open(p)
	s.Note("kitty")
	s.Note("kitty")
	s.Note("")
	s.Note("org.mozilla.firefox")
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	got, err := Read(p)
	if err != nil || got["kitty"].Count != 2 || got["org.mozilla.firefox"].Count != 1 || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	s2 := Open(p)
	s2.Note("kitty")
	if s2.All()["kitty"].Count != 3 {
		t.Error("counts must continue across restarts")
	}
	if m, err := Read(filepath.Join(t.TempDir(), "missing.json")); err != nil || len(m) != 0 {
		t.Errorf("missing file: %v %v", m, err)
	}
}
