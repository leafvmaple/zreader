package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/leafvmaple/zreader/internal/store"
)

// progressFixture gives a signed-in user and a book to track progress in.
func progressFixture(t *testing.T) (*Server, int64, *http.Cookie) {
	t.Helper()
	srv, st := newAuthTestServer(t)
	bookID := seedBook(t, st)
	u, err := st.CreateUser(context.Background(), "reader", "correct-horse", store.RoleAdmin)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	token, _, err := st.CreateSession(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return srv, bookID, &http.Cookie{Name: sessionCookie, Value: token}
}

func progressPath(bookID int64) string {
	return fmt.Sprintf("/api/v1/progress/%d", bookID)
}

func decodeProgress(t *testing.T, body []byte) progressDTO {
	t.Helper()
	var p progressDTO
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode progress: %v (%s)", err, body)
	}
	return p
}

// The version a client writes against has to come from the server, not
// from the client's own clock. Two devices whose clocks disagree used to
// have the race decided by whichever ran faster: the slow one was
// rejected on every write and dragged to the other's position, forever.
func TestProgressVersionIsServerAssigned(t *testing.T) {
	srv, bookID, session := progressFixture(t)

	// A device whose clock is years behind still gets a usable version back.
	rr := do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
		"char_offset": 100, "chapter_idx": 1, "chapter_offset": 0,
		"base_updated_at": 0,
	}, session)
	if rr.Code != http.StatusOK {
		t.Fatalf("first write = %d body=%s", rr.Code, rr.Body.String())
	}
	first := decodeProgress(t, rr.Body.Bytes())
	if first.UpdatedAt <= 1_600_000_000 {
		t.Errorf("UpdatedAt = %d, want a server timestamp", first.UpdatedAt)
	}

	// Writing against the version we were handed succeeds...
	rr = do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
		"char_offset": 200, "chapter_idx": 1, "chapter_offset": 100,
		"base_updated_at": first.UpdatedAt,
	}, session)
	if rr.Code != http.StatusOK {
		t.Fatalf("write against current version = %d body=%s", rr.Code, rr.Body.String())
	}
	second := decodeProgress(t, rr.Body.Bytes())

	// ...and writing against the version before it does not, no matter what
	// the second device thinks the time is.
	rr = do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
		"char_offset": 50, "chapter_idx": 1, "chapter_offset": 0,
		"base_updated_at": first.UpdatedAt - 1,
	}, session)
	if rr.Code != http.StatusConflict {
		t.Fatalf("stale write = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	var conflict struct {
		Error  string      `json:"error"`
		Server progressDTO `json:"server"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict.Error != "stale_write" {
		t.Errorf("error = %q, want stale_write", conflict.Error)
	}
	if conflict.Server.CharOffset != 200 {
		t.Errorf("conflict body reports offset %d, want the stored 200", conflict.Server.CharOffset)
	}
	if conflict.Server.UpdatedAt != second.UpdatedAt {
		t.Errorf("conflict version = %d, want %d", conflict.Server.UpdatedAt, second.UpdatedAt)
	}
}

// A fresh device has no version to write against and must not be locked
// out of a book it has never opened.
func TestProgressZeroBaseAlwaysWins(t *testing.T) {
	srv, bookID, session := progressFixture(t)

	do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
		"char_offset": 900, "chapter_idx": 3, "chapter_offset": 0, "base_updated_at": 0,
	}, session)
	rr := do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
		"char_offset": 10, "chapter_idx": 1, "chapter_offset": 0, "base_updated_at": 0,
	}, session)
	if rr.Code != http.StatusOK {
		t.Fatalf("write with no base = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := decodeProgress(t, rr.Body.Bytes()).CharOffset; got != 10 {
		t.Errorf("stored offset = %d, want 10", got)
	}
}

// A browser still running JS cached from before this change sends its own
// clock in updated_at. It has to keep syncing.
func TestProgressLegacyUpdatedAtStillWorks(t *testing.T) {
	srv, bookID, session := progressFixture(t)

	rr := do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
		"char_offset": 100, "chapter_idx": 1, "chapter_offset": 0,
		"updated_at": 2_000_000_000,
	}, session)
	if rr.Code != http.StatusOK {
		t.Fatalf("legacy write = %d body=%s", rr.Code, rr.Body.String())
	}
	stored := decodeProgress(t, rr.Body.Bytes())
	if stored.UpdatedAt == 2_000_000_000 {
		t.Error("the client's clock was stored as the row version")
	}

	// Its next write carries a clock far ahead of the server's, which under
	// the old rule made it win everything; under the new one it is simply a
	// base older than nothing, so it still writes.
	rr = do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
		"char_offset": 300, "chapter_idx": 2, "chapter_offset": 0,
		"updated_at": 2_000_000_001,
	}, session)
	if rr.Code != http.StatusOK {
		t.Fatalf("second legacy write = %d body=%s", rr.Code, rr.Body.String())
	}
}
