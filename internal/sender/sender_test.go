package sender

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/once-human/desk-agent/internal/queue"
	"github.com/once-human/desk-agent/internal/sample"
)

func newSender(t *testing.T, url string, key func() (string, error)) *Sender {
	t.Helper()
	q := queue.New(filepath.Join(t.TempDir(), "q.jsonl"))
	return New(url, "test", key, q, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func okKey() (string, error) { return "k", nil }

func TestStatsRecordEachOutcome(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	defer srv.Close()
	s := newSender(t, srv.URL, okKey)
	smp := sample.Sample{T: "2026-10-26T10:00:00Z", Active: true}

	if st := s.Stats(); st.Result != "" || !st.LastOK.IsZero() {
		t.Fatalf("fresh: %+v", st)
	}
	s.Send(context.Background(), smp)
	st := s.Stats()
	if st.Result != "ok" || st.Status != 200 || st.LastOK.IsZero() || st.Queued != 0 {
		t.Fatalf("ok: %+v", st)
	}
	lastOK := st.LastOK

	// After a failure the sender backs off (later samples queue without a
	// request), so each failure mode is checked on its own sender.
	status = http.StatusUnauthorized
	s401 := newSender(t, srv.URL, okKey)
	s401.Send(context.Background(), smp)
	if st := s401.Stats(); st.Result != "unauthorized" || st.Status != 401 || !st.LastOK.IsZero() || st.Queued != 1 {
		t.Fatalf("401 must not set LastOK and must keep the sample: %+v", st)
	}

	status = http.StatusBadGateway
	s502 := newSender(t, srv.URL, okKey)
	s502.Send(context.Background(), smp)
	if st := s502.Stats(); st.Result == "ok" || st.Status != 502 || st.Queued != 1 {
		t.Fatalf("502: %+v", st)
	}
	_ = lastOK
}

func TestStatsNoKeyAndNetwork(t *testing.T) {
	s := newSender(t, "http://127.0.0.1:1", func() (string, error) { return "", errors.New("no key") })
	s.Send(context.Background(), sample.Sample{T: "2026-10-26T10:00:00Z"})
	if st := s.Stats(); st.Result != "nokey" {
		t.Fatalf("%+v", st)
	}
	s2 := newSender(t, "http://127.0.0.1:1", okKey)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	s2.Send(ctx, sample.Sample{T: "2026-10-26T10:00:00Z"})
	st := s2.Stats()
	if st.Result != "network" || st.Status != 0 || st.Queued != 1 {
		t.Fatalf("unreachable endpoint should queue: %+v", st)
	}
}

func TestBatchingHoldsSamplesUntilDue(t *testing.T) {
	var bodies, lastNext, got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b sample.Batch
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies.Add(1)
		got.Add(int64(len(b.Samples)))
		lastNext.Store(int64(b.NextS))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s := newSender(t, srv.URL, okKey)
	s.SetEvery(15 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	mk := func(n int) sample.Sample {
		return sample.Sample{T: time.Date(2026, 10, 26, 10, 0, n*30, 0, time.UTC).Format(time.RFC3339), Active: true}
	}
	// The first sample goes out at once; the next ones wait for the next upload.
	s.Send(ctx, mk(0))
	waitFor(t, func() bool { return got.Load() == 1 })
	s.Send(ctx, mk(1))
	s.Send(ctx, mk(2))
	time.Sleep(150 * time.Millisecond)
	if bodies.Load() != 1 || s.Stats().Queued != 2 {
		t.Fatalf("held samples were uploaded early: bodies=%d queued=%d", bodies.Load(), s.Stats().Queued)
	}
	if lastNext.Load() != 900 {
		t.Fatalf("next_s = %d, want 900", lastNext.Load())
	}
	// The final sample takes everything waiting with it and says nothing more is coming.
	s.SendFinal(mk(3), 2*time.Second)
	if got.Load() != 4 || lastNext.Load() != 0 || s.Stats().Queued != 0 {
		t.Fatalf("final flush: got=%d next=%d queued=%d", got.Load(), lastNext.Load(), s.Stats().Queued)
	}
	// After that the next sample (lid opening) goes straight out again.
	s.Send(ctx, mk(4))
	waitFor(t, func() bool { return got.Load() == 5 })
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestBatchingUploadsEarlyWhenYouWalkAway(t *testing.T) {
	var got atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b sample.Batch
		_ = json.NewDecoder(r.Body).Decode(&b)
		got.Add(int64(len(b.Samples)))
	}))
	defer srv.Close()
	s := newSender(t, srv.URL, okKey)
	s.SetEvery(15 * time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)
	mk := func(n int, active bool) sample.Sample {
		return sample.Sample{T: time.Date(2026, 10, 26, 10, 0, n*30, 0, time.UTC).Format(time.RFC3339), Active: active}
	}
	s.Send(ctx, mk(0, true))
	waitFor(t, func() bool { return got.Load() == 1 })
	// Right after an upload a flip waits (the gap), so nothing goes out yet.
	s.Send(ctx, mk(1, false))
	time.Sleep(100 * time.Millisecond)
	if got.Load() != 1 {
		t.Fatalf("flip uploaded inside the minimum gap: %d", got.Load())
	}
	// Once the gap has passed, the next flip goes out at once.
	s.mu.Lock()
	s.lastFlush = time.Now().Add(-6 * time.Minute)
	s.mu.Unlock()
	s.Send(ctx, mk(2, true))
	waitFor(t, func() bool { return got.Load() == 3 })
}
