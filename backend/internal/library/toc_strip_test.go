package library

import (
	"strings"
	"testing"
)

// body returns a paragraph long enough to clear tocHollowMaxRunes.
func body(seed string) string {
	return strings.Repeat(seed, 30)
}

// mobiStyleBook mirrors the shape a converted MOBI arrives in: a global
// volume list, then per-volume contents blocks each ending in a stub
// navigation line, with the real chapters in between.
func mobiStyleBook() string {
	nums := []string{"一", "二", "三", "四", "五"}
	var b strings.Builder
	// Whole-book volume list, then volume one's own contents block,
	// closed by the stub navigation line these blocks end with.
	for _, n := range []string{"一", "二", "三"} {
		b.WriteString("第" + n + "卷\n")
	}
	b.WriteString("第一卷\n")
	for _, n := range nums {
		b.WriteString("第" + n + "章 甲乙丙\n")
	}
	b.WriteString("返回主目录\n")
	for _, n := range nums {
		b.WriteString("第" + n + "章 甲乙丙\n")
		b.WriteString(body("春夏秋冬") + "\n")
	}
	return b.String()
}

func TestStripTableOfContentsRemovesConvertedTOC(t *testing.T) {
	text := mobiStyleBook()
	chapters := ParseChapters(text, nil)
	if len(chapters) < 12 {
		t.Fatalf("fixture did not reproduce the duplicate-TOC shape: got %d chapters", len(chapters))
	}

	got, kept := StripTableOfContents(text, chapters)
	// The volume heading that introduced the block survives as a
	// divider; the 5 real chapters nest under it.
	if len(kept) != 6 {
		t.Fatalf("kept %d chapters, want 1 divider + 5 real: %v", len(kept), titles(kept))
	}
	if kept[0].Title != "第一卷" || kept[0].Level >= kept[1].Level {
		t.Errorf("volume divider not preserved as parent: %v", titles(kept))
	}
	if strings.Contains(got, "返回主目录") {
		t.Error("contents block text survived into the body")
	}
	if n := strings.Count(got, "第一章 甲乙丙"); n != 1 {
		t.Errorf("chapter header appears %d times, want 1 (contents copy removed)", n)
	}
	if strings.Contains(got, "第三卷") {
		t.Error("whole-book volume list survived")
	}
	for i, c := range kept {
		if c.Idx != i+1 {
			t.Errorf("chapter %d has Idx %d, want %d", i, c.Idx, i+1)
		}
		if !strings.HasPrefix(got[c.ByteOffset:], c.Title) {
			t.Errorf("chapter %q ByteOffset %d does not land on its header", c.Title, c.ByteOffset)
		}
		if want := len([]rune(got[:c.ByteOffset])); c.CharOffset != want {
			t.Errorf("chapter %q CharOffset = %d, want %d", c.Title, c.CharOffset, want)
		}
	}
}

func TestStripTableOfContentsKeepsNestedDividers(t *testing.T) {
	// 部 → 卷 → 章 with no body between them is legitimate hierarchy,
	// and exactly as long as the marker registry is deep.
	text := "第一部\n第一卷\n第一章 甲乙丙\n" + body("春夏秋冬") + "\n" +
		"第二章 甲乙丙\n" + body("东南西北") + "\n"
	chapters := ParseChapters(text, nil)
	got, kept := StripTableOfContents(text, chapters)
	if got != text {
		t.Error("text was rewritten; nested dividers should be left alone")
	}
	if len(kept) != len(chapters) {
		t.Errorf("dropped %d of %d chapters: %v", len(chapters)-len(kept), len(chapters), titles(kept))
	}
}

func TestStripTableOfContentsSpareShortBooks(t *testing.T) {
	// Every chapter is a few lines. There is no contents block here,
	// only a book made of short pieces.
	var b strings.Builder
	for _, n := range []string{"一", "二", "三", "四", "五", "六", "七", "八"} {
		b.WriteString("第" + n + "章 甲乙丙\n短句。\n")
	}
	text := b.String()
	chapters := ParseChapters(text, nil)
	got, kept := StripTableOfContents(text, chapters)
	if got != text || len(kept) != len(chapters) {
		t.Errorf("gutted an all-short book: %d of %d chapters left", len(kept), len(chapters))
	}
}

func TestStripTableOfContentsLeavesCleanBookIdentical(t *testing.T) {
	var b strings.Builder
	for _, n := range []string{"一", "二", "三", "四", "五", "六"} {
		b.WriteString("第" + n + "章 甲乙丙\n" + body("春夏秋冬") + "\n")
	}
	text := b.String()
	chapters := ParseChapters(text, nil)
	got, kept := StripTableOfContents(text, chapters)
	if got != text {
		t.Error("clean book was rewritten")
	}
	if len(kept) != len(chapters) {
		t.Errorf("clean book lost chapters: %d of %d", len(kept), len(chapters))
	}
}

func TestStripTableOfContentsTrailingBlock(t *testing.T) {
	// A contents block at the very end of the file has no following
	// chapter to bound it — it must be excised to EOF.
	var b strings.Builder
	for _, n := range []string{"一", "二", "三", "四", "五", "六"} {
		b.WriteString("第" + n + "章 甲乙丙\n" + body("春夏秋冬") + "\n")
	}
	b.WriteString("第一卷\n第二卷\n第三卷\n第四卷\n第五卷\n")
	text := b.String()
	chapters := ParseChapters(text, nil)
	got, kept := StripTableOfContents(text, chapters)
	if len(kept) != 6 {
		t.Fatalf("kept %d chapters, want 6: %v", len(kept), titles(kept))
	}
	if strings.Contains(got, "第五卷") {
		t.Error("trailing contents block survived")
	}
}
