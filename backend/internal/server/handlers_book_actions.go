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

	book, err := s.store.GetBook(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "get_book", err)
		return
	}
	chapters, err := s.store.LoadChapters(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_chapters", err)
		return
	}
	runes, err := library.GetFlatRunes(book.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_epub", err)
		return
	}
	text := string(runes)
	matches := searchBookText(text, q, chapters, limit)
	writeJSON(w, http.StatusOK, map[string]any{
		"query":   q,
		"matches": matches,
	})
}

func searchBookText(text, query string, chapters []store.Chapter, limit int) []searchMatchDTO {
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(query)
	queryRunes := utf8.RuneCountInString(query)
	if queryRunes == 0 {
		return nil
	}
	allRunes := []rune(text)
	matches := make([]searchMatchDTO, 0, limit)
	byteCursor := 0
	for len(matches) < limit {
		i := strings.Index(lowerText[byteCursor:], lowerQuery)
		if i < 0 {
			break
		}
		byteStart := byteCursor + i
		charStart := utf8.RuneCountInString(text[:byteStart])
		matches = append(matches, searchMatchDTO{
			CharOffset: int64(charStart),
			ChapterIdx: chapterIdxAtCharOffset(int64(charStart), chapters),
			Snippet:    searchSnippet(allRunes, charStart, queryRunes),
		})
		_, size := utf8.DecodeRuneInString(text[byteStart:])
		if size <= 0 {
			break
		}
		byteCursor = byteStart + size
	}
	return matches
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
		b.Note = sql.NullString{String: note, Valid: true}
	}
	b, err = s.store.AddBookmark(r.Context(), b)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "add_bookmark", err)
		return
	}
	writeJSON(w, http.StatusCreated, toBookmarkDTO(b))
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
	writeJSON(w, http.StatusOK, map[string]any{"scan": res, "job": toJobDTO(done)})
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
	if deleteSource {
		sourcePath, err := library.FindBookSource(folder.Path, book)
		if err != nil {
			return fmt.Errorf("%w: %v", errSourceNotFound, err)
		}
		if err := os.Remove(sourcePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete source: %w", err)
		}
	}
	if err := os.Remove(book.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete cache: %w", err)
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
