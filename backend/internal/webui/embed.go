// Package webui serves the built SPA. The frontend's `pnpm build` writes its
// output into ./dist (relative to this file) and go:embed pulls it into the
// binary at compile time.
//
// At runtime, Handler serves /index.html for any path that doesn't resolve to
// a real file, so the client-side router can take over (SPA fallback).
package webui

import (
	"io/fs"
	"net/http"
	"strings"

	"embed"
)

//go:embed all:dist
var distFS embed.FS

// fsRoot returns dist/ as an fs.FS (so paths look like /index.html, /assets/*).
func fsRoot() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("webui: missing dist/ subdir — did you run `pnpm build`? " + err.Error())
	}
	return sub
}

// Handler serves static assets from the embedded dist with SPA fallback:
// unknown paths (no file match) return /index.html so React Router can
// handle them. /api/* is excluded by the caller's mux.
func Handler() http.Handler {
	root := fsRoot()
	fileServer := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(root, clean); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// Not a real file → SPA fallback.
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
