package store

import (
	"context"
	"database/sql"
	"fmt"
	"hash/fnv"
	"strings"
	"unicode"
)

// Book is the persisted metadata for a single source file. Chapters live in
// a separate table and are loaded via LoadChapters.
type Book struct {
	ID            int64
	FolderID      int64
	Path          string
	SourcePath    sql.NullString
	Title         string
	Author        sql.NullString
	Description   sql.NullString
	Category      sql.NullString
	Favorite      bool
	ReadingStatus string
	CoverColor    sql.NullString
	CoverLabel    sql.NullString
	Format        string
	Encoding      sql.NullString
	SizeBytes     int64
	CharCount     sql.NullInt64
	ChapterCount  sql.NullInt64
	FileMtime     int64
	FileHash      sql.NullString
	AddedAt       int64
	ScannedAt     int64
}

// Chapter is one row in the chapters table.
//
// Level mirrors library.Chapter.Level: 0 = 卷 / volume header, 1 = 章
// / 折 / etc. The flat list is kept ordered by ByteOffset; the
// frontend nests level=1 entries under the most recent level=0 entry.
type Chapter struct {
	ID         int64
	BookID     int64
	Idx        int64
	Title      string
	Level      int64
	ByteOffset int64
	CharOffset int64
}

// UpsertBook inserts or updates a row by `path` (which is UNIQUE). Returns
// the resulting row id and a bool indicating whether a new row was inserted
// (true) vs. an existing row updated (false).
//
// The caller is expected to have already detected encoding, counted chars,
// and parsed chapters. This function only persists.
func (s *Store) UpsertBook(ctx context.Context, b Book) (int64, bool, error) {
	now := s.nowUnix()
	if b.ReadingStatus == "" {
		b.ReadingStatus = ReadingStatusUnread
	}
	if !b.CoverColor.Valid || !b.CoverLabel.Valid {
		color, label := DefaultCover(b.Title, b.Author.String)
		if !b.CoverColor.Valid {
			b.CoverColor = sql.NullString{String: color, Valid: true}
		}
		if !b.CoverLabel.Valid {
			b.CoverLabel = sql.NullString{String: label, Valid: true}
		}
	}

	// Try update first — common case after the first scan.
	res, err := s.db.ExecContext(ctx, `
        UPDATE books
           SET folder_id     = ?,
               source_path   = ?,
               format        = ?,
               encoding      = ?,
               size_bytes    = ?,
               char_count    = ?,
               chapter_count = ?,
               file_mtime    = ?,
               file_hash     = ?,
               scanned_at    = ?
         WHERE path = ?`,
		b.FolderID, b.SourcePath, b.Format, b.Encoding,
		b.SizeBytes, b.CharCount, b.ChapterCount, b.FileMtime, b.FileHash, now,
		b.Path,
	)
	if err != nil {
		return 0, false, fmt.Errorf("update book: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		var id int64
		if err := s.db.QueryRowContext(ctx, `SELECT id FROM books WHERE path = ?`, b.Path).Scan(&id); err != nil {
			return 0, false, err
		}
		return id, false, nil
	}

	// Not present — insert.
	res, err = s.db.ExecContext(ctx, `
        INSERT INTO books(folder_id, path, source_path, title, author, description,
                          category, favorite, reading_status, cover_color, cover_label,
                          format, encoding,
                          size_bytes, char_count, chapter_count, file_mtime,
                          file_hash, added_at, scanned_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.FolderID, b.Path, b.SourcePath, b.Title, b.Author, b.Description,
		b.Category, boolToInt(b.Favorite), b.ReadingStatus, b.CoverColor, b.CoverLabel,
		b.Format, b.Encoding,
		b.SizeBytes, b.CharCount, b.ChapterCount, b.FileMtime, b.FileHash, now, now,
	)
	if err != nil {
		return 0, false, fmt.Errorf("insert book: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, true, nil
}

// ReplaceChapters wipes any existing chapter rows for the book and inserts the
// supplied slice in order. Wrapped in a transaction so a partial replace
// can't leave a half-populated TOC.
func (s *Store) ReplaceChapters(ctx context.Context, bookID int64, chapters []Chapter) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM chapters WHERE book_id = ?`, bookID); err != nil {
		return fmt.Errorf("clear chapters: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO chapters(book_id, idx, title, level, byte_offset, char_offset) VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, c := range chapters {
		if _, err := stmt.ExecContext(ctx, bookID, c.Idx, c.Title, c.Level, c.ByteOffset, c.CharOffset); err != nil {
			return fmt.Errorf("insert chapter %d: %w", c.Idx, err)
		}
	}
	return tx.Commit()
}

// ListBooks returns every book, newest scan first. folderID==0 means "all".
func (s *Store) ListBooks(ctx context.Context, folderID int64) ([]Book, error) {
	var (
		rows *sql.Rows
		err  error
	)
	const selectCols = `
        id, folder_id, path, source_path, title, author, description, category,
        favorite, reading_status, cover_color, cover_label, format, encoding,
        size_bytes, char_count, chapter_count, file_mtime, file_hash,
        added_at, scanned_at`

	if folderID > 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+selectCols+` FROM books WHERE folder_id = ? ORDER BY scanned_at DESC`,
			folderID)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+selectCols+` FROM books ORDER BY scanned_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Book
	for rows.Next() {
		var b Book
		var favorite int64
		if err := rows.Scan(
			&b.ID, &b.FolderID, &b.Path, &b.SourcePath, &b.Title, &b.Author,
			&b.Description, &b.Category, &favorite, &b.ReadingStatus, &b.CoverColor, &b.CoverLabel,
			&b.Format, &b.Encoding,
			&b.SizeBytes, &b.CharCount, &b.ChapterCount, &b.FileMtime, &b.FileHash,
			&b.AddedAt, &b.ScannedAt,
		); err != nil {
			return nil, err
		}
		b.Favorite = favorite != 0
		ensureBookDefaults(&b)
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBook fetches a single book by id.
func (s *Store) GetBook(ctx context.Context, id int64) (Book, error) {
	var b Book
	var favorite int64
	err := s.db.QueryRowContext(ctx, `
        SELECT id, folder_id, path, source_path, title, author, description, category,
               favorite, reading_status, cover_color, cover_label, format, encoding,
               size_bytes, char_count, chapter_count, file_mtime, file_hash,
               added_at, scanned_at
          FROM books WHERE id = ?`, id).Scan(
		&b.ID, &b.FolderID, &b.Path, &b.SourcePath, &b.Title, &b.Author,
		&b.Description, &b.Category, &favorite, &b.ReadingStatus, &b.CoverColor, &b.CoverLabel,
		&b.Format, &b.Encoding,
		&b.SizeBytes, &b.CharCount, &b.ChapterCount, &b.FileMtime, &b.FileHash,
		&b.AddedAt, &b.ScannedAt,
	)
	b.Favorite = favorite != 0
	ensureBookDefaults(&b)
	return b, err
}

const (
	ReadingStatusUnread   = "unread"
	ReadingStatusReading  = "reading"
	ReadingStatusFinished = "finished"
	ReadingStatusPaused   = "paused"
)

var validReadingStatuses = map[string]bool{
	ReadingStatusUnread:   true,
	ReadingStatusReading:  true,
	ReadingStatusFinished: true,
	ReadingStatusPaused:   true,
}

// NormaliseReadingStatus returns a stable reading-status token.
func NormaliseReadingStatus(v string) (string, bool) {
	v = strings.TrimSpace(strings.ToLower(v))
	if v == "" {
		return ReadingStatusUnread, true
	}
	return v, validReadingStatuses[v]
}

type BookUpdate struct {
	Title         *string
	Author        *string
	Description   *string
	Category      *string
	Favorite      *bool
	ReadingStatus *string
	CoverColor    *string
	CoverLabel    *string
}

// UpdateBookMetadata writes user-owned library management fields.
func (s *Store) UpdateBookMetadata(ctx context.Context, id int64, upd BookUpdate) (Book, error) {
	b, err := s.GetBook(ctx, id)
	if err != nil {
		return Book{}, err
	}
	if upd.Title != nil {
		title := strings.TrimSpace(*upd.Title)
		if title == "" {
			return Book{}, fmt.Errorf("title is required")
		}
		b.Title = title
	}
	if upd.Author != nil {
		b.Author = nullString(strings.TrimSpace(*upd.Author))
	}
	if upd.Description != nil {
		b.Description = nullString(strings.TrimSpace(*upd.Description))
	}
	if upd.Category != nil {
		b.Category = nullString(strings.TrimSpace(*upd.Category))
	}
	if upd.Favorite != nil {
		b.Favorite = *upd.Favorite
	}
	if upd.ReadingStatus != nil {
		status, ok := NormaliseReadingStatus(*upd.ReadingStatus)
		if !ok {
			return Book{}, fmt.Errorf("invalid reading_status")
		}
		b.ReadingStatus = status
	}
	if upd.CoverColor != nil {
		b.CoverColor = nullString(strings.TrimSpace(*upd.CoverColor))
	}
	if upd.CoverLabel != nil {
		b.CoverLabel = nullString(strings.TrimSpace(*upd.CoverLabel))
	}
	ensureBookDefaults(&b)

	res, err := s.db.ExecContext(ctx, `
        UPDATE books
           SET title = ?, author = ?, description = ?, category = ?,
               favorite = ?, reading_status = ?, cover_color = ?, cover_label = ?
         WHERE id = ?`,
		b.Title, b.Author, b.Description, b.Category, boolToInt(b.Favorite),
		b.ReadingStatus, b.CoverColor, b.CoverLabel, id,
	)
	if err != nil {
		return Book{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Book{}, sql.ErrNoRows
	}
	return s.GetBook(ctx, id)
}

// DuplicateGroup is one set of books sharing the same source fingerprint.
type DuplicateGroup struct {
	Hash  string
	Books []Book
}

func (s *Store) DuplicateGroups(ctx context.Context) ([]DuplicateGroup, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT file_hash
          FROM books
         WHERE file_hash IS NOT NULL AND file_hash <> ''
         GROUP BY file_hash
        HAVING COUNT(*) > 1
         ORDER BY COUNT(*) DESC, file_hash ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []DuplicateGroup
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		books, err := s.booksByHash(ctx, hash)
		if err != nil {
			return nil, err
		}
		groups = append(groups, DuplicateGroup{Hash: hash, Books: books})
	}
	return groups, rows.Err()
}

func (s *Store) booksByHash(ctx context.Context, hash string) ([]Book, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, folder_id, path, source_path, title, author, description, category,
               favorite, reading_status, cover_color, cover_label, format, encoding,
               size_bytes, char_count, chapter_count, file_mtime, file_hash,
               added_at, scanned_at
          FROM books
         WHERE file_hash = ?
         ORDER BY title ASC, id ASC`, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Book
	for rows.Next() {
		var b Book
		var favorite int64
		if err := rows.Scan(
			&b.ID, &b.FolderID, &b.Path, &b.SourcePath, &b.Title, &b.Author,
			&b.Description, &b.Category, &favorite, &b.ReadingStatus, &b.CoverColor, &b.CoverLabel,
			&b.Format, &b.Encoding, &b.SizeBytes, &b.CharCount, &b.ChapterCount,
			&b.FileMtime, &b.FileHash, &b.AddedAt, &b.ScannedAt,
		); err != nil {
			return nil, err
		}
		b.Favorite = favorite != 0
		ensureBookDefaults(&b)
		out = append(out, b)
	}
	return out, rows.Err()
}

var coverPalette = []string{
	"#4f6f52",
	"#8a5a44",
	"#3f6f8f",
	"#7a5a8f",
	"#8f5f4a",
	"#596070",
	"#4f7a78",
	"#8a6f3f",
}

// DefaultCover returns deterministic default-cover fields for a book.
func DefaultCover(title, author string) (color, label string) {
	seed := strings.TrimSpace(title + "\x00" + author)
	if seed == "\x00" || seed == "" {
		seed = "zreader"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	color = coverPalette[int(h.Sum32())%len(coverPalette)]

	label = "书"
	for _, r := range strings.TrimSpace(title) {
		if unicode.IsSpace(r) {
			continue
		}
		label = strings.ToUpper(string(r))
		break
	}
	return color, label
}

func ensureBookDefaults(b *Book) {
	if b.ReadingStatus == "" {
		b.ReadingStatus = ReadingStatusUnread
	}
	if !b.CoverColor.Valid || b.CoverColor.String == "" || !b.CoverLabel.Valid || b.CoverLabel.String == "" {
		author := ""
		if b.Author.Valid {
			author = b.Author.String
		}
		color, label := DefaultCover(b.Title, author)
		if !b.CoverColor.Valid || b.CoverColor.String == "" {
			b.CoverColor = sql.NullString{String: color, Valid: true}
		}
		if !b.CoverLabel.Valid || b.CoverLabel.String == "" {
			b.CoverLabel = sql.NullString{String: label, Valid: true}
		}
	}
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// DeleteBook removes one book row. Chapters, progress, and bookmarks are
// removed by ON DELETE CASCADE.
func (s *Store) DeleteBook(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// LoadChapters returns chapters of a book ordered by idx ascending.
func (s *Store) LoadChapters(ctx context.Context, bookID int64) ([]Chapter, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, book_id, idx, title, level, byte_offset, char_offset
         FROM chapters WHERE book_id = ? ORDER BY idx ASC`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chapter
	for rows.Next() {
		var c Chapter
		if err := rows.Scan(&c.ID, &c.BookID, &c.Idx, &c.Title, &c.Level, &c.ByteOffset, &c.CharOffset); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteBooksMissing removes book rows whose `path` is NOT in the supplied
// slice but whose folder_id matches. This is how a scan prunes deleted files.
// Returns the number of rows removed.
func (s *Store) DeleteBooksMissing(ctx context.Context, folderID int64, presentPaths []string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	// Use a temp table to feed paths in (cheap and avoids SQL-too-long for
	// large libraries).
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS scan_present(path TEXT PRIMARY KEY)`); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scan_present`); err != nil {
		return 0, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO scan_present(path) VALUES (?)`)
	if err != nil {
		return 0, err
	}
	for _, p := range presentPaths {
		if _, err := stmt.ExecContext(ctx, p); err != nil {
			_ = stmt.Close()
			return 0, err
		}
	}
	_ = stmt.Close()

	res, err := tx.ExecContext(ctx, `
        DELETE FROM books
         WHERE folder_id = ?
           AND path NOT IN (SELECT path FROM scan_present)`, folderID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}
