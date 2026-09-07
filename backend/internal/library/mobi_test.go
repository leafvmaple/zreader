package library

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Fixture builder -------------------------------------------------------
//
// Real MOBI files can't be checked in — the corpus is private and a binary
// blob is unreviewable — so the tests assemble one. Building the container
// by hand is also the only way to exercise the offset table and trailing-byte
// handling deliberately rather than hoping a sample happens to cover them.

type mobiFixture struct {
	text        string
	compression int
	encoding    int
	fullName    string
	author      string
	extraFlags  int
	recordSize  int
}

func buildMobi(t *testing.T, f mobiFixture) []byte {
	t.Helper()
	if f.recordSize == 0 {
		f.recordSize = 4096
	}

	body := []byte(f.text)
	var textRecs [][]byte
	for off := 0; off < len(body); off += f.recordSize {
		end := off + f.recordSize
		if end > len(body) {
			end = len(body)
		}
		chunk := body[off:end]
		if f.compression == compressionPalmDoc {
			chunk = palmDocCompress(chunk)
		}
		if f.extraFlags&1 != 0 {
			// A one-byte multibyte overlap trailer: value 0 means "1 byte".
			chunk = append(chunk, 0)
		}
		textRecs = append(textRecs, chunk)
	}

	// EXTH carrying the author.
	var exth []byte
	if f.author != "" {
		a := []byte(f.author)
		entry := make([]byte, 8+len(a))
		binary.BigEndian.PutUint32(entry[0:], exthAuthor)
		binary.BigEndian.PutUint32(entry[4:], uint32(8+len(a)))
		copy(entry[8:], a)
		exth = append(exth, []byte("EXTH")...)
		hdr := make([]byte, 8)
		binary.BigEndian.PutUint32(hdr[0:], uint32(12+len(entry)))
		binary.BigEndian.PutUint32(hdr[4:], 1)
		exth = append(exth, hdr...)
		exth = append(exth, entry...)
		for len(exth)%4 != 0 {
			exth = append(exth, 0)
		}
	}

	const mobiLen = 232
	rec0 := make([]byte, 16+mobiLen)
	binary.BigEndian.PutUint16(rec0[0:], uint16(f.compression))
	binary.BigEndian.PutUint32(rec0[4:], uint32(len(body)))
	binary.BigEndian.PutUint16(rec0[8:], uint16(len(textRecs)))
	binary.BigEndian.PutUint16(rec0[10:], uint16(f.recordSize))
	binary.BigEndian.PutUint16(rec0[12:], encryptionNone)

	copy(rec0[16:], "MOBI")
	binary.BigEndian.PutUint32(rec0[20:], mobiLen)
	binary.BigEndian.PutUint32(rec0[16+12:], uint32(f.encoding))
	if exth != nil {
		binary.BigEndian.PutUint32(rec0[16+112:], 0x40)
	}
	binary.BigEndian.PutUint16(rec0[16+226:], uint16(f.extraFlags))

	rec0 = append(rec0, exth...)
	// The full name sits after the headers; record its position.
	nameOff := len(rec0)
	rec0 = append(rec0, []byte(f.fullName)...)
	binary.BigEndian.PutUint32(rec0[16+84:], uint32(nameOff))
	binary.BigEndian.PutUint32(rec0[16+88:], uint32(len(f.fullName)))

	recs := append([][]byte{rec0}, textRecs...)

	// Palm container: header, offset table, records.
	out := make([]byte, palmHeaderLen)
	copy(out[0:], "zreader-test")
	copy(out[60:], "BOOK")
	copy(out[64:], "MOBI")
	binary.BigEndian.PutUint16(out[76:], uint16(len(recs)))

	dataStart := palmHeaderLen + len(recs)*palmRecordInfo
	table := make([]byte, 0, len(recs)*palmRecordInfo)
	off := dataStart
	for _, r := range recs {
		e := make([]byte, palmRecordInfo)
		binary.BigEndian.PutUint32(e[0:], uint32(off))
		table = append(table, e...)
		off += len(r)
	}
	out = append(out, table...)
	for _, r := range recs {
		out = append(out, r...)
	}
	return out
}

// palmDocCompress emits a stream the decompressor must handle: literal runs,
// single literals, and back-references. It doesn't have to compress well —
// it has to produce every token class.
func palmDocCompress(src []byte) []byte {
	var out []byte
	for i := 0; i < len(src); {
		// Look for a repeat we can encode as a back-reference.
		best, bestDist := 0, 0
		for dist := 1; dist <= 2047 && dist <= i; dist++ {
			n := 0
			for n < 10 && i+n < len(src) && src[i+n-dist] == src[i+n] {
				n++
			}
			if n > best {
				best, bestDist = n, dist
			}
		}
		if best >= 3 {
			if best > 10 {
				best = 10
			}
			val := 0x8000 | (bestDist << 3) | (best - 3)
			out = append(out, byte(val>>8), byte(val))
			i += best
			continue
		}
		c := src[i]
		if c == 0 || (c >= 9 && c <= 0x7F) {
			out = append(out, c)
		} else {
			// Literal-run escape: one following byte, verbatim.
			out = append(out, 1, c)
		}
		i++
	}
	return out
}

func writeMobi(t *testing.T, dir, name string, f mobiFixture) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buildMobi(t, f), 0o644); err != nil {
		t.Fatalf("write mobi: %v", err)
	}
	return path
}

// --- Tests -----------------------------------------------------------------

func TestReadMobi_PalmDocRoundTrip(t *testing.T) {
	// Repetition is what exercises the back-reference token; without it the
	// compressor emits only literals and the LZ77 path goes untested.
	text := strings.Repeat("<p>甲乙丙丁，戊己庚辛。子丑寅卯，辰巳午未。</p>", 200)
	path := writeMobi(t, t.TempDir(), "book.mobi", mobiFixture{
		text:        text,
		compression: compressionPalmDoc,
		encoding:    65001,
		fullName:    "示例书",
		author:      "佚名",
	})

	got, err := ReadMobi(path)
	if err != nil {
		t.Fatalf("ReadMobi: %v", err)
	}
	if got.HTML != text {
		t.Errorf("text did not round-trip: got %d bytes, want %d", len(got.HTML), len(text))
	}
	if got.Title != "示例书" {
		t.Errorf("Title = %q, want 示例书", got.Title)
	}
	if got.Author != "佚名" {
		t.Errorf("Author = %q, want 佚名", got.Author)
	}
}

func TestReadMobi_Uncompressed(t *testing.T) {
	text := "<p>甲乙丙丁。</p><p>戊己庚辛。</p>"
	path := writeMobi(t, t.TempDir(), "book.mobi", mobiFixture{
		text: text, compression: compressionNone, encoding: 65001, fullName: "示例书",
	})
	got, err := ReadMobi(path)
	if err != nil {
		t.Fatalf("ReadMobi: %v", err)
	}
	if got.HTML != text {
		t.Errorf("HTML = %q, want %q", got.HTML, text)
	}
}

// Text spanning several records is the normal case for a real book; a
// single-record fixture would never catch an offset-table mistake.
func TestReadMobi_MultipleRecords(t *testing.T) {
	text := strings.Repeat("甲乙丙丁戊己庚辛壬癸。", 900) // well over one 4096-byte record
	path := writeMobi(t, t.TempDir(), "book.mobi", mobiFixture{
		text: text, compression: compressionPalmDoc, encoding: 65001, fullName: "示例书",
	})
	got, err := ReadMobi(path)
	if err != nil {
		t.Fatalf("ReadMobi: %v", err)
	}
	if got.HTML != text {
		t.Errorf("multi-record text mismatch: got %d bytes, want %d", len(got.HTML), len(text))
	}
}

// Trailing bytes are appended to every record and are NOT text. Leaving them
// in doesn't fail loudly — it sprinkles junk through the prose — so it needs
// its own case.
func TestReadMobi_StripsTrailingBytes(t *testing.T) {
	text := strings.Repeat("甲乙丙丁，戊己庚辛。", 500)
	path := writeMobi(t, t.TempDir(), "book.mobi", mobiFixture{
		text: text, compression: compressionPalmDoc, encoding: 65001,
		fullName: "示例书", extraFlags: 1,
	})
	got, err := ReadMobi(path)
	if err != nil {
		t.Fatalf("ReadMobi: %v", err)
	}
	if got.HTML != text {
		t.Errorf("trailing bytes were not stripped: got %d bytes, want %d", len(got.HTML), len(text))
	}
}

func TestReadMobi_RejectsHuffCdic(t *testing.T) {
	path := writeMobi(t, t.TempDir(), "book.mobi", mobiFixture{
		text: "x", compression: compressionHuffCdic, encoding: 65001, fullName: "示例书",
	})
	_, err := ReadMobi(path)
	if err == nil {
		t.Fatal("expected HUFF/CDIC to be reported, not silently mis-decoded")
	}
	if !strings.Contains(err.Error(), "HUFF/CDIC") {
		t.Errorf("error %q should name the compression so the fallback is explicable", err)
	}
}

// A truncated or non-MOBI file must produce an error, never a panic — the
// scanner runs this over whatever the user put in the folder.
func TestReadMobi_MalformedInput(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"empty":            {},
		"too short":        []byte("PDB"),
		"not a mobi":       []byte(strings.Repeat("x", 200)),
		"truncated record": buildMobi(t, mobiFixture{text: "abc", compression: compressionNone, encoding: 65001})[:100],
	}
	for name, body := range cases {
		path := filepath.Join(dir, "x.mobi")
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadMobi(path); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestImportConvertibleEbookToCache_NativeNeedsNoConverter(t *testing.T) {
	tmp := t.TempDir()
	text := strings.Repeat("<p>甲乙丙丁，戊己庚辛。</p>", 300)
	src := writeMobi(t, tmp, "BookA - AuthorX.mobi", mobiFixture{
		text: text, compression: compressionPalmDoc, encoding: 65001, fullName: "BookA",
	})
	// The converter must not be consulted at all for a readable file.
	prev := runEbookConvert
	runEbookConvert = func(string, string) error {
		t.Fatal("the external converter ran for a file the native reader handles")
		return nil
	}
	t.Cleanup(func() { runEbookConvert = prev })

	cr, err := ImportConvertibleEbookToCache(tmp, src)
	if err != nil {
		t.Fatalf("ImportConvertibleEbookToCache: %v", err)
	}
	if cr.CacheFormat != "epub" {
		t.Errorf("CacheFormat = %q, want epub", cr.CacheFormat)
	}
	if _, err := os.Stat(cr.Path); err != nil {
		t.Errorf("cached EPUB was not written: %v", err)
	}
	if cr.SourceEnc != "mobi" {
		t.Errorf("SourceEnc = %q, want mobi", cr.SourceEnc)
	}
}

func TestMobiHTMLToText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"paragraphs", "<p>甲乙</p><p>丙丁</p>", "甲乙\n\n丙丁\n\n"},
		{"br splits", "甲乙<br/>丙丁", "甲乙\n\n丙丁\n\n"},
		{"inline kept", "<p>甲<i>乙</i><b>丙</b></p>", "甲乙丙\n\n"},
		{"entities", "<p>&lt;甲&gt;&amp;&#20057;</p>", "<甲>&乙\n\n"},
		{"script dropped", "<script>var x=1;</script><p>甲乙</p>", "甲乙\n\n"},
		{"style dropped", "<style>p{color:red}</style><p>甲乙</p>", "甲乙\n\n"},
		{"unterminated tag", "<p>甲乙</p><p>丙丁", "甲乙\n\n丙丁\n\n"},
		{"blank paragraphs dropped", "<p></p><p>  </p><p>甲乙</p>", "甲乙\n\n"},
		{"comment ignored", "<!-- note --><p>甲乙</p>", "甲乙\n\n"},
	}
	for _, c := range cases {
		if got := MobiHTMLToText(c.in); got != c.want {
			t.Errorf("%s: MobiHTMLToText(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
