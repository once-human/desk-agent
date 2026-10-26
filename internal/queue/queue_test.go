package queue

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/once-human/desk-agent/internal/sample"
)

func mk(t time.Time) sample.Sample { return sample.Sample{T: sample.Format(t, 0), Active: true} }

func TestAppendPeekDropFIFO(t *testing.T) {
	q := New(filepath.Join(t.TempDir(), "q.jsonl"))
	now := time.Now()
	for i := 0; i < 5; i++ {
		if err := q.Append(mk(now.Add(time.Duration(i) * time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := q.Peek(3)
	if len(got) != 3 || got[0].T != mk(now).T {
		t.Fatalf("peek %v", got)
	}
	if err := q.Drop(3); err != nil {
		t.Fatal(err)
	}
	if q.Len() != 2 {
		t.Fatalf("len %d", q.Len())
	}
}

func TestCapKeepsNewest(t *testing.T) {
	q := New(filepath.Join(t.TempDir(), "q.jsonl"))
	q.MaxLines = 3
	now := time.Now()
	for i := 0; i < 6; i++ {
		_ = q.Append(mk(now.Add(time.Duration(i) * time.Second)))
	}
	got, _ := q.Peek(10)
	if len(got) != 3 || got[0].T != mk(now.Add(3*time.Second)).T {
		t.Fatalf("%v", got)
	}
}

func TestExpiredAreDropped(t *testing.T) {
	q := New(filepath.Join(t.TempDir(), "q.jsonl"))
	now := time.Now()
	_ = q.Append(mk(now.Add(-8 * 24 * time.Hour))) // too old: never stored
	_ = q.Append(mk(now))
	if q.Len() != 1 {
		t.Fatalf("len %d", q.Len())
	}
	q.Now = func() time.Time { return now.Add(8 * 24 * time.Hour) } // time passes
	if q.Len() != 0 {
		t.Fatalf("expired entry survived: %d", q.Len())
	}
}

func TestMissingFileIsEmpty(t *testing.T) {
	q := New(filepath.Join(t.TempDir(), "nope", "q.jsonl"))
	if q.Len() != 0 {
		t.Fatal("expected empty")
	}
	if got, err := q.Peek(5); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}
