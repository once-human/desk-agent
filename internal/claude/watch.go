package claude

import (
	"context"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Watcher calls OnChange whenever the hook state file changes, and again when
// "working" expires on its own (no stop event within Window). It uses inotify,
// so it costs nothing while Claude Code is not in use.
type Watcher struct {
	Path     string
	OnChange func(working bool, cwd string)
	Now      func() time.Time
}

// Run blocks until ctx ends. The state directory must exist.
func (w *Watcher) Run(ctx context.Context) error {
	if w.Now == nil {
		w.Now = time.Now
	}
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), "inotify") // non-blocking: Close() unblocks Read
	defer f.Close()
	if _, err := unix.InotifyAddWatch(fd, filepath.Dir(w.Path), unix.IN_MOVED_TO|unix.IN_CLOSE_WRITE|unix.IN_CREATE|unix.IN_DELETE); err != nil {
		return err
	}

	events := make(chan struct{}, 1)
	go func() {
		base := filepath.Base(w.Path)
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if err != nil {
				close(events)
				return
			}
			for off := 0; off+unix.SizeofInotifyEvent <= n; {
				ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
				nameBytes := buf[off+unix.SizeofInotifyEvent : off+unix.SizeofInotifyEvent+int(ev.Len)]
				name := string(nameBytes)
				if i := indexNUL(name); i >= 0 {
					name = name[:i]
				}
				if name == base {
					select {
					case events <- struct{}{}:
					default:
					}
				}
				off += unix.SizeofInotifyEvent + int(ev.Len)
			}
		}
	}()

	var timer *time.Timer
	var timerC <-chan time.Time
	evaluate := func() {
		working, cwd, expires := Read(w.Path).Evaluate(w.Now())
		w.OnChange(working, cwd)
		if timer != nil {
			timer.Stop()
			timer, timerC = nil, nil
		}
		if working {
			timer = time.NewTimer(time.Until(expires) + 100*time.Millisecond)
			timerC = timer.C
		}
	}
	evaluate()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return os.ErrClosed
			}
			evaluate()
		case <-timerC:
			evaluate()
		}
	}
}

func indexNUL(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return i
		}
	}
	return -1
}
