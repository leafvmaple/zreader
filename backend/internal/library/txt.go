package library

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// InlineChapterPattern locates a structured chapter marker ("第X章/节/回/卷/
// 篇/集/部/折", "Chapter N") *anywhere* within a logical line, provided it
// is preceded by line-start (with optional whitespace) or by a sentence-
// ending punctuation mark. Captures the marker plus a short subtitle as
// group 1.
//
// Subtitle capture has two arms (tried in order):
//  1. Up to 11 chars followed by a parenthesised part marker like
//     "（上）"/"（下）"/"（补）"/"（外传）" — gives clean titles for multi-
//     part chapters.
//  2. Otherwise up to 10 chars — keeps a reasonable bound on glued-body
//     chapters where there's no natural delimiter.
//
// This catches both well-formatted books (chapter on its own line) and
// books where the marker is glued to surrounding body text (common in
// 折-numbered web-novel typesetting). The preceding-context requirement
// keeps body mentions like "翻到第三章" — preceded by an ordinary character
// — from false-positive matching.
var InlineChapterPattern = regexp.MustCompile(
	`(?:^[\s\p{Zs}]*|[」』）)。！？\.\!\?])\s*` +
		`(` +
		`第\s*[\d零一二三四五六七八九十百千万0-9]+\s*[章节回卷篇集部折]` +
		`(?:[^\r\n。「」]{0,11}（[^）\r\n]{1,6}）|[^\r\n。「」]{0,10})` +
		`|Chapter\s+\d+[^\r\n]{0,30}` +
		`|CHAPTER\s+\d+[^\r\n]{0,30}` +
		`)`,
)

// NamedChapterPattern matches well-known Chinese named chapters ("楔子",
// "序章", "尾声", etc.) when they occupy their own line. Kept line-anchored
// because the words appear frequently in body text — only trust them when
// they stand alone.
var NamedChapterPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(楔子|序章|序言|序篇|引子|前言|尾声|后记|番外|终章|结语)` +
		`[^\r\n]{0,40}$`,
)

// StrictChapterPattern is the line-anchored union of structured + named
// chapter headers. Retained for callers that pass a regex explicitly to
// ParseChapters; ParseChapters(text, nil) prefers the tiered InlineChapter
// + NamedChapter combo so it can also pick up markers glued to body text.
var StrictChapterPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(?:` +
		`第\s*[\d零一二三四五六七八九十百千万0-9]+\s*[章节回卷篇集部折]` +
		`|Chapter\s+\d+` +
		`|CHAPTER\s+\d+` +
		`|楔子|序章|序言|序篇|引子|前言|尾声|后记|番外|终章|结语` +
		`)` +
		`[^\r\n]{0,40}$`,
)

// LooseDigitPattern matches a line that is *only* a small integer (e.g. "1",
// "12"). Some web-novel TXTs use bare numbers as chapter dividers; we accept
// these only as a fallback because they false-positive on stray numeric lines.
var LooseDigitPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*[0-9]{1,4}[\s\p{Zs}]*$`,
)

// AuthorByPattern matches an inline "by: NAME" / "By：NAME" tag commonly
// embedded near the top of web-novel TXTs (e.g. "铸蝉记 by:轩辕悬"). Both
// ASCII and full-width colons are accepted. Case-insensitive on "by".
//
// Group 1 captures everything before "by" (the implicit in-file title, often
// empty when the line is just "by: NAME"). Group 2 captures the author name.
var AuthorByPattern = regexp.MustCompile(`(?i)^(.*?)\bby\s*[:：]\s*([^\r\n]+?)\s*$`)

// authorScanLines bounds how many lines from the top of the file we inspect
// for an author tag — keeps us in the title block and avoids picking up a
// stray "...by:..." that lives inside the body.
const authorScanLines = 30

// DefaultChapterPattern is retained for callers that pass it explicitly.
// Prefer ParseChapters(text, nil) to get the tiered fallback behavior.
var DefaultChapterPattern = StrictChapterPattern

// Tier-down thresholds. If strict yields at least this many hits we trust it
// outright; otherwise we additionally try the loose pattern and merge.
const (
	minStrictForNoFallback = 3
	minLooseForFallback    = 2
)

// Chapter is a parsed chapter header along with its position in both the
// decoded UTF-8 string (CharOffset) and the original bytes (ByteOffset).
// ByteOffset is computed against the decoded string for now since we hand
// off UTF-8 content to the reader; we'll plug in original-byte mapping when
// streaming raw file slices.
type Chapter struct {
	Idx        int
	Title      string
	CharOffset int
	ByteOffset int
}

// ParseChapters scans `text` (already-decoded UTF-8) for chapter headers.
//
// When `re` is non-nil the caller's regex is the only matcher used (line-
// anchored — the whole trimmed line becomes the title).
//
// When `re` is nil we apply a tiered strategy:
//  1. InlineChapterPattern (sub-line) ∪ NamedChapterPattern (line-anchored)
//     — handles structured markers anywhere in a line plus standalone named
//     chapters. If the union yields enough hits, return it.
//  2. Otherwise also run LooseDigitPattern and merge — handles books that
//     use bare numeric dividers like "1", "2".
//  3. If nothing matches at any tier, emit a synthetic "正文" so the
//     reader always has at least one TOC entry.
func ParseChapters(text string, re *regexp.Regexp) []Chapter {
	if re != nil {
		out := scanChaptersAnchored(text, re)
		if len(out) == 0 {
			return syntheticChapter()
		}
		return out
	}

	inline := scanChaptersInline(text, InlineChapterPattern)
	named := scanChaptersAnchored(text, NamedChapterPattern)
	primary := mergeChapters(inline, named)
	if len(primary) >= minStrictForNoFallback {
		return primary
	}

	loose := scanChaptersAnchored(text, LooseDigitPattern)
	if len(loose) >= minLooseForFallback {
		return mergeChapters(primary, loose)
	}

	if len(primary) > 0 {
		return primary
	}
	return syntheticChapter()
}

// scanChaptersAnchored walks text line-by-line and emits a Chapter for each
// line whose trimmed content matches re. The trimmed line becomes Title and
// ByteOffset points at the start of the line. Idx is 1-based within the
// returned slice.
func scanChaptersAnchored(text string, re *regexp.Regexp) []Chapter {
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
		if trimmed != "" && re.MatchString(trimmed) {
			out = append(out, Chapter{
				Idx:        len(out) + 1,
				Title:      trimmed,
				CharOffset: charOffset,
				ByteOffset: byteOffset,
			})
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

// scanChaptersInline walks `text` as logical paragraphs (see
// buildLogicalLines) and emits a Chapter for *every* match of `re` within
// each paragraph. The first capture group becomes Title; ByteOffset and
// CharOffset point at the start of that capture in the *original* text —
// even when the matched span crosses a wrap-break that has been elided.
//
// Iterating logical paragraphs (instead of raw physical lines) is what lets
// us recover the full chapter title in files that have been hard-wrapped at
// a fixed character count, e.g. "第十一折　过\n眼亲恩，霜雪蒙尘（中）".
func scanChaptersInline(text string, re *regexp.Regexp) []Chapter {
	var out []Chapter
	for _, ll := range buildLogicalLines(text) {
		for _, m := range re.FindAllStringSubmatchIndex(ll.text, -1) {
			if len(m) < 4 || m[2] < 0 || m[3] < 0 {
				continue
			}
			titleStart, titleEnd := m[2], m[3]
			title := strings.TrimSpace(ll.text[titleStart:titleEnd])
			if title == "" {
				continue
			}
			origByte, origChar := ll.translate(titleStart)
			out = append(out, Chapter{
				Idx:        len(out) + 1,
				Title:      title,
				CharOffset: origChar,
				ByteOffset: origByte,
			})
		}
	}
	return out
}

// logicalLine is a paragraph: one or more physical lines joined together
// after dropping wrap-only line breaks. Wraps are detected by indent — a
// new paragraph always starts with whitespace; any non-empty line WITHOUT
// leading whitespace is treated as a continuation of the previous one.
//
// `breaks` records the positions in `text` where physical-line breaks were
// elided, so a match position inside `text` can be translated back to the
// corresponding offset in the original file.
type logicalLine struct {
	text       string
	byteOffset int // start of text in original file
	charOffset int // start of text in original file (runes)
	breaks     []lineBreak
}

type lineBreak struct {
	joinedBytePos int // position in logicalLine.text at which the break occurred
	origByteSkip  int // bytes of original text consumed by the elided break (1 for \n, 2 for \r\n)
	origRuneSkip  int // runes consumed (same as bytes here — \r and \n are 1 rune each)
}

// translate maps a byte position within logicalLine.text to the original
// file's byte and char offsets.
func (l *logicalLine) translate(joinedBytePos int) (origByte, origChar int) {
	byteSkip, runeSkip := 0, 0
	for _, b := range l.breaks {
		if b.joinedBytePos <= joinedBytePos {
			byteSkip += b.origByteSkip
			runeSkip += b.origRuneSkip
		} else {
			break
		}
	}
	origByte = l.byteOffset + joinedBytePos + byteSkip
	origChar = l.charOffset + utf8.RuneCountInString(l.text[:joinedBytePos]) + runeSkip
	return
}

// buildLogicalLines splits text into paragraphs. When the file uses the
// indented-paragraph convention (the majority of leading non-empty lines
// begin with whitespace — the common case for Chinese e-book TXTs), any
// non-empty *unindented* line is treated as a wrap-only continuation of
// the preceding paragraph. When the file does NOT use indented paragraphs,
// each non-empty physical line stands alone — avoids accidentally gluing
// unrelated lines together in plainly-formatted text.
func buildLogicalLines(text string) []logicalLine {
	joinContinuations := usesIndentedParagraphs(text)

	var out []logicalLine
	var current *logicalLine

	flush := func() {
		if current != nil {
			out = append(out, *current)
			current = nil
		}
	}

	byteOffset, charOffset := 0, 0
	prevBreakBytes, prevBreakRunes := 0, 0
	for {
		if byteOffset > len(text) {
			break
		}
		nl := strings.IndexByte(text[byteOffset:], '\n')
		lineEnd := len(text)
		if nl >= 0 {
			lineEnd = byteOffset + nl
		}
		contentEnd := lineEnd
		hasCR := contentEnd > byteOffset && text[contentEnd-1] == '\r'
		if hasCR {
			contentEnd--
		}
		content := text[byteOffset:contentEnd]
		contentRunes := utf8.RuneCountInString(content)

		switch {
		case content == "":
			flush()
		case joinContinuations && current != nil && !startsWithIndent(content):
			joinedPos := len(current.text)
			current.breaks = append(current.breaks, lineBreak{
				joinedBytePos: joinedPos,
				origByteSkip:  prevBreakBytes,
				origRuneSkip:  prevBreakRunes,
			})
			current.text += content
		default:
			flush()
			current = &logicalLine{
				text:       content,
				byteOffset: byteOffset,
				charOffset: charOffset,
			}
		}

		charOffset += contentRunes
		if nl < 0 {
			break
		}
		breakBytes, breakRunes := 1, 1
		if hasCR {
			breakBytes++
			breakRunes++
		}
		charOffset += breakRunes
		prevBreakBytes = breakBytes
		prevBreakRunes = breakRunes
		byteOffset = lineEnd + 1
	}
	flush()
	return out
}

// usesIndentedParagraphs returns true if most paragraph-starting lines
// (i.e. non-empty lines immediately following an empty line, plus the
// first non-empty line in the file) begin with whitespace. We sample up to
// 100 such starts. Counting all non-empty lines would skew toward "no
// indent" on wrap-heavy files where continuation lines outnumber starts.
func usesIndentedParagraphs(text string) bool {
	const sampleLimit = 100
	starts, indented := 0, 0
	prevEmpty := true
	rest := text
	for starts < sampleLimit {
		nl := strings.IndexByte(rest, '\n')
		var line string
		if nl < 0 {
			line = rest
		} else {
			line = rest[:nl]
		}
		content := strings.TrimRight(line, "\r")
		if content == "" {
			prevEmpty = true
		} else {
			if prevEmpty {
				starts++
				if startsWithIndent(content) {
					indented++
				}
			}
			prevEmpty = false
		}
		if nl < 0 {
			break
		}
		rest = rest[nl+1:]
	}
	return starts > 0 && indented*2 > starts
}

func startsWithIndent(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(r)
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

// TxtMetadata is what DetectMetadata harvests from a file's leading lines.
// Fields are "" when nothing was detected.
type TxtMetadata struct {
	Title  string
	Author string
}

// DetectMetadata scans the first authorScanLines lines of text for an inline
// "by:" tag (see AuthorByPattern) and returns the implicit title (prefix
// before "by:") together with the captured author name.
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
