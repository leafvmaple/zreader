package server

import (
	"context"
	"net/http"
)

type ctxKey int

const (
	ctxKeyUser ctxKey = iota
)

// User is the authenticated principal. zreader runs in single-user mode by
// default — there is no auth, and every request gets the same synthetic
// "default" user. The struct is kept so the per-user progress table layout
// is forward-compatible with a future multi-user mode.
type User struct {
	ID string
}

const defaultUserID = "default"

// withUser attaches the default user to the request context. Authentication
// (if any) is expected to be performed by a reverse proxy in front of this
// service.
func withUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), ctxKeyUser, User{ID: defaultUserID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// currentUser returns the user attached by withUser.
func currentUser(r *http.Request) User {
	u, _ := r.Context().Value(ctxKeyUser).(User)
	return u
}
