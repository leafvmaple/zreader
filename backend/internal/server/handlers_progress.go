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

	// Reading a book is what should make it "in progress" and then
	// "finished". The column only ever moved when the reader set it by
	// hand, so a book read cover to cover still sat on the shelf as
	// unread. Failure here is logged, not returned: the position is
	// already saved and that is what the request was for.
	if book, err := s.store.GetBook(r.Context(), bookID); err == nil {
		if err := s.store.AdvanceReadingStatus(r.Context(), bookID, atBookEnd(p.CharOffset, book)); err != nil {
			s.cfg.Logger.Printf("advance reading status for book %d: %v", bookID, err)
		}
	}
	writeJSON(w, http.StatusOK, progressDTO{
		BookID:        bookID,
		CharOffset:    p.CharOffset,
		ChapterIdx:    p.ChapterIdx,
		ChapterOffset: p.ChapterOffset,
		UpdatedAt:     p.UpdatedAt,
	})
}

// atBookEnd reports whether a saved position is close enough to the end to
// call the book finished.
//
// What "close enough" means depends on what the units are. For a
// page-based book the position is a page index and it is exact: the last
// page is the end, and nothing else is.
//
// For text it cannot be exact. Progress is a linear pixel-to-character
// estimate over the chapter's measured height, so the last screenful maps
// to offsets before the chapter's true end — scrolled to the very bottom
// of the last chapter, a 38k-character book reported 467 short and a 495k
// one 772 short. The gap tracks one viewport of text, not the book's
// length, which is why the slack has a floor rather than being a flat
// percentage.
func atBookEnd(offset int64, book store.Book) bool {
	if !book.CharCount.Valid || book.CharCount.Int64 <= 0 {
		return false
	}
	total := book.CharCount.Int64

	// An image PDF stores pages in both fields (see the scanner's
	// pdf-image branch), where a character slack of a thousand is most of
	// the book — it marked every PDF finished the moment it was opened.
	if book.Format == "pdf-image" {
		return offset >= total-1
	}

	slack := total / 100
	if slack < progressEndSlackMin {
		slack = progressEndSlackMin
	}
	if slack > progressEndSlackMax {
		slack = progressEndSlackMax
	}
	return offset >= total-slack
}

const (
	progressEndSlackMin = 1000
	progressEndSlackMax = 3000
)
