// Package sender POSTs samples to the site and keeps an offline queue.
package sender

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/once-human/desk-agent/internal/queue"
	"github.com/once-human/desk-agent/internal/sample"
)

const (
	maxBatch   = 500
	minBackoff = 2 * time.Second
	maxBackoff = 60 * time.Second
	// flipGap is the shortest gap between uploads triggered by a change of
	// active/idle rather than by the schedule.
	flipGap = 5 * time.Minute
)

type outcome int

const (
	sent    outcome = iota
	retry           // network error, 5xx, 429, 401: keep the data
	discard         // other 4xx: the server will never accept this batch
)

// Sender delivers samples. It keeps one keep-alive connection open and falls
// back to the queue whenever a send fails.
type Sender struct {
	Endpoint  string
	UserAgent string
	// Key returns the current site key. It is called for every request so a
	// rotated key is picked up without a restart.
	Key func() (string, error)

	Queue  *queue.Queue
	Client *http.Client
	Log    *slog.Logger

	mu      sync.Mutex
	offline bool
	wake    chan struct{}
	stats   Stats

	// every is how often samples are delivered. 0 sends each one as it
	// happens. A longer gap lets the site's database sleep in between.
	every     time.Duration
	lastFlush time.Time
	// lastActive is the previous buffered sample's active flag; a change
	// (you sat down, you walked away) is worth an early upload.
	lastActive *bool
	// flushMu keeps one reader of the queue at a time (peek, post, drop).
	flushMu sync.Mutex
}

// SetEvery sets how often samples are uploaded; 0 means immediately. Safe to
// call while running (a config reload).
func (s *Sender) SetEvery(d time.Duration) {
	s.mu.Lock()
	s.every = d
	s.mu.Unlock()
	if d <= 0 {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

func (s *Sender) batching() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.every, s.every > 0
}

// Stats describes the most recent delivery attempts, for the GUI and status.
type Stats struct {
	LastAttempt time.Time `json:"lastAttempt"`
	LastOK      time.Time `json:"lastOK"`
	// Result is "ok", "network", "unauthorized", "busy", "rejected" or "nokey".
	Result string `json:"result"`
	// Status is the last HTTP status, 0 when the request never got an answer.
	Status int `json:"status"`
	Queued int `json:"queued"`
}

// Stats returns a snapshot of delivery health.
func (s *Sender) Stats() Stats {
	s.mu.Lock()
	st := s.stats
	s.mu.Unlock()
	st.Queued = s.Queue.Len()
	return st
}

func (s *Sender) record(result string, status int) {
	s.mu.Lock()
	now := time.Now()
	s.stats.LastAttempt, s.stats.Result, s.stats.Status = now, result, status
	if result == "ok" {
		s.stats.LastOK = now
	}
	s.mu.Unlock()
}

// New builds a Sender with sane transport settings.
func New(endpoint, ua string, key func() (string, error), q *queue.Queue, log *slog.Logger) *Sender {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConns:          1,
		ForceAttemptHTTP2:     true,
	}
	return &Sender{
		Endpoint: endpoint, UserAgent: ua, Key: key, Queue: q, Log: log,
		Client: &http.Client{Transport: tr, Timeout: 12 * time.Second},
		wake:   make(chan struct{}, 1),
	}
}

// Send delivers s now, or queues it if offline or the send fails.
func (s *Sender) Send(ctx context.Context, smp sample.Sample) {
	if every, ok := s.batching(); ok {
		s.buffer(smp, every)
		return
	}
	s.mu.Lock()
	off := s.offline
	s.mu.Unlock()
	if off {
		s.enqueue(smp)
		return
	}
	switch s.post(ctx, []sample.Sample{smp}, 0) {
	case sent:
	case retry:
		s.enqueue(smp)
	case discard:
	}
}

// SendFinal is for the last sample before suspend or shutdown. It blocks up
// to timeout so the packet leaves before the machine sleeps; on failure it
// queues the sample like any other.
func (s *Sender) SendFinal(smp sample.Sample, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, ok := s.batching(); ok {
		// Everything waiting goes out with it, and nothing more is coming.
		if err := s.Queue.Append(smp); err != nil {
			s.Log.Error("queue append failed", "err", err)
			return
		}
		if s.flushMu.TryLock() {
			defer s.flushMu.Unlock()
			for ctx.Err() == nil {
				n, o, err := s.flushLocked(ctx, 0)
				if err != nil || n == 0 || o == retry {
					break
				}
			}
			// The next sample (the lid opening, say) goes out right away.
			s.mu.Lock()
			s.lastFlush = time.Time{}
			s.mu.Unlock()
		}
		return
	}
	if s.post(ctx, []sample.Sample{smp}, 0) == retry {
		s.enqueue(smp)
	}
}

// buffer holds a sample for the next upload and wakes the flusher when one
// is due (the first sample after starting, or every's worth since the last).
func (s *Sender) buffer(smp sample.Sample, every time.Duration) {
	if err := s.Queue.Append(smp); err != nil {
		s.Log.Error("queue append failed", "err", err)
		return
	}
	s.mu.Lock()
	since := time.Since(s.lastFlush)
	flipped := s.lastActive != nil && *s.lastActive != smp.Active
	a := smp.Active
	s.lastActive = &a
	// Due on the schedule, or because you sat down / walked away, but never
	// more often than every few minutes so the database can still sleep.
	due := s.lastFlush.IsZero() || since >= every || (flipped && since >= flipGap)
	s.mu.Unlock()
	if due {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// flushLocked uploads the oldest batch and drops it once the site has it.
// The caller holds flushMu. n is how many samples were in the batch.
func (s *Sender) flushLocked(ctx context.Context, nextS int) (n int, o outcome, err error) {
	batch, err := s.Queue.Peek(maxBatch)
	if err != nil {
		return 0, retry, err
	}
	if len(batch) == 0 {
		return 0, sent, nil
	}
	o = s.post(ctx, batch, nextS)
	if o == sent || o == discard {
		_ = s.Queue.Drop(len(batch))
	}
	if o == sent {
		s.mu.Lock()
		s.lastFlush = time.Now()
		s.mu.Unlock()
	}
	return len(batch), o, nil
}

func (s *Sender) enqueue(smp sample.Sample) {
	if err := s.Queue.Append(smp); err != nil {
		s.Log.Error("queue append failed", "err", err)
	}
	s.mu.Lock()
	s.offline = true
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run is the background flusher. Call it once, in its own goroutine. It also
// drains whatever a previous run left in the queue.
func (s *Sender) Run(ctx context.Context) {
	if s.Queue.Len() > 0 {
		s.mu.Lock()
		s.offline = true
		s.mu.Unlock()
		select {
		case s.wake <- struct{}{}:
		default: // a wake-up is already waiting
		}
	}
	backoff := minBackoff
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		}
		for ctx.Err() == nil {
			every, _ := s.batching()
			next := int(every / time.Second)
			s.flushMu.Lock()
			n, o, err := s.flushLocked(ctx, next)
			s.flushMu.Unlock()
			if err != nil {
				s.Log.Error("queue read failed", "err", err)
				break
			}
			if n == 0 {
				_ = s.Queue.Drop(maxBatch) // clears expired leftovers
				s.mu.Lock()
				s.offline = false
				s.mu.Unlock()
				backoff = minBackoff
				break
			}
			switch o {
			case sent, discard:
				backoff = minBackoff
			case retry:
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				if backoff *= 2; backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
	}
}

func (s *Sender) post(ctx context.Context, batch []sample.Sample, nextS int) outcome {
	key, err := s.Key()
	if err != nil {
		s.Log.Error("no site key", "err", err)
		s.record("nokey", 0)
		return retry
	}
	body, err := json.Marshal(sample.Batch{Samples: batch, NextS: nextS})
	if err != nil {
		s.Log.Error("encode failed", "err", err)
		return discard
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(body))
	if err != nil {
		s.Log.Error("bad endpoint", "err", err)
		return discard
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", s.UserAgent)

	resp, err := s.Client.Do(req)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.Log.Debug("send failed, will retry", "err", errString(err))
		}
		s.record("network", 0)
		return retry
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)) // let the connection be reused

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		s.record("ok", resp.StatusCode)
		return sent
	case resp.StatusCode == http.StatusUnauthorized:
		s.Log.Error("site rejected the key (401); run `desk-agent set-key`")
		s.record("unauthorized", resp.StatusCode)
		return retry
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		s.Log.Warn("site busy", "status", resp.StatusCode)
		s.record("busy", resp.StatusCode)
		return retry
	default:
		s.Log.Error("site refused a batch, dropping it", "status", resp.StatusCode, "samples", len(batch))
		s.record("rejected", resp.StatusCode)
		return discard
	}
}

// errString avoids logging a URL: url.Error includes it.
func errString(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	var oe *net.OpError
	if errors.As(err, &oe) {
		return fmt.Sprintf("%s %s", oe.Op, oe.Err)
	}
	return "network error"
}
