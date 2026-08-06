package script

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegistrySelectsIndependentProfiles(t *testing.T) {
	defaultEngine := mustEngine(t, `function onRequest(req){ req.headers["X-Profile"]="default"; }`)
	openAIPath := writeScript(t, `function onRequest(req){ req.headers["X-Profile"]="openai"; }`)
	registry, err := NewRegistry(defaultEngine, []ProfileOptions{{Name: "openai", Path: openAIPath, Timeout: time.Second, Reload: ReloadOff}})
	if err != nil {
		t.Fatal(err)
	}
	if registry.Default() != defaultEngine {
		t.Fatal("default engine was not retained")
	}
	profile, ok := registry.Lookup("openai")
	if !ok || profile == nil || profile == defaultEngine {
		t.Fatal("named profile was not independently loaded")
	}
	if _, ok := registry.Lookup("missing"); ok {
		t.Fatal("unknown profile should not resolve")
	}
	infos := registry.Profiles()
	if len(infos) != 1 || infos[0].Name != "openai" || !infos[0].HasRequest || infos[0].Reload != ReloadOff {
		t.Fatalf("infos=%+v", infos)
	}
}

func TestRegistryConcurrentProfilesDoNotShareRuntimeState(t *testing.T) {
	t.Parallel()

	registry, err := NewRegistry(nil, []ProfileOptions{
		{Name: "a", Path: writeScript(t, `var calls = 0; function onRequest(req){ calls++; req.headers["X-Result"]="a:"+req.headers["X-Id"]+":"+calls; }`)},
		{Name: "b", Path: writeScript(t, `var calls = 0; function onRequest(req){ calls++; req.headers["X-Result"]="b:"+req.headers["X-Id"]+":"+calls; }`)},
	})
	if err != nil {
		t.Fatal(err)
	}

	const requests = 100
	var wg sync.WaitGroup
	errCh := make(chan error, requests*2)
	for _, name := range []string{"a", "b"} {
		engine, _ := registry.Lookup(name)
		for i := 0; i < requests; i++ {
			wg.Add(1)
			go func(profile string, id int) {
				defer wg.Done()
				requestID := fmt.Sprintf("%s-%d", profile, id)
				req := &Request{Header: http.Header{"X-Id": []string{requestID}}}
				if _, err := engine.OnRequest(req); err != nil {
					errCh <- err
					return
				}
				if got := req.Header.Get("X-Result"); !strings.HasPrefix(got, profile+":"+requestID+":") {
					errCh <- fmt.Errorf("profile %s request %s got %q", profile, requestID, got)
				}
			}(name, i)
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestRegistryFailsAtomicallyOnInvalidProfile(t *testing.T) {
	_, err := NewRegistry(nil, []ProfileOptions{
		{Name: "good", Path: writeScript(t, `function onRequest(req){}`)},
		{Name: "bad", Path: writeScript(t, `function onRequest( {`)},
	})
	if err == nil || !strings.Contains(err.Error(), `profile "bad"`) {
		t.Fatalf("error=%v", err)
	}
}

func TestRegistryProfileReloadIsolation(t *testing.T) {
	pathA := writeScript(t, `function onRequest(req){ req.headers["X-Version"]="a1"; }`)
	pathB := writeScript(t, `function onRequest(req){ req.headers["X-Version"]="b1"; }`)
	registry, err := NewRegistry(nil, []ProfileOptions{{Name: "a", Path: pathA}, {Name: "b", Path: pathB}})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := registry.Lookup("a")
	b, _ := registry.Lookup("b")
	if err := os.WriteFile(pathA, []byte(`function onRequest(req){ req.headers["X-Version"]="a2"; }`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(); err != nil {
		t.Fatal(err)
	}
	reqA := &Request{Header: make(map[string][]string)}
	reqB := &Request{Header: make(map[string][]string)}
	_, _ = a.OnRequest(reqA)
	_, _ = b.OnRequest(reqB)
	if reqA.Header.Get("X-Version") != "a2" || reqB.Header.Get("X-Version") != "b1" {
		t.Fatalf("a=%q b=%q", reqA.Header.Get("X-Version"), reqB.Header.Get("X-Version"))
	}
}
