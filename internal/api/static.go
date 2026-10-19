package api

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Static frontend hosting.
//
// Lanory ships as a single Go binary, so the built React app is embedded with
// go:embed when web/dist exists at build time and served from that embedded
// filesystem. When it does not exist, the directory is served from disk instead
// (--web-dir / server.web_dir), which is what a developer running `make dev`
// wants. When neither is present, a small placeholder page is served so the
// service still starts and the API is still usable.
//
// Client-side routes (the React router owns /topology, /devices, ...) must fall
// back to index.html: a hard refresh on /devices would otherwise 404.

// webDistRoot is the directory holding the frontend that is embedded into the
// binary. It ships with a README so the embed pattern always compiles, even on
// a fresh clone where the frontend has not been built; in that state the
// embedded filesystem contains no index.html and the server falls back to
// server.web_dir and finally to a placeholder page.
//
// The real build is copied here by:
//
//	make web && cp -r web/dist/. internal/api/webapp/
//
// An explicit --web-dir always takes precedence, so a developer running
// `go run ./cmd/lanory --web-dir web/dist` gets the freshly built assets.
const webDistRoot = "webapp"

// webDist is the embedded frontend.
//
//go:embed webapp
var webDist embed.FS

// staticAssets serves the frontend, choosing the source at construction time:
//
//  1. Embedded web/dist (release build).
//  2. ServerConfig.WebDir on disk (development).
//  3. A built-in placeholder page.
//
// The choice is made once so a request never pays for filesystem probing.
type staticAssets struct {
	// source describes where files come from: "embedded", "disk" or "placeholder".
	source string
	// fsys serves files when source is embedded or disk.
	fsys http.FileSystem
	// indexHTML is the SPA entry point, read once at construction.
	indexHTML []byte
}

// newStaticAssets builds the asset server for the given directory.
//
// An empty dir means "use the embedded copy if there is one".
func newStaticAssets(dir string) *staticAssets {
	// An explicit directory always wins, even when it is empty: the operator
	// asked for that directory, and silently serving something else would be
	// confusing.
	if dir != "" {
		index, err := os.ReadFile(filepath.Join(dir, "index.html"))
		if err != nil {
			return &staticAssets{source: "placeholder", indexHTML: []byte(placeholderHTML)}
		}
		return &staticAssets{source: "disk", fsys: http.Dir(dir), indexHTML: index}
	}

	if embedded, err := fs.Sub(webDist, webDistRoot); err == nil {
		if index, err := fs.ReadFile(embedded, "index.html"); err == nil {
			return &staticAssets{source: "embedded", fsys: http.FS(embedded), indexHTML: index}
		}
	}

	return &staticAssets{source: "placeholder", indexHTML: []byte(placeholderHTML)}
}

// HasFrontend reports whether a real frontend is being served.
func (s *staticAssets) HasFrontend() bool { return s.source != "placeholder" }

// Source returns "embedded", "disk" or "placeholder".
func (s *staticAssets) Source() string { return s.source }

// Handler returns an http.Handler for the frontend.
//
// Paths that do not match a file fall back to index.html so the client side
// router can resolve them. Requests that look like file downloads (they carry a
// file extension) are answered with a real 404 instead: falling back to HTML
// for a missing /assets/app.js would break the browser's module loader with a
// confusing MIME error.
func (s *staticAssets) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			fail(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		// Reject traversal on the raw path before any cleaning: filepath.Clean
		// would happily turn "/../etc/passwd" into "etc/passwd", which then
		// resolves inside the asset root and looks legitimate.
		raw := strings.TrimPrefix(r.URL.Path, "/")
		for _, seg := range strings.Split(raw, "/") {
			if seg == ".." {
				notFound(w, "file")
				return
			}
		}
		// Reject encoded traversal (%2e%2e) that the router did not decode.
		if strings.Contains(strings.ToLower(raw), "%2e%2e") {
			notFound(w, "file")
			return
		}

		clean := filepath.Clean(raw)
		if clean == "." || clean == "/" || clean == "" {
			clean = "index.html"
		}
		// Defence in depth: a cleaned path must still be inside the root.
		if strings.HasPrefix(clean, "..") || strings.Contains(clean, "../") {
			notFound(w, "file")
			return
		}

		if s.fsys != nil && fileExists(s.fsys, clean) {
			w.Header().Set("Cache-Control", cacheControlFor(clean))
			http.FileServer(s.fsys).ServeHTTP(w, r)
			return
		}

		// index.html is the SPA entry point, not an asset: a build that only
		// ships the API still serves the placeholder for it. A genuinely
		// missing asset (with a file extension) is a real 404 so the browser's
		// module loader gets a clear failure instead of HTML.
		if clean != "index.html" && looksLikeAsset(clean) {
			notFound(w, "file")
			return
		}

		// SPA fallback.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(s.indexHTML)
	})
}

// fileExists reports whether path is a regular file inside fsys.
func fileExists(fsys http.FileSystem, path string) bool {
	f, err := fsys.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	return true
}

// looksLikeAsset reports whether a path names a file rather than a route.
func looksLikeAsset(path string) bool {
	return filepath.Ext(path) != ""
}

// cacheControlFor picks a cache policy.
//
// Vite fingerprints everything in /assets, so those files can be cached for a
// year. index.html must never be cached, otherwise a deploy is not picked up
// until the browser is restarted.
func cacheControlFor(path string) string {
	if path == "index.html" {
		return "no-cache, no-store, must-revalidate"
	}
	if strings.HasPrefix(path, "assets/") || strings.HasPrefix(path, "assets\\") {
		return "public, max-age=31536000, immutable"
	}
	return "public, max-age=3600"
}

// placeholderHTML is served when no frontend was built. It states the fact
// plainly and points at the API instead of pretending the UI is there.
const placeholderHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Lanory</title>
<style>
  :root { color-scheme: light dark; }
  body {
    margin: 0; min-height: 100vh; display: grid; place-items: center;
    font: 15px/1.6 ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif;
    background: #0b0f14; color: #e6edf3;
  }
  main { max-width: 34rem; padding: 2rem; }
  h1 { font-size: 1.4rem; margin: 0 0 .5rem; letter-spacing: -.01em; }
  p { margin: .5rem 0; color: #9aa7b4; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .9em;
         background: #161c24; padding: .15rem .35rem; border-radius: 4px; }
  a { color: #58a6ff; }
</style>
</head>
<body>
<main>
  <h1>Lanory is running</h1>
  <p>The web frontend has not been built into this binary, so only the API is available.</p>
  <p>Build it with <code>make web</code> (from the <code>web/</code> directory) and restart,
     or point the server at a build directory with <code>--web-dir</code>.</p>
  <p>Meanwhile, try <a href="/api/v1/health">/api/v1/health</a>.</p>
</main>
</body>
</html>
`
