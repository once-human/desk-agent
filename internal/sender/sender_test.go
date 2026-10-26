package sender

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
