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

// ImportConvertibleEbookToCache imports MOBI/AZW/AZW3 by converting them to
// EPUB with an external converter, then feeding that EPUB through the normal
// canonical cache pipeline.
func ImportConvertibleEbookToCache(folder, sourcePath string) (CacheResult, error) {
	st, err := os.Stat(sourcePath)
	if err != nil {
		return CacheResult{}, fmt.Errorf("stat source: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "zreader-ebook-*")
	if err != nil {
		return CacheResult{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpEpub := filepath.Join(tmpDir, "converted.epub")
	if err := runEbookConvert(sourcePath, tmpEpub); err != nil {
		return CacheResult{}, err
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
