package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Book is the persisted metadata for a single source file. Chapters live in
// a separate table and are loaded via LoadChapters.
type Book struct {
	ID           int64
	FolderID     int64
	Path         string
	SourcePath   sql.NullString
	Title        string
	Author       sql.NullString
	Format       string
	Encoding     sql.NullString
	SizeBytes    int64
	CharCount    sql.NullInt64
	ChapterCount sql.NullInt64
	FileMtime    int64
	FileHash     sql.NullString
	AddedAt      int64
	ScannedAt    int64
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

	// Try update first — common case after the first scan.
	res, err := s.db.ExecContext(ctx, `
        UPDATE books
           SET folder_id     = ?,
               source_path   = ?,
               title         = ?,
               author        = ?,
               format        = ?,
               encoding      = ?,
               size_bytes    = ?,
               char_count    = ?,
               chapter_count = ?,
               file_mtime    = ?,
               file_hash     = ?,
               scanned_at    = ?
         WHERE path = ?`,
		b.FolderID, b.SourcePath, b.Title, b.Author, b.Format, b.Encoding,
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
        INSERT INTO books(folder_id, path, source_path, title, author, format, encoding,
                          size_bytes, char_count, chapter_count, file_mtime,
                          file_hash, added_at, scanned_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		b.FolderID, b.Path, b.SourcePath, b.Title, b.Author, b.Format, b.Encoding,
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
        id, folder_id, path, source_path, title, author, format, encoding,
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
		if err := rows.Scan(
			&b.ID, &b.FolderID, &b.Path, &b.SourcePath, &b.Title, &b.Author, &b.Format, &b.Encoding,
			&b.SizeBytes, &b.CharCount, &b.ChapterCount, &b.FileMtime, &b.FileHash,
			&b.AddedAt, &b.ScannedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBook fetches a single book by id.
func (s *Store) GetBook(ctx context.Context, id int64) (Book, error) {
	var b Book
	err := s.db.QueryRowContext(ctx, `
        SELECT id, folder_id, path, source_path, title, author, format, encoding,
               size_bytes, char_count, chapter_count, file_mtime, file_hash,
               added_at, scanned_at
          FROM books WHERE id = ?`, id).Scan(
		&b.ID, &b.FolderID, &b.Path, &b.SourcePath, &b.Title, &b.Author, &b.Format, &b.Encoding,
		&b.SizeBytes, &b.CharCount, &b.ChapterCount, &b.FileMtime, &b.FileHash,
		&b.AddedAt, &b.ScannedAt,
	)
	return b, err
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
