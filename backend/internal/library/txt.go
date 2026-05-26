package library

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// ChapterPattern matches a structured chapter header at the start of a
// (formatted) line. ParseChapters assumes the input has been normalised
// by the format step (library.FormatToCache during scan) first — every
// chapter marker stands at the beginning of its own paragraph — so
// there's no sub-line / preceding-punctuation gate here. If a real-
// world TXT defeats detection, fix it in FormatText so the parser
// keeps seeing canonical input; don't grow this pattern with format-
// specific fallbacks.
//
// Group 1 captures the chapter title: marker + a short subtitle. The
// subtitle arm tries `[…]{0,11}（X）` first (clean part-marker form like
// `（上）/（下）/（补）/（外传）`), otherwise caps at 10 chars (bound for
// chapter titles glued to the first body paragraph).
var ChapterPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(` +
		`第\s*[\d零一二三四五六七八九十百千万0-9]+\s*[章节回卷篇集部折]` +
		`(?:[^\r\n。「」]{0,11}（[^）\r\n]{1,6}）|[^\r\n。「」]{0,10})` +
		`|Chapter\s+\d+[^\r\n]{0,30}` +
		`|CHAPTER\s+\d+[^\r\n]{0,30}` +
		`)`,
)

// NamedChapterPattern matches well-known Chinese named chapters ("楔子",
// "序章", "尾声", etc.) when they occupy their own line. The words appear
// frequently in body text, so even after format we trust them only when
// the trimmed line begins with one — the `$` slop bound is small.
var NamedChapterPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(楔子|序章|序言|序篇|引子|前言|尾声|后记|番外|终章|结语)` +
		`[^\r\n]{0,40}$`,
)

// LooseDigitPattern matches a line that is *only* a small integer ("1",
// "12"). Some web-novel TXTs use bare numbers as chapter dividers; we
// accept these only as a fallback because stray numeric lines exist in
// body text too.
var LooseDigitPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*[0-9]{1,4}[\s\p{Zs}]*$`,
)

// AuthorByPattern matches an inline "by: NAME" / "By：NAME" tag commonly
// embedded near the top of web-novel TXTs (e.g. "铸蝉记 by:轩辕悬"). Both
// ASCII and full-width colons are accepted. Case-insensitive on "by".
//
// Group 1 captures the in-file title prefix (often empty when the line
// is just "by: NAME"); group 2 captures the author name.
var AuthorByPattern = regexp.MustCompile(`(?i)^(.*?)\bby\s*[:：]\s*([^\r\n]+?)\s*$`)

const authorScanLines = 30

// Tier-down thresholds. If the structured + named match count is at
// least this many we trust it outright; otherwise we additionally try
// LooseDigitPattern and merge.
const (
	minPrimaryForNoFallback = 3
	minLooseForFallback     = 2
)

// Chapter is a parsed chapter header along with its position in both
// the decoded UTF-8 string (CharOffset) and the original bytes
// (ByteOffset). ByteOffset is computed against the decoded string for
// now since we hand off UTF-8 content to the reader; we'll plug in
// original-byte mapping when streaming raw file slices.
type Chapter struct {
	Idx        int
	Title      string
	CharOffset int
	ByteOffset int
}

// ParseChapters scans `text` (decoded UTF-8, normalised by FormatText
// via the scanner's format step) for chapter headers.
//
// When `re` is non-nil the caller's regex is the only matcher used.
//
// When `re` is nil we apply a tiered strategy:
//  1. ChapterPattern ∪ NamedChapterPattern — structured + named.
//  2. If sparse, also merge in LooseDigitPattern — for books that use
//     bare numeric dividers like "1", "2".
//  3. Synthetic "正文" if nothing matches.
func ParseChapters(text string, re *regexp.Regexp) []Chapter {
	if re != nil {
		out := scanLineAnchored(text, re)
		if len(out) == 0 {
			return syntheticChapter()
		}
		return out
	}

	structured := scanLineAnchored(text, ChapterPattern)
	named := scanLineAnchored(text, NamedChapterPattern)
	primary := mergeChapters(structured, named)
	if len(primary) >= minPrimaryForNoFallback {
		return primary
	}

	loose := scanLineAnchored(text, LooseDigitPattern)
	if len(loose) >= minLooseForFallback {
		return mergeChapters(primary, loose)
	}

	if len(primary) > 0 {
		return primary
	}
	return syntheticChapter()
}

// scanLineAnchored walks text by physical lines and emits a Chapter for
// each line whose trimmed content matches re. If re has a capture group
// the first capture's content (trimmed) becomes Title; otherwise the
// trimmed line itself does.
func scanLineAnchored(text string, re *regexp.Regexp) []Chapter {
	var out []Chapter
	charOffset := 0
	byteOffset := 0
	for {
		nl := strings.IndexByte(text[byteOffset:], '\n')
		var lineEndByte int
		if nl < 0 {
			lineEndByte = len(text)
		} else {
			lineEndByte = byteOffset + nl
		}
		line := text[byteOffset:lineEndByte]

		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			if m := re.FindStringSubmatch(trimmed); m != nil {
				title := trimmed
				if len(m) > 1 && m[1] != "" {
					title = strings.TrimSpace(m[1])
				}
				out = append(out, Chapter{
					Idx:        len(out) + 1,
					Title:      title,
					CharOffset: charOffset,
					ByteOffset: byteOffset,
				})
			}
		}

		runesInLine := utf8.RuneCountInString(line)
		charOffset += runesInLine
		if nl < 0 {
			break
		}
		charOffset++
		byteOffset = lineEndByte + 1
	}

	return out
}

// mergeChapters combines two chapter lists, sorts by ByteOffset, dedupes
// any offset collisions, and renumbers Idx from 1.
func mergeChapters(a, b []Chapter) []Chapter {
	all := make([]Chapter, 0, len(a)+len(b))
	all = append(all, a...)
	all = append(all, b...)
	sort.Slice(all, func(i, j int) bool {
		return all[i].ByteOffset < all[j].ByteOffset
	})

	out := make([]Chapter, 0, len(all))
	prevOffset := -1
	for _, c := range all {
		if c.ByteOffset == prevOffset {
			continue
		}
		c.Idx = len(out) + 1
		out = append(out, c)
		prevOffset = c.ByteOffset
	}
	return out
}

func syntheticChapter() []Chapter {
	return []Chapter{{Idx: 1, Title: "正文", CharOffset: 0, ByteOffset: 0}}
}

// TxtMetadata is what DetectMetadata harvests from a file's leading
// lines. Fields are "" when nothing was detected.
type TxtMetadata struct {
	Title  string
	Author string
}

// DetectMetadata scans the first authorScanLines lines of text for an
// inline "by:" tag (see AuthorByPattern) and returns the implicit title
// (prefix before "by:") together with the captured author name.
func DetectMetadata(text string) TxtMetadata {
	rest := text
	for i := 0; i < authorScanLines; i++ {
		nl := strings.IndexByte(rest, '\n')
		var line string
		if nl < 0 {
			line = rest
		} else {
			line = rest[:nl]
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if m := AuthorByPattern.FindStringSubmatch(trimmed); m != nil {
				return TxtMetadata{
					Title:  strings.TrimSpace(m[1]),
					Author: strings.TrimSpace(m[2]),
				}
			}
		}
		if nl < 0 {
			break
		}
		rest = rest[nl+1:]
	}
	return TxtMetadata{}
}
