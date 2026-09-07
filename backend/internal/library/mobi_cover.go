package library

// MOBI cover extraction.
//
// MOBI stores images as ordinary Palm records: the MOBI header gives the
// index the image run starts at, and EXTH record 201 gives the cover's
// position within that run. Nothing declares a media type, so the bytes
// are decoded to find out what they are — which doubles as the check
// that the declared position points at an image at all.
//
// That check matters because both the header index and the EXTH offset
// are routinely absent or stale in files from hand-rolled converters. A
// declared position is used only when it decodes; otherwise the record
// run is scanned for the first record that does, which is where the
// cover sits by convention.

import (
	"bytes"
	"encoding/binary"
	"image"
	_ "image/gif"  // registers the decoders image.DecodeConfig dispatches to
	_ "image/jpeg" // — MOBI covers are almost always JPEG, but not always
	_ "image/png"
)

const (
	exthCoverOffset  = 201
	exthHasFakeCover = 203

	// mobiCoverMinDim rejects inline decorations — scene-break ornaments,
	// publisher logos — that would otherwise win the forward scan in a
	// file that declares no cover.
	mobiCoverMinDim = 150
)

// mobiImageFormats maps what image.DecodeConfig reports to the media type
// we serve. Anything outside this set is skipped rather than passed
// through: an unrecognised blob would only produce a broken <img>.
var mobiImageFormats = map[string]string{
	"jpeg": "image/jpeg",
	"png":  "image/png",
	"gif":  "image/gif",
}

// mobiCover returns the book's cover, or nil when it has none we can use.
// A missing cover is a normal outcome, not an error — the shelf falls
// back to a generated one.
func mobiCover(recs [][]byte, h mobiHeader) *Cover {
	if h.exthStart > 0 && len(recs) > 0 && h.exthStart < len(recs[0]) {
		exth := parseEXTHRaw(recs[0][h.exthStart:])

		// A "fake" cover is one the converter drew itself from the title
		// and author. Ours does the same job and matches the rest of the
		// shelf, so prefer it over a stranger's rendering of the same two
		// strings.
		if v, ok := exthUint32(exth, exthHasFakeCover); ok && v != 0 {
			return nil
		}
		if v, ok := exthUint32(exth, exthCoverOffset); ok && h.firstImage > 0 {
			if c := mobiImageAt(recs, h.firstImage+int(v)); c != nil {
				return c
			}
		}
	}

	start := h.firstImage
	if start <= 0 || start >= len(recs) {
		// No usable declaration. Text records never decode as an image, so
		// scanning from the first of them costs a magic-byte check each
		// and cannot mistake text for a cover.
		start = 1
	}
	for i := start; i < len(recs); i++ {
		if c := mobiImageAt(recs, i); c != nil {
			return c
		}
	}
	return nil
}

// mobiImageAt decodes record i far enough to learn its format and size,
// returning nil unless it is an image we can serve at cover size.
func mobiImageAt(recs [][]byte, i int) *Cover {
	if i <= 0 || i >= len(recs) {
		return nil
	}
	data := recs[i]
	if len(data) == 0 || len(data) > maxCoverBytes {
		return nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	mediaType, ok := mobiImageFormats[format]
	if !ok {
		return nil
	}
	if cfg.Width < mobiCoverMinDim || cfg.Height < mobiCoverMinDim {
		return nil
	}
	return &Cover{Data: data, MediaType: mediaType}
}

// exthUint32 reads a numeric EXTH record. The numeric types store a
// big-endian uint32; a record of any other width is malformed and is
// reported as absent rather than guessed at.
func exthUint32(exth map[int][]byte, typ int) (uint32, bool) {
	v, ok := exth[typ]
	if !ok || len(v) != 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(v), true
}
