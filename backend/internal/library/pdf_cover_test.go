package library

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// jpegOfSize renders a solid JPEG at the given dimensions. Real fixtures
// can't be used here: the corpus is private, and a checked-in scan would
// be a binary blob nobody can review.
func jpegOfSize(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// fakePDF assembles just enough PDF shape for the scanner: a header and a
// sequence of stream objects. The extractor never parses the xref table,
// so this exercises exactly the bytes it looks at.
func fakePDF(streams ...[]byte) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	for i, data := range streams {
		b.WriteString("1 0 obj\n<< /Length ")
		b.WriteString(itoa(len(data)))
		b.WriteString(" >>\nstream\n")
		b.Write(data)
		b.WriteString("\nendstream\nendobj\n")
		_ = i
	}
	b.WriteString("%%EOF\n")
	return b.Bytes()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func writePDF(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	return path
}

func TestExtractPDFCover_FindsFirstLargeJPEG(t *testing.T) {
	want := jpegOfSize(t, 600, 900)
	path := writePDF(t, fakePDF(
		[]byte("BT /F1 12 Tf (hello) Tj ET"), // a content stream, not an image
		want,
		jpegOfSize(t, 400, 400),
	))

	got, err := ExtractPDFCover(path)
	if err != nil {
		t.Fatalf("ExtractPDFCover: %v", err)
	}
	if got == nil {
		t.Fatal("no cover found in a PDF containing a large JPEG")
	}
	if got.MediaType != "image/jpeg" {
		t.Errorf("MediaType = %q, want image/jpeg", got.MediaType)
	}
	if !bytes.Equal(got.Data, want) {
		t.Errorf("extracted %d bytes, want the %d-byte first image", len(got.Data), len(want))
	}
	if _, err := jpeg.DecodeConfig(bytes.NewReader(got.Data)); err != nil {
		t.Errorf("extracted bytes are not a decodable JPEG: %v", err)
	}
}

// A logo or scanner artefact must not become the cover.
func TestExtractPDFCover_SkipsSmallImages(t *testing.T) {
	big := jpegOfSize(t, 500, 700)
	path := writePDF(t, fakePDF(jpegOfSize(t, 40, 40), jpegOfSize(t, 100, 20), big))

	got, err := ExtractPDFCover(path)
	if err != nil {
		t.Fatalf("ExtractPDFCover: %v", err)
	}
	if got == nil || !bytes.Equal(got.Data, big) {
		t.Error("the small leading images were not skipped")
	}
}

func TestExtractPDFCover_NoImages(t *testing.T) {
	path := writePDF(t, fakePDF([]byte("BT /F1 12 Tf (only text here) Tj ET")))

	got, err := ExtractPDFCover(path)
	if err != nil {
		t.Fatalf("ExtractPDFCover on an image-less PDF returned an error: %v", err)
	}
	if got != nil {
		t.Errorf("found a cover in an image-less PDF: %d bytes", len(got.Data))
	}
	if pdfHasCover(path) {
		t.Error("pdfHasCover = true for an image-less PDF")
	}
}

// A file that isn't a PDF at all, or is truncated mid-stream, is a normal
// thing to meet during a scan and must not fail it.
func TestExtractPDFCover_MalformedInputIsNotAnError(t *testing.T) {
	cases := map[string][]byte{
		"not a pdf":        []byte("this is a plain text file"),
		"empty":            {},
		"truncated stream": append([]byte("%PDF-1.7\nstream\n"), jpegOfSize(t, 300, 300)[:50]...),
	}
	for name, body := range cases {
		path := writePDF(t, body)
		got, err := ExtractPDFCover(path)
		if err != nil {
			t.Errorf("%s: unexpected error %v", name, err)
		}
		if got != nil {
			t.Errorf("%s: unexpectedly produced a cover", name)
		}
	}
}

func TestExtractPDFCover_HandlesCRLFAfterStreamKeyword(t *testing.T) {
	want := jpegOfSize(t, 400, 600)
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n1 0 obj\n<< /Length 1 >>\nstream\r\n")
	b.Write(want)
	b.WriteString("\r\nendstream\nendobj\n%%EOF\n")

	got, err := ExtractPDFCover(writePDF(t, b.Bytes()))
	if err != nil {
		t.Fatalf("ExtractPDFCover: %v", err)
	}
	if got == nil || !bytes.Equal(got.Data, want) {
		t.Error("a stream introduced with CRLF was not read")
	}
}

func TestExtractPDFCover_MissingFile(t *testing.T) {
	if _, err := ExtractPDFCover(filepath.Join(t.TempDir(), "nope.pdf")); err == nil {
		t.Error("expected an error for a missing file")
	}
}
