package library

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var ErrEbookConverterMissing = errors.New("ebook converter not configured")

var runEbookConvert = defaultRunEbookConvert

// ImportConvertibleEbookToCache imports MOBI/AZW/AZW3.
//
// The native reader (mobi.go) handles these without any external tool, which
// is what makes the format work on a default install — `ebook-convert` is a
// Python + Qt application two orders of magnitude larger than the image it
// would ship in, so it was never going to be bundled.
//
// The converter is kept as a fallback for the cases the native reader
// declines: HUFF/CDIC-compressed text, and any file whose structure it can't
// make sense of. That ordering matters — trying the converter first would
// make the common case depend on a tool almost nobody has installed.
func ImportConvertibleEbookToCache(folder, sourcePath string) (CacheResult, error) {
	st, err := os.Stat(sourcePath)
	if err != nil {
		return CacheResult{}, fmt.Errorf("stat source: %w", err)
	}

	cr, nativeErr := importMobiToCache(folder, sourcePath, st)
	if nativeErr == nil {
		return cr, nil
	}

	tmpDir, err := os.MkdirTemp("", "zreader-ebook-*")
	if err != nil {
		return CacheResult{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpEpub := filepath.Join(tmpDir, "converted.epub")
	if err := runEbookConvert(sourcePath, tmpEpub); err != nil {
		// Report both failures: "converter not configured" alone hides that
		// the native reader had a specific, actionable reason for declining.
		return CacheResult{}, fmt.Errorf("native reader: %w; converter fallback: %w", nativeErr, err)
	}
	enc := strings.TrimPrefix(strings.ToLower(filepath.Ext(sourcePath)), ".")
	return importEpubFileToCache(folder, tmpEpub, sourcePath, enc, st)
}

func defaultRunEbookConvert(sourcePath, outPath string) error {
	converter := strings.TrimSpace(os.Getenv("ZREADER_EBOOK_CONVERT"))
	if converter == "" {
		var err error
		converter, err = exec.LookPath("ebook-convert")
		if err != nil {
			return fmt.Errorf("%w: install Calibre's ebook-convert or set ZREADER_EBOOK_CONVERT", ErrEbookConverterMissing)
		}
	}
	cmd := exec.Command(converter, sourcePath, outPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("ebook-convert failed: %w", err)
		}
		return fmt.Errorf("ebook-convert failed: %w: %s", err, msg)
	}
	if _, err := os.Stat(outPath); err != nil {
		return fmt.Errorf("converted epub missing: %w", err)
	}
	return nil
}

// importMobiToCache reads a MOBI/AZW/AZW3 natively and runs its text through
// the same pipeline a .txt source takes. There is no MOBI → EPUB step: the
// cache format is EPUB either way, and going via one would mean rebuilding
// structure that FormatText/ParseChapters are about to re-derive anyway.
func importMobiToCache(folder, sourcePath string, st os.FileInfo) (CacheResult, error) {
	book, err := ReadMobi(sourcePath)
	if err != nil {
		return CacheResult{}, err
	}
	text := MobiHTMLToText(book.HTML)
	if strings.TrimSpace(text) == "" {
		return CacheResult{}, errors.New("mobi has no readable text")
	}

	title, author := ResolveMetadata(filepath.Base(sourcePath), TxtMetadata{
		Title:  book.Title,
		Author: book.Author,
	})
	enc := strings.TrimPrefix(strings.ToLower(filepath.Ext(sourcePath)), ".")
	hash, err := headHashFile(sourcePath)
	if err != nil {
		return CacheResult{}, err
	}
	cr, err := writeTextSourceToCache(folder, sourcePath, nil, st, enc, text, title, author, nil, hash)
	cr.SourcePath = sourcePath
	return cr, err
}
