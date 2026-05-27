package library

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BackupSuffix is the historic suffix used by the deprecated in-place
// FormatBook flow. The scanner's Phase 0 migration step scans for these
// files and restores them over the (mutated) source — so a legacy
// install transitions cleanly to the new "source is untouched" model
// without any user action.
const BackupSuffix = ".bak"

// DefaultAuthor is used as the author component of the cached path when
// neither the in-file by:line nor the filename yields one. Chinese
// convention for "anonymous" is `佚名`.
const DefaultAuthor = "佚名"

// fsReservedRunes is the set of characters we replace with `_` when
// turning a title or author into a filesystem path component. Covers
// Windows-reserved chars and the Unix path separator.
const fsReservedRunes = "/\\:*?\"<>|"

// parseFilenameMeta extracts title + author from filenames like
// "<title> - <author>.txt" (the convention in the test corpus). The
// last ` - ` is the split; anything to its left is title, anything to
// its right is author. Returns "" for author when no separator is
// present (whole stem becomes the title).
func parseFilenameMeta(filename string) (title, author string) {
	stem := strings.TrimSuffix(filename, filepath.Ext(filename))
	if i := strings.LastIndex(stem, " - "); i >= 0 {
		return strings.TrimSpace(stem[:i]), strings.TrimSpace(stem[i+len(" - "):])
	}
	return strings.TrimSpace(stem), ""
}

// sanitizePathComponent maps a title or author into a filesystem-safe
// path segment: filesystem-reserved chars and control codes become `_`;
// leading/trailing whitespace is trimmed; an entirely-stripped value
// falls back to `_` so we never produce an empty segment.
func sanitizePathComponent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || strings.ContainsRune(fsReservedRunes, r) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		out = "_"
	}
	return out
}

// ResolveMetadata picks the final title + author for a source file by
// combining in-file `by:` metadata, filename convention, and defaults.
// The by:line wins over the filename; filename wins over the default
// (`佚名` for missing author, the bare stem for missing title).
func ResolveMetadata(sourceName string, fromText TxtMetadata) (title, author string) {
	fileTitle, fileAuthor := parseFilenameMeta(sourceName)
	title = fromText.Title
	if title == "" {
		title = fileTitle
	}
	author = fromText.Author
	if author == "" {
		author = fileAuthor
	}
	if author == "" {
		author = DefaultAuthor
	}
	return title, author
}

// CachedPath returns the per-book formatted-output path inside folder:
// `<folder>/<author>/<title>.txt`. Both segments are sanitised.
func CachedPath(folder, author, title string) string {
	return filepath.Join(folder, sanitizePathComponent(author), sanitizePathComponent(title)+".txt")
}

// FormatToCache reads a source TXT, formats it (FormatText), and writes
// the result to `<folder>/<author>/<title>.txt` — never mutating the
// source. Returns the cached path plus the resolved title and author
// (caller persists these in the store along with the path).
//
// The cached file is overwritten on every call; the scanner relies on
// this to re-apply format changes whenever the format logic is updated.
// Output is always UTF-8.
func FormatToCache(folder, sourcePath string) (cachedPath, title, author string, err error) {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", "", "", fmt.Errorf("read: %w", err)
	}
	_, text, err := DetectAndDecode(raw)
	if err != nil {
		return "", "", "", fmt.Errorf("decode: %w", err)
	}
	meta := DetectMetadata(text)
	title, author = ResolveMetadata(filepath.Base(sourcePath), meta)

	formatted := FormatText(text)

	cachedPath = CachedPath(folder, author, title)
	if err := os.MkdirAll(filepath.Dir(cachedPath), 0o755); err != nil {
		return "", "", "", fmt.Errorf("mkdir: %w", err)
	}
	tmpPath := cachedPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(formatted), 0o644); err != nil {
		return "", "", "", fmt.Errorf("write temp: %w", err)
	}
	if err := os.Rename(tmpPath, cachedPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", "", fmt.Errorf("rename: %w", err)
	}
	return cachedPath, title, author, nil
}

// RestoreBackups walks the folder for legacy `<file>.txt.bak` files
// (left by the in-place FormatBook flow before the source-untouched
// redesign) and restores each .bak over its .txt sibling, then deletes
// the .bak. Returns the number of files restored. The scanner runs
// this once at the start of each scan; a fresh install / clean folder
// is a fast no-op.
func RestoreBackups(folder string) (int, error) {
	restored := 0
	walkErr := filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, BackupSuffix) {
			return nil
		}
		target := strings.TrimSuffix(path, BackupSuffix)
		if err := os.Rename(path, target); err != nil {
			return fmt.Errorf("restore %s: %w", path, err)
		}
		restored++
		return nil
	})
	return restored, walkErr
}

// chapterSplitPattern matches structured chapter markers that — when found
// inside a wrapped paragraph — should be promoted to their own paragraph.
// The structured / English arms (`第X章`, `Chapter N`) are gated by
// chapterSplitPrecedes (they can appear in body text like `翻到第三章`).
// The bracketed-numeral arm (`「一」`, `【3】`, …) is split unconditionally
// — the bracket pair around a numeral is syntactically unambiguous, and
// real-world TXTs (《十景缎》) occasionally glue the marker to the
// previous paragraph without any terminal punctuation. The vocabulary
// mirrors ChapterPattern / BracketedNumeralPattern in txt.go.
var chapterSplitPattern = regexp.MustCompile(
	`第\s*[\d零一二三四五六七八九十百千万0-9]+\s*[章节回卷篇集部折]` +
		`|Chapter\s+\d+|CHAPTER\s+\d+` +
		`|[「『【〈\[]\s*[零〇一二三四五六七八九十百千万0-9]+\s*[」』】〉\]]`,
)

// chapterTitleSpacingPattern normalises the whitespace between a Chinese
// chapter marker (第X章/折/…) and the first Han character of its
// subtitle. Zero or more existing spaces (ASCII or full-width) collapse
// to exactly one full-width `　`. The replacement is a no-op when the
// marker stands alone, when it's followed by `（X）` directly (e.g.
// `第九折（上）` — no subtitle), or when the line isn't a chapter title.
var chapterTitleSpacingPattern = regexp.MustCompile(
	`^([\s\p{Zs}]*第\s*[\d零一二三四五六七八九十百千万0-9]+\s*[章节回卷篇集部折])[\s\p{Zs}]*(\p{Han})`,
)

// titleBodySplitPattern matches a structured chapter title that opens a
// paragraph and is immediately followed by body text on the same line
// (no delimiter between them). The subtitle is captured as a strictly
// symmetric `XXXX，XXXX` form — 4+4 is most common, 3+3 and 5+5 are
// also accepted; asymmetric subtitles (4+3 etc.) are intentionally
// rejected because splitting them risks stealing one char from the body.
//
// An optional parenthesised part marker like "（上）"/"（下）" extends the
// captured title when present.
//
// What this DELIBERATELY does NOT cover (= title stays glued to body
// after format, parser still captures it via ChapterPattern's 10-char
// cap, but the file isn't split):
//   - Single-phrase subtitles (no `，`)
//   - Asymmetric subtitles like 4+3 or 5+4
//   - Subtitles whose body continuation also happens to start with
//     `XXXX，` (rhythmic 4-char prose) — we may over-split here; this
//     is a known limitation of structure-without-semantics matching.
var titleBodySplitPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(?:` +
		`第\s*[\d零一二三四五六七八九十百千万0-9]+\s*[章节回卷篇集部折]` +
		`[\s\p{Zs}]*` +
		`(?:` +
		`[\p{Han}]{4}，[\p{Han}]{4}` +
		`|[\p{Han}]{5}，[\p{Han}]{5}` +
		`|[\p{Han}]{3}，[\p{Han}]{3}` +
		`)` +
		`(?:（[^）\r\n]{1,6}）)?` +
		`|[「『【〈\[]\s*[零〇一二三四五六七八九十百千万0-9]+\s*[」』】〉\]]` +
		`)`,
)

// chapterSplitPrecedes is the set of characters that, when immediately
// before a chapter marker, signal the marker actually starts a new
// chapter rather than appearing as body text. End-of-quote / end-of-
// sentence in CJK + ASCII. Only consulted for the gated arms of
// chapterSplitPattern (structured `第X章`, `Chapter N`); bracketed
// numerals always split.
var chapterSplitPrecedes = map[rune]struct{}{
	'」': {}, '』': {}, '）': {}, ')': {},
	'。': {}, '！': {}, '？': {}, '.': {}, '!': {}, '?': {},
	'…': {}, '”': {}, '’': {},
}

// FormatText returns text normalised for downstream chapter parsing:
//   - Wrap-only line breaks are removed (paragraphs reconstructed via the
//     leading-indent convention; see buildLogicalLines).
//   - Structured chapter markers glued mid-paragraph after quote/sentence
//     punctuation are split out so each chapter starts its own paragraph.
//   - Paragraphs are separated by exactly one blank line; output is
//     always LF-terminated.
//
// When the file doesn't appear to use the indented-paragraph convention
// (see usesIndentedParagraphs) text is returned unchanged — without an
// indent signal we can't safely tell a wrap break from a real paragraph
// break.
func FormatText(text string) string {
	if !usesIndentedParagraphs(text) {
		return text
	}
	paragraphs := buildLogicalLines(text)
	if len(paragraphs) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	first := true
	for _, p := range paragraphs {
		for _, piece := range splitAtChapters(p.text) {
			for _, sub := range splitTitleFromBody(piece) {
				// Strip leading & trailing indent / whitespace so every
				// emitted paragraph (chapter title, body, mid-paragraph
				// split, …) starts flush left. The reader applies its
				// own visual indent at display time, so storing literal
				// `　　` only complicates uniform handling — title lines
				// shouldn't be indented anyway.
				sub = strings.TrimSpace(sub)
				sub = chapterTitleSpacingPattern.ReplaceAllString(sub, "${1}　${2}")
				if sub == "" {
					continue
				}
				if isMetadataLine(sub) {
					continue
				}
				if !first {
					b.WriteString("\n\n")
				}
				b.WriteString(sub)
				first = false
			}
		}
	}
	b.WriteByte('\n')
	return b.String()
}

// metadataLineMaxRunes caps the length of a paragraph eligible for
// the metadata-line filter (see isMetadataLine). Real title/author
// headers are short — capping at 40 runes keeps the filter from
// stripping a body sentence that happens to contain the substring
// `by:` (rare in CJK prose but possible in bilingual / translated
// text). Pick a value comfortably above the longest plausible
// "<title> by:<author>" header.
const metadataLineMaxRunes = 40

// isMetadataLine reports whether a trimmed paragraph is a standalone
// title/author header like `铸蝉记 by:轩辕悬` or `by:轩辕悬` that some
// web-novel TXTs put between the title page and the first chapter.
// DetectMetadata already harvests title + author from the same form
// (against the raw text, before FormatText runs), so dropping the line
// from the formatted output doesn't lose anything — it just stops the
// metadata leaking into the reader as a body paragraph.
func isMetadataLine(paragraph string) bool {
	if utf8.RuneCountInString(paragraph) > metadataLineMaxRunes {
		return false
	}
	return AuthorByPattern.MatchString(paragraph)
}

// splitTitleFromBody splits a paragraph that opens with a structured
// chapter title from its glued first body sentence. See
// titleBodySplitPattern for the strict structural rule used to locate
// the title's end. When the paragraph either doesn't start with a
// chapter title, doesn't fit the symmetric subtitle form, or contains
// nothing after the title, the paragraph is returned unchanged.
func splitTitleFromBody(paragraph string) []string {
	loc := titleBodySplitPattern.FindStringIndex(paragraph)
	if loc == nil || loc[1] >= len(paragraph) {
		return []string{paragraph}
	}
	return []string{paragraph[:loc[1]], paragraph[loc[1]:]}
}

// splitAtChapters splits a joined paragraph at every chapter marker.
// Structured `第X章` / `Chapter N` markers are only split when preceded
// by sentence- or quote-ending punctuation (the bare form can appear in
// body text like "翻到第三章"). Bracketed-numeral markers (`「一」`,
// `【3】`) are always split — the bracket+numeral pair is unambiguous
// and some sources glue the marker straight to body text with no
// terminal punctuation (《十景缎》case).
func splitAtChapters(paragraph string) []string {
	indices := chapterSplitPattern.FindAllStringIndex(paragraph, -1)
	if len(indices) == 0 {
		return []string{paragraph}
	}
	var pieces []string
	prev := 0
	for _, m := range indices {
		if m[0] <= prev {
			continue
		}
		if !isBracketedNumeralStart(paragraph, m[0]) && !precededBySentenceEnd(paragraph, m[0]) {
			continue
		}
		pieces = append(pieces, paragraph[prev:m[0]])
		prev = m[0]
	}
	pieces = append(pieces, paragraph[prev:])
	return pieces
}

func precededBySentenceEnd(s string, pos int) bool {
	if pos == 0 {
		return false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:pos])
	_, ok := chapterSplitPrecedes[r]
	return ok
}

func isBracketedNumeralStart(s string, pos int) bool {
	r, _ := utf8.DecodeRuneInString(s[pos:])
	switch r {
	case '「', '『', '【', '〈', '[':
		return true
	}
	return false
}

// --- helpers shared with FormatText (used to be in txt.go) ---

// logicalLine is a paragraph: one or more physical lines joined together
// after dropping wrap-only line breaks. `breaks` records where physical
// line breaks were elided so positions in `text` can be translated back
// to original file offsets (currently only FormatText itself uses these
// paragraphs; the chapter parser runs on already-formatted text).
type logicalLine struct {
	text       string
	byteOffset int
	charOffset int
	breaks     []lineBreak
}

type lineBreak struct {
	joinedBytePos int
	origByteSkip  int
	origRuneSkip  int
}

// buildLogicalLines splits text into paragraphs. When the file uses the
// indented-paragraph convention (the majority of paragraph-starting lines
// begin with whitespace) any non-empty *unindented* line is treated as a
// wrap-only continuation of the preceding paragraph. Without an indent
// convention each non-empty physical line stands alone — avoids
// accidentally gluing unrelated lines together.
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
// (non-empty lines immediately following an empty line, plus the first
// non-empty line in the file) begin with whitespace. We sample up to 100
// such starts — counting all non-empty lines would skew toward "no
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
