package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/leafvmaple/zreader/internal/webui"
)

// newRouter wires all HTTP routes. /api/v1/* serves JSON; everything else
// falls through to the embedded SPA (with SPA-style index.html fallback).
func (s *Server) newRouter() http.Handler {
	mux := http.NewServeMux()

	// Health + identity
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})
	mux.HandleFunc("GET /api/v1/info", func(w http.ResponseWriter, r *http.Request) {
		u := currentUser(r)
		writeJSON(w, http.StatusOK, map[string]any{
			"app":     "zreader",
			"version": s.cfg.Version,
			"time":    time.Now().UTC().Format(time.RFC3339),
			"user":    map[string]string{"id": u.ID},
		})
	})

	// Library folders + scan
	mux.HandleFunc("GET /api/v1/library/folders", s.handleListFolders)
	mux.HandleFunc("POST /api/v1/library/folders", s.handleAddFolder)
	mux.HandleFunc("DELETE /api/v1/library/folders/{id}", s.handleDeleteFolder)
	mux.HandleFunc("POST /api/v1/library/scan", s.handleScan)
	mux.HandleFunc("POST /api/v1/library/upload", s.handleUploadBooks)

	// Books
	mux.HandleFunc("GET /api/v1/books", s.handleListBooks)
	mux.HandleFunc("GET /api/v1/books/{id}", s.handleGetBook)
	mux.HandleFunc("GET /api/v1/books/{id}/content", s.handleBookContent)

	// Reading progress (per-user)
	mux.HandleFunc("GET /api/v1/progress/{book_id}", s.handleGetProgress)
	mux.HandleFunc("PUT /api/v1/progress/{book_id}", s.handlePutProgress)

	// Catch-all: /api/* gets JSON 404; everything else serves the SPA.
	spa := webui.Handler()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "not_found",
				"path":  r.URL.Path,
			})
			return
		}
		spa.ServeHTTP(w, r)
	})

	return logRequests(s, withUser(mux))
}

// writeJSON sends v as JSON with the given status. Errors during encoding are
// silently dropped because headers have already flushed.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError is the small helper handlers use for failure paths. The `code`
// is a stable machine-readable token; `err` is logged but only its message
// goes to the client (no stack/internal details).
func writeError(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, map[string]any{
		"error":   code,
		"message": err.Error(),
	})
}

// logRequests is a minimal access log middleware.
func logRequests(s *Server, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		s.cfg.Logger.Printf("%s %s %s in %s", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}
