package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withOCR enables OCR for one test and installs a stub runner that writes
// a text-layer PDF containing the given lines. Real ocrmypdf is not
// invoked — the contract under test is the caching, fallback and metadata
// plumbing around it, not Tesseract.
func withOCR(t *testing.T, lines []string) *int {
	t.Helper()
	t.Setenv("ZREADER_OCR", "1")
	calls := 0
	prev := runOCR
	runOCR = func(sourcePath, outPath string) error {
		calls++
		writeSimplePDF(t, outPath, "", "", lines)
		return nil
	}
	t.Cleanup(func() { runOCR = prev })
	return &calls
}

func imageOnlyPDF(t *testing.T, dir, name string) string {
	t.Helper()
	src := filepath.Join(dir, name)
	writeSimplePDF(t, src, "ImageOnly", "AuthorX", nil)
	return src
}

// The headline behaviour: a scanned PDF stops being a page-viewer-only
// book and joins the ordinary text pipeline.
func TestFormatPDFToCache_OCRProducesTextBook(t *testing.T) {
	tmp := t.TempDir()
	src := imageOnlyPDF(t, tmp, "ImageOnly - AuthorX.pdf")
	withOCR(t, []string{"Chapter 1", "Alpha beta gamma delta.", "Epsilon zeta eta theta."})

	cr, err := FormatPDFToCache(tmp, src)
	if err != nil {
		t.Fatalf("FormatPDFToCache: %v", err)
	}
	if cr.CacheFormat != "epub" {
		t.Fatalf("CacheFormat = %q, want epub — OCR should route through the text pipeline", cr.CacheFormat)
	}
	if !strings.HasSuffix(cr.Path, ".epub") {
		t.Errorf("Path = %q, want a cached EPUB", cr.Path)
	}
	if _, err := os.Stat(cr.Path); err != nil {
		t.Errorf("cached EPUB was not written: %v", err)
	}
	// Metadata from the original PDF must survive --force-ocr rebuilding it.
	if cr.Title != "ImageOnly" || cr.Author != "AuthorX" {
		t.Errorf("metadata = (%q, %q), want the source PDF's own", cr.Title, cr.Author)
	}
}

// OCR takes minutes, so a re-scan must reuse the previous result. Without
// this every scan would re-OCR the scanned part of the library.
func TestFormatPDFToCache_OCRResultIsCachedAcrossScans(t *testing.T) {
	tmp := t.TempDir()
	src := imageOnlyPDF(t, tmp, "ImageOnly - AuthorX.pdf")
	calls := withOCR(t, []string{"Chapter 1", "Alpha beta gamma."})

	for i := 0; i < 3; i++ {
		if _, err := FormatPDFToCache(tmp, src); err != nil {
			t.Fatalf("scan %d: %v", i, err)
		}
	}
	if *calls != 1 {
		t.Errorf("ocr ran %d times across three scans, want 1", *calls)
	}
}

// Replacing the source has to invalidate the cached OCR — otherwise a
// corrected scan would keep serving the old text forever.
func TestFormatPDFToCache_NewerSourceReRunsOCR(t *testing.T) {
	tmp := t.TempDir()
	src := imageOnlyPDF(t, tmp, "ImageOnly - AuthorX.pdf")
	calls := withOCR(t, []string{"Chapter 1", "Alpha beta gamma."})

	if _, err := FormatPDFToCache(tmp, src); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	// Touch the source into the future so it is unambiguously newer.
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(src, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if _, err := FormatPDFToCache(tmp, src); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if *calls != 2 {
		t.Errorf("ocr ran %d times, want 2 after the source changed", *calls)
	}
}

func TestFormatPDFToCache_OCRDisabledFallsBackToPageViewer(t *testing.T) {
	tmp := t.TempDir()
	src := imageOnlyPDF(t, tmp, "ImageOnly - AuthorX.pdf")
	// ZREADER_OCR unset — the default.
	prev := runOCR
	runOCR = func(string, string) error {
		t.Fatal("ocr ran without being enabled")
		return nil
	}
	t.Cleanup(func() { runOCR = prev })

	cr, err := FormatPDFToCache(tmp, src)
	if err != nil {
		t.Fatalf("FormatPDFToCache: %v", err)
	}
	if cr.CacheFormat != "pdf-image" {
		t.Errorf("CacheFormat = %q, want pdf-image when OCR is off", cr.CacheFormat)
	}
}

// A failed OCR must not fail the scan — the book is still readable as
// pages, which is exactly what it was before OCR existed.
func TestFormatPDFToCache_OCRFailureIsNotFatal(t *testing.T) {
	tmp := t.TempDir()
	src := imageOnlyPDF(t, tmp, "ImageOnly - AuthorX.pdf")
	t.Setenv("ZREADER_OCR", "1")
	prev := runOCR
	runOCR = func(string, string) error { return errors.New("tesseract exploded") }
	t.Cleanup(func() { runOCR = prev })

	_, err := FormatPDFToCache(tmp, src)
	if err == nil {
		t.Fatal("expected the OCR failure to be reported")
	}
	if !strings.Contains(err.Error(), "tesseract exploded") {
		t.Errorf("error %q does not name the underlying cause", err)
	}
}

// OCR that runs but finds nothing readable (blank scans, a photo album)
// is a normal outcome, not an error.
func TestFormatPDFToCache_OCRWithNoTextFallsBack(t *testing.T) {
	tmp := t.TempDir()
	src := imageOnlyPDF(t, tmp, "ImageOnly - AuthorX.pdf")
	withOCR(t, nil) // stub writes a PDF with no text

	cr, err := FormatPDFToCache(tmp, src)
	if err != nil {
		t.Fatalf("FormatPDFToCache: %v", err)
	}
	if cr.CacheFormat != "pdf-image" {
		t.Errorf("CacheFormat = %q, want pdf-image when OCR yields no text", cr.CacheFormat)
	}
}

// A crash mid-run must not leave a partial PDF that the staleness check
// would then accept as a finished result.
func TestOCR_PartialOutputIsNotCached(t *testing.T) {
	tmp := t.TempDir()
	src := imageOnlyPDF(t, tmp, "ImageOnly - AuthorX.pdf")
	t.Setenv("ZREADER_OCR", "1")
	prev := runOCR
	runOCR = func(_, outPath string) error {
		if err := os.WriteFile(outPath, []byte("half a pdf"), 0o644); err != nil {
			t.Fatalf("write partial: %v", err)
		}
		return errors.New("interrupted")
	}
	t.Cleanup(func() { runOCR = prev })

	if _, err := ocrSearchablePDF(tmp, src, "AuthorX", "ImageOnly"); err == nil {
		t.Fatal("expected an error")
	}
	cached := ocrCachePath(tmp, "AuthorX", "ImageOnly")
	if _, err := os.Stat(cached); err == nil {
		t.Error("a failed run left a cached file behind")
	}
	if _, err := os.Stat(cached + ".tmp"); err == nil {
		t.Error("a failed run left its temp file behind")
	}
}

func TestOCREnabled(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{}, false},
		{map[string]string{"ZREADER_OCR": "1"}, true},
		{map[string]string{"ZREADER_OCR": "true"}, true},
		{map[string]string{"ZREADER_OCR": "on"}, true},
		{map[string]string{"ZREADER_OCR": "0"}, false},
		{map[string]string{"ZREADER_OCR": "no"}, false},
		{map[string]string{"ZREADER_OCR_CMD": "/usr/bin/ocrmypdf"}, true},
	}
	for _, c := range cases {
		t.Run("", func(t *testing.T) {
			t.Setenv("ZREADER_OCR", "")
			t.Setenv("ZREADER_OCR_CMD", "")
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if got := OCREnabled(); got != c.want {
				t.Errorf("OCREnabled() = %v with %v, want %v", got, c.env, c.want)
			}
		})
	}
}

func TestOCRTimeout(t *testing.T) {
	t.Setenv("ZREADER_OCR_TIMEOUT", "")
	if got := ocrTimeout(); got != defaultOCRTimeout {
		t.Errorf("default timeout = %s, want %s", got, defaultOCRTimeout)
	}
	t.Setenv("ZREADER_OCR_TIMEOUT", "90s")
	if got := ocrTimeout(); got != 90*time.Second {
		t.Errorf("timeout = %s, want 90s", got)
	}
	// Nonsense falls back rather than producing a zero deadline, which
	// would make every OCR run fail instantly.
	t.Setenv("ZREADER_OCR_TIMEOUT", "banana")
	if got := ocrTimeout(); got != defaultOCRTimeout {
		t.Errorf("timeout = %s for an unparsable value, want the default", got)
	}
}
