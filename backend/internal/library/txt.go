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
	"strconv"
	"strings"
	"unicode/utf8"
)

// chapterPatternFor builds a regex matching `第X<unit>[subtitle]` on
// its own (formatted) line, for a specific unit character. We have one
// rule per unit (章/节/回/折/篇/集) instead of one combined rule so the
// survey-and-select pass in ParseChapters can pick the unit a given
// book actually uses — a book using `第X回` doesn't want stray
// `第二节晚自习` body matches polluting its TOC.
//
// Group 1 captures the title (marker + subtitle). The subtitle arm
// tries `[…]{0,11}（X）` first (clean part-marker form like
// `（上）/（下）/（补）/（外传）`), otherwise caps at 10 chars.
//
// Whole-line anchored at the end — a real chapter title fills its
// own line; text trailing past the 10-char subtitle bound means the
// line is body that happens to start with `第X<unit>`. Glued-to-body
// cases are peeled out earlier by FormatText's titleBodySplitPattern.
func chapterPatternFor(unit string) *regexp.Regexp {
	return regexp.MustCompile(
		`^[\s\p{Zs}]*` +
			`(第\s*` + chapterNumeral + `+\s*` + unit +
			`(?:[^\r\n。「」]{0,11}（[^）\r\n]{1,6}）|[^\r\n。「」]{0,10})` +
			`)` +
			`[\s\p{Zs}]*$`,
	)
}

// Per-unit chapter patterns. Each is scored independently in the
// fit-assessment pass so spurious unit chars from body text get
// filtered out by the dominant unit's score.
var (
	chapterByZhangPattern = chapterPatternFor("章")
	chapterByJiePattern   = chapterPatternFor("节")
	chapterByHuiPattern   = chapterPatternFor("回")
	chapterByZhePattern   = chapterPatternFor("折")
	chapterByPianPattern  = chapterPatternFor("篇")
	chapterByJiPattern    = chapterPatternFor("集")
)

// ChapterEnglishPattern matches `Chapter N` / `CHAPTER N` style
// headers (English-language e-novels in our corpus occasionally use
// these). Capture group 1 = title.
var ChapterEnglishPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(Chapter\s+\d+[^\r\n]{0,30}|CHAPTER\s+\d+[^\r\n]{0,30})` +
		`[\s\p{Zs}]*$`,
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

// PartPattern matches a 部 header on its own line (`第X部[subtitle]`).
// Whole-line anchored to reject body mentions like "他翻到下一部
// 里..." — 部 is rare enough as a structural marker that this is the
// safer trade-off. The (subtitle bounded to 30 chars) allows for
// short part subtitles like "亡命天涯" while still rejecting long
// body sentences that happen to contain `第X部`.
//
// Books that use 部 as an outermost grouping (above 卷) are uncommon
// but real — the TOC tree builder treats this as tier 0 (outer-most)
// when present.
//
// Group 1 captures the full title.
var PartPattern = regexp.MustCompile(
	`^[\s\p{Zs}]*` +
		`(第\s*` + chapterNumeral + `+\s*部[^\r\n]{0,30})` +
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
//   - Author capped at 16 chars to accommodate ASCII pen names
//     (real-world net-novel handles run up to ~15 chars with
//     underscores and digits) while still imposing a bound that
//     rejects clear body-prose runs. Whitespace and Chinese / ASCII
//     punctuation are forbidden, so an author capture stops at the
//     first comma / full stop / quote boundary even if the line
//     contains more text after.
//
// Anchored at line start + end. ASCII and full-width colon both
// accepted.
var AuthorLabelPattern = regexp.MustCompile(
	`^([^\r\n]{0,30}?)作者\s*[:：]\s*([^\r\n，。！？,.!?\s]{1,16})\s*$`,
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

// ruleMode picks the selection strategy for a chapter rule's matches.
type ruleMode int

const (
	// modeTrust: the pattern's word-shape is distinctive enough that
	// any match is taken at face value. Used for low-cardinality fixed
	// vocabularies (楔子/序章/…) and unambiguous bracketed numerals.
	modeTrust ruleMode = iota
	// modeCompete: the pattern can also fire on body prose (every
	// `第X<unit>` shape, enumerated `一、…`). Kept only when its
	// confidence score clears the dominance threshold at its rank.
	modeCompete
)

// chapterRule is one row in the marker-hierarchy registry. Rank fixes
// the rule's place in the hierarchy — smaller = outer, larger =
// leaf-ward. Mode controls how the rule's matches are validated.
// MinCount sets an absolute floor on match count for compete-mode
// rules (default 1 if zero): patterns whose noise floor warrants
// extra evidence (enumerated, loose-digit) can require more.
//
// Several rules can share a rank (chapter tier has `第X章`, `第X节`,
// `第X回`, etc., plus the named/bracketed/enumerated shapes).
type chapterRule struct {
	name     string
	rank     int
	pattern  *regexp.Regexp
	mode     ruleMode
	minCount int
}

// chapterRules is the registry of marker tiers in rank order. Adding
// a new tier — say `第X篇` between 卷 and 章 — is a one-line insert
// here plus bumping the deeper rules' ranks. The parsing, scoring,
// merging, depth computation, tree building and EPUB serialisation
// downstream all derive their behaviour from this table — no constant
// lookups, no per-tier branching elsewhere in the codebase.
//
// Chapter-tier `第X<unit>` is split per unit char so a `第X回` book
// can score `第X节` body lines down to zero; otherwise the union arm
// would lump them together and one stray `第二节晚自习` would smuggle
// itself into the TOC.
var chapterRules = []chapterRule{
	{name: "part", rank: 0, pattern: PartPattern, mode: modeCompete},
	{name: "volume", rank: 1, pattern: VolumePattern, mode: modeCompete},
	{name: "ch-zhang", rank: 2, pattern: chapterByZhangPattern, mode: modeCompete},
	{name: "ch-jie", rank: 2, pattern: chapterByJiePattern, mode: modeCompete},
	{name: "ch-hui", rank: 2, pattern: chapterByHuiPattern, mode: modeCompete},
	{name: "ch-zhe", rank: 2, pattern: chapterByZhePattern, mode: modeCompete},
	{name: "ch-pian", rank: 2, pattern: chapterByPianPattern, mode: modeCompete},
	{name: "ch-ji", rank: 2, pattern: chapterByJiPattern, mode: modeCompete},
	{name: "ch-en", rank: 2, pattern: ChapterEnglishPattern, mode: modeCompete},
	{name: "named", rank: 2, pattern: NamedChapterPattern, mode: modeTrust},
	{name: "bracket", rank: 2, pattern: BracketedNumeralPattern, mode: modeTrust},
	{name: "enum", rank: 2, pattern: EnumeratedNumeralPattern, mode: modeCompete, minCount: 2},
}

// rankMinKept caps single-match false positives at container tiers.
// Parts/volumes that come in groups (real ones) clear this; an
// isolated body line like `第一部经书` won't. The chapter tier accepts
// singletons because a one-chapter book is plausible.
var rankMinKept = map[int]int{0: 2, 1: 2, 2: 1}

// dominanceRatio is the fraction of the rank's strongest signal a
// compete-mode rule must reach to be kept. 0.5 means "at least half
// as much evidence as the dominant rule at this rank".
const dominanceRatio = 0.5

// chapterLeafRank is the rank of the deepest tier in chapterRules.
// The loose-digit fallback uses this rank, and the "is the chapter
// tier dense enough?" gate counts kept entries at this rank.
var chapterLeafRank = func() int {
	r := 0
	for _, rule := range chapterRules {
		if rule.rank > r {
			r = rule.rank
		}
	}
	return r
}()

// ParseChapters scans `text` (decoded UTF-8, normalised by FormatText
// via the scanner's format step) for chapter headers.
//
// When `re` is non-nil the caller's regex is the sole matcher and
// every match is emitted at Level=0 — the caller is treating the
// regex as a single-tier matcher with no hierarchy.
//
// When `re` is nil the matcher runs in two phases:
//
//	Phase 1 — Survey: scan every rule, collect matches per rule.
//	          Nothing is decided yet.
//
//	Phase 2 — Score & select: each compete-mode rule gets a confidence
//	          score = count × (0.3 + 0.7 × consecutiveRatio), trust-
//	          mode rules use raw count. At each rank we keep:
//	          (a) every trust rule with at least one match;
//	          (b) every compete rule whose score is >= max(max_compete
//	              _score × dominanceRatio, trust_count × 1.0) AND
//	              count >= rule.minCount;
//	          (c) the rank itself only if the total kept-count meets
//	              rankMinKept[rank] (singleton parts/volumes drop).
//
// Surviving matches merge by offset; assignDepth rewrites Level to
// tree-position depth. If the chapter tier still ends up sparse, the
// loose-digit fallback fills in; if nothing matched at all, a
// synthetic "正文" stands alone.
func ParseChapters(text string, re *regexp.Regexp) []Chapter {
	if re != nil {
		out := scanLineAnchored(text, re, 0)
		if len(out) == 0 {
			return syntheticChapter()
		}
		return out
	}

	matches := make([][]Chapter, len(chapterRules))
	for i, r := range chapterRules {
		matches[i] = scanLineAnchored(text, r.pattern, r.rank)
	}

	primary := selectChapters(matches)

	leafCount := 0
	for _, c := range primary {
		if c.Level == chapterLeafRank {
			leafCount++
		}
	}
	if leafCount >= minPrimaryForNoFallback {
		return assignDepth(primary)
	}
	loose := scanLineAnchored(text, LooseDigitPattern, chapterLeafRank)
	if len(loose) >= minLooseForFallback {
		return assignDepth(mergeChapters(primary, loose))
	}
	if len(primary) > 0 {
		return assignDepth(primary)
	}
	return syntheticChapter()
}

// selectChapters runs the score-and-keep policy over the per-rule
// match lists. Returns the merged kept matches; the merge order
// keeps offset ordering stable.
func selectChapters(matches [][]Chapter) []Chapter {
	// Per-rank statistics from Phase 1 results.
	maxCompeteScore := map[int]float64{}
	trustCount := map[int]int{}
	for i, r := range chapterRules {
		if len(matches[i]) == 0 {
			continue
		}
		switch r.mode {
		case modeTrust:
			trustCount[r.rank] += len(matches[i])
		case modeCompete:
			s := scoreRule(matches[i])
			if s > maxCompeteScore[r.rank] {
				maxCompeteScore[r.rank] = s
			}
		}
	}

	// Decide which rules' matches to keep.
	keep := make([]bool, len(chapterRules))
	keptCount := map[int]int{}
	for i, r := range chapterRules {
		if len(matches[i]) == 0 {
			continue
		}
		if r.mode == modeTrust {
			keep[i] = true
			keptCount[r.rank] += len(matches[i])
			continue
		}
		// Compete: must clear absolute floor + dominance threshold.
		minC := r.minCount
		if minC < 1 {
			minC = 1
		}
		if len(matches[i]) < minC {
			continue
		}
		s := scoreRule(matches[i])
		threshold := maxCompeteScore[r.rank] * dominanceRatio
		if t := float64(trustCount[r.rank]); t > threshold {
			threshold = t
		}
		if s < threshold {
			continue
		}
		keep[i] = true
		keptCount[r.rank] += len(matches[i])
	}

	// Drop ranks whose total kept count doesn't meet the minimum —
	// catches isolated body false-positives at container tiers (a
	// lone `第一部经书` standalone paragraph in a chapter-only book).
	for rank, total := range keptCount {
		min := rankMinKept[rank]
		if min == 0 {
			min = 1
		}
		if total < min {
			for i, r := range chapterRules {
				if r.rank == rank {
					keep[i] = false
				}
			}
		}
	}

	// Collect kept lists; mergeChapters dedups + sorts by offset.
	lists := make([][]Chapter, 0, len(chapterRules))
	for i := range chapterRules {
		if keep[i] {
			lists = append(lists, matches[i])
		}
	}
	return mergeChapters(lists...)
}

// scoreRule computes a confidence score for a rule's match list.
// score = count × (0.3 + 0.7 × consecutiveRatio). Higher count and
// higher consecutiveness both raise the score; the 0.3 floor keeps
// a single-match rule from scoring zero just because there's no
// pair to be consecutive with.
func scoreRule(matches []Chapter) float64 {
	if len(matches) == 0 {
		return 0
	}
	return float64(len(matches)) * (0.3 + 0.7*consecutiveRatio(matches))
}

// consecutiveRatio is the fraction of adjacent match pairs whose
// parsed chapter numbers are exactly +1 apart. A book where the
// chapter index resets per volume (1..10, 1..10, …) still scores
// high — only the inter-volume reset pairs miss, the rest are
// consecutive. A noise pattern with random body numbers scores low.
// Returns 1.0 for <2 parseable numbers (vacuous — no pairs to
// compare).
func consecutiveRatio(matches []Chapter) float64 {
	nums := make([]int, 0, len(matches))
	for _, c := range matches {
		if n, ok := parseChapterNumber(c.Title); ok {
			nums = append(nums, n)
		}
	}
	if len(nums) < 2 {
		return 1.0
	}
	consec := 0
	for i := 1; i < len(nums); i++ {
		if nums[i] == nums[i-1]+1 {
			consec++
		}
	}
	return float64(consec) / float64(len(nums)-1)
}

// assignDepth walks an offset-sorted chapter list (whose Level field
// initially carries each entry's marker rank from chapterRules) and
// rewrites Level to the entry's depth in the implicit tree that a
// stack-based builder would produce: 0 for entries with no smaller-
// rank ancestor still open, otherwise parent_depth + 1.
//
// Missing tiers collapse naturally — a 部+章 book (ranks 0, 2) emits
// depths 0, 1 with no empty depth-slot for the absent volume tier.
// Stray leaf-rank entries that appear before any container (the
// `楔子` opening before `第一部` in classical wuxia layouts) emit at
// depth 0 because their stack is empty — matching the visual position
// the EPUB nav will place them at.
//
// This is the only pass that maintains Level state; the rest of the
// pipeline reads Level as a depth value without rewriting it.
func assignDepth(chapters []Chapter) []Chapter {
	stack := make([]int, 0, 4) // open ancestor ranks
	out := make([]Chapter, len(chapters))
	for i, c := range chapters {
		for len(stack) > 0 && stack[len(stack)-1] >= c.Level {
			stack = stack[:len(stack)-1]
		}
		depth := len(stack)
		stack = append(stack, c.Level)
		c.Level = depth
		out[i] = c
	}
	return out
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
	return []Chapter{{Idx: 1, Title: "正文", Level: 0, CharOffset: 0, ByteOffset: 0}}
}

// parseChapterNumber extracts the integer index from a chapter title.
// Handles both ASCII digits ("第12章…", "Chapter 7") and CJK numerals
// up to four-digit values ("第一章", "第二十", "第一百零一"). The
// numeral run is located by skipping any leading `第` and consuming
// the first contiguous digit-or-cjk-numeral run; everything after
// that run is the unit + subtitle and is ignored.
//
// Returns (0, false) when no numeral run is parseable — used by
// consecutiveRatio to skip titles whose index can't be compared
// (named chapters like 楔子, or English `Chapter N` where N parses
// fine via the ASCII fast path).
func parseChapterNumber(title string) (int, bool) {
	rest := strings.TrimSpace(title)
	if rest == "" {
		return 0, false
	}
	// English `Chapter N` / `CHAPTER N`: skip word + whitespace.
	if low := strings.ToLower(rest); strings.HasPrefix(low, "chapter") {
		rest = strings.TrimLeft(rest[len("chapter"):], " \t")
	} else {
		// Skip a leading `第` (with optional whitespace after).
		rest = strings.TrimPrefix(rest, "第")
		rest = strings.TrimLeft(rest, " \t　")
		// Skip a leading bracket (for "「一」", "（三）" etc.).
		for _, b := range "「『【〈[（(" {
			rest = strings.TrimPrefix(rest, string(b))
		}
	}
	run := extractNumeralRun(rest)
	if run == "" {
		return 0, false
	}
	return parseNumeralRun(run)
}

// extractNumeralRun returns the longest leading run of ASCII digit /
// CJK numeral characters from s, stopping at the first non-numeral
// rune (typically a unit char like 章/卷/部 or punctuation).
func extractNumeralRun(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || isCJKNumeral(r) {
			b.WriteRune(r)
			continue
		}
		break
	}
	return b.String()
}

func isCJKNumeral(r rune) bool {
	switch r {
	case '零', '〇', '一', '二', '三', '四', '五', '六', '七', '八', '九',
		'十', '百', '千', '万':
		return true
	}
	return false
}

// parseNumeralRun converts a pure numeral run (ASCII or CJK) into an
// int. Supports CJK positional notation up to 万 (10000-class books
// are vanishingly rare). The classical "implicit leading one" rule
// is honoured — `十` parses as 10, `十一` as 11, `百` as 100. Returns
// (0, false) on unparseable input.
func parseNumeralRun(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	// ASCII fast path — handles "1", "12", "1234", etc.
	if isAllASCIIDigits(s) {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return 0, false
		}
		return n, true
	}
	cjkDigit := map[rune]int{
		'零': 0, '〇': 0,
		'一': 1, '二': 2, '三': 3, '四': 4, '五': 5,
		'六': 6, '七': 7, '八': 8, '九': 9,
	}
	cjkUnit := map[rune]int{
		'十': 10, '百': 100, '千': 1000, '万': 10000,
	}
	total := 0
	current := 0
	runes := []rune(s)
	for i, r := range runes {
		if d, ok := cjkDigit[r]; ok {
			current = d
			continue
		}
		if u, ok := cjkUnit[r]; ok {
			// Leading unit ("十" → 10, "百" → 100, …) carries an
			// implicit 1. Also treat units right after a zero
			// ("一百零五" — already handled by current=5 above; this
			// branch is for sequences like "十" alone).
			if current == 0 && (i == 0 || runes[i-1] == '零' || runes[i-1] == '〇') {
				current = 1
			}
			total += current * u
			current = 0
			continue
		}
		return 0, false
	}
	total += current
	if total == 0 {
		return 0, false
	}
	return total, true
}

func isAllASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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
