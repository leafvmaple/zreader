package store

// Accounts and sessions.
//
// Passwords are stored as bcrypt hashes; session cookies are stored as a
// SHA-256 of the token, never the token itself, so a database that leaks
// cannot be replayed as a set of live logins.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"

	// LegacyUserID is the synthetic owner every row had before accounts
	// existed. The first account created adopts these rows so an upgrading
	// install keeps its reading positions.
	LegacyUserID = "default"

	// bcryptCost is above the library default of 10: this is a personal
	// server where a login taking ~200ms is unnoticeable, and the extra
	// factor of four matters if the database is ever taken.
	bcryptCost = 12

	sessionTTL = 30 * 24 * time.Hour
)

var (
	ErrUserExists   = errors.New("username already taken")
	ErrInvalidLogin = errors.New("invalid username or password")
	ErrLastAdmin    = errors.New("cannot remove the last admin")
)

type User struct {
	ID        string
	Username  string
	Role      string
	CreatedAt int64
}

func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// CountUsers reports how many accounts exist. Zero means the install has
// not been set up yet, which is what puts the UI into first-run mode.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser adds an account. The first account ever created is forced to
// admin regardless of what the caller asked for — an install whose only
// user cannot manage users would be unrecoverable.
func (s *Store) CreateUser(ctx context.Context, username, password, role string) (User, error) {
	username = NormaliseUsername(username)
	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return User{}, err
	}

	n, err := s.CountUsers(ctx)
	if err != nil {
		return User{}, err
	}
	if n == 0 {
		role = RoleAdmin
	}
	if role != RoleAdmin {
		role = RoleUser
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}

	u := User{ID: uuid.NewString(), Username: username, Role: role, CreatedAt: s.nowUnix()}
	_, err = s.db.ExecContext(ctx, `
        INSERT INTO users(id, username, password_hash, role, created_at)
        VALUES (?, ?, ?, ?, ?)`,
		u.ID, u.Username, string(hash), u.Role, u.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrUserExists
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

// AdoptLegacyData re-owns the rows written before accounts existed. Called
// once, for the first account created; a no-op afterwards because the
// legacy id stops appearing.
func (s *Store) AdoptLegacyData(ctx context.Context, userID string) error {
	for _, table := range []string{"user_progress", "bookmarks"} {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE `+table+` SET user_id = ? WHERE user_id = ?`, userID, LegacyUserID); err != nil {
			return fmt.Errorf("adopt %s: %w", table, err)
		}
	}
	return nil
}

// Authenticate checks a username and password.
//
// An unknown user and a wrong password return the same error, and the
// unknown-user path still runs a bcrypt comparison against a dummy hash so
// the two take the same time — otherwise the response latency reveals
// which usernames exist.
func (s *Store) Authenticate(ctx context.Context, username, password string) (User, error) {
	var u User
	var hash string
	err := s.db.QueryRowContext(ctx, `
        SELECT id, username, password_hash, role, created_at
          FROM users WHERE username = ?`, NormaliseUsername(username)).
		Scan(&u.ID, &u.Username, &hash, &u.Role, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		_ = bcrypt.CompareHashAndPassword([]byte(dummyHash), []byte(password))
		return User{}, ErrInvalidLogin
	}
	if err != nil {
		return User{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return User{}, ErrInvalidLogin
	}
	return u, nil
}

// dummyHash is a valid bcrypt hash of a value nobody knows, used purely to
// spend the same time on the unknown-user path as on a real comparison.
const dummyHash = "$2a$12$C6UzMDM.H6dfI/f/IKcEe.7wYyQmWpDbb0iHfvHQfLxZ8Zc0z1qDy"

// --- Sessions --------------------------------------------------------------

// CreateSession issues a session and returns the raw token. The token is
// only ever returned here; the database keeps its hash.
func (s *Store) CreateSession(ctx context.Context, userID string) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(raw)
	expires := time.Now().Add(sessionTTL)

	if _, err := s.db.ExecContext(ctx, `
        INSERT INTO sessions(token_hash, user_id, created_at, expires_at)
        VALUES (?, ?, ?, ?)`,
		HashToken(token), userID, s.nowUnix(), expires.Unix()); err != nil {
		return "", time.Time{}, fmt.Errorf("insert session: %w", err)
	}
	return token, expires, nil
}

// UserForSession resolves a session token to its owner, treating an expired
// row as absent.
func (s *Store) UserForSession(ctx context.Context, token string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `
        SELECT u.id, u.username, u.role, u.created_at
          FROM sessions s JOIN users u ON u.id = s.user_id
         WHERE s.token_hash = ? AND s.expires_at > ?`,
		HashToken(token), s.nowUnix()).
		Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt)
	return u, err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, HashToken(token))
	return err
}

// DeleteUserSessions logs an account out everywhere. Used on password
// change and on delete, so a stolen cookie stops working the moment the
// password is rotated.
func (s *Store) DeleteUserSessions(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// PurgeExpiredSessions clears rows past their expiry. Nothing depends on it
// for correctness — UserForSession already filters — it just keeps the
// table from growing without bound.
func (s *Store) PurgeExpiredSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, s.nowUnix())
	return err
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// --- Management ------------------------------------------------------------

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, username, role, created_at FROM users ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `
        SELECT id, username, role, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt)
	return u, err
}

// SetPassword rotates a password and drops every session the account has,
// so a change actually evicts whoever was logged in.
func (s *Store) SetPassword(ctx context.Context, userID, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), userID); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return s.DeleteUserSessions(ctx, userID)
}

// SetRole changes an account's role, refusing to remove the last admin —
// an install with no admin can't create one back.
func (s *Store) SetRole(ctx context.Context, userID, role string) error {
	if role != RoleAdmin {
		role = RoleUser
		if last, err := s.isLastAdmin(ctx, userID); err != nil {
			return err
		} else if last {
			return ErrLastAdmin
		}
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET role = ? WHERE id = ?`, role, userID)
	return err
}

// DeleteUser removes an account and, by cascade, its sessions. Reading
// progress and bookmarks go with it — they are keyed by user_id and mean
// nothing without the account.
func (s *Store) DeleteUser(ctx context.Context, userID string) error {
	if last, err := s.isLastAdmin(ctx, userID); err != nil {
		return err
	} else if last {
		return ErrLastAdmin
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM user_progress WHERE user_id = ?`,
		`DELETE FROM bookmarks WHERE user_id = ?`,
		`DELETE FROM sessions WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) isLastAdmin(ctx context.Context, userID string) (bool, error) {
	var role string
	if err := s.db.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, userID).Scan(&role); err != nil {
		return false, err
	}
	if role != RoleAdmin {
		return false, nil
	}
	var admins int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = ?`, RoleAdmin).Scan(&admins); err != nil {
		return false, err
	}
	return admins <= 1, nil
}

// --- Validation ------------------------------------------------------------

// NormaliseUsername lower-cases and trims, so "Zohar" and "zohar" are the
// same account rather than two that look identical in a list.
func NormaliseUsername(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func ValidateUsername(s string) error {
	if len(s) < 2 || len(s) > 32 {
		return errors.New("username must be 2-32 characters")
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.'
		if !ok {
			return errors.New("username may only contain letters, digits, and _ - .")
		}
	}
	return nil
}

func ValidatePassword(s string) error {
	if len([]rune(s)) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	// bcrypt ignores everything past 72 bytes. Accepting a longer password
	// would mean silently authenticating on a prefix — two passwords sharing
	// their first 72 bytes would be interchangeable — so the limit is stated
	// rather than hidden. Note it is bytes, not runes: a CJK passphrase hits
	// it at 24 characters.
	if len(s) > 72 {
		return errors.New("password must be at most 72 bytes (24 Chinese characters)")
	}
	return nil
}
