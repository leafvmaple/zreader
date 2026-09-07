package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/leafvmaple/zreader/internal/store"
)

type ctxKey int

const ctxKeyUser ctxKey = iota

// publicAPIPaths are the only /api endpoints reachable without a session.
//
// Everything else — including the book list — is behind auth. That is the
// whole point: this used to be an app you were told not to expose, and a
// library listing leaks what someone reads even without the text.
var publicAPIPaths = map[string]bool{
	"/api/v1/health":      true,
	"/api/v1/auth/status": true,
	"/api/v1/auth/setup":  true,
	"/api/v1/auth/login":  true,
	"/api/v1/auth/logout": true,
}

// requireAuth gates the API on a valid session and attaches the account to
// the request context.
//
// Non-API paths fall through untouched: the SPA has to load in order to
// render the login screen, and it holds nothing sensitive on its own.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || publicAPIPaths[r.URL.Path] {
			// Attach the session anyway when there is one, so /auth/status
			// can report who is signed in.
			if u, ok := s.sessionUser(r); ok {
				r = r.WithContext(context.WithValue(r.Context(), ctxKeyUser, u))
			}
			next.ServeHTTP(w, r)
			return
		}

		u, ok := s.sessionUser(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthenticated", errors.New("sign in to continue"))
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyUser, u)))
	})
}

// requireAdmin wraps the handful of endpoints that manage other people's
// accounts.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !currentUser(r).IsAdmin() {
			writeError(w, http.StatusForbidden, "forbidden", errors.New("admin access required"))
			return
		}
		next(w, r)
	}
}

// currentUser returns the account attached by requireAuth. It is the zero
// value on the public paths, which none of the authenticated handlers see.
func currentUser(r *http.Request) store.User {
	u, _ := r.Context().Value(ctxKeyUser).(store.User)
	return u
}
