package server

// Cleaned export.
//
// GET /api/v1/books/{id}/export streams the book as chapter-oriented JSONL
// pirate-rip debris removed — see internal/export for the passes and for
// why the rules are data rather than a plugin interface.
//
// ?preview=1 returns just the statistics plus the first few chapters, so the
// UI can show what a given rule combination would actually remove before
// the user commits to a download.

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/leafvmaple/zreader/internal/export"
	"github.com/leafvmaple/zreader/internal/library"
)

// previewRecords is how many records the preview returns — enough to judge
// the cleaning by eye without shipping the book through the JSON encoder
// twice.
const previewRecords = 3

func (s *Server) handleExportBook(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_id", err)
		return
	}
	book, err := s.store.GetBook(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", errors.New("book not found"))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get_book", err)
		return
	}
	if book.Format != "epub" {
		writeError(w, http.StatusBadRequest, "unsupported_format",
			errors.New("only text-backed books can be exported; image-only PDFs have no text layer"))
		return
	}

	view, err := library.GetFlatTextView(book.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "read_text", err)
		return
	}
	rows, err := s.store.LoadChapters(r.Context(), book.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_chapters", err)
		return
	}
	chapters := make([]export.Chapter, 0, len(rows))
	for _, c := range rows {
		chapters = append(chapters, export.Chapter{
			Idx:        int(c.Idx),
			Title:      c.Title,
			CharOffset: int(c.CharOffset),
		})
	}

	// A bad rules file must not take the endpoint down — fall back to the
	// built-ins and tell the caller what was wrong in a header.
	rules, ruleErr := export.LoadRules(filepath.Join(s.cfg.DataDir, export.RuleOverridesName))
	if ruleErr != nil {
		s.cfg.Logger.Printf("export: %v (using built-in rules)", ruleErr)
	}

	options := export.CorpusOptions{
		Rules:     parseRules(r),
		Anonymize: boolParam(r, "anonymize", false),
	}
	meta := export.Meta{Title: book.Title}
	if book.Author.Valid {
		meta.Author = book.Author.String
	}

	records, stats := export.BuildCorpus(meta, chapters, view.Text, options, rules)
	filename := export.CorpusFilename(view.Text)

	if r.URL.Query().Get("preview") != "" {
		head := records
		if len(head) > previewRecords {
			head = head[:previewRecords]
		}
		out := map[string]any{"filename": filename, "stats": stats, "sample": head}
		if ruleErr != nil {
			out["rules_warning"] = ruleErr.Error()
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	if stats.ReplacementCharacters > 0 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_text_encoding",
			errors.New("source contains Unicode replacement characters; repair or re-import it before exporting"))
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", contentDisposition(filename))
	if err := export.WriteCorpusJSONL(w, records); err != nil {
		// Headers are already out; the truncated body is the only signal
		// available, so just record it.
		s.cfg.Logger.Printf("export book %d: %v", book.ID, err)
	}
}

// parseRules reads the rule toggles. Absent means on: the endpoint's
// reason to exist is cleaning, so a bare ?  request should clean.
func parseRules(r *http.Request) export.Rules {
	return export.Rules{
		Promo:       boolParam(r, "promo", true),
		EdgeLines:   boolParam(r, "edges", true),
		Normalise:   boolParam(r, "normalise", true),
		AuthorNotes: boolParam(r, "notes", true),
	}
}

func boolParam(r *http.Request, name string, fallback bool) bool {
	v := r.URL.Query().Get(name)
	if v == "" {
		return fallback
	}
	return v != "0" && !strings.EqualFold(v, "false")
}

// contentDisposition builds a header that survives a CJK filename.
//
// Two encodings, because they serve different readers: the plain
// `filename` is a pure-ASCII fallback, and RFC 5987's `filename*` carries
// the real name. `filename*` is percent-encoded UTF-8 — NOT the
// `=?utf-8?q?…?=` MIME word used in mail headers, which browsers do not
// decode here.
func contentDisposition(name string) string {
	ascii := asciiFilename(name)
	if ascii == "" {
		ascii = "export.jsonl"
	}
	return fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", ascii, rfc5987Escape(name))
}

// rfc5987Escape percent-encodes everything outside the unreserved set.
// Erring toward over-escaping is free — the header is machine-read.
func rfc5987Escape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// asciiFilename keeps only characters safe in a bare `filename`. A title
// written entirely in CJK reduces to just the extension, which is no use
// as a filename — so a result with no stem is reported as empty and the
// caller substitutes a generic name.
func asciiFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	out := strings.TrimLeft(b.String(), ".-_")
	if i := strings.LastIndex(out, "."); i <= 0 {
		return ""
	}
	return out
}
