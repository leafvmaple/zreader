package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/leafvmaple/zreader/internal/store"
)

// ScanResult is the per-folder summary returned to callers (and serialised
// into the /api/v1/library/scan response).
type ScanResult struct {
	FolderID int64    `json:"folder_id"`
	Path     string   `json:"path"`
	Added    int      `json:"added"`
	Updated  int      `json:"updated"`
	Removed  int      `json:"removed"`
	Failed   []string `json:"failed,omitempty"`
}

// Scanner walks a library folder and ingests TXT files into the store.
type Scanner struct {
	Store  *store.Store
	Logger *log.Logger
}

// ScanFolder is split into four phases:
//
//	Phase 0 — Migrate: restore legacy `<file>.txt.bak` over `<file>.txt`
//	          so we always start from the pristine source.
//	Phase 1 — Format:  for each top-level `*.txt` source, run FormatText
//	          and write the result to `<folder>/<author>/<title>.txt`.
//	          Sources themselves are never modified.
//	Phase 2 — Ingest:  upsert each cached file into the store and
//	          (re-)parse its chapters.
//	Phase 3 — Prune:   drop store rows whose cached path no longer
//	          exists (e.g. the source was removed).
//
// Errors on individual files don't abort the scan; failed paths are
// collected in ScanResult.Failed.
func (s *Scanner) ScanFolder(ctx context.Context, folder store.Folder) (ScanResult, error) {
	res := ScanResult{FolderID: folder.ID, Path: folder.Path}

	info, err := os.Stat(folder.Path)
	if err != nil {
		return res, fmt.Errorf("stat folder: %w", err)
	}
	if !info.IsDir() {
		return res, fmt.Errorf("%s is not a directory", folder.Path)
	}

	s.infof("folder %d (%s): scan begin", folder.ID, folder.Path)
	defer func() {
		s.infof("folder %d: scan done — added=%d updated=%d removed=%d failed=%d",
			folder.ID, res.Added, res.Updated, res.Removed, len(res.Failed))
	}()

	// Phase 0 — migrate any legacy in-place-format backups.
	if restored, err := RestoreBackups(folder.Path); err != nil {
		s.warnf("restore backups in %s: %v", folder.Path, err)
	} else if restored > 0 {
		s.infof("phase 0: restored %d legacy .bak source(s)", restored)
	}

	// Phase 1 — format sources to cached paths.
	type cachedEntry struct {
		path   string
		title  string
		author string
	}
	var cached []cachedEntry
	walkErr := filepath.WalkDir(folder.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			s.warnf("walk error at %s: %v", path, err)
			return nil
		}
		if d.IsDir() {
			if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".txt") {
			return nil
		}
		// Sources live at the top level. Anything under a subdirectory
		// is treated as cached output (or arbitrary user organisation)
		// and is left alone here — Phase 2 will pick up whatever the
		// format step wrote.
		if filepath.Dir(path) != folder.Path {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		cp, title, author, err := FormatToCache(folder.Path, path)
		if err != nil {
			s.warnf("format %s: %v", filepath.Base(path), err)
			res.Failed = append(res.Failed, path)
			return nil
		}
		s.infof("format %s → %s (author=%q title=%q)",
			filepath.Base(path), relPath(folder.Path, cp), author, title)
		cached = append(cached, cachedEntry{path: cp, title: title, author: author})
		return nil
	})
	if walkErr != nil && walkErr != context.Canceled {
		return res, walkErr
	}

	// Phase 2 — ingest cached files.
	presentPaths := make([]string, 0, len(cached))
	for _, c := range cached {
		st, err := os.Stat(c.path)
		if err != nil {
			s.warnf("stat cached %s: %v", c.path, err)
			res.Failed = append(res.Failed, c.path)
			continue
		}
		book, isNew, err := s.ingestFile(ctx, folder.ID, c.path, c.title, c.author, st)
		if err != nil {
			s.warnf("ingest %s: %v", c.path, err)
			res.Failed = append(res.Failed, c.path)
			continue
		}
		presentPaths = append(presentPaths, book.Path)
		if isNew {
			res.Added++
		} else {
			res.Updated++
		}
	}

	// Phase 3 — drop store rows for cached files that no longer exist.
	removed, err := s.Store.DeleteBooksMissing(ctx, folder.ID, presentPaths)
	if err != nil {
		return res, fmt.Errorf("prune missing: %w", err)
	}
	res.Removed = int(removed)

	if err := s.Store.TouchFolderScan(ctx, folder.ID); err != nil {
		return res, fmt.Errorf("touch scan: %w", err)
	}
	return res, nil
}

// ingestFile decodes a cached TXT, parses chapters, and persists the
// row + chapter list. Title and author are passed in by ScanFolder
// (already resolved at format time) so we never need a second
// DetectMetadata pass here — the metadata path is the only thing we
// need to look up from the file content.
func (s *Scanner) ingestFile(ctx context.Context, folderID int64, path, title, author string, st fs.FileInfo) (store.Book, bool, error) {
	name := filepath.Base(path)

	raw, err := os.ReadFile(path)
	if err != nil {
		return store.Book{}, false, fmt.Errorf("read file: %w", err)
	}

	encName, text, err := DetectAndDecode(raw)
	if err != nil {
		return store.Book{}, false, fmt.Errorf("decode: %w", err)
	}

	chapters := ParseChapters(text, nil)
	hash := headHash(raw)

	authorLog := author
	if authorLog == "" {
		authorLog = "?"
	}
	s.infof("ingest %s: enc=%s chars=%d chapters=%d title=%q author=%s",
		name, encName, utf8.RuneCountInString(text), len(chapters), title, authorLog)

	book := store.Book{
		FolderID:     folderID,
		Path:         path,
		Title:        title,
		Author:       sql.NullString{String: author, Valid: author != "" && author != DefaultAuthor},
		Format:       "txt",
		Encoding:     sql.NullString{String: encName, Valid: encName != ""},
		SizeBytes:    st.Size(),
		CharCount:    sql.NullInt64{Int64: int64(utf8.RuneCountInString(text)), Valid: true},
		ChapterCount: sql.NullInt64{Int64: int64(len(chapters)), Valid: true},
		FileMtime:    st.ModTime().Unix(),
		FileHash:     sql.NullString{String: hash, Valid: hash != ""},
	}

	id, isNew, err := s.Store.UpsertBook(ctx, book)
	if err != nil {
		return store.Book{}, false, err
	}
	book.ID = id

	storeChapters := make([]store.Chapter, 0, len(chapters))
	for _, c := range chapters {
		storeChapters = append(storeChapters, store.Chapter{
			Idx:        int64(c.Idx),
			Title:      c.Title,
			ByteOffset: int64(c.ByteOffset),
			CharOffset: int64(c.CharOffset),
		})
	}
	if err := s.Store.ReplaceChapters(ctx, id, storeChapters); err != nil {
		return store.Book{}, false, err
	}

	return book, isNew, nil
}

// relPath is best-effort folder-relative path for logging; falls back
// to the absolute path on failure (so log lines never lose information).
func relPath(folder, target string) string {
	if r, err := filepath.Rel(folder, target); err == nil {
		return filepath.ToSlash(r)
	}
	return target
}

// headHash returns the hex sha256 of up to the first 64 KiB of src. Used
// to recognise the same file after it's been moved (since path is the
// primary identity, but we want a fallback fingerprint).
func headHash(src []byte) string {
	const headBytes = 64 * 1024
	if len(src) > headBytes {
		src = src[:headBytes]
	}
	sum := sha256.Sum256(src)
	return hex.EncodeToString(sum[:])
}

func (s *Scanner) infof(format string, args ...any) {
	if s.Logger != nil {
		s.Logger.Printf("[scan] "+format, args...)
	}
}

func (s *Scanner) warnf(format string, args ...any) {
	if s.Logger != nil {
		s.Logger.Printf("[scan] WARN "+format, args...)
	}
}
