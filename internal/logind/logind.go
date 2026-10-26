// Package logind watches suspend, resume, shutdown and the lid through
// systemd-logind on the system D-Bus.
package logind

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	dest     = "org.freedesktop.login1"
	path     = dbus.ObjectPath("/org/freedesktop/login1")
	managerI = "org.freedesktop.login1.Manager"
)

// Events are the callbacks Run invokes. OnSleep and OnShutdown run while a
// delay inhibitor is held, so they may block briefly (a few seconds) to flush
// a final sample before the machine goes down.
type Events struct {
	OnSleep    func()
	OnResume   func()
	OnShutdown func()
	OnLid      func(closed bool)
}

// LidFromProps extracts LidClosed from a PropertiesChanged payload.
func LidFromProps(changed map[string]dbus.Variant) (closed, ok bool) {
	v, found := changed["LidClosed"]
	if !found {
		return false, false
	}
	b, isBool := v.Value().(bool)
	return b, isBool
}

// Run connects to logind and dispatches events until ctx ends. It returns an
// error right away if the system bus or logind is unavailable.
func Run(ctx context.Context, log *slog.Logger, ev Events) error {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return fmt.Errorf("system bus: %w", err)
	}
	defer conn.Close()
	mgr := conn.Object(dest, path)

	for _, m := range []string{"PrepareForSleep", "PrepareForShutdown"} {
		if err := conn.AddMatchSignal(dbus.WithMatchObjectPath(path), dbus.WithMatchInterface(managerI), dbus.WithMatchMember(m)); err != nil {
			return err
		}
	}
	if err := conn.AddMatchSignal(dbus.WithMatchObjectPath(path), dbus.WithMatchInterface("org.freedesktop.DBus.Properties"), dbus.WithMatchMember("PropertiesChanged")); err != nil {
		return err
	}
	sigs := make(chan *dbus.Signal, 16)
	conn.Signal(sigs)
	defer conn.RemoveSignal(sigs)

	var inhibitor *os.File
	take := func() {
		if inhibitor != nil {
			return
		}
		var fd dbus.UnixFD
		call := mgr.Call(managerI+".Inhibit", 0, "sleep:shutdown", "desk-agent", "Send a final status before sleeping", "delay")
		if call.Err != nil {
			log.Warn("cannot take sleep inhibitor; the final sample may not get out before suspend", "err", call.Err)
			return
		}
		if err := call.Store(&fd); err != nil {
			log.Warn("inhibitor reply unreadable", "err", err)
			return
		}
		inhibitor = os.NewFile(uintptr(fd), "desk-agent-inhibitor")
	}
	release := func() {
		if inhibitor != nil {
			inhibitor.Close()
			inhibitor = nil
		}
	}
	defer release()
	take()

	if closed, err := LidClosed(conn); err == nil && ev.OnLid != nil {
		ev.OnLid(closed)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case s, ok := <-sigs:
			if !ok {
				return errors.New("system bus connection closed")
			}
			switch s.Name {
			case managerI + ".PrepareForSleep":
				if len(s.Body) == 0 {
					continue
				}
				if going, _ := s.Body[0].(bool); going {
					if ev.OnSleep != nil {
						ev.OnSleep()
					}
					release() // let the suspend proceed
				} else {
					take()
					if ev.OnResume != nil {
						ev.OnResume()
					}
					// The lid may have been opened while asleep.
					if closed, err := LidClosed(conn); err == nil && ev.OnLid != nil {
						ev.OnLid(closed)
					}
				}
			case managerI + ".PrepareForShutdown":
				if len(s.Body) > 0 {
					if going, _ := s.Body[0].(bool); going {
						if ev.OnShutdown != nil {
							ev.OnShutdown()
						}
						release()
					}
				}
			case "org.freedesktop.DBus.Properties.PropertiesChanged":
				if len(s.Body) < 2 {
					continue
				}
				if iface, _ := s.Body[0].(string); iface != managerI {
					continue
				}
				changed, _ := s.Body[1].(map[string]dbus.Variant)
				if closed, ok := LidFromProps(changed); ok && ev.OnLid != nil {
					ev.OnLid(closed)
				}
			}
		}
	}
}

// LidClosed reads the current lid state.
func LidClosed(conn *dbus.Conn) (bool, error) {
	v, err := conn.Object(dest, path).GetProperty(managerI + ".LidClosed")
	if err != nil {
		return false, err
	}
	b, ok := v.Value().(bool)
	if !ok {
		return false, errors.New("LidClosed is not a bool")
	}
	return b, nil
}

// Probe is used by doctor: it reports whether logind answers and the lid
// state (which is false on machines without a lid switch).
func Probe() (lidClosed bool, err error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	done := make(chan struct{})
	var closed bool
	go func() { closed, err = LidClosed(conn); close(done) }()
	select {
	case <-done:
		return closed, err
	case <-time.After(3 * time.Second):
		return false, errors.New("logind did not answer")
	}
}
