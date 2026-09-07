package library

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tinyPNG is a 1x1 PNG — enough to prove the bytes survive a
// build → extract round trip without embedding a real cover.
var tinyPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89,
	0x00, 0x00, 0x00, 0x0a, 'I', 'D', 'A', 'T',
	0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01,
	0x0d, 0x0a, 0x2d, 0xb4,
	0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D', 0xae, 0x42, 0x60, 0x82,
}

func writeEpubWithCover(t *testing.T, dir string, cover *Cover) string {
	t.Helper()
	text := "第一章　起\n\n正文段一。\n"
	chapters := []Chapter{{Idx: 1, Title: "第一章　起", Level: 0, ByteOffset: 0, CharOffset: 0}}
	epubPath := filepath.Join(dir, "book.epub")
	f, err := os.Create(epubPath)
	if err != nil {
		t.Fatalf("create epub: %v", err)
	}
	defer f.Close()
	if _, err := BuildEpub(f, "示例书", "佚名", text, chapters, cover); err != nil {
		t.Fatalf("BuildEpub: %v", err)
	}
	return epubPath
}

func TestBuildEpub_CoverRoundTrip(t *testing.T) {
	dir := t.TempDir()
	epubPath := writeEpubWithCover(t, dir, &Cover{Data: tinyPNG, MediaType: "image/png"})

	if !HasCover(epubPath) {
		t.Fatal("HasCover = false for an EPUB built with a cover")
	}
	got, err := ExtractCover(epubPath)
	if err != nil {
		t.Fatalf("ExtractCover: %v", err)
	}
	if got == nil {
		t.Fatal("ExtractCover returned nil for an EPUB built with a cover")
	}
	if got.MediaType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", got.MediaType)
	}
	if !bytes.Equal(got.Data, tinyPNG) {
		t.Errorf("cover bytes changed across the round trip (%d in, %d out)", len(tinyPNG), len(got.Data))
	}

	// The OPF must declare the cover both ways so third-party readers
	// (EPUB 2 meta pointer) and our own extractor (EPUB 3 property)
	// each find it.
	raw, err := os.ReadFile(epubPath)
	if err != nil {
		t.Fatalf("read epub: %v", err)
	}
	opf := readEpubFile(t, raw, "EPUB/content.opf")
	if !strings.Contains(opf, `properties="cover-image"`) {
		t.Error("OPF manifest is missing the EPUB 3 cover-image property")
	}
	if !strings.Contains(opf, `<meta name="cover" content="cover-image"/>`) {
		t.Error("OPF metadata is missing the EPUB 2 cover pointer")
	}
}

func TestBuildEpub_NoCover(t *testing.T) {
	dir := t.TempDir()
	epubPath := writeEpubWithCover(t, dir, nil)

	if HasCover(epubPath) {
		t.Error("HasCover = true for an EPUB built without a cover")
	}
	got, err := ExtractCover(epubPath)
	if err != nil {
		t.Fatalf("ExtractCover: %v", err)
	}
	if got != nil {
		t.Errorf("ExtractCover = %+v, want nil", got)
	}
	// A cover-less build must not leave a stray manifest entry behind.
	raw, err := os.ReadFile(epubPath)
	if err != nil {
		t.Fatalf("read epub: %v", err)
	}
	if strings.Contains(readEpubFile(t, raw, "EPUB/content.opf"), "cover-image") {
		t.Error("OPF declares a cover for a book that has none")
	}
}

// TestExtractCover_Epub2MetaPointer covers the older convention: no
// properties="cover-image" anywhere, just <meta name="cover"> naming a
// manifest id. Most pre-EPUB3 Chinese ebooks are shaped this way.
func TestExtractCover_Epub2MetaPointer(t *testing.T) {
	dir := t.TempDir()
	epubPath := filepath.Join(dir, "legacy.epub")
	f, err := os.Create(epubPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	zw := zip.NewWriter(f)
	add := func(name string, body []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	add("META-INF/container.xml", []byte(`<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`))
	add("OEBPS/content.opf", []byte(`<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" version="2.0">
  <metadata><meta name="cover" content="my-cover"/></metadata>
  <manifest><item id="my-cover" href="images/front.png" media-type="image/png"/></manifest>
  <spine/>
</package>`))
	add("OEBPS/images/front.png", tinyPNG)
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	_ = f.Close()

	got, err := ExtractCover(epubPath)
	if err != nil {
		t.Fatalf("ExtractCover: %v", err)
	}
	if got == nil || !bytes.Equal(got.Data, tinyPNG) {
		t.Fatalf("ExtractCover did not follow the EPUB 2 meta pointer (got %+v)", got)
	}
}
