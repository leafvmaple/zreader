package library

// Cover art for PDFs that store page images as raw samples.
//
// The JPEG scan in pdf_cover.go handles the common case: a scanned page is
// one DCTDecode stream, which is already a file a browser renders. PDFs
// that Flate-compress raw samples instead carry no such file — the bytes
// are a pixel array, and turning them into an image means knowing how to
// read them.
//
// This handles the subset that is unambiguous, and declines the rest:
//
//	8 bits per component, DeviceGray / DeviceRGB / ICCBased(N=1,3), and
//	Indexed over those. No /Decode array, no /ImageMask, no /SMask.
//
// Left out on purpose: DeviceCMYK and Separation (need ink models to look
// right), Lab, DeviceN, sub-byte depths, and 16-bit samples. Guessing at
// any of those produces a cover with wrong colours, which is worse than
// the generated one it would replace.
//
// The filter is checked before the stream is opened rather than after,
// because rsc.io/pdf's Reader panics on a filter it does not implement —
// including a Flate stream with any PNG predictor but Up. The recover is
// still there as a backstop; the check is what keeps it from being load-
// bearing.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"

	pdf "rsc.io/pdf"
)

const (
	// pdfFlatePages bounds how far in we look. A cover is on page 1; a
	// frontispiece or a scanned title page can push it to 2 or 3.
	pdfFlatePages = 3

	// pdfFlateMaxPixels caps the allocation a declared width × height can
	// ask for, so a corrupt or hostile header cannot turn a library scan
	// into an out-of-memory kill.
	pdfFlateMaxPixels = 40 << 20

	// pdfFlateJPEGQuality re-encodes the samples for the shelf. These are
	// photographs and page scans, so JPEG is both the right format and the
	// one that keeps a full-page raster under maxCoverBytes.
	pdfFlateJPEGQuality = 82
)

// extractPDFFlateCover returns a cover built from the first usable
// Flate-coded image on the opening pages, or nil when the file has none we
// can read correctly.
func extractPDFFlateCover(pdfPath string) (cov *Cover, err error) {
	defer func() {
		if v := recover(); v != nil {
			cov, err = nil, fmt.Errorf("read pdf images: %v", v)
		}
	}()

	f, err := os.Open(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("open pdf %s: %w", pdfPath, err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat pdf %s: %w", pdfPath, err)
	}
	r, err := pdf.NewReader(f, st.Size())
	if err != nil {
		return nil, fmt.Errorf("open pdf reader %s: %w", pdfPath, err)
	}

	pages := r.NumPage()
	if pages > pdfFlatePages {
		pages = pdfFlatePages
	}
	for n := 1; n <= pages; n++ {
		xobj := r.Page(n).Resources().Key("XObject")
		for _, name := range xobj.Keys() {
			v := xobj.Key(name)
			if v.Key("Subtype").Name() != "Image" {
				continue
			}
			img := decodePDFFlateImage(v)
			if img == nil {
				continue
			}
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: pdfFlateJPEGQuality}); err != nil {
				continue
			}
			if buf.Len() > maxCoverBytes {
				continue
			}
			return &Cover{Data: buf.Bytes(), MediaType: "image/jpeg"}, nil
		}
	}
	return nil, nil
}

// decodePDFFlateImage turns one image XObject into a Go image, or returns
// nil when it is not one of the shapes this understands.
func decodePDFFlateImage(v pdf.Value) image.Image {
	w, h := int(v.Key("Width").Int64()), int(v.Key("Height").Int64())
	if w < pdfCoverMinDim || h < pdfCoverMinDim || w*h > pdfFlateMaxPixels {
		return nil
	}
	if v.Key("BitsPerComponent").Int64() != 8 {
		return nil
	}
	// A /Decode array remaps sample values; an image mask is a stencil, not
	// a picture; an /SMask means the samples alone are not the final image.
	if v.Key("Decode").Kind() != pdf.Null ||
		v.Key("ImageMask").Kind() != pdf.Null ||
		v.Key("SMask").Kind() != pdf.Null {
		return nil
	}
	if !isPlainFlateStream(v) {
		return nil
	}

	space := v.Key("ColorSpace")
	if palette := pdfIndexedPalette(space); palette != nil {
		return buildPDFIndexedImage(v, w, h, palette)
	}
	comps := pdfColorComponents(space)
	if comps == 0 {
		return nil
	}
	samples := readPDFSamples(v, w*h*comps)
	if samples == nil {
		return nil
	}
	if comps == 1 {
		img := image.NewGray(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			copy(img.Pix[y*img.Stride:], samples[y*w:(y+1)*w])
		}
		return img
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i, p := 0, 0; i < w*h; i, p = i+1, p+3 {
		o := i * 4
		img.Pix[o+0] = samples[p+0]
		img.Pix[o+1] = samples[p+1]
		img.Pix[o+2] = samples[p+2]
		img.Pix[o+3] = 0xFF
	}
	return img
}

// isPlainFlateStream reports whether the stream is FlateDecode with a
// predictor rsc.io/pdf implements. Anything else would panic inside
// Reader, so it is rejected before the stream is opened.
func isPlainFlateStream(v pdf.Value) bool {
	filter := v.Key("Filter")
	switch filter.Kind() {
	case pdf.Name:
		if filter.Name() != "FlateDecode" {
			return false
		}
	case pdf.Array:
		if filter.Len() != 1 || filter.Index(0).Name() != "FlateDecode" {
			return false
		}
	default:
		return false
	}
	parms := v.Key("DecodeParms")
	if parms.Kind() == pdf.Array {
		if parms.Len() != 1 {
			return false
		}
		parms = parms.Index(0)
	}
	pred := parms.Key("Predictor")
	return pred.Kind() == pdf.Null || pred.Int64() <= 1 || pred.Int64() == 12
}

// pdfColorComponents maps a colour space to its component count, for the
// spaces whose 8-bit samples are already the values Go's images want.
func pdfColorComponents(space pdf.Value) int {
	switch space.Kind() {
	case pdf.Name:
		switch space.Name() {
		case "DeviceGray", "CalGray", "G":
			return 1
		case "DeviceRGB", "CalRGB", "RGB":
			return 3
		}
	case pdf.Array:
		if space.Len() == 2 && space.Index(0).Name() == "ICCBased" {
			switch space.Index(1).Key("N").Int64() {
			case 1:
				return 1
			case 3:
				return 3
			}
		}
	}
	return 0
}

// pdfIndexedPalette returns the RGB palette of an /Indexed colour space
// over a base this understands, or nil.
func pdfIndexedPalette(space pdf.Value) []color.RGBA {
	if space.Kind() != pdf.Array || space.Len() != 4 {
		return nil
	}
	if n := space.Index(0).Name(); n != "Indexed" && n != "I" {
		return nil
	}
	comps := pdfColorComponents(space.Index(1))
	if comps == 0 {
		return nil
	}
	count := int(space.Index(2).Int64()) + 1
	if count <= 0 || count > 256 {
		return nil
	}

	lookup := space.Index(3)
	var table []byte
	switch lookup.Kind() {
	case pdf.String:
		table = []byte(lookup.RawString())
	case pdf.Stream:
		if !isPlainFlateStream(lookup) {
			return nil
		}
		table = readPDFSamples(lookup, count*comps)
	}
	if len(table) < count*comps {
		return nil
	}

	palette := make([]color.RGBA, count)
	for i := 0; i < count; i++ {
		if comps == 1 {
			g := table[i]
			palette[i] = color.RGBA{R: g, G: g, B: g, A: 0xFF}
			continue
		}
		o := i * 3
		palette[i] = color.RGBA{R: table[o], G: table[o+1], B: table[o+2], A: 0xFF}
	}
	return palette
}

func buildPDFIndexedImage(v pdf.Value, w, h int, palette []color.RGBA) image.Image {
	samples := readPDFSamples(v, w*h)
	if samples == nil {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		idx := int(samples[i])
		if idx >= len(palette) {
			// Out-of-range indices are legal-ish and render as black in
			// most viewers; matching that beats bailing on the whole page.
			idx = 0
		}
		c := palette[idx]
		o := i * 4
		img.Pix[o+0], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = c.R, c.G, c.B, 0xFF
	}
	return img
}

// readPDFSamples reads exactly want bytes of decoded stream data, or nil if
// the stream is shorter — a truncated sample array would render as a band
// of correct pixels above a block of noise.
func readPDFSamples(v pdf.Value, want int) []byte {
	if want <= 0 || want > pdfFlateMaxPixels*4 {
		return nil
	}
	rc := v.Reader()
	defer rc.Close()
	buf := make([]byte, want)
	if _, err := io.ReadFull(rc, buf); err != nil {
		return nil
	}
	return buf
}
