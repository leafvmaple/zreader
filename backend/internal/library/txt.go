package library

// Chapter & volume detection.
//
// Pipeline (see also FormatText in format.go, ScanFolder in scanner.go):
//
//	source TXT
//	  → DetectAndDecode             — UTF-8 normalisation
//	  → DetectMetadata              — by:/作者：scan (this file)
//	  → FormatText                  — paragraph normalisation, glue split, etc.
//	  → ParseChapters               — chapter list extraction (this file)
//
// ParseChapters runs a tiered scan, one regex per "marker shape":
//
//	┌─ tier 0 — volumes ─┐  ┌────────── tier 1 — chapters ──────────┐  ┌─ fallback ─┐
//	│ VolumePattern      │  │ ChapterPattern   (第X章/折/...)        │  │ LooseDigit │
//	│  level=0, header   │  │ NamedChapterPattern  (楔子/序章/...)   │  │ Pattern    │
//	└────────────────────┘  │ BracketedNumeralPattern (「一」/【3】) │  │ (bare 1,12)│
//	                        │ EnumeratedNumeralPattern (一、subtitle)│  │ merged only│
//	                        │  all level=1, leaf chapters            │  │ if primary │
//	                        └────────────────────────────────────────┘  │ tier sparse│
//	                                                                     └────────────┘
//
// All shapes are line-anchored against the FORMATTED text — FormatText is
// the canonicaliser, so the parser never needs sub-line / preceding-
// punctuation heuristics. If a TXT defeats detection, fix FormatText to
// emit canonical headers, don't grow the parse patterns.
//
// Shared regex primitives (numeral class, unit class, bracketed-numeral
// shape, symmetric subtitle alternation) live in patterns.go. Use them
// for every new pattern — avoids the drift we hit between format-time
// and parse-time copies.
//
// Adding a new marker shape:
//
//  1. Add the regex in this file (line-anchored, capture group 1 = the
//     title text scanLineAnchored should emit).
//  2. Pick its level (0 = grouping, 1 = leaf) and add it to the
//     mergeChapters call in ParseChapters.
//  3. If it can appear glued mid-paragraph or glued to first body
//     sentence, add a matching arm to chapterSplitPattern / titleBody
//     SplitPattern in format.go so the formatter normalises it.
//  4. If the shape benefits from spacing fixes (e.g. marker glued
//     straight to a Han subtitle), extend chapterTitleSpacingPattern.

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
// The `卷` unit is intentionally NOT in the structured arm — volume
// headers are matched by VolumePattern (level=0) so the frontend can
// nest chapters underneath. Books that mix `第X卷` and `第Y章` would
// otherwise have both end up flat at the same level.
//
// Group 1 captures the chapter title: marker + a short subtitle. The
// subtitle arm tries `[…]{0,11}（X）` first (clean part-marker form like
// `（上）/（下）/（补）/（外传）`), otherwise caps at 10 chars (bound for
// chapter titles glued to the first body paragraph).
var ChapterPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(` +
		`第\s*` + chapterNumeral + `+\s*` + structuredUnit +
		`(?:[^\r\n。「」]{0,11}（[^）\r\n]{1,6}）|[^\r\n。「」]{0,10})` +
		`|Chapter\s+\d+[^\r\n]{0,30}` +
		`|CHAPTER\s+\d+[^\r\n]{0,30}` +
		`)`,
)

// VolumePattern matches a 卷 header that occupies its own (formatted)
// line. Two forms are accepted: `第X卷[subtitle]` and `卷X[subtitle]`.
// The whole line must match — volume headers don't glue to body the
// way 章/折 sometimes do, and a stricter shape avoids false-positives
// on body sentences that mention "...第二卷的内容..." in prose.
//
// Group 1 captures the full title (marker + subtitle, capped at 30
// chars to stay generous for longer volume names while still bounded).
var VolumePattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(` +
		`第\s*` + chapterNumeral + `+\s*卷[^\r\n]{0,30}` +
		`|卷\s*` + chapterNumeral + `+[^\r\n]{0,30}` +
		`)` +
		`[\s\p{Zs}]*$`,
)

// NamedChapterPattern matches well-known Chinese named chapters ("楔子",
// "序章", "尾声", etc.) when they occupy their own line. The words appear
// frequently in body text, so even after format we trust them only when
// the trimmed line begins with one — the `$` slop bound is small.
//
// `[\s\p{Zs}]*` between the two Han chars of each name accommodates
// sources that visually centre headers with a full-width space inside
// the word (`楔　子`, `序　章`). Go regex `\s` is ASCII-only —
// `\p{Zs}` covers full-width (U+3000) and other Unicode space-class
// chars. The captured title preserves whatever spacing the source
// used.
var NamedChapterPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(楔[\s\p{Zs}]*子` +
		`|序[\s\p{Zs}]*章` +
		`|序[\s\p{Zs}]*言` +
		`|序[\s\p{Zs}]*篇` +
		`|引[\s\p{Zs}]*子` +
		`|前[\s\p{Zs}]*言` +
		`|尾[\s\p{Zs}]*声` +
		`|后[\s\p{Zs}]*记` +
		`|番[\s\p{Zs}]*外` +
		`|终[\s\p{Zs}]*章` +
		`|结[\s\p{Zs}]*语` +
		`)` +
		`[^\r\n]{0,40}$`,
)

// LooseDigitPattern matches a line that is *only* a small integer ("1",
// "12"). Some web-novel TXTs use bare numbers as chapter dividers; we
// accept these only as a fallback because stray numeric lines exist in
// body text too.
var LooseDigitPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*[0-9]{1,4}[\s\p{Zs}]*$`,
)

// BracketedNumeralPattern matches a line that is *only* a bracketed
// numeral — e.g. "「一」", "【3】", "〈12〉", "[二十]". Common in older
// martial-arts / wuxia TXTs. Brackets restricted to a set unlikely to
// start a dialog line with a single-numeral payload (《》 omitted —
// used for book titles; （） / () omitted — too common inline).
var BracketedNumeralPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` + bracketedNumeralBody + `[\s\p{Zs}]*$`,
)

// EnumeratedNumeralPattern matches a line that is *only* `<numeral>、
// <subtitle>` — the Chinese enumeration form ("一、甲乙丙", "二、丁戊己",
// "1、起源"). Used by short-story collections and personal essays as
// chapter dividers. Whole-line anchored to avoid catching enumeration
// runs that appear inline in body prose ("理由有三：一、…二、…").
// Numeral and subtitle caps are tight (CJK ≤ 4 chars, Arabic ≤ 3
// digits, subtitle ≤ 30 chars) so the shape stays chapter-title-like
// — long enough to cover personal-essay subtitles that occasionally
// run that wide, short enough to reject body paragraphs that happen
// to open with a `<numeral>、` enumeration.
var EnumeratedNumeralPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(` + chapterNumeral + `{1,4}\s*、\s*[^\r\n]{1,30})` +
		`[\s\p{Zs}]*$`,
)

// AuthorByPattern matches an inline "by: NAME" / "By：NAME" tag commonly
// embedded near the top of web-novel TXTs (e.g. `<title> by:<author>`).
// Both ASCII and full-width colons are accepted. Case-insensitive on
// "by".
//
// Group 1 captures the in-file title prefix (often empty when the line
// is just "by: NAME"); group 2 captures the author name.
var AuthorByPattern = regexp.MustCompile(`(?i)^(.*?)\bby\s*[:：]\s*([^\r\n]+?)\s*$`)

// AuthorLabelPattern matches a Chinese `作者：name` author-label line.
// Two shapes are recognised by the same regex:
//
//   - Standalone: `作者：AAA` — group 1 empty, group 2 = author.
//   - Inline with title prefix: `<title>作者：<author>` — common in
//     scraped TXTs that glue title and author onto one centred line.
//     Group 1 captures the title (used by ResolveMetadata when
//     present); group 2 = author.
//
// Defensive bounds:
//   - Title prefix capped at 30 chars so a long body sentence ending
//     with `…作者：…` can't false-positive as a title.
//   - Author capped at 6 chars (real Chinese pen names are 2-5,
//     occasionally 6) and forbids punctuation / whitespace. Combined
//     with the `\s*$` end-anchor this rejects body lines like
//     `作者：AAA加上更多文字` — greedy capture exceeds 6 chars and
//     the remaining tail can't satisfy `\s*$`.
//
// Anchored at line start + end. ASCII and full-width colon both
// accepted.
var AuthorLabelPattern = regexp.MustCompile(
	`^([^\r\n]{0,30}?)作者\s*[:：]\s*([^\r\n，。！？,.!?\s]{1,6})\s*$`,
)

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
//
// Level distinguishes structural depth in the TOC:
//   - 0 = 卷 / volume header (parent grouping)
//   - 1 = 章 / 折 / 楔子 / numeric divider / etc. (leaf chapter)
//
// The list is kept flat and ordered by ByteOffset; the frontend walks
// it once and renders level=1 entries nested under the most recent
// level=0 entry (synthesising an implicit first volume if needed).
type Chapter struct {
	Idx        int
	Title      string
	Level      int
	CharOffset int
	ByteOffset int
}

// ParseChapters scans `text` (decoded UTF-8, normalised by FormatText
// via the scanner's format step) for chapter headers.
//
// When `re` is non-nil the caller's regex is the only matcher used and
// every match is emitted at level=1 (the caller is treating the regex
// as a chapter-level matcher; volume detection would need its own pass
// and isn't supported through this hook).
//
// When `re` is nil we apply a tiered strategy:
//  1. VolumePattern — `第X卷` / `卷X` headers (level=0).
//  2. ChapterPattern ∪ NamedChapterPattern ∪ BracketedNumeralPattern ∪
//     EnumeratedNumeralPattern — structured + named + bracketed CJK
//     numerals + `<numeral>、<subtitle>` enumeration (level=1).
//  3. If the chapter-level tier is sparse, also merge in LooseDigitPattern
//     (level=1) — for books that use bare numeric dividers like "1", "2".
//  4. Synthetic "正文" (level=1) if nothing matches.
func ParseChapters(text string, re *regexp.Regexp) []Chapter {
	if re != nil {
		out := scanLineAnchored(text, re, 1)
		if len(out) == 0 {
			return syntheticChapter()
		}
		return out
	}

	volumes := scanLineAnchored(text, VolumePattern, 0)
	chapters := mergeChapters(
		scanLineAnchored(text, ChapterPattern, 1),
		scanLineAnchored(text, NamedChapterPattern, 1),
		scanLineAnchored(text, BracketedNumeralPattern, 1),
		scanLineAnchored(text, EnumeratedNumeralPattern, 1),
	)
	primary := mergeChapters(volumes, chapters)

	if len(chapters) >= minPrimaryForNoFallback {
		return primary
	}

	loose := scanLineAnchored(text, LooseDigitPattern, 1)
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
// trimmed line itself does. `level` is stamped on every emitted entry —
// callers pass 0 for volume-tier patterns and 1 for chapter-tier ones.
func scanLineAnchored(text string, re *regexp.Regexp, level int) []Chapter {
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
					Level:      level,
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

// mergeChapters fuses any number of per-pattern chapter lists into a
// single ordered list. Steps:
//
//  1. Concatenate all inputs.
//  2. Stable-sort by ByteOffset (stability matters for offset ties —
//     earlier lists in the variadic order win, mirroring "primary
//     tier matched first" semantics).
//  3. Drop any later entry that shares an offset with the previous
//     entry (two patterns matched the same line — keep one).
//  4. Renumber Idx from 1.
func mergeChapters(lists ...[]Chapter) []Chapter {
	total := 0
	for _, l := range lists {
		total += len(l)
	}
	all := make([]Chapter, 0, total)
	for _, l := range lists {
		all = append(all, l...)
	}
	sort.SliceStable(all, func(i, j int) bool {
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
	return []Chapter{{Idx: 1, Title: "正文", Level: 1, CharOffset: 0, ByteOffset: 0}}
}

// TxtMetadata is what DetectMetadata harvests from a file's leading
// lines. Fields are "" when nothing was detected.
type TxtMetadata struct {
	Title  string
	Author string
}

// DetectMetadata scans the first authorScanLines lines of text for an
// embedded author hint. Two shapes are recognised:
//
//   - `<title> by: <author>` (AuthorByPattern) — captures BOTH title
//     and author. Title is whatever precedes `by:` on that line.
//   - `作者：<author>` on a standalone line (AuthorLabelPattern) —
//     captures author only; title falls back to the filename via
//     ResolveMetadata.
//
// First match wins (we don't keep scanning once author is set). The
// `by:` arm runs first because it's a stricter shape (the bare-colon
// 作者 form is more likely to occur incidentally in body text — though
// we still anchor it at line start to guard against that).
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
			if m := AuthorLabelPattern.FindStringSubmatch(trimmed); m != nil {
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
