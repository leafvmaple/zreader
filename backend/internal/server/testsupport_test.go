package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/leafvmaple/zreader/internal/store"
)

// testRouter returns the real router with an authenticated admin session
// attached to every request that doesn't already carry one.
//
// The handler tests predate accounts and are about handler behaviour, not
// the login flow; making each of them drive setup → login → cookie would
// bury what they actually assert. Tests that care about auth build their
// requests without this (see handlers_auth_test.go).
func testRouter(t *testing.T, srv *Server) http.Handler {
	t.Helper()
	ctx := context.Background()

	u := testAdmin(t, srv.store)
	token, _, err := srv.store.CreateSession(ctx, u.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	inner := srv.newRouter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(sessionCookie); err != nil {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		}
		inner.ServeHTTP(w, r)
	})
}

// testAdmin returns the store's admin, creating it on first use. It is
// idempotent because testRouter is called once per request, not once per
// test.
func testAdmin(t *testing.T, st *store.Store) store.User {
	t.Helper()
	ctx := context.Background()

	users, err := st.ListUsers(ctx)
	if err != nil {
		t.Fatalf("list users: %v", err)
	}
	if len(users) > 0 {
		return users[0]
	}
	u, err := st.CreateUser(ctx, "tester", "password123", store.RoleAdmin)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	return u
}
