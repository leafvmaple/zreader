package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestOpenMigratesLegacySchema(t *testing.T) {
	dir := t.TempDir()
	createLegacyDB(t, dir)

	st := openExistingTestStore(t, dir)
	defer closeStore(t, st)

	bookColumns := tableColumns(t, st, "books")
	for _, name := range []string{
		"source_path",
		"description",
		"category",
		"favorite",
		"reading_status",
		"cover_color",
		"cover_label",
	} {
		if !bookColumns[name] {
			t.Fatalf("books.%s was not added by migration", name)
		}
	}
	if !tableColumns(t, st, "chapters")["level"] {
		t.Fatalf("chapters.level was not added by migration")
	}

	book, err := st.GetBook(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetBook after migration: %v", err)
	}
	if book.ReadingStatus != ReadingStatusUnread {
		t.Fatalf("ReadingStatus = %q, want %q", book.ReadingStatus, ReadingStatusUnread)
	}
	if !book.CoverColor.Valid || !book.CoverLabel.Valid {
		t.Fatalf("default cover fields were not available after migration")
	}

	err = st.ReplaceChapters(context.Background(), book.ID, []Chapter{{
		Idx:        1,
		Title:      "Chapter A",
		Level:      0,
		ByteOffset: 0,
		CharOffset: 0,
	}})
	if err != nil {
		t.Fatalf("ReplaceChapters after migration: %v", err)
	}
	chapters, err := st.LoadChapters(context.Background(), book.ID)
	if err != nil {
		t.Fatalf("LoadChapters after migration: %v", err)
	}
	if len(chapters) != 1 || chapters[0].Level != 0 {
		t.Fatalf("chapters after migration = %+v, want one level-0 chapter", chapters)
	}
}

func TestUpsertBookPreservesUserMetadataAndBookState(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	defer closeStore(t, st)

	folder, err := st.AddFolder(ctx, filepath.Join(t.TempDir(), "library"))
	if err != nil {
		t.Fatalf("AddFolder: %v", err)
	}
	cachePath := filepath.Join(folder.Path, "AuthorX", "BookA.epub")

	bookID, isNew, err := st.UpsertBook(ctx, Book{
		FolderID:     folder.ID,
		Path:         cachePath,
		SourcePath:   sql.NullString{String: filepath.Join(folder.Path, "BookA - AuthorX.txt"), Valid: true},
		Title:        "BookA",
		Author:       sql.NullString{String: "AuthorX", Valid: true},
		Format:       "epub",
		Encoding:     sql.NullString{String: "utf-8", Valid: true},
		SizeBytes:    100,
		CharCount:    sql.NullInt64{Int64: 300, Valid: true},
		ChapterCount: sql.NullInt64{Int64: 2, Valid: true},
		FileMtime:    10,
		FileHash:     sql.NullString{String: "hash-a", Valid: true},
	})
	if err != nil {
		t.Fatalf("initial UpsertBook: %v", err)
	}
	if !isNew {
		t.Fatalf("initial UpsertBook reported existing row")
	}
	if err := st.ReplaceChapters(ctx, bookID, []Chapter{
		{Idx: 1, Title: "Chapter A", Level: 1, ByteOffset: 0, CharOffset: 0},
		{Idx: 2, Title: "Chapter B", Level: 1, ByteOffset: 100, CharOffset: 100},
	}); err != nil {
		t.Fatalf("initial ReplaceChapters: %v", err)
	}

	editedTitle := "EditedTitle"
	editedAuthor := "EditedAuthor"
	description := "Short synthetic description"
	status := ReadingStatusFinished
	favorite := true
	if _, err := st.UpdateBookMetadata(ctx, bookID, BookUpdate{
		Title:         &editedTitle,
		Author:        &editedAuthor,
		Description:   &description,
		Favorite:      &favorite,
		ReadingStatus: &status,
	}); err != nil {
		t.Fatalf("UpdateBookMetadata: %v", err)
	}
	if _, err := st.SetBookTags(ctx, bookID, []string{"TagB", "TagA", "TagA"}); err != nil {
		t.Fatalf("SetBookTags: %v", err)
	}
	if err := st.PutProgress(ctx, Progress{
		UserID:        "default",
		BookID:        bookID,
		CharOffset:    42,
		ChapterIdx:    1,
		ChapterOffset: 42,
		UpdatedAt:     123,
	}); err != nil {
		t.Fatalf("PutProgress: %v", err)
	}
	if _, err := st.AddBookmark(ctx, Bookmark{
		UserID:     "default",
		BookID:     bookID,
		CharOffset: 50,
		ChapterIdx: sql.NullInt64{Int64: 1, Valid: true},
		Note:       sql.NullString{String: "Synthetic note", Valid: true},
	}); err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}

	updatedID, isNew, err := st.UpsertBook(ctx, Book{
		FolderID:     folder.ID,
		Path:         cachePath,
		SourcePath:   sql.NullString{String: filepath.Join(folder.Path, "BookA - AuthorX.txt"), Valid: true},
		Title:        "RescanTitle",
		Author:       sql.NullString{String: "RescanAuthor", Valid: true},
		Format:       "epub",
		Encoding:     sql.NullString{String: "gb18030", Valid: true},
		SizeBytes:    200,
		CharCount:    sql.NullInt64{Int64: 500, Valid: true},
		ChapterCount: sql.NullInt64{Int64: 1, Valid: true},
		FileMtime:    20,
		FileHash:     sql.NullString{String: "hash-b", Valid: true},
	})
	if err != nil {
		t.Fatalf("rescan UpsertBook: %v", err)
	}
	if isNew || updatedID != bookID {
		t.Fatalf("rescan UpsertBook = id %d new %v, want id %d existing", updatedID, isNew, bookID)
	}
	if err := st.ReplaceChapters(ctx, bookID, []Chapter{
		{Idx: 1, Title: "Chapter C", Level: 1, ByteOffset: 0, CharOffset: 0},
	}); err != nil {
		t.Fatalf("rescan ReplaceChapters: %v", err)
	}

	got, err := st.GetBook(ctx, bookID)
	if err != nil {
		t.Fatalf("GetBook: %v", err)
	}
	if got.Title != editedTitle || !got.Author.Valid || got.Author.String != editedAuthor {
		t.Fatalf("metadata after rescan = title %q author %+v, want edited values", got.Title, got.Author)
	}
	if !got.Description.Valid || got.Description.String != description {
		t.Fatalf("description after rescan = %+v, want %q", got.Description, description)
	}
	if !got.Favorite || got.ReadingStatus != ReadingStatusFinished {
		t.Fatalf("user state after rescan = favorite %v status %q", got.Favorite, got.ReadingStatus)
	}
	if got.SizeBytes != 200 || !got.Encoding.Valid || got.Encoding.String != "gb18030" {
		t.Fatalf("source fields after rescan = size %d encoding %+v", got.SizeBytes, got.Encoding)
	}
	if !got.ChapterCount.Valid || got.ChapterCount.Int64 != 1 {
		t.Fatalf("ChapterCount after rescan = %+v, want 1", got.ChapterCount)
	}

	chapters, err := st.LoadChapters(ctx, bookID)
	if err != nil {
		t.Fatalf("LoadChapters: %v", err)
	}
	if len(chapters) != 1 || chapters[0].Title != "Chapter C" {
		t.Fatalf("chapters after rescan = %+v, want replacement chapter", chapters)
	}
	tags, err := st.TagsForBook(ctx, bookID)
	if err != nil {
		t.Fatalf("TagsForBook: %v", err)
	}
	if len(tags) != 2 || tags[0].Name != "TagA" || tags[1].Name != "TagB" {
		t.Fatalf("tags after rescan = %+v, want TagA/TagB", tags)
	}
	progress, err := st.GetProgress(ctx, "default", bookID)
	if err != nil {
		t.Fatalf("GetProgress: %v", err)
	}
	if progress.CharOffset != 42 || progress.UpdatedAt != 123 {
		t.Fatalf("progress after rescan = %+v, want preserved progress", progress)
	}
	bookmarks, err := st.ListBookmarks(ctx, "default", bookID)
	if err != nil {
		t.Fatalf("ListBookmarks: %v", err)
	}
	if len(bookmarks) != 1 || bookmarks[0].CharOffset != 50 {
		t.Fatalf("bookmarks after rescan = %+v, want preserved bookmark", bookmarks)
	}
}

func TestDeleteFolderCascadesBookState(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	defer closeStore(t, st)

	folder, err := st.AddFolder(ctx, filepath.Join(t.TempDir(), "library"))
	if err != nil {
		t.Fatalf("AddFolder: %v", err)
	}
	bookID, _, err := st.UpsertBook(ctx, Book{
		FolderID:     folder.ID,
		Path:         filepath.Join(folder.Path, "AuthorX", "BookA.epub"),
		Title:        "BookA",
		Author:       sql.NullString{String: "AuthorX", Valid: true},
		Format:       "epub",
		SizeBytes:    100,
		CharCount:    sql.NullInt64{Int64: 100, Valid: true},
		ChapterCount: sql.NullInt64{Int64: 1, Valid: true},
		FileMtime:    10,
		FileHash:     sql.NullString{String: "hash-a", Valid: true},
	})
	if err != nil {
		t.Fatalf("UpsertBook: %v", err)
	}
	if err := st.ReplaceChapters(ctx, bookID, []Chapter{{Idx: 1, Title: "Chapter A"}}); err != nil {
		t.Fatalf("ReplaceChapters: %v", err)
	}
	if _, err := st.SetBookTags(ctx, bookID, []string{"TagA"}); err != nil {
		t.Fatalf("SetBookTags: %v", err)
	}
	if err := st.PutProgress(ctx, Progress{UserID: "default", BookID: bookID, UpdatedAt: 123}); err != nil {
		t.Fatalf("PutProgress: %v", err)
	}
	bookmark, err := st.AddBookmark(ctx, Bookmark{UserID: "default", BookID: bookID, CharOffset: 1})
	if err != nil {
		t.Fatalf("AddBookmark: %v", err)
	}

	if err := st.DeleteFolder(ctx, folder.ID); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if _, err := st.GetBook(ctx, bookID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetBook after DeleteFolder error = %v, want sql.ErrNoRows", err)
	}
	if got := countRows(t, st, "chapters", "book_id = ?", bookID); got != 0 {
		t.Fatalf("chapters remaining after DeleteFolder = %d", got)
	}
	if got := countRows(t, st, "book_tags", "book_id = ?", bookID); got != 0 {
		t.Fatalf("book_tags remaining after DeleteFolder = %d", got)
	}
	if _, err := st.GetProgress(ctx, "default", bookID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetProgress after DeleteFolder error = %v, want sql.ErrNoRows", err)
	}
	if got := countRows(t, st, "bookmarks", "id = ?", bookmark.ID); got != 0 {
		t.Fatalf("bookmarks remaining after DeleteFolder = %d", got)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	return openExistingTestStore(t, t.TempDir())
}

func openExistingTestStore(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return st
}

func closeStore(t *testing.T, st *Store) {
	t.Helper()
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func createLegacyDB(t *testing.T, dir string) {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", filepath.Join(dir, "library.db")))
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
        PRAGMA foreign_keys = ON;

        CREATE TABLE schema_version (
            version INTEGER PRIMARY KEY
        );

        CREATE TABLE library_folders (
            id           INTEGER PRIMARY KEY AUTOINCREMENT,
            path         TEXT    NOT NULL UNIQUE,
            added_at     INTEGER NOT NULL,
            last_scan_at INTEGER
        );

        CREATE TABLE books (
            id            INTEGER PRIMARY KEY AUTOINCREMENT,
            folder_id     INTEGER NOT NULL REFERENCES library_folders(id) ON DELETE CASCADE,
            path          TEXT    NOT NULL UNIQUE,
            title         TEXT    NOT NULL,
            author        TEXT,
            format        TEXT    NOT NULL,
            encoding      TEXT,
            size_bytes    INTEGER NOT NULL,
            char_count    INTEGER,
            chapter_count INTEGER,
            file_mtime    INTEGER NOT NULL,
            file_hash     TEXT,
            added_at      INTEGER NOT NULL,
            scanned_at    INTEGER NOT NULL
        );

        CREATE TABLE chapters (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            book_id     INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
            idx         INTEGER NOT NULL,
            title       TEXT    NOT NULL,
            byte_offset INTEGER NOT NULL,
            char_offset INTEGER NOT NULL,
            UNIQUE(book_id, idx)
        );

        CREATE TABLE user_progress (
            user_id        TEXT    NOT NULL,
            book_id        INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
            char_offset    INTEGER NOT NULL DEFAULT 0,
            chapter_idx    INTEGER NOT NULL DEFAULT 0,
            chapter_offset INTEGER NOT NULL DEFAULT 0,
            updated_at     INTEGER NOT NULL,
            PRIMARY KEY (user_id, book_id)
        );

        INSERT INTO schema_version(version) VALUES (1);
        INSERT INTO library_folders(path, added_at) VALUES ('/synthetic/library', 1);
        INSERT INTO books(folder_id, path, title, author, format, encoding,
                          size_bytes, char_count, chapter_count, file_mtime,
                          file_hash, added_at, scanned_at)
        VALUES (1, '/synthetic/library/AuthorX/BookA.epub', 'BookA', 'AuthorX',
                'epub', 'utf-8', 100, 300, 1, 10, 'hash-a', 1, 1);
    `)
	if err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
}

func tableColumns(t *testing.T, st *Store, table string) map[string]bool {
	t.Helper()
	rows, err := st.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("table_info(%s): %v", table, err)
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			notNull   int
			defaultV  sql.NullString
			primaryKV int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultV, &primaryKV); err != nil {
			t.Fatalf("scan table_info(%s): %v", table, err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info(%s) rows: %v", table, err)
	}
	return cols
}

func countRows(t *testing.T, st *Store, table, where string, args ...any) int64 {
	t.Helper()
	var n int64
	q := `SELECT COUNT(*) FROM ` + table
	if where != "" {
		q += ` WHERE ` + where
	}
	if err := st.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
