// Command preview-lab starts the development-only response preview workbench.
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

//go:embed static fixtures.json fixture-assets
var labFS embed.FS

type fixture struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Target      string `json:"target"`
	StatusLine  string `json:"statusLine"`
	Headers     string `json:"headers"`
	Encoding    string `json:"encoding"`
	Body        string `json:"body"`
	Truncated   bool   `json:"truncated"`
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8091", "listen address")
	root := flag.String("root", "", "repository root (auto-detected by default)")
	flag.Parse()

	repoRoot, err := resolveRepoRoot(*root)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := newHandler(repoRoot)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("preview lab: http://%s", *listen)
	log.Fatal(http.ListenAndServe(*listen, handler))
}

func resolveRepoRoot(explicit string) (string, error) {
	if explicit != "" {
		return validateRepoRoot(explicit)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if root, err := validateRepoRoot(dir); err == nil {
			return root, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("preview-lab: repository root not found; use -root")
		}
		dir = parent
	}
}

func validateRepoRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	for _, path := range []string{"go.mod", filepath.Join("internal", "web", "static", "preview", "viewer.mjs")} {
		if _, err := os.Stat(filepath.Join(abs, path)); err != nil {
			return "", fmt.Errorf("preview-lab: invalid repository root %q: %w", abs, err)
		}
	}
	return abs, nil
}

func newHandler(repoRoot string) (http.Handler, error) {
	if _, err := validateFixtures(); err != nil {
		return nil, err
	}
	static, err := fs.Sub(labFS, "static")
	if err != nil {
		return nil, err
	}
	fixtureAssets, err := fs.Sub(labFS, "fixture-assets")
	if err != nil {
		return nil, err
	}
	previewDir := filepath.Join(repoRoot, "internal", "web", "static", "preview")
	sharedStyle := filepath.Join(repoRoot, "internal", "web", "static", "style.css")

	mux := http.NewServeMux()
	mux.Handle("GET /preview/", http.StripPrefix("/preview/", http.FileServer(http.Dir(previewDir))))
	mux.Handle("GET /fixture-assets/", http.StripPrefix("/fixture-assets/", http.FileServerFS(fixtureAssets)))
	mux.HandleFunc("GET /fixtures.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		data, _ := labFS.ReadFile("fixtures.json")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /shared-style.css", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, sharedStyle)
	})
	mux.Handle("GET /", http.FileServerFS(static))
	return mux, nil
}

func validateFixtures() ([]fixture, error) {
	data, err := labFS.ReadFile("fixtures.json")
	if err != nil {
		return nil, err
	}
	var fixtures []fixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		return nil, fmt.Errorf("preview-lab: decode fixtures: %w", err)
	}
	if len(fixtures) == 0 {
		return nil, errors.New("preview-lab: no fixtures")
	}
	for index, item := range fixtures {
		if item.Name == "" || item.StatusLine == "" || (item.Encoding != "text" && item.Encoding != "base64") {
			return nil, fmt.Errorf("preview-lab: invalid fixture %d (%q)", index, item.Name)
		}
	}
	return fixtures, nil
}
