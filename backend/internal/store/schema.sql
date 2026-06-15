-- zreader SQLite schema. Embedded into the binary via go:embed.
--
-- Layout choices:
--   * One row in `library_folders` per scanned directory.
--   * One row in `books` per discovered file. `path` is the absolute path on
--     disk; UNIQUE so re-scans don't duplicate. We keep size+mtime+hash so we
--     can detect renames vs. content changes.
--   * `chapters` is regenerated on every (re)scan; never edited by the user.
--   * `user_progress` is keyed (user_id, book_id) so the table layout is
--     forward-compatible with multi-user mode. In single-user mode user_id is
--     always the literal string "default".
--   * `bookmarks` is a future-facing table (not used by MVP handlers yet)
--     but cheap to create up-front so we don't have to write a migration.
--   * v0.7 library-management fields on `books` are user-owned metadata.
--     Re-scans update source/chapter data but keep these values stable.

PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS library_folders (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    path         TEXT    NOT NULL UNIQUE,
    added_at     INTEGER NOT NULL,
    last_scan_at INTEGER
);

CREATE TABLE IF NOT EXISTS books (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    folder_id     INTEGER NOT NULL REFERENCES library_folders(id) ON DELETE CASCADE,
    path          TEXT    NOT NULL UNIQUE,
    source_path   TEXT,
    title         TEXT    NOT NULL,
    author        TEXT,
    description   TEXT,
    category      TEXT,
    favorite      INTEGER NOT NULL DEFAULT 0,
    reading_status TEXT  NOT NULL DEFAULT 'unread',
    cover_color   TEXT,
    cover_label   TEXT,
    format        TEXT    NOT NULL,         -- cached format, currently 'epub'
    encoding      TEXT,                     -- detected source encoding
    size_bytes    INTEGER NOT NULL,
    char_count    INTEGER,                  -- count after decode to UTF-8
    chapter_count INTEGER,
    file_mtime    INTEGER NOT NULL,
    file_hash     TEXT,                     -- hex sha256 of first 64KB
    added_at      INTEGER NOT NULL,
    scanned_at    INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_books_folder ON books(folder_id);
CREATE INDEX IF NOT EXISTS idx_books_title  ON books(title);
CREATE INDEX IF NOT EXISTS idx_books_hash   ON books(file_hash);

CREATE TABLE IF NOT EXISTS tags (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    name     TEXT    NOT NULL UNIQUE,
    color    TEXT,
    added_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS book_tags (
    book_id INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_book_tags_tag ON book_tags(tag_id);

CREATE TABLE IF NOT EXISTS chapters (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    book_id     INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    idx         INTEGER NOT NULL,
    title       TEXT    NOT NULL,
    level       INTEGER NOT NULL DEFAULT 1, -- 0 = 卷 / volume, 1 = 章 / 折 / etc.
    byte_offset INTEGER NOT NULL,           -- offset in original source bytes
    char_offset INTEGER NOT NULL,           -- offset in decoded UTF-8 runes
    UNIQUE(book_id, idx)
);

CREATE INDEX IF NOT EXISTS idx_chapters_book ON chapters(book_id);

CREATE TABLE IF NOT EXISTS user_progress (
    user_id        TEXT    NOT NULL,
    book_id        INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    char_offset    INTEGER NOT NULL DEFAULT 0,
    chapter_idx    INTEGER NOT NULL DEFAULT 0,
    chapter_offset INTEGER NOT NULL DEFAULT 0,
    updated_at     INTEGER NOT NULL,
    PRIMARY KEY (user_id, book_id)
);

CREATE TABLE IF NOT EXISTS bookmarks (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     TEXT    NOT NULL,
    book_id     INTEGER NOT NULL REFERENCES books(id) ON DELETE CASCADE,
    char_offset INTEGER NOT NULL,
    chapter_idx INTEGER,
    note        TEXT,
    created_at  INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_bookmarks_user_book ON bookmarks(user_id, book_id);

CREATE TABLE IF NOT EXISTS library_jobs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    type        TEXT    NOT NULL,
    status      TEXT    NOT NULL,
    label       TEXT,
    payload     TEXT,
    folder_id   INTEGER,
    book_id     INTEGER,
    total       INTEGER NOT NULL DEFAULT 0,
    completed   INTEGER NOT NULL DEFAULT 0,
    added       INTEGER NOT NULL DEFAULT 0,
    updated     INTEGER NOT NULL DEFAULT 0,
    removed     INTEGER NOT NULL DEFAULT 0,
    failed      TEXT,
    error       TEXT,
    created_at  INTEGER NOT NULL,
    started_at  INTEGER,
    finished_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_library_jobs_created ON library_jobs(created_at DESC);
