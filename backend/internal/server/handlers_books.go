package server

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/leafvmaple/zreader/internal/library"
	"github.com/leafvmaple/zreader/internal/store"
)

type bookDTO struct {
	ID           int64  `json:"id"`
	FolderID     int64  `json:"folder_id"`
	Path         string `json:"path"`
	SourcePath   string `json:"source_path,omitempty"`
	Title        string `json:"title"`
	Author       string `json:"author,omitempty"`
	Format       string `json:"format"`
	Encoding     string `json:"encoding,omitempty"`
	SizeBytes    int64  `json:"size_bytes"`
	CharCount    int64  `json:"char_count,omitempty"`
	ChapterCount int64  `json:"chapter_count,omitempty"`
	FileMtime    int64  `json:"file_mtime"`
	AddedAt      int64  `json:"added_at"`
	ScannedAt    int64  `json:"scanned_at"`
}

type chapterDTO struct {
	Idx        int64  `json:"idx"`
	Title      string `json:"title"`
	Level      int64  `json:"level"`
	CharOffset int64  `json:"char_offset"`
}

func toBookDTO(b store.Book) bookDTO {
	d := bookDTO{
		ID: b.ID, FolderID: b.FolderID, Path: b.Path, Title: b.Title,
		Format: b.Format, SizeBytes: b.SizeBytes,
		FileMtime: b.FileMtime, AddedAt: b.AddedAt, ScannedAt: b.ScannedAt,
	}
	if b.Author.Valid {
		d.Author = b.Author.String
	}
	if b.SourcePath.Valid {
		d.SourcePath = b.SourcePath.String
	}
	if b.Encoding.Valid {
		d.Encoding = b.Encoding.String
	}
	if b.CharCount.Valid {
		d.CharCount = b.CharCount.Int64
	}
	if b.ChapterCount.Valid {
		d.ChapterCount = b.ChapterCount.Int64
	}
	return d
}

func (s *Server) handleListBooks(w http.ResponseWriter, r *http.Request) {
	var folderID int64
	if v := r.URL.Query().Get("folder_id"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
			folderID = parsed
		}
	}
	books, err := s.store.ListBooks(r.Context(), folderID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_books", err)
		return
	}
	out := make([]bookDTO, 0, len(books))
	for _, b := range books {
		out = append(out, toBookDTO(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"books": out})
}

func (s *Server) handleGetBook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
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
	cdtos := make([]chapterDTO, 0, len(chapters))
	for _, c := range chapters {
		cdtos = append(cdtos, chapterDTO{Idx: c.Idx, Title: c.Title, Level: c.Level, CharOffset: c.CharOffset})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"book":     toBookDTO(book),
		"chapters": cdtos,
	})
}

// handleBookContent serves a slice of the book's plain-text view.
// Query:
//
//	from (char offset, default 0)
//	len  (chars to return, default 5000, max 50000)
//
// The text is reconstructed from the cached EPUB on the fly via
// library.GetFlatRunes — its in-process cache amortises the EPUB
// parse across requests so the per-slice cost is just rune-slice
// indexing.
func (s *Server) handleBookContent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}

	from := parseIntQuery(r, "from", 0)
	if from < 0 {
		from = 0
	}
	const defaultLen = 5000
	const maxLen = 50000
	want := parseIntQuery(r, "len", defaultLen)
	if want <= 0 {
		want = defaultLen
	}
	if want > maxLen {
		want = maxLen
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

	runes, err := library.GetFlatRunes(book.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_epub", err)
		return
	}

	totalChars := int64(len(runes))
	if int64(from) >= totalChars {
		writeJSON(w, http.StatusOK, map[string]any{
			"text": "", "from": from, "len": 0,
			"total_chars": totalChars, "eof": true,
		})
		return
	}
	end := from + want
	if int64(end) > totalChars {
		end = int(totalChars)
	}
	slice := string(runes[from:end])
	writeJSON(w, http.StatusOK, map[string]any{
		"text":        slice,
		"from":        from,
		"len":         end - from,
		"total_chars": totalChars,
		"eof":         int64(end) >= totalChars,
	})
}

func parseIntQuery(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
