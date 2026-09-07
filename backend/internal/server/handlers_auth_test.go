package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/leafvmaple/zreader/internal/store"
)

func newAuthTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(Config{Port: 0, Store: st}), st
}

// do sends a request through the real router with no session attached
// unless cookies are given.
func do(t *testing.T, srv *Server, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	srv.newRouter().ServeHTTP(rr, req)
	return rr
}

// seedBook inserts a minimal book row. user_progress has a foreign key to
// books, so progress cannot be written for an id that doesn't exist.
func seedBook(t *testing.T, st *store.Store) int64 {
	t.Helper()
	ctx := context.Background()
	folder, err := st.AddFolder(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("add folder: %v", err)
	}
	id, _, err := st.UpsertBook(ctx, store.Book{
		FolderID: folder.ID,
		Path:     t.TempDir() + "/BookA.epub",
		Title:    "BookA",
		Format:   "epub",
	})
	if err != nil {
		t.Fatalf("upsert book: %v", err)
	}
	return id
}

func sessionCookieFrom(t *testing.T, rr *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range (&http.Response{Header: rr.Header()}).Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatalf("no session cookie in response: %v", rr.Header())
	return nil
}

// A fresh install must report that it needs setting up, and must not serve
// the library to anyone before that happens.
func TestAuth_FreshInstallRequiresSetup(t *testing.T) {
	srv, _ := newAuthTestServer(t)

	rr := do(t, srv, http.MethodGet, "/api/v1/auth/status", nil)
	var status struct {
		SetupRequired bool     `json:"setup_required"`
		User          *userDTO `json:"user"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.SetupRequired {
		t.Error("setup_required = false on a fresh install")
	}
	if status.User != nil {
		t.Error("a fresh install reported a signed-in user")
	}

	if rr := do(t, srv, http.MethodGet, "/api/v1/books", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("GET /books before setup = %d, want 401", rr.Code)
	}
}

func TestAuth_SetupCreatesAdminAndSignsIn(t *testing.T) {
	srv, st := newAuthTestServer(t)

	rr := do(t, srv, http.MethodPost, "/api/v1/auth/setup",
		map[string]string{"username": "Zohar", "password": "correct-horse"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("setup = %d body=%s", rr.Code, rr.Body.String())
	}

	var out struct{ User userDTO }
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.User.Role != store.RoleAdmin {
		t.Errorf("first account role = %q, want admin", out.User.Role)
	}
	// Usernames normalise, so "Zohar" and "zohar" can't become two accounts.
	if out.User.Username != "zohar" {
		t.Errorf("username = %q, want normalised to lowercase", out.User.Username)
	}

	c := sessionCookieFrom(t, rr)
	if !c.HttpOnly {
		t.Error("session cookie is not HttpOnly — script could read it")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie SameSite = %v, want Lax", c.SameSite)
	}
	// The raw token must never be what the database holds.
	if _, err := st.UserForSession(context.Background(), c.Value); err != nil {
		t.Fatalf("issued cookie does not resolve to a session: %v", err)
	}
	if rr := do(t, srv, http.MethodGet, "/api/v1/books", nil, c); rr.Code != http.StatusOK {
		t.Errorf("GET /books with the setup session = %d, want 200", rr.Code)
	}
}

// Setup is unauthenticated by necessity; it has to close afterwards or it
// is a permanent open door to admin.
func TestAuth_SetupRefusesOnceConfigured(t *testing.T) {
	srv, _ := newAuthTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/auth/setup",
		map[string]string{"username": "zohar", "password": "correct-horse"})

	rr := do(t, srv, http.MethodPost, "/api/v1/auth/setup",
		map[string]string{"username": "intruder", "password": "correct-horse"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("second setup = %d, want 409 — setup must close after the first account", rr.Code)
	}
}

// Reading positions recorded before accounts existed belong to the person
// upgrading, not to nobody.
func TestAuth_SetupAdoptsPreAccountData(t *testing.T) {
	srv, st := newAuthTestServer(t)
	ctx := context.Background()
	bookID := seedBook(t, st)
	if err := st.PutProgress(ctx, store.Progress{
		UserID: store.LegacyUserID, BookID: bookID, CharOffset: 4242, ChapterIdx: 3,
	}); err != nil {
		t.Fatalf("seed legacy progress: %v", err)
	}

	rr := do(t, srv, http.MethodPost, "/api/v1/auth/setup",
		map[string]string{"username": "zohar", "password": "correct-horse"})
	var out struct{ User userDTO }
	_ = json.Unmarshal(rr.Body.Bytes(), &out)

	got, err := st.GetProgress(ctx, out.User.ID, bookID)
	if err != nil {
		t.Fatalf("progress was not adopted by the first account: %v", err)
	}
	if got.CharOffset != 4242 {
		t.Errorf("CharOffset = %d, want the pre-account value 4242", got.CharOffset)
	}
}

func TestAuth_LoginLogout(t *testing.T) {
	srv, _ := newAuthTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/auth/setup",
		map[string]string{"username": "zohar", "password": "correct-horse"})

	rr := do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "zohar", "password": "correct-horse"})
	if rr.Code != http.StatusOK {
		t.Fatalf("login = %d body=%s", rr.Code, rr.Body.String())
	}
	c := sessionCookieFrom(t, rr)

	if rr := do(t, srv, http.MethodGet, "/api/v1/auth/me", nil, c); rr.Code != http.StatusOK {
		t.Fatalf("/auth/me = %d, want 200", rr.Code)
	}
	if rr := do(t, srv, http.MethodPost, "/api/v1/auth/logout", nil, c); rr.Code != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", rr.Code)
	}
	// The session must be dead server-side, not merely cleared in the browser.
	if rr := do(t, srv, http.MethodGet, "/api/v1/books", nil, c); rr.Code != http.StatusUnauthorized {
		t.Errorf("the logged-out cookie still worked: %d", rr.Code)
	}
}

// Wrong password and unknown user must be indistinguishable, or the login
// endpoint becomes a username oracle.
func TestAuth_LoginFailuresAreIndistinguishable(t *testing.T) {
	srv, _ := newAuthTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/auth/setup",
		map[string]string{"username": "zohar", "password": "correct-horse"})

	wrongPass := do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "zohar", "password": "wrong-horse"})
	noSuchUser := do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "nobody", "password": "wrong-horse"})

	if wrongPass.Code != http.StatusUnauthorized || noSuchUser.Code != http.StatusUnauthorized {
		t.Fatalf("statuses = %d / %d, want both 401", wrongPass.Code, noSuchUser.Code)
	}
	if wrongPass.Body.String() != noSuchUser.Body.String() {
		t.Errorf("responses differ, leaking which usernames exist:\n  %s\n  %s",
			wrongPass.Body.String(), noSuchUser.Body.String())
	}
}

func TestAuth_LoginThrottled(t *testing.T) {
	srv, _ := newAuthTestServer(t)
	do(t, srv, http.MethodPost, "/api/v1/auth/setup",
		map[string]string{"username": "zohar", "password": "correct-horse"})

	var last int
	for i := 0; i < throttleAfter+1; i++ {
		last = do(t, srv, http.MethodPost, "/api/v1/auth/login",
			map[string]string{"username": "zohar", "password": "wrong-horse"}).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("after %d failures status = %d, want 429", throttleAfter+1, last)
	}
	// Throttling must not lock out the real password permanently... but it
	// does hold until the window passes, which is the point. Verify the
	// correct password is also refused while blocked, so the throttle can't
	// be bypassed by simply knowing it.
	if got := do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "zohar", "password": "correct-horse"}).Code; got != http.StatusTooManyRequests {
		t.Errorf("throttle bypassed by a correct password: %d", got)
	}
}

// A plain user must not be able to reach account management.
func TestAuth_NonAdminCannotManageUsers(t *testing.T) {
	srv, st := newAuthTestServer(t)
	ctx := context.Background()
	if _, err := st.CreateUser(ctx, "admin", "correct-horse", store.RoleAdmin); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	reader, err := st.CreateUser(ctx, "reader", "correct-horse", store.RoleUser)
	if err != nil {
		t.Fatalf("create reader: %v", err)
	}
	token, _, err := st.CreateSession(ctx, reader.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	c := &http.Cookie{Name: sessionCookie, Value: token}

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/users"},
		{http.MethodPost, "/api/v1/users"},
		{http.MethodDelete, "/api/v1/users/" + reader.ID},
	} {
		if got := do(t, srv, tc.method, tc.path, map[string]string{}, c).Code; got != http.StatusForbidden {
			t.Errorf("%s %s as a plain user = %d, want 403", tc.method, tc.path, got)
		}
	}
	// But the library itself is theirs to read.
	if got := do(t, srv, http.MethodGet, "/api/v1/books", nil, c).Code; got != http.StatusOK {
		t.Errorf("GET /books as a plain user = %d, want 200", got)
	}
}

// Two accounts must not see each other's reading positions — that is the
// whole point of multi-user.
func TestAuth_ProgressIsPerUser(t *testing.T) {
	srv, st := newAuthTestServer(t)
	ctx := context.Background()
	bookID := seedBook(t, st)
	a, _ := st.CreateUser(ctx, "alpha", "correct-horse", store.RoleAdmin)
	b, _ := st.CreateUser(ctx, "beta", "correct-horse", store.RoleUser)
	ta, _, _ := st.CreateSession(ctx, a.ID)
	tb, _, _ := st.CreateSession(ctx, b.ID)
	ca := &http.Cookie{Name: sessionCookie, Value: ta}
	cb := &http.Cookie{Name: sessionCookie, Value: tb}

	if rr := do(t, srv, http.MethodPut, fmt.Sprintf("/api/v1/progress/%d", bookID),
		map[string]int{"char_offset": 999, "chapter_idx": 4}, ca); rr.Code != http.StatusOK {
		t.Fatalf("alpha PUT progress = %d body=%s", rr.Code, rr.Body.String())
	}

	var got struct {
		CharOffset int `json:"char_offset"`
	}
	rr := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/progress/%d", bookID), nil, cb)
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.CharOffset != 0 {
		t.Errorf("beta sees alpha's position: char_offset = %d, want 0", got.CharOffset)
	}

	rr = do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/progress/%d", bookID), nil, ca)
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got.CharOffset != 999 {
		t.Errorf("alpha lost their own position: char_offset = %d, want 999", got.CharOffset)
	}
}

// Rotating a password must evict existing sessions, or a stolen cookie
// survives the very action taken to revoke it.
func TestAuth_PasswordChangeEvictsOtherSessions(t *testing.T) {
	srv, st := newAuthTestServer(t)
	ctx := context.Background()
	do(t, srv, http.MethodPost, "/api/v1/auth/setup",
		map[string]string{"username": "zohar", "password": "correct-horse"})
	u, _ := st.ListUsers(ctx)
	stolen, _, _ := st.CreateSession(ctx, u[0].ID)
	stolenCookie := &http.Cookie{Name: sessionCookie, Value: stolen}

	login := do(t, srv, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "zohar", "password": "correct-horse"})
	mine := sessionCookieFrom(t, login)

	rr := do(t, srv, http.MethodPost, "/api/v1/auth/password",
		map[string]string{"current_password": "correct-horse", "new_password": "battery-staple"}, mine)
	if rr.Code != http.StatusOK {
		t.Fatalf("password change = %d body=%s", rr.Code, rr.Body.String())
	}

	if got := do(t, srv, http.MethodGet, "/api/v1/books", nil, stolenCookie).Code; got != http.StatusUnauthorized {
		t.Errorf("a session from before the password change still works: %d", got)
	}
	// The caller keeps working: they get a fresh cookie back, so changing
	// your own password doesn't sign you out of the tab you did it in.
	fresh := sessionCookieFrom(t, rr)
	if got := do(t, srv, http.MethodGet, "/api/v1/books", nil, fresh).Code; got != http.StatusOK {
		t.Errorf("the caller was signed out by their own password change: %d", got)
	}
}

func TestAuth_PasswordChangeRequiresCurrentPassword(t *testing.T) {
	srv, _ := newAuthTestServer(t)
	setup := do(t, srv, http.MethodPost, "/api/v1/auth/setup",
		map[string]string{"username": "zohar", "password": "correct-horse"})
	c := sessionCookieFrom(t, setup)

	rr := do(t, srv, http.MethodPost, "/api/v1/auth/password",
		map[string]string{"current_password": "guessing", "new_password": "battery-staple"}, c)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("password change without the current password = %d, want 401", rr.Code)
	}
}

// Removing the last admin would leave an install nobody can administer.
func TestAuth_CannotRemoveLastAdmin(t *testing.T) {
	_, st := newAuthTestServer(t)
	ctx := context.Background()
	admin, _ := st.CreateUser(ctx, "admin", "correct-horse", store.RoleAdmin)
	if _, err := st.CreateUser(ctx, "reader", "correct-horse", store.RoleUser); err != nil {
		t.Fatalf("create reader: %v", err)
	}

	if err := st.DeleteUser(ctx, admin.ID); err == nil {
		t.Error("deleting the only admin was allowed")
	}
	if err := st.SetRole(ctx, admin.ID, store.RoleUser); err == nil {
		t.Error("demoting the only admin was allowed")
	}
}

func TestAuth_PasswordRules(t *testing.T) {
	cases := map[string]bool{
		"short":                 false, // under 8
		"password123":           true,
		strings.Repeat("a", 72): true,
		strings.Repeat("a", 73): false, // bcrypt ignores past 72 bytes
		strings.Repeat("中", 24): true,  // 72 bytes exactly
		strings.Repeat("中", 25): false, // 75 bytes
	}
	for pw, want := range cases {
		err := store.ValidatePassword(pw)
		if (err == nil) != want {
			t.Errorf("ValidatePassword(%d bytes) error = %v, want ok=%v", len(pw), err, want)
		}
	}
}
