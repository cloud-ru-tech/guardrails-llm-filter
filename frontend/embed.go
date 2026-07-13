// Package frontend embeds the management console SPA (a React + Vite build) and
// serves it with a single-page-application fallback. This lets the console ship
// inside the guardrails-llm-filter binary and be served from the same origin as
// the management REST API, so no separate web server or CORS setup is needed.
package frontend

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// distFS holds the built console. `npm run build` writes ./dist; a committed
// dist/.gitkeep keeps this embed valid before the UI is built (a bare `go build`
// with no Node step), in which case only the placeholder is present and
// Available reports false. The `all:` prefix is required so the leading-dot
// placeholder is matched.
//
//go:embed all:dist
var distFS embed.FS

var (
	// distSub is distFS rooted at the dist directory.
	distSub fs.FS
	// available is true when a real build (index.html) is embedded.
	available bool
	// indexHTML is the cached SPA entry document served for client-side routes.
	indexHTML []byte
)

func init() {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return
	}
	distSub = sub
	if data, err := fs.ReadFile(sub, "index.html"); err == nil {
		indexHTML = data
		available = true
	}
}

// Available reports whether a real console build is embedded (index.html
// present). It is false for a bare `go build` where only dist/.gitkeep exists,
// letting the service start with the API but no console.
func Available() bool { return available }

// Handler serves the embedded console. Existing files (index.html, hashed
// assets, fonts) are served as-is; any other path returns index.html so the
// client-side router resolves routes such as /overview and /rules. When no
// build is embedded it responds 404 for every path.
func Handler() http.Handler {
	if !available {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "console not built", http.StatusNotFound)
		})
	}
	fileServer := http.FileServerFS(distSub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && fileExists(name) {
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndex(w, r)
	})
}

// fileExists reports whether name is a regular (non-directory) file in the
// embedded build.
func fileExists(name string) bool {
	f, err := distSub.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	return err == nil && !info.IsDir()
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Zero modtime disables Last-Modified/If-Modified-Since negotiation; the SPA
	// shell is tiny and always references content-hashed assets.
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(indexHTML))
}
