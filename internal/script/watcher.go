package script

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ReloadMode selects how an Engine watches its script file for changes.
type ReloadMode int

const (
	// ReloadOff disables hot-reload; the script is loaded once at startup.
	ReloadOff ReloadMode = iota
	// ReloadWatch uses filesystem notifications (fsnotify).
	ReloadWatch
	// ReloadPoll periodically stats the file's modification time.
	ReloadPoll
)

// DefaultPollInterval is the stat cadence used by ReloadPoll when no interval
// is supplied.
const DefaultPollInterval = time.Second

func (m ReloadMode) String() string {
	switch m {
	case ReloadWatch:
		return "watch"
	case ReloadPoll:
		return "poll"
	default:
		return "off"
	}
}

// ParseReloadMode parses a CLI value into a ReloadMode. An empty string
// defaults to ReloadWatch.
func ParseReloadMode(raw string) (ReloadMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "watch":
		return ReloadWatch, nil
	case "poll":
		return ReloadPoll, nil
	case "off", "none", "false":
		return ReloadOff, nil
	default:
		return ReloadOff, fmt.Errorf("invalid reload mode %q (want watch, poll, or off)", raw)
	}
}

// Watch starts watching the engine's script file and calls Reload on change.
// onReload, if non-nil, is invoked after each reload attempt with its error
// (nil on success) so callers can log the outcome. It returns a stop function;
// calling it ends watching and releases resources. For ReloadOff it is a no-op.
func (e *Engine) Watch(mode ReloadMode, interval time.Duration, onReload func(error)) (stop func(), err error) {
	if e == nil || e.source != "" || mode == ReloadOff {
		return func() {}, nil
	}
	if onReload == nil {
		onReload = func(error) {}
	}

	switch mode {
	case ReloadPoll:
		if interval <= 0 {
			interval = DefaultPollInterval
		}
		return e.watchPoll(interval, onReload), nil
	default:
		return e.watchFS(onReload)
	}
}

// watchPoll stats the script file on a ticker and reloads when mtime changes.
func (e *Engine) watchPoll(interval time.Duration, onReload func(error)) func() {
	done := make(chan struct{})
	last := fileModTime(e.path)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				mod := fileModTime(e.path)
				if mod.Equal(last) {
					continue
				}
				last = mod
				onReload(e.Reload())
			}
		}
	}()

	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// watchFS reloads on filesystem events. It watches the parent directory rather
// than the file itself so editor "atomic save" (write-temp + rename) is caught
// even though it replaces the file's inode.
func (e *Engine) watchFS(onReload func(error)) (func(), error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("create file watcher: %w", err)
	}

	dir := filepath.Dir(e.path)
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("watch %q: %w", dir, err)
	}

	target := filepath.Clean(e.path)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case event, ok := <-w.Events:
				if !ok {
					return
				}
				if filepath.Clean(event.Name) != target {
					continue
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
					continue
				}
				onReload(e.Reload())
			case _, ok := <-w.Errors:
				if !ok {
					return
				}
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			_ = w.Close()
		})
	}, nil
}

// fileModTime returns the file's modification time, or the zero time if it
// cannot be stat'd (e.g. mid-rename).
func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
