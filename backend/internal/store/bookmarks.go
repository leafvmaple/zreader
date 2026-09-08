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

// UpdateBookmarkNote replaces one bookmark's note and returns the row as
// it now stands. An empty note clears it back to NULL, so "no note" is one
// state rather than two.
//
// Scoped by user and book like every other bookmark call: the id alone
// would let one account edit another's note by guessing a number.
func (s *Store) UpdateBookmarkNote(ctx context.Context, userID string, bookID, bookmarkID int64, note string) (Bookmark, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE bookmarks SET note = ? WHERE id = ? AND user_id = ? AND book_id = ?`,
		nullString(note), bookmarkID, userID, bookID,
	)
	if err != nil {
		return Bookmark{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Bookmark{}, sql.ErrNoRows
	}

	var b Bookmark
	err = s.db.QueryRowContext(ctx, `
        SELECT id, user_id, book_id, char_offset, chapter_idx, note, created_at
          FROM bookmarks WHERE id = ? AND user_id = ? AND book_id = ?`,
		bookmarkID, userID, bookID,
	).Scan(&b.ID, &b.UserID, &b.BookID, &b.CharOffset, &b.ChapterIdx, &b.Note, &b.CreatedAt)
	if err != nil {
		return Bookmark{}, err
	}
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
