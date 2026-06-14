package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
)

// Folder represents one user-authorised library directory on the NAS.
type Folder struct {
	ID         int64
	Path       string
	AddedAt    int64
	LastScanAt sql.NullInt64
	BookCount  int64 // populated by ListFolders, not by InsertFolder
}

// ErrFolderExists is returned when AddFolder hits the UNIQUE(path) constraint.
var ErrFolderExists = errors.New("folder already registered")

// AddFolder registers a new library folder. The path is normalised to an
// absolute clean path so different ways of writing the same directory don't
// produce duplicate rows. Returns ErrFolderExists if a row with the same
// normalised path already exists.
func (s *Store) AddFolder(ctx context.Context, path string) (Folder, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Folder{}, fmt.Errorf("normalise path: %w", err)
	}
	abs = filepath.Clean(abs)

	now := s.nowUnix()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO library_folders(path, added_at) VALUES (?, ?)`,
		abs, now,
	)
	if err != nil {
		// SQLite returns "UNIQUE constraint failed" — easier to detect via a
		// secondary lookup than by string-matching the driver error.
		if existing, lookupErr := s.folderByPath(ctx, abs); lookupErr == nil {
			return existing, ErrFolderExists
		}
		return Folder{}, fmt.Errorf("insert folder: %w", err)
	}
	id, _ := res.LastInsertId()
	return Folder{ID: id, Path: abs, AddedAt: now}, nil
}

// folderByPath is the internal lookup used by AddFolder's conflict path.
func (s *Store) folderByPath(ctx context.Context, path string) (Folder, error) {
	var f Folder
	err := s.db.QueryRowContext(ctx,
		`SELECT id, path, added_at, last_scan_at FROM library_folders WHERE path = ?`,
		path,
	).Scan(&f.ID, &f.Path, &f.AddedAt, &f.LastScanAt)
	return f, err
}

// ListFolders returns every registered folder with a denormalised book count.
func (s *Store) ListFolders(ctx context.Context) ([]Folder, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT f.id, f.path, f.added_at, f.last_scan_at,
               (SELECT COUNT(*) FROM books b WHERE b.folder_id = f.id) AS book_count
        FROM library_folders f
        ORDER BY f.added_at ASC
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Folder
	for rows.Next() {
		var f Folder
		if err := rows.Scan(&f.ID, &f.Path, &f.AddedAt, &f.LastScanAt, &f.BookCount); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetFolder fetches one registered library folder by id.
func (s *Store) GetFolder(ctx context.Context, id int64) (Folder, error) {
	var f Folder
	err := s.db.QueryRowContext(ctx,
		`SELECT id, path, added_at, last_scan_at FROM library_folders WHERE id = ?`,
		id,
	).Scan(&f.ID, &f.Path, &f.AddedAt, &f.LastScanAt)
	return f, err
}

// DeleteFolder removes a folder and (via ON DELETE CASCADE) every book and
// chapter under it. User progress for those books is also wiped — acceptable
// for MVP since the user explicitly removed the source.
func (s *Store) DeleteFolder(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM library_folders WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// TouchFolderScan stamps last_scan_at on a folder after a scan completes.
func (s *Store) TouchFolderScan(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE library_folders SET last_scan_at = ? WHERE id = ?`,
		s.nowUnix(), id,
	)
	return err
}
