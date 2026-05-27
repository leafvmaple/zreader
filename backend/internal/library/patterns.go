package library

// Regex building blocks shared across format-time (format.go) and
// parse-time (txt.go) chapter / volume patterns.
//
// Why these live here, not inline in each regex:
//
// The same primitives — the numeral character class, the structured
// chapter-marker unit set, the bracketed-numeral shape, the symmetric
// Han subtitle alternation — appear in 8+ different regexes between
// the two files. Inlining them locally was the source of two bugs we
// hit: a `〇` (U+3007 ideographic-number-zero) that was in the
// bracketed-numeral class but missing from the structured-marker
// class, and a quiet drift between the format-time and parse-time
// definitions of "what counts as a chapter marker". Centralising
// them keeps the definitions consistent and makes "add support for
// numeral `壹贰叁` / a new marker unit / a new bracket pair" a
// one-line change.
//
// They're plain string constants rather than pre-compiled regexes
// because each consumer needs to wrap them in its own anchoring,
// capture groups, and surrounding context.
//
// Conventions for new entries:
//   - Character classes end in their bracket — caller appends `+` or
//     `{N,M}` for repetition.
//   - Composite shapes (e.g. bracketedNumeralBody) bake in their
//     internal repetition but no outer anchors / captures.
//   - Anything that's used in only one place stays inline at the
//     call site.
const (
	// chapterNumeral matches a single ASCII digit OR CJK numeral —
	// the set we've seen in real-world TXTs. `〇` (U+3007) and `零`
	// are both kept because both forms appear depending on the
	// source. Caller repeats with `+` or bounds with `{N,M}`.
	chapterNumeral = `[\d零〇一二三四五六七八九十百千万]`

	// structuredUnit is the unit char that follows the index in
	// `第X<unit>` chapter markers. `卷` is INTENTIONALLY absent so
	// volume markers can be matched separately by VolumePattern at
	// level=0; mixing the two would put 卷 headers flat in the TOC
	// next to their child chapters.
	structuredUnit = `[章节回篇集部折]`

	// anyStructuredUnit adds 卷 to structuredUnit. Used by format-
	// time passes that treat all marker types uniformly — e.g.
	// promoting a chapter marker glued mid-paragraph onto its own
	// paragraph regardless of whether it's a chapter or volume.
	// Parsing keeps the two tiers separate so they end up at
	// different levels.
	anyStructuredUnit = `[章节回卷篇集部折]`

	// bracketedNumeralBody is the standalone `「N」` / `【N】` /
	// `〈N〉` / `[N]` shape — a CJK or Arabic numeral wrapped in
	// one of the recognised bracket pairs (`《》` excluded because
	// it's used for book titles; `（）` / `()` excluded because
	// they're far too common inline).
	//
	// Reused both as a whole-line chapter marker (txt.go's
	// BracketedNumeralPattern) and as a mid-paragraph split point
	// (format.go's chapterSplitPattern / titleBodySplitPattern).
	bracketedNumeralBody = `[「『【〈\[]\s*` + chapterNumeral + `+\s*[」』】〉\]]`

	// symmetricHanSubtitle matches the strict N+N comma-separated
	// Han subtitle shape (`XXXX，XXXX` etc.) we use as a title-end
	// anchor for splitting glued title/body. Three N values are
	// accepted (3, 4, 5 — covering the common rhythms used in
	// chapter titles); asymmetric subtitles like 4+3 / 3+5 fall
	// back to the longest symmetric arm and over-split. See
	// splitTitleFromBody for how this is mitigated.
	symmetricHanSubtitle = `(?:` +
		`[\p{Han}]{4}，[\p{Han}]{4}` +
		`|[\p{Han}]{5}，[\p{Han}]{5}` +
		`|[\p{Han}]{3}，[\p{Han}]{3}` +
		`)`
)
