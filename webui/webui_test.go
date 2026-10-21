package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"path":"` + r.URL.Path + `"}`))
	})
}

func testAssets() fs.FS {
	return fstest.MapFS{
		"index.html":        &fstest.MapFile{Data: []byte(`<!doctype html><div id="root"></div>`)},
		"assets/app-abc.js": &fstest.MapFile{Data: []byte(`console.log("ok")`)},
	}
}

func TestMountDelegatesAPI(t *testing.T) {
	handler := mount(testAPI(), testAssets())
	for _, path := range []string{"/v1", "/v1/runs", "/healthz"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"path":"`+path+`"`) {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestMountServesIndexAssetAndSPAFallback(t *testing.T) {
	handler := mount(testAPI(), testAssets())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="root"`) {
		t.Fatalf("index status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("index cache control = %q", got)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("missing security headers: %+v", rec.Header())
	}

	for _, path := range []string{
		"/runs/run_123?tab=events",
		"/sweeps/sweep_20261020T100512.745802720Z",
	} {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="root"`) {
			t.Fatalf("spa fallback %s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app-abc.js", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "console.log") {
		t.Fatalf("asset status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache control = %q", got)
	}
}

func TestMountMissingAssetAndUnbuiltIndex(t *testing.T) {
	handler := mount(testAPI(), testAssets())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset status=%d body=%s", rec.Code, rec.Body.String())
	}

	unbuilt := mount(testAPI(), fstest.MapFS{"placeholder.txt": &fstest.MapFile{Data: []byte("x")}})
	rec = httptest.NewRecorder()
	unbuilt.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runs", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "npm --prefix web run build") {
		t.Fatalf("unbuilt status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMountRejectsUnsupportedMethod(t *testing.T) {
	handler := mount(testAPI(), testAssets())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status=%d body=%s", rec.Code, rec.Body.String())
	}
}
