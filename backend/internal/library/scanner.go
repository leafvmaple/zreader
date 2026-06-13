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

// Scanner walks a library folder and ingests supported source files into the store.
type Scanner struct {
	Store  *store.Store
	Logger *log.Logger
}

// ScanFolder is split into four phases:
//
//	Phase 0 — Migrate: restore legacy `<file>.txt.bak` over `<file>.txt`
//	          so we always start from the pristine source.
//	Phase 1 — Format:  for each top-level source, write/read a canonical
//	          cached EPUB at `<folder>/<author>/<title>.epub`.
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

	// Phase 0.5 — drop legacy `.txt` cache files left by the pre-EPUB
	// design. Safe to run unconditionally: it only touches `.txt`
	// files in subdirectories (source TXTs at the top level are
	// untouched). Becomes a no-op after the first post-upgrade scan.
	if removed, err := CleanLegacyTxtCache(folder.Path); err != nil {
		s.warnf("clean legacy txt cache in %s: %v", folder.Path, err)
	} else if removed > 0 {
		s.infof("phase 0.5: removed %d stale .txt cache file(s)", removed)
	}

	// Phase 1 — format sources to cached EPUBs.
	var cached []CacheResult
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
		if !IsSupportedSource(d.Name()) {
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

		cr, err := FormatSourceToCache(folder.Path, path)
		if err != nil {
			s.warnf("format %s: %v", filepath.Base(path), err)
			res.Failed = append(res.Failed, path)
			return nil
		}
		s.infof("format %s → %s (author=%q title=%q enc=%s)",
			filepath.Base(path), relPath(folder.Path, cr.Path), cr.Author, cr.Title, cr.SourceEnc)
		cached = append(cached, cr)
		return nil
	})
	if walkErr != nil && walkErr != context.Canceled {
		return res, walkErr
	}

	// Phase 2 — ingest cached EPUBs.
	presentPaths := make([]string, 0, len(cached))
	for _, c := range cached {
		book, isNew, err := s.ingestFile(ctx, folder.ID, c)
		if err != nil {
			s.warnf("ingest %s: %v", c.Path, err)
			res.Failed = append(res.Failed, c.Path)
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

// ingestFile reads a cached EPUB, derives chapters + char count from
// its nav + spine, and persists the book row + chapter rows. The
// source-side metadata (encoding/source marker, mtime, byte size, content hash)
// comes straight from CacheResult — Phase 1 already saw the source and
// there's no reason to re-stat it.
//
// Book row's Path is the cached EPUB path. Format is "epub" — TXT/PDF/EPUB
// are only input formats from this layer up.
func (s *Scanner) ingestFile(ctx context.Context, folderID int64, cr CacheResult) (store.Book, bool, error) {
	name := filepath.Base(cr.Path)

	epubBook, err := ReadEpub(cr.Path)
	if err != nil {
		return store.Book{}, false, fmt.Errorf("read epub: %w", err)
	}

	authorLog := cr.Author
	if authorLog == "" {
		authorLog = "?"
	}
	s.infof("ingest %s: enc=%s chars=%d chapters=%d title=%q author=%s",
		name, cr.SourceEnc, utf8.RuneCountInString(epubBook.FlatText), len(epubBook.Chapters), cr.Title, authorLog)

	book := store.Book{
		FolderID:     folderID,
		Path:         cr.Path,
		Title:        cr.Title,
		Author:       sql.NullString{String: cr.Author, Valid: cr.Author != "" && cr.Author != DefaultAuthor},
		Format:       "epub",
		Encoding:     sql.NullString{String: cr.SourceEnc, Valid: cr.SourceEnc != ""},
		SizeBytes:    cr.SourceSize,
		CharCount:    sql.NullInt64{Int64: int64(utf8.RuneCountInString(epubBook.FlatText)), Valid: true},
		ChapterCount: sql.NullInt64{Int64: int64(len(epubBook.Chapters)), Valid: true},
		FileMtime:    cr.SourceMtime,
		FileHash:     sql.NullString{String: cr.SourceHash, Valid: cr.SourceHash != ""},
	}

	id, isNew, err := s.Store.UpsertBook(ctx, book)
	if err != nil {
		return store.Book{}, false, err
	}
	book.ID = id

	storeChapters := make([]store.Chapter, 0, len(epubBook.Chapters))
	for _, c := range epubBook.Chapters {
		storeChapters = append(storeChapters, store.Chapter{
			Idx:        int64(c.Idx),
			Title:      c.Title,
			Level:      int64(c.Level),
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

func headHashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open source: %w", err)
	}
	defer f.Close()
	head, err := readFirstN(f, 64*1024)
	if err != nil {
		return "", err
	}
	return headHash(head), nil
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
