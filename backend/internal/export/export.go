// Package export turns a book into cleaned, chunked records suitable for
// feeding to a language model or a vector store.
//
// The source is the same flat text the reader serves slices of, so what
// you export is exactly what you read — no second parse of the original
// file. On top of that it removes the debris that pirate-site TXT rips
// carry (promo lines, repeated per-chapter headers, author notes), then
// cuts the result into chunks at paragraph boundaries.
//
// Rules are data, not code. The defaults below cover the common shapes;
// `<data>/clean-rules.json` extends them without a rebuild (see
// LoadRuleOverrides). That's deliberate — the thing that varies between
// one person's library and the next is the patterns, not the pipeline,
// so there is no plugin interface to implement.
//
// Offsets in the output index the book's own char-offset space, the same
// coordinates /content and /progress use. A chunk can therefore be traced
// straight back to a position in the reader.

package export

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Rules selects which cleaning passes run. All default to on in the API
// layer; each can be turned off independently because a well-produced
// EPUB needs none of them and over-cleaning it would only lose text.
type Rules struct {
	// Promo drops pirate-site advertising injected into the prose.
	Promo bool
	// EdgeLines drops per-chapter headers/footers — the same line
	// repeated at the top or bottom of most chapters.
	EdgeLines bool
	// Normalise fixes whitespace and half/full-width punctuation mixing.
	Normalise bool
	// AuthorNotes drops end-of-chapter 作者有话说 / 求票 blocks.
	AuthorNotes bool
}

// Options controls one export run.
type Options struct {
	Rules Rules
	// ChunkChars is the target size of a chunk in runes. Chunks break on
	// paragraph boundaries, so the real size lands at or under this
	// except where a single paragraph is longer.
	ChunkChars int
}

// DefaultChunkChars is a middle ground: large enough that a chunk holds a
// whole scene rather than a fragment, small enough to sit comfortably
// inside any embedding model's window.
const DefaultChunkChars = 2000

// Chunk is one output record. The JSON field names are the wire format —
// changing them breaks anything already indexed.
type Chunk struct {
	Book    string `json:"book"`
	Author  string `json:"author,omitempty"`
	Chapter int    `json:"chapter"`
	Title   string `json:"title"`
	// Offset is the rune offset into the book's flat text where this
	// chunk's first paragraph starts — the same coordinate the reader
	// and the progress API use.
	Offset int    `json:"offset"`
	Chars  int    `json:"chars"`
	Text   string `json:"text"`
}

// Stats reports what the run did, so the UI can say how much was removed
// rather than making the user diff two files to find out.
type Stats struct {
	Chapters       int `json:"chapters"`
	Chunks         int `json:"chunks"`
	CharsIn        int `json:"chars_in"`
	CharsOut       int `json:"chars_out"`
	DroppedParas   int `json:"dropped_paragraphs"`
	RewrittenParas int `json:"rewritten_paragraphs"`
}

// Meta is the book identity stamped onto every chunk. Records are
// self-describing on purpose: several books commonly end up in one index,
// and a chunk with no title in it can't be cited.
type Meta struct {
	Title  string
	Author string
}

// Chapter is the subset of a chapter row this package needs.
type Chapter struct {
	Idx        int
	Title      string
	CharOffset int
}

// para is one source paragraph with its position in the flat text.
type para struct {
	text    string
	offset  int // rune offset into flat text
	chapter int
}

// Build runs the cleaning passes and cuts the result into chunks.
//
// flat is the book's flat text; chapters must be ordered by CharOffset,
// as the store returns them.
func Build(meta Meta, chapters []Chapter, flat string, opts Options, rs *RuleSet) ([]Chunk, Stats) {
	if opts.ChunkChars <= 0 {
		opts.ChunkChars = DefaultChunkChars
	}
	if rs == nil {
		rs = DefaultRules()
	}

	paras := splitParagraphs(flat, chapters)
	stats := Stats{Chapters: len(chapters)}
	for _, p := range paras {
		stats.CharsIn += len([]rune(p.text))
	}

	titles := make(map[int]string, len(chapters))
	for _, c := range chapters {
		titles[c.Idx] = c.Title
	}

	// Chapter titles are carried in the record's Title field, so the copy
	// sitting as the chapter's first paragraph is duplication in the text.
	paras = dropChapterTitleParas(paras, titles, &stats)

	if opts.Rules.EdgeLines {
		paras = dropRepeatedEdgeLines(paras, &stats)
	}
	if opts.Rules.AuthorNotes {
		paras = dropAuthorNotes(paras, rs, &stats)
	}
	if opts.Rules.Promo {
		paras = dropPromo(paras, rs, &stats)
	}
	if opts.Rules.Normalise {
		paras = normaliseParas(paras, rs, &stats)
	}

	// Count surviving paragraph text, NOT the joined chunk text: chunks
	// carry blank-line separators between paragraphs, and including those
	// made CharsOut exceed CharsIn on a run with every rule disabled — a
	// "-3% removed" readout in the UI.
	for _, p := range paras {
		stats.CharsOut += len([]rune(p.text))
	}
	chunks := chunkParagraphs(meta, paras, titles, opts.ChunkChars)
	stats.Chunks = len(chunks)
	return chunks, stats
}

// WriteJSONL writes one compact JSON object per line. json.Encoder already
// appends the newline, which is exactly the JSONL shape.
func WriteJSONL(w io.Writer, chunks []Chunk) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, c := range chunks {
		if err := enc.Encode(c); err != nil {
			return fmt.Errorf("encode chunk: %w", err)
		}
	}
	return nil
}

// --- Paragraph splitting ---------------------------------------------------

// splitParagraphs walks the flat text once, recording each paragraph's rune
// offset and the chapter it falls in. Tracking offsets here — rather than
// recomputing after cleaning — is what lets a chunk keep pointing at a real
// position in the book even though its text has been rewritten.
func splitParagraphs(flat string, chapters []Chapter) []para {
	var out []para
	runeIdx := 0
	ci := 0
	// chapterAt advances a cursor rather than searching per paragraph;
	// paragraphs and chapters are both in ascending offset order.
	chapterAt := func(off int) int {
		for ci+1 < len(chapters) && chapters[ci+1].CharOffset <= off {
			ci++
		}
		if len(chapters) == 0 {
			return 0
		}
		return chapters[ci].Idx
	}

	for _, line := range strings.Split(flat, "\n") {
		n := len([]rune(line))
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			out = append(out, para{text: trimmed, offset: runeIdx, chapter: chapterAt(runeIdx)})
		}
		runeIdx += n + 1 // +1 for the newline itself
	}
	return out
}

// --- Cleaning passes -------------------------------------------------------

func dropChapterTitleParas(paras []para, titles map[int]string, stats *Stats) []para {
	out := paras[:0:0]
	seen := map[int]bool{}
	for _, p := range paras {
		if !seen[p.chapter] {
			seen[p.chapter] = true
			if t, ok := titles[p.chapter]; ok && strings.TrimSpace(t) == p.text {
				stats.DroppedParas++
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// dropRepeatedEdgeLines removes the same line repeated at the head or foot
// of most chapters — a site name, the book title, a "本章完" marker. It is
// the chapter-level version of what the PDF importer does per page: decide
// by repetition rather than by pattern, because these strings are
// per-source and no fixed list would catch them.
func dropRepeatedEdgeLines(paras []para, stats *Stats) []para {
	byChapter := map[int][]int{} // chapter -> indices into paras
	var order []int
	for i, p := range paras {
		if _, ok := byChapter[p.chapter]; !ok {
			order = append(order, p.chapter)
		}
		byChapter[p.chapter] = append(byChapter[p.chapter], i)
	}
	if len(order) < 3 {
		return paras
	}

	firstCount := map[string]int{}
	lastCount := map[string]int{}
	for _, ch := range order {
		idx := byChapter[ch]
		firstCount[edgeKey(paras[idx[0]].text)]++
		lastCount[edgeKey(paras[idx[len(idx)-1]].text)]++
	}
	// A third of the chapters is enough: a footer present in only a few
	// chapters is more likely to be prose that happens to repeat.
	threshold := len(order) / 3
	if threshold < 3 {
		threshold = 3
	}

	// Repetition alone isn't sufficient. A header or footer is a short
	// label; a long paragraph repeating across chapters is far more likely
	// to be a real refrain — an epigraph, a recurring incantation — and
	// deleting it would be a silent content loss, which is the one failure
	// mode worth designing against here.
	repeated := func(i int, counts map[string]int) bool {
		if len([]rune(paras[i].text)) > edgeMaxRunes {
			return false
		}
		return counts[edgeKey(paras[i].text)] >= threshold
	}

	drop := map[int]bool{}
	for _, ch := range order {
		idx := byChapter[ch]
		if repeated(idx[0], firstCount) {
			drop[idx[0]] = true
		}
		last := idx[len(idx)-1]
		if !drop[last] && repeated(last, lastCount) {
			drop[last] = true
		}
	}

	out := paras[:0:0]
	for i, p := range paras {
		if drop[i] {
			stats.DroppedParas++
			continue
		}
		out = append(out, p)
	}
	return out
}

// edgeMaxRunes bounds what the repetition heuristic will delete.
const edgeMaxRunes = 40

// edgeKey normalises a line for repetition counting so that a header
// differing only in chapter number or spacing still groups.
var edgeDigits = regexp.MustCompile(`[0-9〇零一二三四五六七八九十百千万]+`)

func edgeKey(s string) string {
	s = strings.Join(strings.Fields(s), "")
	return edgeDigits.ReplaceAllString(s, "#")
}

// dropAuthorNotes removes 作者有话说 / 求票 blocks. A marker in the last
// few paragraphs of a chapter takes the rest of the chapter with it, which
// is the usual shape — the note is appended after the prose ends. A marker
// earlier than that drops only its own paragraph, since the chapter
// evidently continues afterwards.
func dropAuthorNotes(paras []para, rs *RuleSet, stats *Stats) []para {
	type span struct{ start, end int }
	byChapter := map[int]span{}
	var order []int
	for i, p := range paras {
		if s, ok := byChapter[p.chapter]; ok {
			s.end = i
			byChapter[p.chapter] = s
		} else {
			byChapter[p.chapter] = span{start: i, end: i}
			order = append(order, p.chapter)
		}
	}

	drop := map[int]bool{}
	for _, ch := range order {
		s := byChapter[ch]
		for i := s.start; i <= s.end; i++ {
			if drop[i] || !rs.matchAny(rs.AuthorNotes, paras[i].text) {
				continue
			}
			// "Last few" scales with chapter length: 3 paragraphs, or the
			// final fifth, whichever reaches further back.
			n := s.end - s.start + 1
			tail := n / 5
			if tail < 3 {
				tail = 3
			}
			if i >= s.end-tail+1 {
				for j := i; j <= s.end; j++ {
					drop[j] = true
				}
				break
			}
			drop[i] = true
		}
	}

	out := paras[:0:0]
	for i, p := range paras {
		if drop[i] {
			stats.DroppedParas++
			continue
		}
		out = append(out, p)
	}
	return out
}

// promoMaxRunes bounds whole-paragraph removal. A promo line is a short
// interjection; a long paragraph that merely contains a matching substring
// is prose with an injected URL, so we cut the match and keep the prose.
const promoMaxRunes = 60

func dropPromo(paras []para, rs *RuleSet, stats *Stats) []para {
	out := paras[:0:0]
	for _, p := range paras {
		if !rs.matchAny(rs.Promo, p.text) {
			out = append(out, p)
			continue
		}
		if len([]rune(p.text)) <= promoMaxRunes {
			stats.DroppedParas++
			continue
		}
		cleaned := p.text
		for _, re := range rs.Promo {
			cleaned = re.ReplaceAllString(cleaned, "")
		}
		cleaned = strings.TrimSpace(collapseSpaces(cleaned))
		if cleaned == "" {
			stats.DroppedParas++
			continue
		}
		if cleaned != p.text {
			stats.RewrittenParas++
		}
		p.text = cleaned
		out = append(out, p)
	}
	return out
}

var (
	// Any mix of periods/dots of length 3+ is an ellipsis written badly.
	ellipsis = regexp.MustCompile(`[.。．·]{3,}|…{3,}`)
	// Half-width punctuation wedged between CJK characters.
	halfWidth = regexp.MustCompile(`([\p{Han}])([,;:!?])`)
)

// repeatableMarks are the punctuation marks worth collapsing when they
// run. Written as a function rather than a regex because RE2 has no
// backreferences, so "three or more of the same mark" can't be expressed
// as a pattern — only "three or more marks from this set", which would
// also fold 「？！」 into one.
const repeatableMarks = "！？~～—"

// collapseRepeatedMarks folds a run of three or more identical marks down
// to one. Two are left alone: 「！！」 is a stylistic choice, not damage.
func collapseRepeatedMarks(s string) string {
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(runes); {
		r := runes[i]
		if !strings.ContainsRune(repeatableMarks, r) {
			b.WriteRune(r)
			i++
			continue
		}
		j := i
		for j < len(runes) && runes[j] == r {
			j++
		}
		if j-i >= 3 {
			b.WriteRune(r)
		} else {
			for k := i; k < j; k++ {
				b.WriteRune(r)
			}
		}
		i = j
	}
	return b.String()
}

var halfToFull = map[string]string{
	",": "，", ";": "；", ":": "：", "!": "！", "?": "？",
}

func normaliseParas(paras []para, rs *RuleSet, stats *Stats) []para {
	out := paras[:0:0]
	for _, p := range paras {
		before := p.text
		s := p.text
		for _, r := range rs.Replace {
			s = strings.ReplaceAll(s, r[0], r[1])
		}
		s = ellipsis.ReplaceAllString(s, "……")
		s = collapseRepeatedMarks(s)
		s = halfWidth.ReplaceAllStringFunc(s, func(m string) string {
			r := []rune(m)
			return string(r[0]) + halfToFull[string(r[1])]
		})
		s = strings.TrimSpace(collapseSpaces(s))
		if s == "" {
			stats.DroppedParas++
			continue
		}
		if s != before {
			stats.RewrittenParas++
		}
		p.text = s
		out = append(out, p)
	}
	return out
}

// collapseSpaces squeezes runs of any whitespace (including the full-width
// ideographic space used for indentation) down to nothing between CJK and
// to a single space otherwise, so Latin text stays readable.
func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r != ' ' && r != '\t' && r != '　' && !unicode.IsSpace(r) {
			b.WriteRune(r)
			continue
		}
		j := i
		for j < len(runes) && (unicode.IsSpace(runes[j]) || runes[j] == '　') {
			j++
		}
		prevCJK := b.Len() > 0 && isHanAt(b.String())
		nextCJK := j < len(runes) && unicode.Is(unicode.Han, runes[j])
		if !prevCJK || !nextCJK {
			if b.Len() > 0 && j < len(runes) {
				b.WriteRune(' ')
			}
		}
		i = j - 1
	}
	return b.String()
}

func isHanAt(s string) bool {
	rs := []rune(s)
	if len(rs) == 0 {
		return false
	}
	last := rs[len(rs)-1]
	return unicode.Is(unicode.Han, last) || strings.ContainsRune("，。！？；：、「」『』（）《》…—", last)
}

// --- Chunking --------------------------------------------------------------

// chunkParagraphs packs paragraphs into chunks without ever splitting a
// paragraph, except when one paragraph alone exceeds the target — then it
// is cut at sentence ends. Chapters never share a chunk: a chunk that
// straddles a chapter boundary can't carry one honest title.
func chunkParagraphs(meta Meta, paras []para, titles map[int]string, target int) []Chunk {
	var out []Chunk
	var buf []string
	bufLen := 0
	bufOffset := 0
	bufChapter := -1

	flush := func() {
		if len(buf) == 0 {
			return
		}
		text := strings.Join(buf, "\n\n")
		out = append(out, Chunk{
			Book:    meta.Title,
			Author:  meta.Author,
			Chapter: bufChapter,
			Title:   titles[bufChapter],
			Offset:  bufOffset,
			Chars:   len([]rune(text)),
			Text:    text,
		})
		buf = nil
		bufLen = 0
	}

	for _, p := range paras {
		if p.chapter != bufChapter {
			flush()
			bufChapter = p.chapter
		}
		for _, piece := range splitOversized(p.text, target) {
			n := len([]rune(piece))
			if bufLen > 0 && bufLen+n > target {
				flush()
			}
			if len(buf) == 0 {
				bufOffset = p.offset
			}
			buf = append(buf, piece)
			bufLen += n
		}
	}
	flush()
	return out
}

// sentenceEnd matches the trailing punctuation of a CJK sentence, keeping
// any closing quote or bracket with it.
var sentenceEnd = regexp.MustCompile(`[。！？…][」』】）"']*`)

func splitOversized(text string, target int) []string {
	if len([]rune(text)) <= target {
		return []string{text}
	}
	locs := sentenceEnd.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return []string{text}
	}
	var out []string
	start := 0
	cur := 0
	for _, loc := range locs {
		cur = loc[1]
		if len([]rune(text[start:cur])) >= target {
			out = append(out, strings.TrimSpace(text[start:cur]))
			start = cur
		}
	}
	if rest := strings.TrimSpace(text[start:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

// sortChunks is only used by tests that build chunks out of order.
func sortChunks(cs []Chunk) {
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Offset < cs[j].Offset })
}
