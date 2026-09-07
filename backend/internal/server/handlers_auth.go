package server

// Authentication and account management.
//
// Sessions are cookie-based rather than token-in-JS: an HttpOnly cookie
// cannot be read by script, which removes the whole class of XSS-steals-
// your-token problems that localStorage tokens carry. The trade is CSRF
// exposure, handled by SameSite=Lax plus the fact that every mutating
// endpoint here is JSON-only — a cross-site form post cannot set
// Content-Type: application/json.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/leafvmaple/zreader/internal/store"
)

const sessionCookie = "zreader_session"

type userDTO struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
}

func toUserDTO(u store.User) userDTO {
	return userDTO{ID: u.ID, Username: u.Username, Role: u.Role, CreatedAt: u.CreatedAt}
}

// handleAuthStatus is the only endpoint the SPA can call before logging in.
// It reports whether the install still needs its first account and, when a
// session cookie is present and valid, who it belongs to — so the client can
// choose between the setup screen, the login screen, and the app without
// three separate probes.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count_users", err)
		return
	}
	out := map[string]any{"setup_required": n == 0}
	if u, ok := s.sessionUser(r); ok {
		out["user"] = toUserDTO(u)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetup creates the first account. It is unauthenticated by
// necessity — there is nobody to authenticate as — so it refuses once any
// account exists, which is what stops it being a permanent open door.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "count_users", err)
		return
	}
	if n > 0 {
		writeError(w, http.StatusConflict, "already_setup", errors.New("this install already has accounts"))
		return
	}

	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	u, err := s.store.CreateUser(r.Context(), body.Username, body.Password, store.RoleAdmin)
	if err != nil {
		writeError(w, http.StatusBadRequest, "create_user", err)
		return
	}
	// Everything read before accounts existed belonged to the synthetic
	// "default" user. Hand it to the account being created, so an upgrading
	// install keeps its reading positions instead of starting empty.
	if err := s.store.AdoptLegacyData(r.Context(), u.ID); err != nil {
		s.cfg.Logger.Printf("adopt legacy data: %v", err)
	}
	s.issueSession(w, r, u, http.StatusCreated)
}

// loginThrottle slows password guessing. It is per-username and in-memory:
// this is a single-process personal server, so a shared store would be
// machinery without a purpose, and losing the counters on restart only
// costs an attacker the effort of triggering a restart.
type loginThrottle struct {
	mu       sync.Mutex
	attempts map[string]*throttleEntry
}

type throttleEntry struct {
	count int
	until time.Time
}

const (
	throttleAfter = 5
	throttleFor   = 60 * time.Second
)

func (t *loginThrottle) blocked(key string) (time.Duration, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.attempts[key]
	if e == nil || time.Now().After(e.until) {
		return 0, false
	}
	if e.count < throttleAfter {
		return 0, false
	}
	return time.Until(e.until), true
}

func (t *loginThrottle) fail(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.attempts == nil {
		t.attempts = map[string]*throttleEntry{}
	}
	e := t.attempts[key]
	if e == nil || time.Now().After(e.until) {
		e = &throttleEntry{}
		t.attempts[key] = e
	}
	e.count++
	e.until = time.Now().Add(throttleFor)
}

func (t *loginThrottle) succeed(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, key)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	key := store.NormaliseUsername(body.Username)
	if wait, blocked := s.throttle.blocked(key); blocked {
		writeError(w, http.StatusTooManyRequests, "too_many_attempts",
			errors.New("too many failed attempts, try again in "+wait.Round(time.Second).String()))
		return
	}

	u, err := s.store.Authenticate(r.Context(), body.Username, body.Password)
	if err != nil {
		s.throttle.fail(key)
		// Unknown user and wrong password are deliberately the same
		// response: distinguishing them tells an attacker which usernames
		// are worth attacking.
		writeError(w, http.StatusUnauthorized, "invalid_login", store.ErrInvalidLogin)
		return
	}
	s.throttle.succeed(key)
	s.issueSession(w, r, u, http.StatusOK)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.store.DeleteSession(r.Context(), c.Value); err != nil {
			s.cfg.Logger.Printf("delete session: %v", err)
		}
	}
	http.SetCookie(w, s.sessionCookie(r, "", time.Unix(0, 0)))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserDTO(currentUser(r))})
}

// handleChangeOwnPassword lets any account rotate its own password. The
// current password is required so a borrowed session can't lock the real
// owner out.
func (s *Server) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	me := currentUser(r)
	if _, err := s.store.Authenticate(r.Context(), me.Username, body.Current); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_login", errors.New("current password is wrong"))
		return
	}
	if err := s.store.SetPassword(r.Context(), me.ID, body.New); err != nil {
		writeError(w, http.StatusBadRequest, "set_password", err)
		return
	}
	// SetPassword dropped every session including this one; hand back a
	// fresh cookie so changing your own password doesn't log you out.
	s.issueSession(w, r, me, http.StatusOK)
}

// --- Admin: account management ---------------------------------------------

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_users", err)
		return
	}
	out := make([]userDTO, 0, len(users))
	for _, u := range users {
		out = append(out, toUserDTO(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	u, err := s.store.CreateUser(r.Context(), body.Username, body.Password, body.Role)
	if errors.Is(err, store.ErrUserExists) {
		writeError(w, http.StatusConflict, "user_exists", err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "create_user", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": toUserDTO(u)})
}

func (s *Server) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.store.GetUser(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", errors.New("user not found"))
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "get_user", err)
		return
	}

	var body struct {
		Password *string `json:"password"`
		Role     *string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	if body.Password != nil {
		if err := s.store.SetPassword(r.Context(), id, *body.Password); err != nil {
			writeError(w, http.StatusBadRequest, "set_password", err)
			return
		}
	}
	if body.Role != nil {
		if err := s.store.SetRole(r.Context(), id, *body.Role); errors.Is(err, store.ErrLastAdmin) {
			writeError(w, http.StatusConflict, "last_admin", err)
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "set_role", err)
			return
		}
	}
	u, err := s.store.GetUser(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get_user", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserDTO(u)})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == currentUser(r).ID {
		writeError(w, http.StatusBadRequest, "self_delete",
			errors.New("you cannot delete the account you are signed in as"))
		return
	}
	if err := s.store.DeleteUser(r.Context(), id); errors.Is(err, store.ErrLastAdmin) {
		writeError(w, http.StatusConflict, "last_admin", err)
		return
	} else if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", errors.New("user not found"))
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "delete_user", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Session plumbing ------------------------------------------------------

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, u store.User, status int) {
	token, expires, err := s.store.CreateSession(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_session", err)
		return
	}
	http.SetCookie(w, s.sessionCookie(r, token, expires))
	writeJSON(w, status, map[string]any{"user": toUserDTO(u)})
}

func (s *Server) sessionCookie(r *http.Request, value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:  sessionCookie,
		Value: value,
		Path:  "/",
		// HttpOnly is the point of using a cookie at all: script cannot read
		// it, so an XSS bug cannot walk off with the session.
		HttpOnly: true,
		// Lax still sends the cookie on top-level navigation, so following a
		// link to a book works, while blocking it on cross-site POSTs.
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
		Expires:  expires,
	}
}

// requestIsHTTPS reports whether the *original* request was HTTPS. Behind a
// reverse proxy — the deployment this project documents — TLS terminates at
// the proxy and r.TLS is nil, so the forwarded header is the only signal.
// Marking the cookie Secure over plain HTTP would make it undeliverable, so
// this errs toward not setting it.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if i := strings.IndexByte(proto, ','); i >= 0 {
		proto = proto[:i]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func (s *Server) sessionUser(r *http.Request) (store.User, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return store.User{}, false
	}
	u, err := s.store.UserForSession(r.Context(), c.Value)
	if err != nil {
		return store.User{}, false
	}
	return u, true
}
