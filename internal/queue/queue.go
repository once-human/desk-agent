// Package queue is the offline buffer: a capped, age-limited JSON-lines file.
package queue

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/once-human/desk-agent/internal/sample"
)

const (
	DefaultMaxLines = 5000
	DefaultMaxAge   = 7 * 24 * time.Hour
)

// Queue is safe for concurrent use inside one process.
type Queue struct {
	Path     string
	MaxLines int
	MaxAge   time.Duration
	Now      func() time.Time

	mu      sync.Mutex
	appends int
}

// trimEvery is how many appends pass between full rewrites that enforce the
// age and size limits.
const trimEvery = 100

func New(path string) *Queue {
	return &Queue{Path: path, MaxLines: DefaultMaxLines, MaxAge: DefaultMaxAge, Now: time.Now}
}

// Append adds s, then trims the file to the cap and age limits (oldest
// first). A sample that is already too old is silently dropped, since the
// server would reject it.
func (q *Queue) Append(s sample.Sample) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	// Adding a line is a single small write. The file is only re-read and
	// trimmed every so often (reads trim as well), so a sample every 30
	// seconds doesn't rewrite the whole file each time.
	q.appends++
	if q.appends%trimEvery != 0 {
		if err := os.MkdirAll(filepath.Dir(q.Path), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(q.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, werr := f.Write(append(b, '\n'))
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		return werr
	}
	lines, err := q.readLocked()
	if err != nil {
		return err
	}
	lines = append(lines, b)
	return q.writeLocked(q.trim(lines))
}

// Peek returns up to n of the oldest queued samples without removing them.
func (q *Queue) Peek(n int) ([]sample.Sample, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	lines, err := q.readLocked()
	if err != nil {
		return nil, err
	}
	lines = q.trim(lines)
	if n > len(lines) {
		n = len(lines)
	}
	out := make([]sample.Sample, 0, n)
	for _, l := range lines[:n] {
		var s sample.Sample
		if json.Unmarshal(l, &s) == nil {
			out = append(out, s)
		}
	}
	return out, nil
}

// Drop removes the n oldest entries (after a successful send).
func (q *Queue) Drop(n int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	lines, err := q.readLocked()
	if err != nil {
		return err
	}
	lines = q.trim(lines)
	if n > len(lines) {
		n = len(lines)
	}
	return q.writeLocked(lines[n:])
}

// Len is the number of live entries.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	lines, err := q.readLocked()
	if err != nil {
		return 0
	}
	return len(q.trim(lines))
}

// trim drops unparsable and expired lines, then enforces MaxLines by
// discarding the oldest.
func (q *Queue) trim(lines [][]byte) [][]byte {
	cutoff := q.Now().Add(-q.MaxAge)
	kept := lines[:0:0]
	for _, l := range lines {
		var s sample.Sample
		if json.Unmarshal(l, &s) != nil {
			continue
		}
		t, err := s.Time()
		if err != nil || t.Before(cutoff) {
			continue
		}
		kept = append(kept, l)
	}
	if q.MaxLines > 0 && len(kept) > q.MaxLines {
		kept = kept[len(kept)-q.MaxLines:]
	}
	return kept
}

func (q *Queue) readLocked() ([][]byte, error) {
	f, err := os.Open(q.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var lines [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		l := bytes.TrimSpace(sc.Bytes())
		if len(l) == 0 {
			continue
		}
		lines = append(lines, append([]byte(nil), l...))
	}
	return lines, sc.Err()
}

func (q *Queue) writeLocked(lines [][]byte) error {
	if len(lines) == 0 {
		if err := os.Remove(q.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(q.Path), 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, l := range lines {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	tmp := q.Path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, q.Path)
}
