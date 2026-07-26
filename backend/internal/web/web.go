// Package web serves the built frontend from inside the binary, so a release is
// one file and there is no static directory to point at or get wrong.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// dist is populated by `npm run build`; the placeholder keeps `go build`
// working in a checkout where the frontend has not been built yet.
//
//go:embed all:dist
var dist embed.FS

// Handler serves the SPA, falling back to index.html so client-side routes
// survive a reload.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic("embedded frontend is missing: " + err.Error())
	}
	// The dist directory ships with a placeholder so `go build` works in a fresh
	// checkout. Without this check that placeholder builds a binary that serves a
	// directory listing instead of the panel, and nothing says why.
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		panic("no frontend was embedded: build frontend/ and copy dist/ into internal/web/dist before go build")
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path != "/" {
			f, err := sub.Open(strings.TrimPrefix(r.URL.Path, "/"))
			if err != nil {
				serveIndex(w, r, sub)
				return
			}
			_ = f.Close()
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	raw, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(raw)
}
