package library

// Embedded table-of-contents removal.
//
// Converted books — MOBI rips especially — often carry their own
// contents pages inside the text stream: a block of chapter titles,
// one per line, each an anchor to the real chapter further down. Once
// MobiHTMLToText flattens the markup to plain text those lines are
// indistinguishable from chapter headers, so ParseChapters matches
// them and the reader gets two entries for every chapter — the front
// half of the TOC empty, the back half real. A book with a per-volume
// contents block gets that treatment once per volume.
//
// The signal is structural rather than lexical, so it works whatever
// the contents block is titled (目录 / Contents / nothing at all) and
// whatever the source format was: contents entries have no body. A
// run of consecutive headers with nothing between them is a list of
// links, not chapters.
//
// The one legitimate way to produce empty headers is nesting — 第一部
// immediately followed by 第一卷 immediately followed by 第一章 — so a
// run must be longer than the marker hierarchy is deep before we call
// it a contents block. See tocMinRun.

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// tocHollowMaxRunes is the body size, in non-whitespace runes, up to
// which a chapter counts as empty. Not zero: contents entries often
// carry a stub navigation line (a "back to contents" link), and the
// last entry of each block reliably does.
const tocHollowMaxRunes = 40

// tocMinRun is the shortest run of empty headers we are willing to
// call a contents block.
//
// Derived from the rule registry rather than fixed, because the thing
// it guards against is depth: with N marker tiers, N consecutive empty
// headers can be a legitimate chain of grouping dividers, and N+1
// cannot. Adding a tier to chapterRules widens this automatically.
var tocMinRun = func() int {
	ranks := map[int]bool{}
	for _, r := range chapterRules {
		ranks[r.rank] = true
	}
	return len(ranks) + 1
}()

// tocMaxHollowNum/Den bound the share of a book's chapters that may be
// empty before we decline to act. A collection of vignettes, poems or
// microfiction is empty by this measure from end to end; there is no
// contents block to find, only short chapters, and excising the long
// runs would delete the book.
const (
	tocMaxHollowNum = 4
	tocMaxHollowDen = 5
)

// StripTableOfContents removes contents blocks from a formatted text
// and its parsed chapter list, returning both rewritten.
//
// Both halves matter. Dropping the chapter entries alone would leave
// the contents lines behind as body text, tacked onto the end of
// whichever chapter precedes them — invisible in the TOC, visible mid-
// read. So the block's byte range is excised too and the surviving
// chapters' offsets are recomputed against the shortened text.
//
// Returns its inputs unchanged when it finds nothing, so callers can
// use it unconditionally.
func StripTableOfContents(text string, chapters []Chapter) (string, []Chapter) {
	if len(chapters) < tocMinRun {
		return text, chapters
	}

	hollow := make([]bool, len(chapters))
	hollowCount := 0
	for i, c := range chapters {
		end := len(text)
		if i+1 < len(chapters) {
			end = chapters[i+1].ByteOffset
		}
		if chapterBodyRunes(text, c.ByteOffset, end) <= tocHollowMaxRunes {
			hollow[i] = true
			hollowCount++
		}
	}
	if hollowCount*tocMaxHollowDen > len(chapters)*tocMaxHollowNum {
		return text, chapters
	}

	drop := make([]bool, len(chapters))
	divider := make([]bool, len(chapters))
	resume := make([]int, len(chapters))
	found := false
	for i := 0; i < len(chapters); {
		if !hollow[i] {
			i++
			continue
		}
		j := i
		for j < len(chapters) && hollow[j] {
			j++
		}
		if j-i >= tocMinRun {
			end := len(text)
			if j < len(chapters) {
				end = chapters[j].ByteOffset
			}
			for k := i; k < j; k++ {
				drop[k] = true
			}
			// A contents block is introduced by the heading of the
			// section it lists, and that heading is real structure —
			// often the only volume divider a converted book has left,
			// since the content stream itself carries none. Keep the
			// last header in the block that parents the one after it,
			// and drop only the list beneath it.
			for k := j - 2; k >= i; k-- {
				if chapters[k].Level < chapters[k+1].Level {
					drop[k] = false
					divider[k] = true
					resume[k] = end
					break
				}
			}
			found = true
		}
		i = j
	}
	if !found {
		return text, chapters
	}

	var b strings.Builder
	b.Grow(len(text))
	runes := 0
	write := func(s string) {
		b.WriteString(s)
		runes += utf8.RuneCountInString(s)
	}

	out := make([]Chapter, 0, len(chapters))
	cursor := 0
	for i, c := range chapters {
		start := c.ByteOffset
		end := len(text)
		if i+1 < len(chapters) {
			end = chapters[i+1].ByteOffset
		}
		// Only the first chapter can have text ahead of it (front
		// matter); after that, cursor is the previous chapter's end.
		if start > cursor {
			write(text[cursor:start])
		}
		cursor = end
		if drop[i] {
			continue
		}
		c.ByteOffset = b.Len()
		c.CharOffset = runes
		c.Idx = len(out) + 1
		out = append(out, c)
		if divider[i] {
			header := text[start:end]
			if nl := strings.IndexByte(header, '\n'); nl >= 0 {
				header = header[:nl+1]
			}
			write(header)
			cursor = resume[i]
			continue
		}
		write(text[start:end])
	}
	if cursor < len(text) {
		write(text[cursor:])
	}
	if len(out) == 0 {
		return text, chapters
	}
	return b.String(), out
}

// chapterBodyRunes counts the non-whitespace runes a chapter holds,
// excluding its own header line. Stops counting once the total clears
// the hollow bound — the answer is only ever compared against it, and
// real chapters are long.
func chapterBodyRunes(text string, start, end int) int {
	if start >= end {
		return 0
	}
	seg := text[start:end]
	nl := strings.IndexByte(seg, '\n')
	if nl < 0 {
		return 0 // header line with no newline after it: nothing follows
	}
	n := 0
	for _, r := range seg[nl+1:] {
		if unicode.IsSpace(r) {
			continue
		}
		n++
		if n > tocHollowMaxRunes {
			return n
		}
	}
	return n
}
