package library

// Source-to-canonical text normalisation.
//
// FormatText turns whatever a TXT happens to look like into a canonical
// form ParseChapters (txt.go) can match against with simple line-anchored
// regexes. Without this step, parsing would need sub-line lookarounds
// for every "messy source" case.
//
// Transforms applied (per paragraph, in order):
//
//   1. buildLogicalLines    — collapse wrap-only line breaks into one
//                              paragraph per logical block (see
//                              usesIndentedParagraphs for the heuristic).
//   2. splitAtChapters      — promote chapter markers found glued mid-
//                              paragraph onto their own paragraph
//                              (chapterSplitPattern).
//   3. splitTitleFromBody   — peel a chapter title off a glued first
//                              body sentence (titleBodySplitPattern).
//   4. TrimSpace            — flush every paragraph left.
//   5. htmlTagPattern       — strip stray <…> markup (drop the
//                              paragraph if nothing's left).
//   6. volumeFormNormalize  — rewrite `卷X` → `第X卷` so VolumePattern
//                              parses both forms uniformly.
//   7. chapterTitleSpacing  — normalise whitespace between marker and
//                              Han subtitle to exactly one `　`.
//   8. isMetadataLine       — drop title-only / author-only / by:author
//                              / 作者：author paragraphs.
//
// Regex primitives shared with txt.go live in patterns.go.

import (
	"errors"
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

// CachedPath returns the per-book cached-EPUB path inside folder:
// `<folder>/<author>/<title>.epub`. Both segments are sanitised.
//
// EPUB is the canonical cached format — the TXT source is normalised
// once during scan (FormatText + ParseChapters) and the EPUB carries
// the resulting structure for every downstream consumer. Source TXTs
// stay untouched at the folder's top level.
func CachedPath(folder, author, title string) string {
	return filepath.Join(folder, sanitizePathComponent(author), sanitizePathComponent(title)+".epub")
}

// CacheResult is what source import hands back to the scanner. The
// scanner uses Path during Phase 2 to ingest, and persists everything
// else as book metadata. Encoding and Hash refer to the source file,
// not the cached EPUB.
type CacheResult struct {
	Path        string
	SourcePath  string
	CacheFormat string // "epub" unless a source needs a dedicated reader mode.
	Title       string
	Author      string
	SourceEnc   string // detected text encoding or source-kind marker
	SourceHash  string // hex sha256 of the source file's first 64 KiB
	SourceSize  int64  // byte size of the source file on disk
	SourceMtime int64  // mtime of the source file, unix seconds
	SourcePages int    // page count for page-based sources such as image PDFs
}

// IsSupportedSource reports whether the scanner should treat name as a
// top-level source file. Text-like inputs are normalised into cached EPUBs;
// image-only PDFs use a dedicated source-backed reader mode.
func IsSupportedSource(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".epub", ".pdf", ".mobi", ".azw", ".azw3":
		return true
	default:
		return false
	}
}

// FormatSourceToCache imports any supported source format into the canonical
// cached EPUB shape.
func FormatSourceToCache(folder, sourcePath string) (CacheResult, error) {
	switch strings.ToLower(filepath.Ext(sourcePath)) {
	case ".txt":
		return FormatToCache(folder, sourcePath)
	case ".epub":
		return ImportEpubToCache(folder, sourcePath)
	case ".pdf":
		return FormatPDFToCache(folder, sourcePath)
	case ".mobi", ".azw", ".azw3":
		return ImportConvertibleEbookToCache(folder, sourcePath)
	default:
		return CacheResult{}, fmt.Errorf("unsupported source format: %s", filepath.Ext(sourcePath))
	}
}

// FormatToCache reads a source TXT, runs the format + chapter-parse
// pipeline, and writes an EPUB to `<folder>/<author>/<title>.epub` —
// never mutating the source. Returns the cached path plus metadata
// the scanner needs for persistence.
//
// The cached file is overwritten on every call; the scanner relies on
// this to re-apply format changes whenever the format logic is updated.
// Output is always EPUB 3 (see BuildEpub) — TXT is purely an input
// format from this point on, and the messy chapter-detection / shape
// normalisation work that runs here is the only place the TXT spec
// lives in the system. Everything downstream reads EPUB.
func FormatToCache(folder, sourcePath string) (CacheResult, error) {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return CacheResult{}, fmt.Errorf("read: %w", err)
	}
	st, err := os.Stat(sourcePath)
	if err != nil {
		return CacheResult{}, fmt.Errorf("stat source: %w", err)
	}
	encName, text, err := DetectAndDecode(raw)
	if err != nil {
		return CacheResult{}, fmt.Errorf("decode: %w", err)
	}
	meta := DetectMetadata(text)
	title, author := ResolveMetadata(filepath.Base(sourcePath), meta)

	cr, err := writeTextSourceToCache(folder, sourcePath, raw, st, encName, text, title, author)
	cr.SourcePath = sourcePath
	return cr, err
}

// FormatPDFToCache extracts a PDF text layer and feeds it through the same
// text -> chapters -> EPUB pipeline as TXT sources. Image-only PDFs keep the
// source file and use the source-backed page reader mode.
func FormatPDFToCache(folder, sourcePath string) (CacheResult, error) {
	st, err := os.Stat(sourcePath)
	if err != nil {
		return CacheResult{}, fmt.Errorf("stat source: %w", err)
	}
	extracted, err := ExtractPDFText(sourcePath)
	if err != nil {
		if errors.Is(err, ErrPDFNoText) {
			return formatImagePDFToCache(sourcePath, st, extracted)
		}
		return CacheResult{}, err
	}
	title, author := ResolveMetadata(filepath.Base(sourcePath), TxtMetadata{
		Title:  extracted.Title,
		Author: extracted.Author,
	})
	hash, err := headHashFile(sourcePath)
	if err != nil {
		return CacheResult{}, err
	}
	cr, err := writeTextSourceToCache(folder, sourcePath, nil, st, "pdf-text", extracted.Text, title, author, hash)
	cr.SourcePath = sourcePath
	return cr, err
}

func formatImagePDFToCache(sourcePath string, st os.FileInfo, meta PDFText) (CacheResult, error) {
	title, author := ResolveMetadata(filepath.Base(sourcePath), TxtMetadata{
		Title:  meta.Title,
		Author: meta.Author,
	})
	hash, err := headHashFile(sourcePath)
	if err != nil {
		return CacheResult{}, err
	}
	return CacheResult{
		Path:        sourcePath,
		SourcePath:  sourcePath,
		CacheFormat: "pdf-image",
		Title:       title,
		Author:      author,
		SourceEnc:   "pdf-image",
		SourceHash:  hash,
		SourceSize:  st.Size(),
		SourceMtime: st.ModTime().Unix(),
		SourcePages: meta.Pages,
	}, nil
}

// ImportEpubToCache reads a top-level EPUB source and rewrites it into the
// same canonical EPUB shape produced from TXT/PDF imports. That keeps all
// downstream content slicing on one code path.
func ImportEpubToCache(folder, sourcePath string) (CacheResult, error) {
	st, err := os.Stat(sourcePath)
	if err != nil {
		return CacheResult{}, fmt.Errorf("stat source: %w", err)
	}
	return importEpubFileToCache(folder, sourcePath, sourcePath, "epub", st)
}

func importEpubFileToCache(folder, epubPath, sourcePath, sourceEnc string, st os.FileInfo) (CacheResult, error) {
	book, err := ReadEpub(epubPath)
	if err != nil {
		return CacheResult{}, fmt.Errorf("read epub: %w", err)
	}
	if strings.TrimSpace(book.FlatText) == "" {
		return CacheResult{}, fmt.Errorf("epub has no readable text")
	}
	title, author := ResolveMetadata(filepath.Base(sourcePath), TxtMetadata{
		Title:  book.Title,
		Author: book.Author,
	})
	flatText := ensureTrailingLF(book.FlatText)
	chapters := fillEmptyChapterTitles(book.Chapters)
	if override, ok, err := chaptersFromSidecar(sourcePath, flatText); err != nil {
		return CacheResult{}, err
	} else if ok {
		chapters = override
	}
	cachedPath := CachedPath(folder, author, title)
	if err := writeEpubCache(cachedPath, title, author, flatText, chapters); err != nil {
		return CacheResult{}, err
	}
	hash, err := headHashFile(sourcePath)
	if err != nil {
		return CacheResult{}, err
	}
	return CacheResult{
		Path:        cachedPath,
		SourcePath:  sourcePath,
		CacheFormat: "epub",
		Title:       title,
		Author:      author,
		SourceEnc:   sourceEnc,
		SourceHash:  hash,
		SourceSize:  st.Size(),
		SourceMtime: st.ModTime().Unix(),
	}, nil
}

func writeTextSourceToCache(folder, sourcePath string, raw []byte, st os.FileInfo, encName, text, title, author string, hashOverride ...string) (CacheResult, error) {
	formatted := FormatText(text, title, author)
	formatted = ensureTrailingLF(formatted)
	chapters := ParseChapters(formatted, nil)
	if sourcePath != "" {
		if override, ok, err := chaptersFromSidecar(sourcePath, formatted); err != nil {
			return CacheResult{}, err
		} else if ok {
			chapters = override
		}
	}

	cachedPath := CachedPath(folder, author, title)
	if err := writeEpubCache(cachedPath, title, author, formatted, chapters); err != nil {
		return CacheResult{}, err
	}
	hash := ""
	if len(hashOverride) > 0 {
		hash = hashOverride[0]
	} else {
		hash = headHash(raw)
	}
	return CacheResult{
		Path:        cachedPath,
		CacheFormat: "epub",
		Title:       title,
		Author:      author,
		SourceEnc:   encName,
		SourceHash:  hash,
		SourceSize:  st.Size(),
		SourceMtime: st.ModTime().Unix(),
	}, nil
}

func writeEpubCache(cachedPath, title, author, formatted string, chapters []Chapter) error {
	if err := os.MkdirAll(filepath.Dir(cachedPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	tmpPath := cachedPath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	if _, err := BuildEpub(tmpFile, title, author, formatted, chapters); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("build epub: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, cachedPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func ensureTrailingLF(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

func fillEmptyChapterTitles(chapters []Chapter) []Chapter {
	out := append([]Chapter(nil), chapters...)
	for i := range out {
		if strings.TrimSpace(out[i].Title) == "" {
			out[i].Title = fmt.Sprintf("Chapter %d", i+1)
		}
	}
	return out
}

// CleanLegacyTxtCache walks folder and removes any `*.txt` files
// nested in subdirectories — those are stale TXT cache outputs from
// the pre-EPUB design. Top-level `*.txt` files (the source TXTs) are
// always left alone. Returns the number of files removed.
//
// Safe to run on a fresh / already-clean folder: it's a fast no-op
// when no nested .txt files are present.
func CleanLegacyTxtCache(folder string) (int, error) {
	removed := 0
	err := filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".txt") {
			return nil
		}
		// Sources live at the top level. Anything deeper is cache.
		if filepath.Dir(p) == folder {
			return nil
		}
		if err := os.Remove(p); err == nil {
			removed++
		}
		return nil
	})
	return removed, err
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
// real-world TXTs occasionally glue the marker to the previous
// paragraph without any terminal punctuation. The vocabulary mirrors
// ChapterPattern / BracketedNumeralPattern in txt.go.
var chapterSplitPattern = regexp.MustCompile(
	`第\s*` + chapterNumeral + `+\s*` + anyStructuredUnit +
		`|Chapter\s+\d+|CHAPTER\s+\d+` +
		`|` + bracketedNumeralBody,
)

// chapterTitleSpacingPattern normalises the whitespace between a Chinese
// chapter marker (第X章/折/…) and the first Han character of its
// subtitle. Zero or more existing spaces (ASCII or full-width) collapse
// to exactly one full-width `　`. The replacement is a no-op when the
// marker stands alone, when it's followed by `（X）` directly (e.g.
// `第九折（上）` — no subtitle), or when the line isn't a chapter title.
var chapterTitleSpacingPattern = regexp.MustCompile(
	`^([\s\p{Zs}]*第\s*` + chapterNumeral + `+\s*` + anyStructuredUnit + `)[\s\p{Zs}]*(\p{Han})`,
)

// volumeFormNormalizePattern rewrites bare-form volume markers
// (`卷X[subtitle]`) into the canonical `第X卷[subtitle]` form. Only the
// marker prefix is touched — any subtitle that follows is left in
// place and gets its whitespace tightened by chapterTitleSpacingPattern
// on the next pass. Anchored at line start (after optional indent) so
// `卷` inside body sentences ("...翻到下一卷的...") is never touched.
//
// Captures: 1 = leading whitespace (preserved), 2 = the numeric index.
var volumeFormNormalizePattern = regexp.MustCompile(
	`^([\s\p{Zs}]*)卷\s*(` + chapterNumeral + `+)`,
)

// htmlTagPattern matches a single-line `<…>` token. Used to strip
// stray rich-text markup that some scraped TXTs embed inline:
// `<center>`, `<img src=…>`, `<br>`, `<font color=…>`, `</p>`, etc. We
// don't try to be HTML-aware — the goal is to delete the markup
// without taking surrounding prose with it, and a single-line greedy
// match on `<…>` does exactly that.
//
// Cross-line tags are not matched in one go, but the per-paragraph
// loop joins continuation lines first (see buildLogicalLines), so a
// `<center>\n<img …>\n</center>` block survives as `<center><img …>
// </center>` on one joined line and all three tags strip together.
//
// Lines that become empty after stripping are dropped by the surrounding
// FormatText loop's `sub == ""` guard.
var htmlTagPattern = regexp.MustCompile(`<[^<>\r\n]*>`)

// titleBodySplitPattern matches a structured chapter title that opens a
// paragraph and is immediately followed by body text on the same line
// (no delimiter between them). The subtitle is captured as a strictly
// symmetric `XXXX，XXXX` form — 4+4 is most common, 3+3 and 5+5 are
// also accepted; asymmetric subtitles (4+3 etc.) are intentionally
// rejected because splitting them risks stealing one char from the body.
//
// Three marker shapes are recognised:
//   - `第X章/折/…` + symmetric subtitle + optional `（上）` part marker
//   - bracketed numeral like `「一」` standing alone
//   - `<numeral>、` + symmetric subtitle (e.g. `三十一、甲乙丙丁，戊己庚辛`
//     glued to body); no part marker on this arm — short-story / essay
//     books that use the enumeration form don't tend to split a single
//     chapter across multiple parts.
//
// What this DELIBERATELY does NOT cover cleanly:
//   - Single-phrase subtitles with no `，` — stay glued, parser still
//     captures via ChapterPattern's 10-char cap.
//   - Asymmetric subtitles like 4+3 / 5+4 / 3+5 — fall back to a greedy
//     symmetric arm and over-split (e.g. 3+5 takes the 3+3 prefix),
//     stealing 1–2 chars from the body. Chapter detection still
//     succeeds with a slightly corrupted title — preferred over losing
//     the chapter entry entirely.
//   - Subtitles whose body continuation also happens to start with
//     `XXXX，` (rhythmic 4-char prose) — we may over-split here; this
//     is a known limitation of structure-without-semantics matching.
var titleBodySplitPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(?:` +
		// `第X章/折/...` + symmetric Han subtitle + optional part marker.
		`第\s*` + chapterNumeral + `+\s*` + anyStructuredUnit +
		`[\s\p{Zs}]*` + symmetricHanSubtitle + `(?:（[^）\r\n]{1,6}）)?` +
		// Bracketed numeral standing alone (whole-line marker).
		`|` + bracketedNumeralBody +
		// `<numeral>、` + symmetric Han subtitle (no part marker — short-
		// story / essay collections that use this form don't split a
		// single chapter across multiple parts).
		`|` + chapterNumeral + `{1,4}\s*、\s*` + symmetricHanSubtitle +
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
// FormatText takes the raw decoded text plus the resolved title and
// author so it can drop standalone metadata lines that exactly equal
// those tokens (see isMetadataLine). Pass `""` for title/author to
// disable the metadata-line filter — useful for tests that exercise
// the format pipeline in isolation.
func FormatText(text, title, author string) string {
	// Run the pipeline when EITHER paragraph signal is present:
	//   - indented-paragraph convention (every paragraph starts with
	//     `　　` or similar; the common shape for novel-length TXTs)
	//   - one-line-per-paragraph convention via sentence terminators
	//     (centred-header / no-blank / no-indent layouts often used
	//     by personal-essay TXTs)
	// When neither holds, we genuinely can't tell wrap from paragraph
	// break and leave the text alone — running the pipeline would
	// shatter wrap-only prose into one-line-per-wrap-segment fragments.
	if !usesIndentedParagraphs(text) && !linesEndWithSentenceTerminator(text) {
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
				sub = htmlTagPattern.ReplaceAllString(sub, "")
				sub = strings.TrimSpace(sub)
				sub = volumeFormNormalizePattern.ReplaceAllString(sub, "${1}第${2}卷")
				sub = chapterTitleSpacingPattern.ReplaceAllString(sub, "${1}　${2}")
				if sub == "" {
					continue
				}
				if isMetadataLine(sub, title, author) {
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

// metadataTokenSeparators is the set of whitespace runes (ASCII space,
// tab, full-width space `　`) accepted between metadata tokens on a
// standalone title/author line.
const metadataTokenSeparators = " \t　"

// metadataAuthorMarker matches the optional marker that introduces an
// author name on a metadata line: `by:` / `By:` / `BY:` (ascii or
// full-width colon), the Chinese `作者：` / `作者:` label, or a bare
// colon used as a "<Title>：<Author>" separator. Anchored at the start
// of the remainder so it never strips a colon that just happens to
// sit later in a body paragraph.
var metadataAuthorMarker = regexp.MustCompile(`^(?i)(by\s*[:：]|author\s*[:：]|作者\s*[:：]|[:：])`)

// isMetadataLine reports whether the trimmed paragraph is composed
// only of the resolved metadata tokens — any combination of the exact
// title and the exact author (with an optional author marker between
// them), separated by whitespace. The paragraph must be consumed in
// full; any leftover content disqualifies it.
//
// The rule: this paragraph carries no body meaning, so the reader
// shouldn't see it. A line that contains only the title, only the
// author, both, or either preceded by an author marker (`by:`,
// `作者：`, bare `：`, …) is dropped.
//
// Matches (with title="<T>", author="<A>"):
//
//	<T>                  → title alone
//	<A>                  → author alone
//	<T> <A>              → title + author (no marker)
//	<T> by:<A>           → title + by:author
//	<T>：<A>             → title:author (bare colon)
//	<T> 作者：<A>        → title + 作者:author
//	by:<A>               → author marker + author
//	作者：<A>            → 作者:author
//
// Doesn't match (preserved as body):
//
//	<T><extra body>      → title prefix but extra trailing content
//	by:<wrong>           → wrong author after marker
//	... by:...           → author marker mid-sentence, no anchor
//	<unrelated line>     → unrelated body line
//
// Pass empty title/author to disable the corresponding token — used
// by tests that don't go through ResolveMetadata.
func isMetadataLine(paragraph, title, author string) bool {
	rest := strings.TrimSpace(paragraph)
	if rest == "" {
		return false
	}
	matched := false

	// Optional leading title — exact-match via HasPrefix. The trailing
	// author check enforces that any post-title remainder is consumed,
	// so a body paragraph starting with the title then continuing into
	// prose won't false-positive.
	if title != "" && strings.HasPrefix(rest, title) {
		rest = strings.TrimLeft(rest[len(title):], metadataTokenSeparators)
		matched = true
	}

	// Optional author marker. Anchored at the start of the remainder
	// so it doesn't strip a colon that just happens to sit later in
	// a body sentence.
	if loc := metadataAuthorMarker.FindStringIndex(rest); loc != nil && loc[0] == 0 {
		rest = strings.TrimLeft(rest[loc[1]:], metadataTokenSeparators)
	}

	// Optional exact author suffix.
	if author != "" && rest == author {
		rest = ""
		matched = true
	}

	return matched && rest == ""
}

// splitTitleFromBody splits a paragraph that opens with a structured
// chapter title from its glued first body sentence. See
// titleBodySplitPattern for the strict structural rule used to locate
// the title's end. When the paragraph either doesn't start with a
// chapter title, doesn't fit the symmetric subtitle form, or contains
// nothing after the title, the paragraph is returned unchanged.
// minBodyTailRunes is the smallest plausible body remainder after a
// title-end match. The symmetric subtitle arms (3+3 / 4+4 / 5+5) in
// titleBodySplitPattern occasionally match a strict PREFIX of an
// asymmetric real title — e.g. 3+3 fires inside a true 3+5 subtitle
// — and orphan the last 1-2 chars as a fake "body" paragraph. Any
// real body sentence is several characters longer than that, so
// requiring the tail to clear this threshold rejects the over-split
// without losing legitimate glued-body splits in test/real data.
const minBodyTailRunes = 3

// splitTitleFromBody peels a chapter title off a glued first body
// sentence. titleBodySplitPattern finds the title's end; anything
// before goes out as the title, anything after as the body.
//
// Two guards keep us from splitting a paragraph that's actually a
// standalone title rather than title+body glued:
//
//   - The match consumes the whole paragraph (`loc[1] >= len(paragraph)`).
//     Common case: clean standalone title with no body — the regex
//     matched it cleanly to the end.
//   - The match consumes only a prefix and the tail is implausibly
//     short (< minBodyTailRunes). This catches the symmetric-arm
//     over-split on asymmetric subtitles, where the "body" is just
//     1-2 stray chars of title remainder.
//
// What this gives up: glued paragraphs whose body is fewer than
// minBodyTailRunes characters won't split. Bodies that short don't
// occur in practice (a real first sentence is 10+ runes), so the
// trade-off is in our favour.
func splitTitleFromBody(paragraph string) []string {
	loc := titleBodySplitPattern.FindStringIndex(paragraph)
	if loc == nil || loc[1] >= len(paragraph) {
		return []string{paragraph}
	}
	if utf8.RuneCountInString(paragraph[loc[1]:]) < minBodyTailRunes {
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
// terminal punctuation.
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

// linesEndWithSentenceTerminator reports whether most non-empty lines
// in the first ~50 lines of the file end with a sentence-ending
// punctuation mark — Chinese `。！？…` plus a few closing forms that
// occur at the very end of a paragraph (`」』”』 ）` etc.) and the
// ASCII equivalents. The signal distinguishes two no-blank-line,
// no-indent layouts that look identical at the line level:
//
//   - Wrap-only: a single long paragraph hard-wrapped at fixed
//     columns. Most lines end mid-sentence (on a CJK char or a
//     non-terminator comma).
//   - One-line-per-paragraph: each physical line is a complete
//     paragraph in its own right. Lines end with sentence-final
//     punctuation by definition.
//
// Used by FormatText as a fallback gate: when usesIndentedParagraphs
// fails but this returns true, we still want to run the format
// pipeline so per-paragraph transforms (HTML strip, metadata-line
// drop, chapter-title spacing, …) get a chance. Without this signal
// FormatText would have to either always run (breaking wrap-only
// files) or never run on no-indent input (the bug that motivated
// adding this helper).
func linesEndWithSentenceTerminator(text string) bool {
	const sampleLimit = 50
	// CJK terminators + ASCII + a couple of closing forms that
	// typically end the LAST line of a dialog/exclamation paragraph.
	const terminators = "。！？…」』”’）.!?)"
	nonEmpty, terminated := 0, 0
	rest := text
	for nonEmpty < sampleLimit {
		nl := strings.IndexByte(rest, '\n')
		var line string
		if nl < 0 {
			line = rest
		} else {
			line = rest[:nl]
		}
		content := strings.TrimRight(strings.TrimRight(line, "\r"), " \t　")
		if content != "" {
			nonEmpty++
			r, _ := utf8.DecodeLastRuneInString(content)
			if strings.ContainsRune(terminators, r) {
				terminated++
			}
		}
		if nl < 0 {
			break
		}
		rest = rest[nl+1:]
	}
	return nonEmpty > 0 && terminated*2 > nonEmpty
}

// usesIndentedParagraphs returns true if most paragraph-starting lines
// begin with whitespace. Two conventions are recognised:
//
//   - Blank-line separated: paragraphs are split by empty lines, possibly
//     with wrap-only continuation lines in between. "Starts" are the
//     first non-empty line plus every non-empty line immediately
//     following an empty line. The indent ratio over starts is the right
//     signal — continuations may or may not be indented.
//
//   - One-line-per-paragraph: every non-empty line is itself a paragraph
//     (no wrap, no internal blanks). When no blank line appears between
//     two non-empty lines in the sample we fall back to counting every
//     non-empty line — otherwise a file of indented single-line
//     paragraphs reports as "not indented" just because its first line
//     (often a bare book title) lacks the indent prefix.
//
// We sample up to 100 of either metric, whichever fills first.
func usesIndentedParagraphs(text string) bool {
	const sampleLimit = 100
	starts, startsIndented := 0, 0
	allLines, allIndented := 0, 0
	prevEmpty := true
	sawInternalBlank := false
	seenContent := false
	rest := text
	for allLines < sampleLimit && starts < sampleLimit {
		nl := strings.IndexByte(rest, '\n')
		var line string
		if nl < 0 {
			line = rest
		} else {
			line = rest[:nl]
		}
		content := strings.TrimRight(line, "\r")
		if content == "" {
			if seenContent {
				prevEmpty = true
			}
		} else {
			indent := startsWithIndent(content)
			allLines++
			if indent {
				allIndented++
			}
			if prevEmpty {
				if seenContent {
					sawInternalBlank = true
				}
				starts++
				if indent {
					startsIndented++
				}
			}
			seenContent = true
			prevEmpty = false
		}
		if nl < 0 {
			break
		}
		rest = rest[nl+1:]
	}
	if sawInternalBlank {
		return starts > 0 && startsIndented*2 > starts
	}
	return allLines > 0 && allIndented*2 > allLines
}

func startsWithIndent(s string) bool {
	if s == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(r)
}
