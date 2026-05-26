// Package library handles filesystem scanning and TXT decoding/parsing.
package library

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/saintfish/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// DetectAndDecode reads the source bytes and returns:
//   - normalised encoding name we stored in the DB ("utf-8", "gb18030", ...)
//   - UTF-8 string content
//   - error if neither BOMs nor chardet produced a confident guess that
//     actually decodes cleanly
//
// Strategy:
//  1. BOM sniff (UTF-8, UTF-16 LE/BE) — these are unambiguous.
//  2. chardet on the first 8KB.
//  3. Fall back to GB18030 (the most common encoding for Chinese TXT
//     dumps), then to UTF-8 with replacement.
//
// We avoid trusting chardet blindly: on short or ambiguous inputs it can
// pick Windows-1252 for Chinese text. We always verify by decoding and
// counting "garbage" characters (U+FFFD).
func DetectAndDecode(src []byte) (encName string, utf8 string, err error) {
	if len(src) == 0 {
		return "utf-8", "", nil
	}

	// 1. BOM sniff.
	if name, decoded, ok := decodeWithBOM(src); ok {
		return name, decoded, nil
	}

	// Pure-ASCII shortcut: chardet often labels ASCII-only text as
	// ISO-8859-1 / Windows-1252 with low confidence. Since ASCII is a UTF-8
	// subset, just emit it as utf-8 and skip the heavier paths.
	if isPureASCII(src) {
		return "utf-8", string(src), nil
	}

	// 2. chardet on a sample.
	sample := src
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	detector := chardet.NewTextDetector()
	if result, derr := detector.DetectBest(sample); derr == nil {
		if dec := chooseEncoding(result.Charset); dec != nil {
			if decoded, ok := tryDecode(src, dec); ok {
				return normaliseName(result.Charset), decoded, nil
			}
		}
	}

	// 3. Fallback chain — try each, keep the first that yields a low garbage
	//    ratio. This covers cases where chardet missed.
	for _, c := range fallbackChain() {
		if decoded, ok := tryDecode(src, c.enc); ok {
			return c.name, decoded, nil
		}
	}

	// Last resort: lossy UTF-8 decode so the file is at least readable.
	return "utf-8?", string(bytes.ToValidUTF8(src, []byte("?"))), nil
}

// decodeWithBOM checks for known byte-order marks and decodes accordingly.
func decodeWithBOM(src []byte) (string, string, bool) {
	switch {
	case len(src) >= 3 && bytes.Equal(src[:3], []byte{0xEF, 0xBB, 0xBF}):
		return "utf-8", string(src[3:]), true
	case len(src) >= 2 && bytes.Equal(src[:2], []byte{0xFF, 0xFE}):
		dec := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder()
		if out, ok := tryDecode(src, dec); ok {
			return "utf-16le", out, true
		}
	case len(src) >= 2 && bytes.Equal(src[:2], []byte{0xFE, 0xFF}):
		dec := unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewDecoder()
		if out, ok := tryDecode(src, dec); ok {
			return "utf-16be", out, true
		}
	}
	return "", "", false
}

// chooseEncoding maps a chardet charset name to a transform.Transformer.
// Unknown charsets return nil so the caller falls through.
func chooseEncoding(name string) *encoding.Decoder {
	switch strings.ToUpper(name) {
	case "UTF-8":
		return unicode.UTF8.NewDecoder()
	case "UTF-16LE":
		return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	case "UTF-16BE":
		return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewDecoder()
	case "GB-18030", "GB18030", "GBK", "GB2312":
		return simplifiedchinese.GB18030.NewDecoder()
	case "BIG5":
		return traditionalchinese.Big5.NewDecoder()
	case "WINDOWS-1252", "ISO-8859-1":
		return charmap.Windows1252.NewDecoder()
	default:
		return nil
	}
}

type fallback struct {
	name string
	enc  *encoding.Decoder
}

func fallbackChain() []fallback {
	return []fallback{
		{"utf-8", unicode.UTF8.NewDecoder()},
		{"gb18030", simplifiedchinese.GB18030.NewDecoder()},
		{"big5", traditionalchinese.Big5.NewDecoder()},
	}
}

// tryDecode runs the transformer end-to-end and returns the result only if
// the garbage ratio (U+FFFD count over total runes) stays under 1%. Empty
// input is considered a successful decode.
func tryDecode(src []byte, dec *encoding.Decoder) (string, bool) {
	r := transform.NewReader(bytes.NewReader(src), dec)
	out, err := io.ReadAll(r)
	if err != nil {
		return "", false
	}
	if len(out) == 0 {
		return "", len(src) == 0
	}
	// Count U+FFFD (0xEF 0xBF 0xBD in UTF-8) and any control chars commonly
	// produced by a wrong decode.
	garbage := bytes.Count(out, []byte{0xEF, 0xBF, 0xBD})
	if garbage*100 > len(out) { // > 1% bytes are replacement marker
		return "", false
	}
	return string(out), true
}

// normaliseName lower-cases and trims chardet's verbose charset labels so we
// store a stable value in the DB.
func normaliseName(n string) string {
	n = strings.ToLower(strings.TrimSpace(n))
	switch n {
	case "gb-18030":
		return "gb18030"
	case "windows-1252":
		return "cp1252"
	}
	return n
}

// isPureASCII returns true if every byte is < 0x80. ASCII is a strict subset
// of UTF-8, so a pure-ASCII file is also valid UTF-8 — no need to invoke
// chardet, which often misfires on short ASCII as Windows-1252.
func isPureASCII(src []byte) bool {
	for _, b := range src {
		if b >= 0x80 {
			return false
		}
	}
	return true
}

// readFirstN is a small helper used when we want to sniff the head of a file
// without slurping it whole. Kept here so encoding.go is self-contained.
func readFirstN(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	got, err := io.ReadFull(r, buf)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("read head: %w", err)
	}
	return buf[:got], nil
}
