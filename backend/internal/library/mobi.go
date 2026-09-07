package library

// Native MOBI / AZW / AZW3 reader.
//
// These used to require Calibre's `ebook-convert` on the host, which meant
// the format simply didn't work on a default install. Bundling Calibre is
// not a real option either: it is a Python + Qt application, two orders of
// magnitude larger than the ~23 MB image it would be riding in.
//
// Reading them directly turns out to be tractable because zreader does not
// need a faithful MOBI → EPUB conversion. It needs text and chapter
// boundaries, and everything downstream already knows how to get those from
// a blob of markup: decompress the book's text, strip the tags, and hand the
// result to the same FormatText → ParseChapters pipeline a .txt takes.
//
// Container: a Palm database. A fixed 78-byte header, a record-offset table,
// then the records. Record 0 holds a PalmDOC header (compression, text
// length, record count) followed by a MOBI header (encoding, title
// position, EXTH metadata). Records 1..n are the compressed text.
//
// Compression: `none` and PalmDOC's LZ77 variant are implemented here.
// HUFF/CDIC (used by some publisher-produced files) is not — it is a
// different algorithm with its own dictionary records, and it is rare in
// practice. Those files report a clear error and fall back to
// `ebook-convert` when one is configured, rather than failing silently.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

// ErrMobiUnsupportedCompression marks a file whose text uses HUFF/CDIC.
var ErrMobiUnsupportedCompression = errors.New("mobi uses HUFF/CDIC compression")

const (
	palmHeaderLen  = 78
	palmRecordInfo = 8

	compressionNone     = 1
	compressionPalmDoc  = 2
	compressionHuffCdic = 17480

	// Encryption values other than 0 mean DRM; there is nothing to do with
	// those but say so.
	encryptionNone = 0
)

// MobiBook is the decoded result: the book's text as markup, plus whatever
// metadata the file declared.
type MobiBook struct {
	HTML   string
	Title  string
	Author string
}

// ReadMobi decodes the MOBI/AZW/AZW3 file at path.
func ReadMobi(path string) (*MobiBook, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read mobi: %w", err)
	}
	recs, err := palmRecords(raw)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, errors.New("mobi has no records")
	}

	hdr, err := parseMobiHeader(recs[0])
	if err != nil {
		return nil, err
	}
	if hdr.encryption != encryptionNone {
		return nil, errors.New("mobi is DRM-encrypted")
	}
	if hdr.compression == compressionHuffCdic {
		return nil, ErrMobiUnsupportedCompression
	}

	html, err := decodeMobiText(recs, hdr)
	if err != nil {
		return nil, err
	}
	out := &MobiBook{HTML: html}
	out.Title, out.Author = mobiMetadata(recs[0], hdr)
	return out, nil
}

// --- Palm database ---------------------------------------------------------

// palmRecords slices the file into its records using the offset table. The
// last record runs to end-of-file; the table stores only start offsets.
func palmRecords(raw []byte) ([][]byte, error) {
	if len(raw) < palmHeaderLen+2 {
		return nil, errors.New("file is too short to be a Palm database")
	}
	count := int(binary.BigEndian.Uint16(raw[76:78]))
	if count == 0 {
		return nil, errors.New("Palm database declares no records")
	}
	tableEnd := palmHeaderLen + count*palmRecordInfo
	if tableEnd > len(raw) {
		return nil, fmt.Errorf("record table (%d entries) runs past end of file", count)
	}

	offsets := make([]int, 0, count)
	for i := 0; i < count; i++ {
		off := int(binary.BigEndian.Uint32(raw[palmHeaderLen+i*palmRecordInfo:]))
		if off > len(raw) {
			return nil, fmt.Errorf("record %d offset %d is past end of file", i, off)
		}
		// Offsets must ascend; a file that violates that is corrupt and
		// slicing it would panic or produce garbage.
		if len(offsets) > 0 && off < offsets[len(offsets)-1] {
			return nil, fmt.Errorf("record %d offset %d moves backwards", i, off)
		}
		offsets = append(offsets, off)
	}

	recs := make([][]byte, count)
	for i, off := range offsets {
		end := len(raw)
		if i+1 < count {
			end = offsets[i+1]
		}
		recs[i] = raw[off:end]
	}
	return recs, nil
}

// --- MOBI header -----------------------------------------------------------

type mobiHeader struct {
	compression int
	encryption  int
	textLength  int
	recordCount int
	encoding    int
	// extraFlags drives how many trailing bytes each text record carries
	// beyond its content — indexes and overlap bytes that are not text.
	extraFlags  int
	fullNameOff int
	fullNameLen int
	exthStart   int // 0 when the file has no EXTH block
}

func parseMobiHeader(rec0 []byte) (mobiHeader, error) {
	var h mobiHeader
	if len(rec0) < 16 {
		return h, errors.New("record 0 is too short for a PalmDOC header")
	}
	h.compression = int(binary.BigEndian.Uint16(rec0[0:2]))
	h.textLength = int(binary.BigEndian.Uint32(rec0[4:8]))
	h.recordCount = int(binary.BigEndian.Uint16(rec0[8:10]))
	h.encryption = int(binary.BigEndian.Uint16(rec0[12:14]))

	// A PalmDOC-only file (no MOBI header) is still readable; default to
	// cp1252, which is what the format predates UTF-8 with.
	h.encoding = 1252
	if len(rec0) < 24 || string(rec0[16:20]) != "MOBI" {
		return h, nil
	}
	mobiLen := int(binary.BigEndian.Uint32(rec0[20:24]))
	if mobiLen < 24 || 16+mobiLen > len(rec0) {
		// Header length is nonsense; keep the PalmDOC values rather than
		// reading past the record.
		return h, nil
	}
	field := func(off int) (int, bool) {
		if 16+off+4 > 16+mobiLen {
			return 0, false
		}
		return int(binary.BigEndian.Uint32(rec0[16+off:])), true
	}
	if v, ok := field(12); ok && v != 0 {
		h.encoding = v
	}
	if v, ok := field(84); ok {
		h.fullNameOff = v
	}
	if v, ok := field(88); ok {
		h.fullNameLen = v
	}
	if v, ok := field(112); ok && v&0x40 != 0 {
		h.exthStart = 16 + mobiLen
	}
	// Extra-data flags live at MOBI header offset 226 (file offset 242) and
	// only exist in headers long enough to contain them.
	if 16+228 <= len(rec0) && mobiLen >= 228 {
		h.extraFlags = int(binary.BigEndian.Uint16(rec0[16+226:]))
	}
	return h, nil
}

// --- Text ------------------------------------------------------------------

func decodeMobiText(recs [][]byte, h mobiHeader) (string, error) {
	n := h.recordCount
	if n <= 0 || n >= len(recs) {
		n = len(recs) - 1
	}

	var buf []byte
	for i := 1; i <= n; i++ {
		rec := trimMobiTrailers(recs[i], h.extraFlags)
		switch h.compression {
		case compressionNone:
			buf = append(buf, rec...)
		case compressionPalmDoc:
			buf = palmDocDecompress(buf, rec)
		default:
			return "", fmt.Errorf("unknown mobi compression %d", h.compression)
		}
		// textLength is authoritative — the final record is padded.
		if h.textLength > 0 && len(buf) >= h.textLength {
			buf = buf[:h.textLength]
			break
		}
	}

	switch h.encoding {
	case 65001:
		return string(buf), nil
	default:
		// cp1252 and friends: hand it to the shared detector, which also
		// rescues files that declare 1252 but actually hold GBK — common in
		// Chinese MOBIs built by hand.
		_, text, err := DetectAndDecode(buf)
		if err != nil {
			return "", fmt.Errorf("decode mobi text: %w", err)
		}
		return text, nil
	}
}

// trimMobiTrailers strips the non-text bytes appended to each record.
//
// extraFlags is a bitmap: every set bit above the lowest adds one
// backwards-encoded length at the end of the record, and bit 0 marks a
// multibyte-character overlap whose size lives in the last byte. Getting
// this wrong doesn't corrupt visibly — it leaves small runs of index data
// scattered through the prose — so it is worth doing even though the
// records "look fine" without it.
func trimMobiTrailers(rec []byte, extraFlags int) []byte {
	for f := extraFlags >> 1; f != 0; f >>= 1 {
		if f&1 == 0 {
			continue
		}
		size := backwardVarint(rec)
		if size <= 0 || size > len(rec) {
			return rec
		}
		rec = rec[:len(rec)-size]
	}
	if extraFlags&1 != 0 && len(rec) > 0 {
		size := int(rec[len(rec)-1]&3) + 1
		if size > len(rec) {
			return rec
		}
		rec = rec[:len(rec)-size]
	}
	return rec
}

// backwardVarint reads the trailing-entry length that MOBI encodes at the
// END of a record, seven bits per byte, terminated by a byte with the high
// bit set. The value counts the varint's own bytes.
func backwardVarint(rec []byte) int {
	result, bitpos := 0, 0
	for i := len(rec) - 1; i >= 0; i-- {
		v := rec[i]
		result |= int(v&0x7F) << bitpos
		bitpos += 7
		if v&0x80 != 0 || bitpos >= 28 {
			return result
		}
	}
	return result
}

// palmDocDecompress appends one decompressed record to out.
//
// The scheme is an LZ77 variant with four token classes distinguished by the
// leading byte: literal runs, single literals, back-references, and the
// space-plus-letter pair that makes English text compress well.
func palmDocDecompress(out, rec []byte) []byte {
	for i := 0; i < len(rec); {
		b := rec[i]
		i++
		switch {
		case b == 0:
			out = append(out, 0)
		case b <= 8:
			// Literal run of b bytes.
			end := i + int(b)
			if end > len(rec) {
				end = len(rec)
			}
			out = append(out, rec[i:end]...)
			i = end
		case b <= 0x7F:
			out = append(out, b)
		case b <= 0xBF:
			// Back-reference: 11 bits of distance, 3 bits of length.
			if i >= len(rec) {
				return out
			}
			val := int(b)<<8 | int(rec[i])
			i++
			dist := (val >> 3) & 0x07FF
			n := (val & 7) + 3
			if dist == 0 || dist > len(out) {
				return out
			}
			// Copy byte-by-byte: source and destination overlap by design,
			// which is how short runs expand.
			for j := 0; j < n; j++ {
				out = append(out, out[len(out)-dist])
			}
		default:
			out = append(out, ' ', b^0x80)
		}
	}
	return out
}

// --- Metadata --------------------------------------------------------------

// EXTH record types we care about. The block carries dozens; these are the
// two that map onto what the library stores.
const (
	exthAuthor = 100
	exthTitle  = 503
)

func mobiMetadata(rec0 []byte, h mobiHeader) (title, author string) {
	if h.exthStart > 0 {
		exth := parseEXTH(rec0[h.exthStart:], h.encoding)
		title = exth[exthTitle]
		author = exth[exthAuthor]
	}
	// The PalmDOC "full name" is the fallback title; it is what most
	// hand-built files set and EXTH 503 is often absent.
	if title == "" && h.fullNameOff > 0 && h.fullNameLen > 0 {
		end := h.fullNameOff + h.fullNameLen
		if end <= len(rec0) {
			title = decodeMobiString(rec0[h.fullNameOff:end], h.encoding)
		}
	}
	return strings.TrimSpace(title), strings.TrimSpace(author)
}

func parseEXTH(b []byte, encoding int) map[int]string {
	out := map[int]string{}
	if len(b) < 12 || string(b[0:4]) != "EXTH" {
		return out
	}
	count := int(binary.BigEndian.Uint32(b[8:12]))
	pos := 12
	for i := 0; i < count; i++ {
		if pos+8 > len(b) {
			break
		}
		typ := int(binary.BigEndian.Uint32(b[pos:]))
		size := int(binary.BigEndian.Uint32(b[pos+4:]))
		if size < 8 || pos+size > len(b) {
			break
		}
		if _, seen := out[typ]; !seen {
			out[typ] = decodeMobiString(b[pos+8:pos+size], encoding)
		}
		pos += size
	}
	return out
}

func decodeMobiString(b []byte, encoding int) string {
	if encoding == 65001 {
		return strings.TrimSpace(string(b))
	}
	if _, s, err := DetectAndDecode(b); err == nil {
		return strings.TrimSpace(s)
	}
	// Last resort: treat as Latin-1 so the bytes at least round-trip to
	// something printable rather than being dropped.
	runes := make([]rune, 0, len(b))
	for _, c := range b {
		runes = append(runes, rune(c))
	}
	return strings.TrimSpace(string(utf16.Decode(utf16.Encode(runes))))
}
