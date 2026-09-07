package library

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// The byte-scanner fixture in pdf_cover_test.go is not a parseable PDF —
// it has no xref, no page tree — and this path goes through a real reader,
// so it needs a real file. Built rather than checked in, for the same
// reason as every other fixture here.
type pdfImage struct {
	width, height int
	// dict holds the image XObject's entries beyond /Width and /Height, so
	// a case can declare an unsupported colour space or filter.
	dict string
	// data is the raw stream payload. deflate wraps it in zlib first.
	data    []byte
	deflate bool
}

func deflated(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatalf("deflate: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("deflate close: %v", err)
	}
	return buf.Bytes()
}

// rgbSamples paints a gradient so a decode that mixes up components or
// strides produces visibly different bytes rather than passing by luck.
func rgbSamples(w, h int) []byte {
	out := make([]byte, 0, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out = append(out, uint8(x%256), uint8(y%256), 0x40)
		}
	}
	return out
}

func graySamples(w, h int) []byte {
	out := make([]byte, 0, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out = append(out, uint8((x+y)%256))
		}
	}
	return out
}

// realPDF assembles a structurally valid single-page PDF carrying one
// image XObject, including the xref table the reader needs to find it.
func realPDF(t *testing.T, img pdfImage) []byte {
	t.Helper()
	payload := img.data
	if img.deflate {
		payload = deflated(t, payload)
	}
	content := "q Q\n"

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
			"/Resources << /XObject << /Im0 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d %s /Length %d >>\nstream\n",
			img.width, img.height, img.dict, len(payload)),
	}

	var body bytes.Buffer
	body.WriteString("%PDF-1.7\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = body.Len()
		fmt.Fprintf(&body, "%d 0 obj\n%s", i+1, obj)
		if i == len(objects)-1 {
			body.Write(payload)
			body.WriteString("\nendstream")
		}
		body.WriteString("\nendobj\n")
	}

	start := body.Len()
	fmt.Fprintf(&body, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&body, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&body, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, start)
	return body.Bytes()
}

func writeRealPDF(t *testing.T, img pdfImage) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(path, realPDF(t, img), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	return path
}

func TestExtractPDFCover_DecodesFlateRGB(t *testing.T) {
	const w, h = 300, 400
	path := writeRealPDF(t, pdfImage{
		width: w, height: h,
		dict:    "/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode",
		data:    rgbSamples(w, h),
		deflate: true,
	})
	cover, err := ExtractPDFCover(path)
	if err != nil {
		t.Fatalf("ExtractPDFCover: %v", err)
	}
	if cover == nil {
		t.Fatal("no cover from a Flate-coded DeviceRGB page image")
	}
	if cover.MediaType != "image/jpeg" {
		t.Errorf("MediaType = %q, want image/jpeg", cover.MediaType)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(cover.Data))
	if err != nil {
		t.Fatalf("cover is not decodable: %v", err)
	}
	if cfg.Width != w || cfg.Height != h {
		t.Errorf("cover is %dx%d, want %dx%d", cfg.Width, cfg.Height, w, h)
	}

	// Dimensions alone would pass with the components in the wrong order
	// or the rows read at the wrong stride. Sample a pixel whose source
	// value is known and check the colour survived; the tolerance is JPEG's.
	decoded, err := jpeg.Decode(bytes.NewReader(cover.Data))
	if err != nil {
		t.Fatalf("decode cover: %v", err)
	}
	r, g, b, _ := decoded.At(10, 200).RGBA()
	got := [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
	want := [3]int{10, 200, 0x40}
	for i := range got {
		if diff := got[i] - want[i]; diff > 18 || diff < -18 {
			t.Errorf("pixel (10,200) = %v, want ~%v — components look reordered or misaligned", got, want)
			break
		}
	}
}

func TestExtractPDFCover_DecodesFlateGray(t *testing.T) {
	const w, h = 320, 420
	path := writeRealPDF(t, pdfImage{
		width: w, height: h,
		dict:    "/ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode",
		data:    graySamples(w, h),
		deflate: true,
	})
	cover, err := ExtractPDFCover(path)
	if err != nil {
		t.Fatalf("ExtractPDFCover: %v", err)
	}
	if cover == nil {
		t.Fatal("no cover from a Flate-coded DeviceGray page image")
	}
}

func TestExtractPDFCover_DecodesIndexedPalette(t *testing.T) {
	const w, h = 300, 400
	// A 4-entry palette; every pixel indexes into it.
	palette := []byte{0xFF, 0x00, 0x00, 0x00, 0xFF, 0x00, 0x00, 0x00, 0xFF, 0x20, 0x20, 0x20}
	samples := make([]byte, w*h)
	for i := range samples {
		samples[i] = byte(i % 4)
	}
	path := writeRealPDF(t, pdfImage{
		width: w, height: h,
		dict: "/ColorSpace [/Indexed /DeviceRGB 3 <" +
			fmt.Sprintf("%x", palette) + ">] /BitsPerComponent 8 /Filter /FlateDecode",
		data:    samples,
		deflate: true,
	})
	cover, err := ExtractPDFCover(path)
	if err != nil {
		t.Fatalf("ExtractPDFCover: %v", err)
	}
	if cover == nil {
		t.Fatal("no cover from an Indexed page image")
	}
}

// Colour spaces we cannot render faithfully must produce no cover rather
// than a wrong-coloured one.
func TestExtractPDFCover_DeclinesUnsupportedColourSpaces(t *testing.T) {
	const w, h = 300, 400
	cases := []struct {
		name string
		dict string
		data []byte
	}{
		{
			name: "cmyk",
			dict: "/ColorSpace /DeviceCMYK /BitsPerComponent 8 /Filter /FlateDecode",
			data: make([]byte, w*h*4),
		},
		{
			name: "four-bit",
			dict: "/ColorSpace /DeviceRGB /BitsPerComponent 4 /Filter /FlateDecode",
			data: make([]byte, w*h*3/2),
		},
		{
			name: "decode-array",
			dict: "/ColorSpace /DeviceGray /BitsPerComponent 8 /Decode [1 0] /Filter /FlateDecode",
			data: graySamples(w, h),
		},
		{
			name: "image-mask",
			dict: "/ColorSpace /DeviceGray /BitsPerComponent 8 /ImageMask true /Filter /FlateDecode",
			data: graySamples(w, h),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeRealPDF(t, pdfImage{
				width: w, height: h, dict: tc.dict, data: tc.data, deflate: true,
			})
			cover, err := ExtractPDFCover(path)
			if err != nil {
				t.Fatalf("ExtractPDFCover: %v", err)
			}
			if cover != nil {
				t.Errorf("built a cover from %s, which cannot be rendered faithfully", tc.name)
			}
		})
	}
}

// rsc.io/pdf panics on a filter it does not implement, so the filter is
// checked before the stream is opened. This is the case that would reach
// the panic if that check were dropped.
func TestExtractPDFCover_DeclinesUnimplementedFilter(t *testing.T) {
	const w, h = 300, 400
	path := writeRealPDF(t, pdfImage{
		width: w, height: h,
		dict: "/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /JPXDecode",
		data: rgbSamples(w, h),
	})
	cover, err := ExtractPDFCover(path)
	if err != nil {
		t.Fatalf("an unimplemented filter should not be an error: %v", err)
	}
	if cover != nil {
		t.Error("built a cover from a JPXDecode stream")
	}
}

// A truncated sample array would render as correct pixels above noise.
func TestExtractPDFCover_DeclinesTruncatedSamples(t *testing.T) {
	const w, h = 300, 400
	full := rgbSamples(w, h)
	path := writeRealPDF(t, pdfImage{
		width: w, height: h,
		dict:    "/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode",
		data:    full[:len(full)/2],
		deflate: true,
	})
	cover, err := ExtractPDFCover(path)
	if err != nil {
		t.Fatalf("ExtractPDFCover: %v", err)
	}
	if cover != nil {
		t.Error("built a cover from half an image")
	}
}

func TestExtractPDFCover_SkipsSmallFlateImages(t *testing.T) {
	const w, h = 40, 40
	path := writeRealPDF(t, pdfImage{
		width: w, height: h,
		dict:    "/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode",
		data:    rgbSamples(w, h),
		deflate: true,
	})
	cover, err := ExtractPDFCover(path)
	if err != nil {
		t.Fatalf("ExtractPDFCover: %v", err)
	}
	if cover != nil {
		t.Error("a 40x40 ornament was accepted as a cover")
	}
}
