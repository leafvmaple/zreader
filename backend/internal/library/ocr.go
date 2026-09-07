package library

// OCR for scanned PDFs.
//
// An image-only PDF has no text layer, so it can't be searched, chunked,
// chapter-parsed or exported — it is readable only through the
// source-backed page viewer. OCR turns it into an ordinary text-layer PDF,
// after which the existing pipeline handles it with no special cases:
// ExtractPDFText → FormatText → ParseChapters → cached EPUB.
//
// The work is delegated to ocrmypdf, following the same pattern as
// MOBI/AZW import delegating to Calibre's ebook-convert. A pure-Go OCR
// engine good enough for Chinese doesn't exist, and the CGO bindings to
// Tesseract would cost the single static binary and the ~23 MB image that
// are the point of this project.
//
// Two things differ from the ebook-convert case, both because OCR takes
// minutes rather than seconds:
//
//   - It is off unless explicitly enabled. ebook-convert is auto-detected
//     on PATH; silently adding half an hour to a library scan because a
//     tool happens to be installed is not the same kind of surprise.
//   - The result is cached beside the cached EPUB, so a re-scan reuses it.
//     Without that, every scan would re-OCR the entire scanned portion of
//     the library.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrOCRDisabled means no OCR was attempted — the caller should fall back
// to the source-backed page reader.
var ErrOCRDisabled = errors.New("ocr not enabled")

// runOCR is swapped in tests.
var runOCR = defaultRunOCR

const (
	// defaultOCRLang matches the library this project is built for.
	// Tesseract needs the corresponding language data installed.
	defaultOCRLang = "chi_sim+eng"

	// defaultOCRTimeout bounds one file. A few hundred scanned pages fits
	// comfortably; the bound exists so a pathological input can't wedge a
	// scan indefinitely.
	defaultOCRTimeout = 30 * time.Minute

	// ocrCacheSuffix is appended to the cached-EPUB stem, so the OCR
	// output lands next to it under <folder>/<author>/.
	ocrCacheSuffix = ".ocr.pdf"
)

// OCREnabled reports whether OCR should be attempted.
//
// Explicit opt-in only: either name the binary with ZREADER_OCR_CMD or set
// ZREADER_OCR to a truthy value. Merely having ocrmypdf on PATH is not
// enough, deliberately.
func OCREnabled() bool {
	if strings.TrimSpace(os.Getenv("ZREADER_OCR_CMD")) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ZREADER_OCR"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ocrCachePath is where the searchable PDF for this source is kept. It
// sits beside the cached EPUB rather than in the data dir because that is
// already where derived artefacts live, and it keeps a book's outputs
// together for anyone looking at the folder.
func ocrCachePath(folder, author, title string) string {
	return strings.TrimSuffix(CachedPath(folder, author, title), ".epub") + ocrCacheSuffix
}

// ocrSearchablePDF returns the path to a text-layer PDF for sourcePath,
// running OCR only when there is no usable cached result.
//
// A cached file counts as usable when it is newer than the source. That's
// the same staleness rule the rest of the pipeline uses, and it means
// replacing the source re-runs OCR while a plain re-scan does not.
func ocrSearchablePDF(folder, sourcePath, author, title string) (string, error) {
	if !OCREnabled() {
		return "", ErrOCRDisabled
	}

	srcInfo, err := os.Stat(sourcePath)
	if err != nil {
		return "", fmt.Errorf("stat source: %w", err)
	}
	cached := ocrCachePath(folder, author, title)
	if info, err := os.Stat(cached); err == nil && info.ModTime().After(srcInfo.ModTime()) && info.Size() > 0 {
		return cached, nil
	}

	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		return "", fmt.Errorf("mkdir ocr cache: %w", err)
	}
	// Write to a temp file in the same directory and rename, so an
	// interrupted run can't leave a half-written PDF that the staleness
	// check above would then accept.
	tmp := cached + ".tmp"
	_ = os.Remove(tmp)
	if err := runOCR(sourcePath, tmp); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, cached); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("rename ocr output: %w", err)
	}
	return cached, nil
}

func defaultRunOCR(sourcePath, outPath string) error {
	cmdPath := strings.TrimSpace(os.Getenv("ZREADER_OCR_CMD"))
	if cmdPath == "" {
		var err error
		cmdPath, err = exec.LookPath("ocrmypdf")
		if err != nil {
			return fmt.Errorf("ocr requested but ocrmypdf not found: install it or set ZREADER_OCR_CMD: %w", err)
		}
	}
	lang := strings.TrimSpace(os.Getenv("ZREADER_OCR_LANG"))
	if lang == "" {
		lang = defaultOCRLang
	}

	ctx, cancel := context.WithTimeout(context.Background(), ocrTimeout())
	defer cancel()

	// --force-ocr rasterises and replaces rather than trying to merge with
	// whatever fragments of text the file claims to have. We only get here
	// when ExtractPDFText already found nothing usable, so there is nothing
	// worth preserving, and it is the one mode that reliably produces a
	// text layer. --output-type pdf skips the PDF/A conversion, which is
	// slower and buys us nothing.
	cmd := exec.CommandContext(ctx, cmdPath,
		"--force-ocr",
		"--output-type", "pdf",
		"-l", lang,
		"--quiet",
		sourcePath, outPath,
	)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("ocr timed out after %s", ocrTimeout())
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("ocrmypdf failed: %w", err)
		}
		return fmt.Errorf("ocrmypdf failed: %w: %s", err, msg)
	}
	if info, err := os.Stat(outPath); err != nil || info.Size() == 0 {
		return errors.New("ocrmypdf produced no output")
	}
	return nil
}

func ocrTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("ZREADER_OCR_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return defaultOCRTimeout
}
