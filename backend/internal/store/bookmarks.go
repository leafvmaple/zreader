package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Bookmark is a user-created reading marker inside one book.
type Bookmark struct {
	ID         int64
	UserID     string
	BookID     int64
	CharOffset int64
	ChapterIdx sql.NullInt64
	Note       sql.NullString
	CreatedAt  int64
}

// ListBookmarks returns bookmarks for one user/book in reading order.
func (s *Store) ListBookmarks(ctx context.Context, userID string, bookID int64) ([]Bookmark, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, user_id, book_id, char_offset, chapter_idx, note, created_at
          FROM bookmarks
         WHERE user_id = ? AND book_id = ?
         ORDER BY char_offset ASC, created_at ASC`,
		userID, bookID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Bookmark
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.ID, &b.UserID, &b.BookID, &b.CharOffset, &b.ChapterIdx, &b.Note, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AddBookmark inserts a bookmark. The caller supplies user/book/offset; the
// store stamps created_at.
func (s *Store) AddBookmark(ctx context.Context, b Bookmark) (Bookmark, error) {
	now := s.nowUnix()
	res, err := s.db.ExecContext(ctx, `
        INSERT INTO bookmarks(user_id, book_id, char_offset, chapter_idx, note, created_at)
        VALUES (?, ?, ?, ?, ?, ?)`,
		b.UserID, b.BookID, b.CharOffset, b.ChapterIdx, b.Note, now,
	)
	if err != nil {
		return Bookmark{}, fmt.Errorf("insert bookmark: %w", err)
	}
	id, _ := res.LastInsertId()
	b.ID = id
	b.CreatedAt = now
	return b, nil
}

// DeleteBookmark removes one bookmark belonging to the given user/book.
func (s *Store) DeleteBookmark(ctx context.Context, userID string, bookID, bookmarkID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM bookmarks WHERE id = ? AND user_id = ? AND book_id = ?`,
		bookmarkID, userID, bookID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
