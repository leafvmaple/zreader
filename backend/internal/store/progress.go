package store

import (
	"context"
	"database/sql"
)

// Progress is the per-(user, book) reading position. The three-field
// representation (char_offset + chapter_idx + chapter_offset) is redundant by
// design: when re-scanning a book changes chapter boundaries we can recover
// position from whichever pair is still valid.
type Progress struct {
	UserID        string
	BookID        int64
	CharOffset    int64
	ChapterIdx    int64
	ChapterOffset int64
	UpdatedAt     int64
}

// GetProgress returns the user's progress for a book, or sql.ErrNoRows if
// nothing has been saved yet (caller treats this as "start of book").
func (s *Store) GetProgress(ctx context.Context, userID string, bookID int64) (Progress, error) {
	var p Progress
	err := s.db.QueryRowContext(ctx, `
        SELECT user_id, book_id, char_offset, chapter_idx, chapter_offset, updated_at
          FROM user_progress
         WHERE user_id = ? AND book_id = ?`, userID, bookID).Scan(
		&p.UserID, &p.BookID, &p.CharOffset, &p.ChapterIdx, &p.ChapterOffset, &p.UpdatedAt,
	)
	return p, err
}

// PutProgress writes (or replaces) the user's progress. The caller controls
// the timestamp so multi-device merge logic can detect "older write wins"
// and reject stale updates at the handler layer.
func (s *Store) PutProgress(ctx context.Context, p Progress) error {
	if p.UpdatedAt == 0 {
		p.UpdatedAt = s.nowUnix()
	}
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO user_progress(user_id, book_id, char_offset, chapter_idx, chapter_offset, updated_at)
        VALUES (?, ?, ?, ?, ?, ?)
        ON CONFLICT(user_id, book_id) DO UPDATE SET
            char_offset    = excluded.char_offset,
            chapter_idx    = excluded.chapter_idx,
            chapter_offset = excluded.chapter_offset,
            updated_at     = excluded.updated_at`,
		p.UserID, p.BookID, p.CharOffset, p.ChapterIdx, p.ChapterOffset, p.UpdatedAt,
	)
	return err
}

// ProgressMap returns progress for a user across many books, keyed by book id.
// Books without saved progress are absent from the map.
func (s *Store) ProgressMap(ctx context.Context, userID string, bookIDs []int64) (map[int64]Progress, error) {
	if len(bookIDs) == 0 {
		return map[int64]Progress{}, nil
	}
	// Build IN(?,?,?) placeholder string.
	placeholders := make([]byte, 0, len(bookIDs)*2)
	args := make([]any, 0, len(bookIDs)+1)
	args = append(args, userID)
	for i, id := range bookIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, id)
	}
	q := `SELECT user_id, book_id, char_offset, chapter_idx, chapter_offset, updated_at
          FROM user_progress
         WHERE user_id = ? AND book_id IN (` + string(placeholders) + `)`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]Progress, len(bookIDs))
	for rows.Next() {
		var p Progress
		if err := rows.Scan(&p.UserID, &p.BookID, &p.CharOffset, &p.ChapterIdx, &p.ChapterOffset, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out[p.BookID] = p
	}
	return out, rows.Err()
}

// Used to keep the unused-import linter calm when database/sql is otherwise
// only referenced via Store (which lives in store.go).
var _ = sql.ErrNoRows
