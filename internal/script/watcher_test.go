package script

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseReloadMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    ReloadMode
		wantErr bool
	}{
		{"", ReloadWatch, false},
		{"watch", ReloadWatch, false},
		{"poll", ReloadPoll, false},
		{"off", ReloadOff, false},
		{"none", ReloadOff, false},
		{"bogus", ReloadOff, true},
	}
	for _, tt := range tests {
		got, err := ParseReloadMode(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseReloadMode(%q): expected error", tt.in)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("ParseReloadMode(%q)=(%v,%v) want %v", tt.in, got, err, tt.want)
		}
	}
}

func TestWatch_OffIsNoop(t *testing.T) {
	t.Parallel()

	e := mustEngine(t, `function onRequest(req){}`)
	stop, err := e.Watch(ReloadOff, 0, nil)
	if err != nil {
		t.Fatalf("Watch(off): %v", err)
	}
	stop() // must not panic / must be safe
	stop() // idempotent
}

// reflectsVersion runs onRequest and reports the X-Ver header the script set.
func reflectsVersion(t *testing.T, e *Engine) string {
	t.Helper()
	r := &Request{Method: "GET", URL: "https://e/", Header: http.Header{}}
	if _, err := e.OnRequest(r); err != nil {
		t.Fatalf("OnRequest: %v", err)
	}
	return r.Header.Get("X-Ver")
}

func TestWatch_PollReloadsOnChange(t *testing.T) {
	t.Parallel()

	path := writeScript(t, `function onRequest(req){ req.headers["X-Ver"]="v1"; }`)
	e, err := New(Options{Path: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	reloaded := make(chan error, 4)
	stop, err := e.Watch(ReloadPoll, 20*time.Millisecond, func(err error) { reloaded <- err })
	if err != nil {
		t.Fatalf("Watch(poll): %v", err)
	}
	defer stop()

	overwrite(t, path, `function onRequest(req){ req.headers["X-Ver"]="v2"; }`)
	// Guarantee a detectable mtime change regardless of filesystem resolution.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	select {
	case err := <-reloaded:
		if err != nil {
			t.Fatalf("reload error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("poll watcher did not reload in time")
	}

	if v := reflectsVersion(t, e); v != "v2" {
		t.Fatalf("poll reload not effective: %q", v)
	}
}

func TestWatch_FSNotifyReloadsOnChange(t *testing.T) {
	t.Parallel()

	path := writeScript(t, `function onRequest(req){ req.headers["X-Ver"]="v1"; }`)
	e, err := New(Options{Path: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	reloaded := make(chan error, 4)
	stop, err := e.Watch(ReloadWatch, 0, func(err error) { reloaded <- err })
	if err != nil {
		t.Fatalf("Watch(watch): %v", err)
	}
	defer stop()

	overwrite(t, path, `function onRequest(req){ req.headers["X-Ver"]="v2"; }`)

	select {
	case err := <-reloaded:
		if err != nil {
			t.Fatalf("reload error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fsnotify watcher did not reload in time")
	}

	if v := reflectsVersion(t, e); v != "v2" {
		t.Fatalf("fsnotify reload not effective: %q", v)
	}
}

func TestWatch_FSNotifyCatchesAtomicRename(t *testing.T) {
	t.Parallel()

	path := writeScript(t, `function onRequest(req){ req.headers["X-Ver"]="v1"; }`)
	e, err := New(Options{Path: path})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	reloaded := make(chan error, 4)
	stop, err := e.Watch(ReloadWatch, 0, func(err error) { reloaded <- err })
	if err != nil {
		t.Fatalf("Watch(watch): %v", err)
	}
	defer stop()

	// Editor-style atomic save: write a sibling temp file, then rename over.
	tmp := filepath.Join(filepath.Dir(path), "relay.js.tmp")
	if err := os.WriteFile(tmp, []byte(`function onRequest(req){ req.headers["X-Ver"]="v2"; }`), 0o600); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("rename: %v", err)
	}

	select {
	case err := <-reloaded:
		if err != nil {
			t.Fatalf("reload error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fsnotify watcher did not catch atomic rename")
	}

	if v := reflectsVersion(t, e); v != "v2" {
		t.Fatalf("atomic-rename reload not effective: %q", v)
	}
}
