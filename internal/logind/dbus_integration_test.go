//go:build dbustest

// Run with: go test -tags dbustest ./internal/logind
// Starts a private bus with a fake org.freedesktop.login1.
package logind

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type fakeManager struct {
	mu       sync.Mutex
	inhibits int
	readEnds []*os.File // closing our write end makes these read EOF
	lid      bool
}

func (f *fakeManager) Inhibit(what, who, why, mode string) (dbus.UnixFD, *dbus.Error) {
	r, w, _ := os.Pipe()
	f.mu.Lock()
	f.inhibits++
	f.readEnds = append(f.readEnds, r)
	f.mu.Unlock()
	// The reply carries a duplicate of the fd. Close this process's copy once
	// it has been sent, so the pipe reaches EOF when the client closes theirs.
	time.AfterFunc(200*time.Millisecond, func() { w.Close() })
	return dbus.UnixFD(w.Fd()), nil
}

type fakeProps struct{ m *fakeManager }

func (p fakeProps) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	p.m.mu.Lock()
	defer p.m.mu.Unlock()
	return dbus.MakeVariant(p.m.lid), nil
}

func TestRunHandlesSleepResumeShutdownAndLid(t *testing.T) {
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--print-address=1")
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Skip("dbus-daemon not available:", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	addr, _ := bufio.NewReader(out).ReadString('\n')
	addr = strings.TrimSpace(addr)
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", addr)

	svc, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	fm := &fakeManager{}
	if err := svc.Export(fm, path, managerI); err != nil {
		t.Fatal(err)
	}
	if err := svc.Export(fakeProps{fm}, path, "org.freedesktop.DBus.Properties"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RequestName(dest, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var log []string
	rec := func(s string) { mu.Lock(); log = append(log, s); mu.Unlock() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = Run(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), Events{
			OnSleep:    func() { rec("sleep") },
			OnResume:   func() { rec("resume") },
			OnShutdown: func() { rec("shutdown") },
			OnLid: func(c bool) {
				if c {
					rec("lid:closed")
				} else {
					rec("lid:open")
				}
			},
		})
	}()
	wait := func() { time.Sleep(300 * time.Millisecond) }
	got := func() string { mu.Lock(); defer mu.Unlock(); return strings.Join(log, ",") }
	emit := func(sig string, v any) {
		_ = svc.Emit(path, managerI+"."+sig, v)
		wait()
	}

	wait()
	if got() != "lid:open" {
		t.Fatalf("initial: %q", got())
	}
	fm.mu.Lock()
	if fm.inhibits != 1 {
		t.Fatalf("expected a delay inhibitor at start, got %d", fm.inhibits)
	}
	first := fm.readEnds[0]
	fm.mu.Unlock()

	emit("PrepareForSleep", true)
	if !strings.HasSuffix(got(), "sleep") {
		t.Fatalf("sleep not delivered: %q", got())
	}
	// the inhibitor must be released after OnSleep so suspend can proceed
	_ = first.SetReadDeadline(time.Now().Add(time.Second))
	if n, err := first.Read(make([]byte, 1)); n != 0 || err != io.EOF {
		t.Fatalf("inhibitor not released: n=%d err=%v", n, err)
	}

	emit("PrepareForSleep", false)
	if !strings.Contains(got(), "sleep,resume") {
		t.Fatalf("resume not delivered: %q", got())
	}
	fm.mu.Lock()
	if fm.inhibits != 2 {
		t.Fatalf("inhibitor not retaken after resume: %d", fm.inhibits)
	}
	fm.mu.Unlock()

	_ = svc.Emit(path, "org.freedesktop.DBus.Properties.PropertiesChanged", managerI,
		map[string]dbus.Variant{"LidClosed": dbus.MakeVariant(true)}, []string{})
	wait()
	if !strings.HasSuffix(got(), "lid:closed") {
		t.Fatalf("lid change not delivered: %q", got())
	}

	emit("PrepareForShutdown", true)
	if !strings.HasSuffix(got(), "shutdown") {
		t.Fatalf("shutdown not delivered: %q", got())
	}
}
