package library

// Cover art for PDFs.
//
// EPUB covers come out of the OPF manifest (cover.go); PDFs have no such
// declaration, and the obvious source — a raster of page 1 — is out of
// reach: rsc.io/pdf's Value.Reader panics on any filter it doesn't
// implement, and page images are almost always DCTDecode, so even the
// embedded JPEG can't be lifted through the exported API. A real
// rasteriser is worse: every pure-Go one is heavy and the CGO ones cost
// the single static binary.
//
// What is reachable, without a dependency and without a full PDF parse,
// is the embedded JPEG itself. A scanned PDF's first page IS one large
// JPEG, which is exactly the cover we want; a text-layer PDF often has a
// cover image on page 1 too. So: scan the head of the file for stream
// objects whose payload starts with a JPEG SOI marker, and take the first
// one big enough to be a page rather than a logo.
//
// PDFs that store images as raw Flate-compressed samples are skipped —
// those aren't a file in any format a browser reads, and re-encoding them
// would mean pulling in the colour-space handling this deliberately
// avoids. Those books fall back to a generated cover.

import (
	"bytes"
	"fmt"
	"image/jpeg"
	"io"
	"os"
)

const (
	// pdfCoverScanBytes bounds the head of the file we search. PDF objects
	// are written in page order in practice, so page 1's image is near the
	// front; reading the whole of a 500 MB scan to maybe find a cover is
	// not a trade worth making during a library scan.
	pdfCoverScanBytes = 16 << 20

	// pdfCoverMinDim rejects logos, bullets and scanner artefacts. A real
	// page scan or cover plate is far larger than this.
	pdfCoverMinDim = 150
)

var (
	jpegSOI      = []byte{0xFF, 0xD8, 0xFF}
	jpegEOI      = []byte{0xFF, 0xD9}
	pdfStreamKw  = []byte("stream")
	pdfEndstream = []byte("endstream")
)

// ExtractPDFCover returns the first embedded JPEG large enough to serve as
// a cover, or (nil, nil) when the PDF has none we can use. Only I/O
// failures produce an error — an unparsable or image-less PDF is a normal
// outcome, not a scan failure.
func ExtractPDFCover(pdfPath string) (*Cover, error) {
	f, err := os.Open(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("open pdf %s: %w", pdfPath, err)
	}
	defer f.Close()

	head, err := io.ReadAll(io.LimitReader(f, pdfCoverScanBytes))
	if err != nil {
		return nil, fmt.Errorf("read pdf %s: %w", pdfPath, err)
	}

	for _, data := range jpegStreams(head) {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			continue
		}
		if cfg.Width < pdfCoverMinDim || cfg.Height < pdfCoverMinDim {
			continue
		}
		if len(data) > maxCoverBytes {
			continue
		}
		return &Cover{Data: data, MediaType: "image/jpeg"}, nil
	}
	return nil, nil
}

// jpegStreams yields the payload of every stream object in buf whose data
// begins with a JPEG SOI.
//
// Anchoring on the `stream` keyword rather than searching for SOI directly
// is what keeps this from matching those three bytes where they occur by
// chance inside other compressed data.
func jpegStreams(buf []byte) [][]byte {
	var out [][]byte
	pos := 0
	for {
		i := bytes.Index(buf[pos:], pdfStreamKw)
		if i < 0 {
			return out
		}
		start := pos + i + len(pdfStreamKw)
		pos = start

		// The keyword is followed by CRLF or LF (PDF 1.7 §7.3.8.1), and we
		// must not confuse it with `endstream`, which contains it.
		data := buf[start:]
		switch {
		case bytes.HasPrefix(data, []byte("\r\n")):
			data = data[2:]
		case bytes.HasPrefix(data, []byte("\n")):
			data = data[1:]
		default:
			continue
		}
		if !bytes.HasPrefix(data, jpegSOI) {
			continue
		}
		end := bytes.Index(data, pdfEndstream)
		if end < 0 {
			continue
		}
		out = append(out, trimToJPEGEnd(data[:end]))
	}
}

// trimToJPEGEnd cuts the stream payload back to the JPEG's own EOI marker.
// The bytes between EOI and `endstream` are PDF line endings, and some
// writers pad further; decoders tolerate the trailing bytes but the file we
// serve should be exactly the image.
func trimToJPEGEnd(data []byte) []byte {
	if i := bytes.LastIndex(data, jpegEOI); i >= 0 {
		return data[:i+len(jpegEOI)]
	}
	return data
}

// pdfHasCover reports whether ExtractPDFCover would find something, for
// the scanner's has_cover column. It does the same work rather than a
// cheaper probe: deciding a JPEG is usable means decoding its header, and
// there is no shortcut to that.
func pdfHasCover(pdfPath string) bool {
	cover, err := ExtractPDFCover(pdfPath)
	return err == nil && cover != nil
}
