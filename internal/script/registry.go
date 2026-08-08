package script

import (
	"fmt"
	"io"
	"sort"
	"time"
)

// ProfileOptions configures one named rewrite profile.
type ProfileOptions struct {
	Name    string
	Path    string
	Source  string
	Timeout time.Duration
	Reload  ReloadMode
	Console io.Writer
}

type registeredProfile struct {
	engine *Engine
	reload ReloadMode
}

// ProfileInfo is safe metadata used by startup logs and diagnostics.
type ProfileInfo struct {
	Name        string
	Path        string
	Timeout     time.Duration
	Reload      ReloadMode
	HasRequest  bool
	HasResponse bool
}

// Registry owns the legacy default engine and all configured named engines.
// Its profile map is immutable after construction; individual engines publish
// hot-reloaded versions atomically.
type Registry struct {
	defaultEngine *Engine
	profiles      map[string]registeredProfile
}

func NewRegistry(defaultEngine *Engine, profiles []ProfileOptions) (*Registry, error) {
	r := &Registry{defaultEngine: defaultEngine, profiles: make(map[string]registeredProfile, len(profiles))}
	for _, profile := range profiles {
		if _, exists := r.profiles[profile.Name]; exists {
			return nil, fmt.Errorf("duplicate rewrite profile %q", profile.Name)
		}
		engine, err := New(Options{Path: profile.Path, Source: profile.Source, Timeout: profile.Timeout, Console: profile.Console})
		if err != nil {
			return nil, fmt.Errorf("load rewrite profile %q: %w", profile.Name, err)
		}
		if engine == nil {
			return nil, fmt.Errorf("rewrite profile %q has an empty script path", profile.Name)
		}
		r.profiles[profile.Name] = registeredProfile{engine: engine, reload: profile.Reload}
	}
	return r, nil
}

func (r *Registry) Default() *Engine {
	if r == nil {
		return nil
	}
	return r.defaultEngine
}

func (r *Registry) Lookup(profile string) (*Engine, bool) {
	if r == nil {
		return nil, false
	}
	entry, ok := r.profiles[profile]
	return entry.engine, ok
}

func (r *Registry) Profiles() []ProfileInfo {
	if r == nil {
		return nil
	}
	out := make([]ProfileInfo, 0, len(r.profiles))
	for name, profile := range r.profiles {
		out = append(out, ProfileInfo{
			Name: name, Path: profile.engine.path, Timeout: profile.engine.timeout, Reload: profile.reload,
			HasRequest: profile.engine.HasRequestHook(), HasResponse: profile.engine.HasResponseHook(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// WatchAll starts the default and named watchers as one lifecycle. If any
// watcher cannot start, already-started watchers are stopped before returning.
func (r *Registry) WatchAll(defaultReload ReloadMode, onReload func(profile string, err error)) (func(), error) {
	if r == nil {
		return func() {}, nil
	}
	if onReload == nil {
		onReload = func(string, error) {}
	}
	stops := make([]func(), 0, len(r.profiles)+1)
	start := func(name string, engine *Engine, mode ReloadMode) error {
		if engine == nil {
			return nil
		}
		stop, err := engine.Watch(mode, 0, func(err error) { onReload(name, err) })
		if err != nil {
			return err
		}
		stops = append(stops, stop)
		return nil
	}
	if err := start("", r.defaultEngine, defaultReload); err != nil {
		return nil, fmt.Errorf("watch default rewrite script: %w", err)
	}
	for _, info := range r.Profiles() {
		profile := r.profiles[info.Name]
		if err := start(info.Name, profile.engine, profile.reload); err != nil {
			for _, stop := range stops {
				stop()
			}
			return nil, fmt.Errorf("watch rewrite profile %q: %w", info.Name, err)
		}
	}
	return func() {
		for _, stop := range stops {
			stop()
		}
	}, nil
}
