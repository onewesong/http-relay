package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewModulesAreEmbedded(t *testing.T) {
	handler, _ := New(Meta{})
	for _, path := range []string{"/preview/core.mjs", "/preview/viewer.mjs"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, recorder.Code)
		}
		if !strings.Contains(recorder.Header().Get("Content-Type"), "javascript") {
			t.Fatalf("GET %s content type = %q", path, recorder.Header().Get("Content-Type"))
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !strings.Contains(recorder.Body.String(), `<script type="module" src="app.js"></script>`) {
		t.Fatal("index does not load app.js as an ES module")
	}
}
