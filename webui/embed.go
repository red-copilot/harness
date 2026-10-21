// Package webui serves the embedded observability single-page application.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:dist
var embedded embed.FS

// Mount wraps the API handler with the embedded SPA. API and health routes are
// delegated unchanged; every other route is served by the frontend.
func Mount(api http.Handler) http.Handler {
	assets, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic("webui: embedded dist is unavailable: " + err.Error())
	}
	return mount(api, assets)
}

func mount(api http.Handler, assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	if api == nil {
		api = http.NotFoundHandler()
	}
	mux.Handle("/v1", api)
	mux.Handle("/v1/", api)
	mux.Handle("/healthz", api)
	mux.Handle("/", spaHandler{assets: assets})
	return mux
}

type spaHandler struct {
	assets fs.FS
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	index, err := fs.ReadFile(h.assets, "index.html")
	if err != nil {
		serveUnbuilt(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		serveAsset(w, r, "index.html", index, "no-store")
		return
	}
	if info, err := fs.Stat(h.assets, name); err == nil && !info.IsDir() {
		data, readErr := fs.ReadFile(h.assets, name)
		if readErr != nil {
			http.NotFound(w, r)
			return
		}
		cache := "no-cache"
		if strings.HasPrefix(name, "assets/") {
			cache = "public, max-age=31536000, immutable"
		}
		serveAsset(w, r, name, data, cache)
		return
	}
	if looksLikeAsset(name) {
		http.NotFound(w, r)
		return
	}
	serveAsset(w, r, "index.html", index, "no-store")
}

func serveAsset(w http.ResponseWriter, r *http.Request, name string, data []byte, cacheControl string) {
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", cacheControl)
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func serveUnbuilt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(`<!doctype html><html><body style="font-family:system-ui;padding:2rem"><h1>Observability UI is not built</h1><p>Run <code>npm --prefix web run build</code> and rebuild <code>cmd/harness</code>.</p></body></html>`))
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
}

func looksLikeAsset(name string) bool {
	if strings.HasPrefix(name, "assets/") {
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs", ".css", ".map", ".json", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".woff", ".woff2", ".ttf", ".eot", ".txt", ".html":
		return true
	default:
		return false
	}
}
