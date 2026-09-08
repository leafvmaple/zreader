package server

import (
	"context"
	"database/sql"
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

// seedSizedBook is seedBook plus a character count, which is what decides
// whether a saved position counts as reaching the end.
func seedSizedBook(t *testing.T, st *store.Store, chars int64) int64 {
	t.Helper()
	ctx := context.Background()
	folder, err := st.AddFolder(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("add folder: %v", err)
	}
	id, _, err := st.UpsertBook(ctx, store.Book{
		FolderID:  folder.ID,
		Path:      t.TempDir() + "/BookB.epub",
		Title:     "BookB",
		Format:    "epub",
		CharCount: sql.NullInt64{Int64: chars, Valid: true},
	})
	if err != nil {
		t.Fatalf("upsert book: %v", err)
	}
	return id
}

func statusOf(t *testing.T, st *store.Store, id int64) string {
	t.Helper()
	b, err := st.GetBook(context.Background(), id)
	if err != nil {
		t.Fatalf("get book: %v", err)
	}
	return b.ReadingStatus
}

// A book read cover to cover used to stay "unread" on the shelf: the
// column only ever moved when the reader edited it by hand.
func TestProgressAdvancesReadingStatus(t *testing.T) {
	srv, _, session := progressFixture(t)
	st := srv.store
	const total = 100_000
	bookID := seedSizedBook(t, st, total)

	if got := statusOf(t, st, bookID); got != store.ReadingStatusUnread {
		t.Fatalf("fresh book status = %q, want unread", got)
	}

	do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
		"char_offset": 5_000, "chapter_idx": 1, "chapter_offset": 0, "base_updated_at": 0,
	}, session)
	if got := statusOf(t, st, bookID); got != store.ReadingStatusReading {
		t.Errorf("after reading 5%%: status = %q, want reading", got)
	}

	// Short of the end by more than the slack: still just reading.
	do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
		"char_offset": total - 5_000, "chapter_idx": 9, "chapter_offset": 0,
	}, session)
	if got := statusOf(t, st, bookID); got != store.ReadingStatusReading {
		t.Errorf("5000 short of the end: status = %q, want reading", got)
	}

	// Within the slack — the reader cannot report the exact total, so this
	// is what reaching the end actually looks like.
	do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
		"char_offset": total - 400, "chapter_idx": 10, "chapter_offset": 0,
	}, session)
	if got := statusOf(t, st, bookID); got != store.ReadingStatusFinished {
		t.Errorf("at the end: status = %q, want finished", got)
	}
}

// Both of these are things the reader said on purpose. Scrolling must not
// overrule either.
func TestProgressLeavesDeliberateStatusAlone(t *testing.T) {
	for _, status := range []string{store.ReadingStatusPaused, store.ReadingStatusFinished} {
		t.Run(status, func(t *testing.T) {
			srv, _, session := progressFixture(t)
			st := srv.store
			bookID := seedSizedBook(t, st, 100_000)
			if _, err := st.UpdateBookMetadata(context.Background(), bookID,
				store.BookUpdate{ReadingStatus: &status}); err != nil {
				t.Fatalf("set status: %v", err)
			}
			do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
				"char_offset": 5_000, "chapter_idx": 1, "chapter_offset": 0,
			}, session)
			if got := statusOf(t, st, bookID); got != status {
				t.Errorf("status = %q, want it left at %q", got, status)
			}
		})
	}
}

// A book whose length is unknown has no end to reach.
func TestProgressWithoutCharCountNeverFinishes(t *testing.T) {
	srv, bookID, session := progressFixture(t)
	do(t, srv, http.MethodPut, progressPath(bookID), map[string]any{
		"char_offset": 999_999_999, "chapter_idx": 1, "chapter_offset": 0,
	}, session)
	if got := statusOf(t, srv.store, bookID); got != store.ReadingStatusReading {
		t.Errorf("status = %q, want reading", got)
	}
}
