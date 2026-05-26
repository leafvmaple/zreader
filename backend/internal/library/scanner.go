package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
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

// ScanFolder walks the folder, decodes each *.txt file, parses chapters, and
// upserts the result. Returns a per-folder summary; errors on individual
// files are collected in ScanResult.Failed (the scan itself does not abort).
func (s *Scanner) ScanFolder(ctx context.Context, folder store.Folder) (ScanResult, error) {
	res := ScanResult{FolderID: folder.ID, Path: folder.Path}

	info, err := os.Stat(folder.Path)
	if err != nil {
		return res, fmt.Errorf("stat folder: %w", err)
	}
	if !info.IsDir() {
		return res, fmt.Errorf("%s is not a directory", folder.Path)
	}

	var presentPaths []string

	walkErr := filepath.WalkDir(folder.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Permission errors on subdirs shouldn't kill the whole scan.
			s.warnf("walk error at %s: %v", path, err)
			return nil
		}
		if d.IsDir() {
			// Skip dotted hidden directories (e.g. .git)
			if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".txt") {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		st, err := d.Info()
		if err != nil {
			res.Failed = append(res.Failed, path)
			return nil
		}

		book, isNew, ferr := s.ingestFile(ctx, folder.ID, path, st)
		if ferr != nil {
			s.warnf("ingest %s: %v", path, ferr)
			res.Failed = append(res.Failed, path)
			return nil
		}
		presentPaths = append(presentPaths, book.Path)
		if isNew {
			res.Added++
		} else {
			res.Updated++
		}
		return nil
	})
	if walkErr != nil && walkErr != context.Canceled {
		return res, walkErr
	}

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

// ingestFile reads, decodes, parses, and persists a single .txt file.
// Returns the resulting Book row and whether it was newly inserted.
func (s *Scanner) ingestFile(ctx context.Context, folderID int64, path string, st fs.FileInfo) (store.Book, bool, error) {
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

	title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	book := store.Book{
		FolderID:     folderID,
		Path:         path,
		Title:        title,
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

// headHash returns the hex sha256 of up to the first 64 KiB of src. Used to
// recognise the same file after it's been moved (since path is the primary
// identity, but we want a fallback fingerprint).
func headHash(src []byte) string {
	const headBytes = 64 * 1024
	if len(src) > headBytes {
		src = src[:headBytes]
	}
	sum := sha256.Sum256(src)
	return hex.EncodeToString(sum[:])
}

func (s *Scanner) warnf(format string, args ...any) {
	if s.Logger != nil {
		s.Logger.Printf("[scan] "+format, args...)
	}
}

// readAll is a thin wrapper kept to avoid pulling io into scanner.go just for
// one call. Currently unused but referenced from earlier drafts; leave to
// keep diff history clean.
var _ = io.ReadAll
