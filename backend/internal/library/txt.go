package library

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// DefaultChapterPattern matches the most common Chinese chapter headers:
//   "第一章", "第 1 章", "第十二回", "第三卷", "Chapter 5"
// plus a tolerant suffix-after-title (up to ~30 chars).
//
// Anchored to a trimmed line so headers in the middle of a paragraph don't
// false-positive. Users can override via project settings later.
var DefaultChapterPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(?:` +
		`第\s*[\d零一二三四五六七八九十百千万0-9]+\s*[章节回卷篇集部]` +
		`|Chapter\s+\d+` +
		`|CHAPTER\s+\d+` +
		`)` +
		`[^\r\n]{0,40}$`,
)

// Chapter is a parsed chapter header along with its position in both the
// decoded UTF-8 string (char_offset) and the original bytes (byte_offset).
// byte_offset is computed against the decoded string for now since we hand
// off UTF-8 content to the reader; we'll plug in original-byte mapping when
// streaming raw file slices.
type Chapter struct {
	Idx        int
	Title      string
	CharOffset int
	ByteOffset int // in the decoded UTF-8 representation (same as CharOffset for now)
}

// ParseChapters scans `text` (already-decoded UTF-8) line by line and emits a
// Chapter for every match against `re`. If `re` is nil, DefaultChapterPattern
// is used.
//
// If no headers are found we emit a synthetic "正文" chapter at offset 0, so
// the reader always has at least one TOC entry — matches Moon+ / 静读 behavior.
func ParseChapters(text string, re *regexp.Regexp) []Chapter {
	if re == nil {
		re = DefaultChapterPattern
	}

	var out []Chapter

	// Walk by line, tracking the rune offset of the start of each line.
	charOffset := 0
	byteOffset := 0
	for {
		// Find next newline starting from byteOffset.
		nl := strings.IndexByte(text[byteOffset:], '\n')
		var lineEndByte int
		if nl < 0 {
			lineEndByte = len(text)
		} else {
			lineEndByte = byteOffset + nl
		}
		line := text[byteOffset:lineEndByte]

		trimmed := strings.TrimSpace(line)
		if trimmed != "" && re.MatchString(trimmed) {
			out = append(out, Chapter{
				Idx:        len(out) + 1,
				Title:      trimmed,
				CharOffset: charOffset,
				ByteOffset: byteOffset,
			})
		}

		// Advance counters past this line + its '\n'.
		runesInLine := utf8.RuneCountInString(line)
		charOffset += runesInLine
		if nl < 0 {
			break
		}
		// account for the '\n' itself
		charOffset++
		byteOffset = lineEndByte + 1
	}

	if len(out) == 0 {
		out = []Chapter{{Idx: 1, Title: "正文", CharOffset: 0, ByteOffset: 0}}
	}
	return out
}
