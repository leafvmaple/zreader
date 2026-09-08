package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/leafvmaple/zreader/internal/library"
	"github.com/leafvmaple/zreader/internal/store"
)

type bookmarkDTO struct {
	ID         int64  `json:"id"`
	BookID     int64  `json:"book_id"`
	CharOffset int64  `json:"char_offset"`
	ChapterIdx int64  `json:"chapter_idx,omitempty"`
	Note       string `json:"note,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

type searchMatchDTO struct {
	CharOffset int64  `json:"char_offset"`
	ChapterIdx int64  `json:"chapter_idx"`
	Snippet    string `json:"snippet"`
}

func toBookmarkDTO(b store.Bookmark) bookmarkDTO {
	d := bookmarkDTO{
		ID:         b.ID,
		BookID:     b.BookID,
		CharOffset: b.CharOffset,
		CreatedAt:  b.CreatedAt,
	}
	if b.ChapterIdx.Valid {
		d.ChapterIdx = b.ChapterIdx.Int64
	}
	if b.Note.Valid {
		d.Note = b.Note.String
	}
	return d
}

func (s *Server) handleSearchBook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "missing_query", errors.New("q is required"))
		return
	}
	limit := parseIntQuery(r, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	// Where to resume from, as a character offset into the book. Without
	// it a search on a long book could only ever show hits from the
	// opening chapters: the scan stopped at the first `limit` matches and
	// there was no way to ask for the next page.
	from := parseIntQuery(r, "from", 0)
	if from < 0 {
		from = 0
	}

	book, err := s.store.GetBook(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "get_book", err)
		return
	}
	if book.Format != "epub" {
		writeError(w, http.StatusBadRequest, "unsupported_search", errors.New("book has no searchable text"))
		return
	}
	chapters, err := s.store.LoadChapters(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_chapters", err)
		return
	}
	view, err := library.GetFlatTextView(book.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_epub", err)
		return
	}
	matches, next, total := searchBookText(view, q, chapters, from, limit)
	out := map[string]any{
		"query":   q,
		"matches": matches,
		"total":   total,
	}
	if next > 0 {
		out["next_from"] = next
	}
	writeJSON(w, http.StatusOK, out)
}

// searchBookText returns up to `limit` matches at or after the character
// offset `from`, the offset to resume at for the next page (0 when there
// are none), and the total number of matches in the book.
//
// The rune offset is carried forward rather than recomputed. It used to be
// utf8.RuneCountInString(view.Text[:byteStart]) per match — a scan from
// byte zero every time, which is invisible when the hits are near the
// front and 290 ms on a 7.4M-character book when they are near the back.
//
// Scanning happens entirely in the folded text. Its byte offsets are its
// own — folding changes byte lengths — but its rune offsets are the
// book's, because the fold is one rune in, one rune out. Snippets are cut
// from the original runes, so what is shown is what is written.
func searchBookText(
	view *library.FlatTextView,
	query string,
	chapters []store.Chapter,
	from, limit int,
) (matches []searchMatchDTO, nextFrom int, total int) {
	foldedQuery := library.FoldStringForSearch(query)
	queryRunes := utf8.RuneCountInString(query)
	if queryRunes == 0 {
		return nil, 0, 0
	}
	matches = make([]searchMatchDTO, 0, limit)

	byteCursor := 0
	charCursor := 0
	for {
		i := strings.Index(view.FoldedText[byteCursor:], foldedQuery)
		if i < 0 {
			break
		}
		byteStart := byteCursor + i
		// Advance the rune count over the bytes just skipped instead of
		// counting the whole prefix again.
		charStart := charCursor + utf8.RuneCountInString(view.FoldedText[byteCursor:byteStart])
		total++

		if charStart >= from {
			if len(matches) < limit {
				matches = append(matches, searchMatchDTO{
					CharOffset: int64(charStart),
					ChapterIdx: chapterIdxAtCharOffset(int64(charStart), chapters),
					Snippet:    searchSnippet(view.Runes, charStart, queryRunes),
				})
			} else if nextFrom == 0 {
				nextFrom = charStart
			}
		}

		_, size := utf8.DecodeRuneInString(view.FoldedText[byteStart:])
		if size <= 0 {
			break
		}
		byteCursor = byteStart + size
		charCursor = charStart + 1
	}
	return matches, nextFrom, total
}

func searchSnippet(runes []rune, start, queryLen int) string {
	const radius = 36
	lo := start - radius
	if lo < 0 {
		lo = 0
	}
	hi := start + queryLen + radius
	if hi > len(runes) {
		hi = len(runes)
	}
	s := strings.Join(strings.Fields(string(runes[lo:hi])), " ")
	if lo > 0 {
		s = "..." + s
	}
	if hi < len(runes) {
		s += "..."
	}
	return s
}

func chapterIdxAtCharOffset(offset int64, chapters []store.Chapter) int64 {
	if len(chapters) == 0 {
		return 1
	}
	idx := chapters[0].Idx
	for _, c := range chapters {
		if c.CharOffset <= offset {
			idx = c.Idx
		} else {
			break
		}
	}
	return idx
}

// maxBookmarkNoteRunes bounds what a note may hold. A bookmark note is a
// line about why you marked the spot, not a place to paste a chapter — and
// the whole list is loaded with the drawer.
const maxBookmarkNoteRunes = 500

func (s *Server) handleListBookmarks(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}
	if _, err := s.store.GetBook(r.Context(), bookID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "get_book", err)
		return
	}
	u := currentUser(r)
	bookmarks, err := s.store.ListBookmarks(r.Context(), u.ID, bookID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_bookmarks", err)
		return
	}
	out := make([]bookmarkDTO, 0, len(bookmarks))
	for _, b := range bookmarks {
		out = append(out, toBookmarkDTO(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"bookmarks": out})
}

func (s *Server) handleAddBookmark(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}
	var body struct {
		CharOffset int64  `json:"char_offset"`
		ChapterIdx int64  `json:"chapter_idx"`
		Note       string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	book, err := s.store.GetBook(r.Context(), bookID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "get_book", err)
		return
	}
	if body.CharOffset < 0 || (book.CharCount.Valid && body.CharOffset > book.CharCount.Int64) {
		writeError(w, http.StatusBadRequest, "bad_offset", fmt.Errorf("char_offset out of range"))
		return
	}

	u := currentUser(r)
	b := store.Bookmark{
		UserID:     u.ID,
		BookID:     bookID,
		CharOffset: body.CharOffset,
	}
	if body.ChapterIdx > 0 {
		b.ChapterIdx = sql.NullInt64{Int64: body.ChapterIdx, Valid: true}
	}
	if note := strings.TrimSpace(body.Note); note != "" {
		if utf8.RuneCountInString(note) > maxBookmarkNoteRunes {
			writeError(w, http.StatusBadRequest, "note_too_long",
				fmt.Errorf("note is limited to %d characters", maxBookmarkNoteRunes))
			return
		}
		b.Note = sql.NullString{String: note, Valid: true}
	}
	b, err = s.store.AddBookmark(r.Context(), b)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "add_bookmark", err)
		return
	}
	writeJSON(w, http.StatusCreated, toBookmarkDTO(b))
}

// handleUpdateBookmark edits a bookmark's note. Adding one at creation
// time was already possible; without this there was no way to write a note
// on a bookmark you had already dropped, which is when you usually know
// what you wanted to say about it.
func (s *Server) handleUpdateBookmark(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}
	bookmarkID, err := strconv.ParseInt(r.PathValue("bookmark_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_bookmark_id", err)
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_json", err)
		return
	}
	note := strings.TrimSpace(body.Note)
	if utf8.RuneCountInString(note) > maxBookmarkNoteRunes {
		writeError(w, http.StatusBadRequest, "note_too_long",
			fmt.Errorf("note is limited to %d characters", maxBookmarkNoteRunes))
		return
	}

	u := currentUser(r)
	b, err := s.store.UpdateBookmarkNote(r.Context(), u.ID, bookID, bookmarkID, note)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", errors.New("bookmark not found"))
			return
		}
		writeError(w, http.StatusInternalServerError, "update_bookmark", err)
		return
	}
	writeJSON(w, http.StatusOK, toBookmarkDTO(b))
}

func (s *Server) handleDeleteBookmark(w http.ResponseWriter, r *http.Request) {
	bookID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}
	bookmarkID, err := strconv.ParseInt(r.PathValue("bookmark_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_bookmark_id", err)
		return
	}
	u := currentUser(r)
	if err := s.store.DeleteBookmark(r.Context(), u.ID, bookID, bookmarkID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "delete_bookmark", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleReparseBook(w http.ResponseWriter, r *http.Request) {
	book, folder, ok := s.bookAndFolder(w, r)
	if !ok {
		return
	}
	payload := jobPayload{BookIDs: []int64{book.ID}, Action: "reparse"}
	job, err := s.createJob(r.Context(), "batch", "Reparse book", payload, folder.ID, book.ID, 1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_job", err)
		return
	}
	_ = s.store.StartJob(r.Context(), job.ID)
	scanner := &library.Scanner{Store: s.store, Logger: s.cfg.Logger}
	res, err := scanner.ReparseBook(r.Context(), folder, book)
	if err != nil {
		_ = s.store.FinishJob(r.Context(), job.ID, store.JobResult{Total: 1, Completed: 1, Error: err.Error()})
		writeError(w, http.StatusConflict, "source_not_found", err)
		return
	}
	if err := s.store.FinishJob(r.Context(), job.ID, store.JobResult{
		Total: 1, Completed: 1, Added: int64(res.Added), Updated: int64(res.Updated), Removed: int64(res.Removed),
	}); err != nil {
		s.cfg.Logger.Printf("finish job %d: %v", job.ID, err)
	}
	done, _ := s.store.GetJob(r.Context(), job.ID)
	writeJSON(w, http.StatusOK, map[string]any{"scan": publicScanResult(res), "job": toJobDTO(done)})
}

func (s *Server) handleDeleteBook(w http.ResponseWriter, r *http.Request) {
	book, folder, ok := s.bookAndFolder(w, r)
	if !ok {
		return
	}
	deleteSource := r.URL.Query().Get("source") != "false"
	if err := s.deleteBook(r.Context(), book, folder, deleteSource); err != nil {
		if errors.Is(err, errSourceNotFound) {
			writeError(w, http.StatusConflict, "source_not_found", err)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "delete_book", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var errSourceNotFound = errors.New("source_not_found")

func (s *Server) deleteBook(ctx context.Context, book store.Book, folder store.Folder, deleteSource bool) error {
	sourcePath, sourceErr := library.FindBookSource(folder.Path, book)
	if deleteSource {
		if sourceErr != nil {
			return fmt.Errorf("%w: %v", errSourceNotFound, sourceErr)
		}
		if err := os.Remove(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete source: %w", err)
		}
	}
	if sourceErr != nil || book.Path != sourcePath {
		if err := os.Remove(book.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete cache: %w", err)
		}
	}
	if err := s.store.DeleteBook(ctx, book.ID); err != nil {
		return err
	}
	return nil
}

func (s *Server) bookAndFolder(w http.ResponseWriter, r *http.Request) (store.Book, store.Folder, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return store.Book{}, store.Folder{}, false
	}
	book, err := s.store.GetBook(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
			return store.Book{}, store.Folder{}, false
		}
		writeError(w, http.StatusInternalServerError, "get_book", err)
		return store.Book{}, store.Folder{}, false
	}
	folder, err := s.store.GetFolder(r.Context(), book.FolderID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "folder_not_found", err)
			return store.Book{}, store.Folder{}, false
		}
		writeError(w, http.StatusInternalServerError, "get_folder", err)
		return store.Book{}, store.Folder{}, false
	}
	return book, folder, true
}
