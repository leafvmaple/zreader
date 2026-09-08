package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/leafvmaple/zreader/internal/store"
)

type progressDTO struct {
	BookID        int64 `json:"book_id"`
	CharOffset    int64 `json:"char_offset"`
	ChapterIdx    int64 `json:"chapter_idx"`
	ChapterOffset int64 `json:"chapter_offset"`
	UpdatedAt     int64 `json:"updated_at"`
}

// handleListProgress returns every saved position for the current user in
// one response.
//
// The shelf used to fan out one GET /progress/{id} per book on every
// refresh — and it refreshes after any mutation, so toggling a single
// favourite star cost one request per book in the library. This collapses
// that to one. Books with no saved position are simply absent; the client
// already treats a missing entry as "not started".
func (s *Server) handleListProgress(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	all, err := s.store.AllProgress(r.Context(), u.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_progress", err)
		return
	}
	out := make([]progressDTO, 0, len(all))
	for _, p := range all {
		out = append(out, progressDTO{
			BookID:        p.BookID,
			CharOffset:    p.CharOffset,
			ChapterIdx:    p.ChapterIdx,
			ChapterOffset: p.ChapterOffset,
			UpdatedAt:     p.UpdatedAt,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BookID < out[j].BookID })
	writeJSON(w, http.StatusOK, map[string]any{"progress": out})
}

func (s *Server) handleGetProgress(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.PathValue("book_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_book_id", err)
		return
	}
	u := currentUser(r)
	p, err := s.store.GetProgress(r.Context(), u.ID, bookID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No saved progress = start of book. Return an empty body so the
			// client can branch on missing fields rather than 404 handling.
			writeJSON(w, http.StatusOK, progressDTO{BookID: bookID})
			return
		}
		writeError(w, http.StatusInternalServerError, "get_progress", err)
		return
	}
	writeJSON(w, http.StatusOK, progressDTO{
		BookID:        p.BookID,
		CharOffset:    p.CharOffset,
		ChapterIdx:    p.ChapterIdx,
		ChapterOffset: p.ChapterOffset,
		UpdatedAt:     p.UpdatedAt,
	})
}

func (s *Server) handlePutProgress(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.PathValue("book_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_book_id", err)
		return
	}
	var body struct {
		CharOffset    int64 `json:"char_offset"`
		ChapterIdx    int64 `json:"chapter_idx"`
		ChapterOffset int64 `json:"chapter_offset"`
		// BaseUpdatedAt is the row version the client last saw, not a
		// timestamp it made up. 0 means "I have not read this row", which
		// wins unconditionally — a first write from a fresh device.
		BaseUpdatedAt int64 `json:"base_updated_at"`
		// UpdatedAt is the pre-0.14 field, where the client sent its own
		// wall clock and the server both compared and stored it. Honoured
		// as a fallback so a browser still running cached JS from before
		// the upgrade keeps syncing.
		UpdatedAt int64 `json:"updated_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	u := currentUser(r)

	// Stale-write guard. The version to compare against is the one the
	// client last read back from us; it used to be the client's own clock,
	// which made the winner of any two-device race whichever device's clock
	// ran faster. A phone a minute slow was rejected on every write, then
	// yanked to the other device's position, forever.
	base := body.BaseUpdatedAt
	if base == 0 {
		base = body.UpdatedAt
	}
	if base > 0 {
		if existing, err := s.store.GetProgress(r.Context(), u.ID, bookID); err == nil {
			if existing.UpdatedAt > base {
				writeJSON(w, http.StatusConflict, map[string]any{
					"error": "stale_write",
					"server": progressDTO{
						BookID:        existing.BookID,
						CharOffset:    existing.CharOffset,
						ChapterIdx:    existing.ChapterIdx,
						ChapterOffset: existing.ChapterOffset,
						UpdatedAt:     existing.UpdatedAt,
					},
				})
				return
			}
		}
	}

	// Stamped here, not by the caller: the row version has to come from one
	// clock for comparisons between devices to mean anything.
	p := store.Progress{
		UserID:        u.ID,
		BookID:        bookID,
		CharOffset:    body.CharOffset,
		ChapterIdx:    body.ChapterIdx,
		ChapterOffset: body.ChapterOffset,
		UpdatedAt:     time.Now().Unix(),
	}
	if err := s.store.PutProgress(r.Context(), p); err != nil {
		writeError(w, http.StatusInternalServerError, "put_progress", err)
		return
	}
	writeJSON(w, http.StatusOK, progressDTO{
		BookID:        bookID,
		CharOffset:    p.CharOffset,
		ChapterIdx:    p.ChapterIdx,
		ChapterOffset: p.ChapterOffset,
		UpdatedAt:     p.UpdatedAt,
	})
}
