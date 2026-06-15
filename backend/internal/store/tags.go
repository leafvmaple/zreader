package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

type Tag struct {
	ID      int64
	Name    string
	Color   sql.NullString
	AddedAt int64
}

func normaliseTagName(name string) string {
	return strings.TrimSpace(name)
}

func (s *Store) ListTags(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT id, name, color, added_at
          FROM tags
         ORDER BY lower(name) ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) TagsForBook(ctx context.Context, bookID int64) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, `
        SELECT t.id, t.name, t.color, t.added_at
          FROM tags t
          JOIN book_tags bt ON bt.tag_id = t.id
         WHERE bt.book_id = ?
         ORDER BY lower(t.name) ASC`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Color, &t.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) SetBookTags(ctx context.Context, bookID int64, names []string) ([]Tag, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM book_tags WHERE book_id = ?`, bookID); err != nil {
		return nil, err
	}
	tags, err := s.ensureTagsTx(ctx, tx, names)
	if err != nil {
		return nil, err
	}
	for _, tag := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO book_tags(book_id, tag_id) VALUES (?, ?)`, bookID, tag.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.TagsForBook(ctx, bookID)
}

func (s *Store) AddTagsToBooks(ctx context.Context, bookIDs []int64, names []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	tags, err := s.ensureTagsTx(ctx, tx, names)
	if err != nil {
		return err
	}
	for _, bookID := range bookIDs {
		for _, tag := range tags {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO book_tags(book_id, tag_id) VALUES (?, ?)`, bookID, tag.ID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) RemoveTagsFromBooks(ctx context.Context, bookIDs []int64, names []string) error {
	names = cleanTagNames(names)
	if len(bookIDs) == 0 || len(names) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, bookID := range bookIDs {
		for _, name := range names {
			if _, err := tx.ExecContext(ctx, `
                DELETE FROM book_tags
                 WHERE book_id = ?
                   AND tag_id IN (SELECT id FROM tags WHERE name = ?)`, bookID, name); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) ensureTagsTx(ctx context.Context, tx *sql.Tx, names []string) ([]Tag, error) {
	names = cleanTagNames(names)
	tags := make([]Tag, 0, len(names))
	now := s.nowUnix()
	for _, name := range names {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO tags(name, added_at) VALUES (?, ?)`, name, now); err != nil {
			return nil, err
		}
		var t Tag
		if err := tx.QueryRowContext(ctx, `SELECT id, name, color, added_at FROM tags WHERE name = ?`, name).
			Scan(&t.ID, &t.Name, &t.Color, &t.AddedAt); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

func cleanTagNames(names []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = normaliseTagName(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func ValidateTagNames(names []string) error {
	for _, name := range cleanTagNames(names) {
		if len([]rune(name)) > 32 {
			return fmt.Errorf("tag %q is too long", name)
		}
	}
	return nil
}
