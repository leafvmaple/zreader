package library

import (
	"strings"
	"testing"
)

func TestParseChapters_StrictOnly(t *testing.T) {
	text := "第一章 起\n内容\n第二章 承\n内容\n第三章 转\n内容\n"
	out := ParseChapters(text, nil)
	if len(out) != 3 {
		t.Fatalf("want 3, got %d: %+v", len(out), out)
	}
}

func TestParseChapters_LooseFallback(t *testing.T) {
	// 1 strict + many bare-digit dividers — tier 2 should kick in.
	text := "楔子\n开篇\n\n1\n第一段\n\n2\n第二段\n\n3\n第三段\n"
	out := ParseChapters(text, nil)
	if len(out) != 4 { // 楔子 + 1,2,3
		t.Fatalf("want 4 chapters (楔子+3 digits), got %d: %+v", len(out), out)
	}
	if out[0].Title != "楔子" || out[1].Title != "1" {
		t.Fatalf("merge/order wrong: %+v", out)
	}
	for i, c := range out {
		if c.Idx != i+1 {
			t.Fatalf("Idx not renumbered: %+v", out)
		}
	}
}

func TestParseChapters_NoMatches_Synthetic(t *testing.T) {
	text := "just some prose with no headers at all.\nmore prose.\n"
	out := ParseChapters(text, nil)
	if len(out) != 1 || out[0].Title != "正文" {
		t.Fatalf("want synthetic 正文, got %+v", out)
	}
}

func TestParseChapters_BracketedNumerals(t *testing.T) {
	// Bracketed CJK numerals — `「一」`/`「二」`/`「三」` — should land
	// in the primary tier without needing the loose-digit fallback.
	// The leading non-bracketed line must NOT match any chapter pattern,
	// so we deliberately avoid words listed in NamedChapterPattern.
	text := "示例标题\n\n「一」\n第一段内容。\n\n「二」\n第二段内容。\n\n「三」\n第三段内容。\n"
	out := ParseChapters(text, nil)
	if len(out) != 3 {
		t.Fatalf("want 3, got %d: %+v", len(out), out)
	}
	if out[0].Title != "「一」" || out[1].Title != "「二」" || out[2].Title != "「三」" {
		t.Fatalf("title mismatch: %+v", out)
	}
}

func TestParseChapters_ParenthesisedNumeral(t *testing.T) {
	// Personal-essay TXTs use `（X）` standalone as a chapter divider.
	// Whole-line anchored so inline `（一）` in body prose doesn't match.
	text := "（零）\n正文。\n\n（一）\n更多正文。\n\n他翻到了（二）页查看。\n\n（二）\n再来正文。\n"
	out := ParseChapters(text, nil)
	if len(out) != 3 {
		t.Fatalf("want 3 (zero/one/two), got %d: %+v", len(out), out)
	}
	want := []string{"（零）", "（一）", "（二）"}
	for i, c := range out {
		if c.Title != want[i] {
			t.Errorf("idx %d: title=%q, want %q", i, c.Title, want[i])
		}
	}
}

func TestParseChapters_StructuredRejectsBodyTrail(t *testing.T) {
	// `第X章/节/…` whole-line anchored: a body paragraph that happens
	// to start with `第X节` and continues into prose must NOT match
	// (the marker isn't followed by a clean chapter-title-shaped tail).
	text := "第一章　起源\n" +
		"内容内容。\n\n" +
		"　　第一节甲乙丙丁戊己庚辛壬癸子丑寅卯辰巳午未申酉。\n\n" +
		"第二章　承接\n" +
		"更多内容。\n"
	out := ParseChapters(text, nil)
	if len(out) != 2 {
		t.Fatalf("want 2 (only real chapters), got %d: %+v", len(out), out)
	}
	if out[0].Title != "第一章　起源" || out[1].Title != "第二章　承接" {
		t.Errorf("wrong titles: %+v", out)
	}
}

func TestParseChapters_BracketedVariants(t *testing.T) {
	// Mix of bracket styles + Arabic + multi-digit numerals.
	text := "【一】\nA\n〈二〉\nB\n『3』\nC\n[二十]\nD\n"
	out := ParseChapters(text, nil)
	if len(out) != 4 {
		t.Fatalf("want 4, got %d: %+v", len(out), out)
	}
}

func TestParseChapters_BracketedNotInsideDialog(t *testing.T) {
	// 「...」 used for dialog (non-numeric body) must NOT match.
	text := "「我来啦」他说。\n「真的吗？」她答道。\n第一章 起\n内容\n第二章 承\n内容\n第三章 转\n内容\n"
	out := ParseChapters(text, nil)
	if len(out) != 3 {
		t.Fatalf("want 3 (dialog lines ignored), got %d: %+v", len(out), out)
	}
	for _, c := range out {
		if strings.HasPrefix(c.Title, "「") {
			t.Fatalf("dialog line leaked into chapters: %+v", c)
		}
	}
}

func TestParseChapters_NamedChapterWithEmbeddedSpace(t *testing.T) {
	// Scraped TXTs sometimes centre named-chapter headers visually by
	// putting a full-width space inside the word — `楔　子`, `序　章`.
	// NamedChapterPattern must still match.
	cases := []struct {
		name string
		text string
		want string
	}{
		{"楔　子 with full-width space", "楔　子\n正文。\n", "楔　子"},
		{"序 章 with ASCII space", "序 章\n正文。\n", "序 章"},
		{"楔子 no space — still matches", "楔子\n正文。\n", "楔子"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ParseChapters(tc.text, nil)
			if len(out) != 1 {
				t.Fatalf("want 1 chapter, got %d: %+v", len(out), out)
			}
			if out[0].Title != tc.want {
				t.Errorf("title=%q, want %q", out[0].Title, tc.want)
			}
		})
	}
}

func TestParseChapters_LooseAloneNotEnough(t *testing.T) {
	// Single bare digit on its own — below loose threshold, should fall through to synthetic.
	text := "some prose\n\n7\n\nmore prose\n"
	out := ParseChapters(text, nil)
	if len(out) != 1 || out[0].Title != "正文" {
		t.Fatalf("want synthetic, got %+v", out)
	}
}

func TestParseChapters_VolumesWithChapters(t *testing.T) {
	// Two volumes with two chapters each. Volume markers must come out at
	// level=0 and chapter markers at level=1, all flat-ordered by offset.
	// Synthetic CJK placeholders only — no corpus content.
	text := "第一卷　甲乙丙丁\n\n" +
		"第一章　子丑寅卯\n正文一。\n\n" +
		"第二章　辰巳午未\n正文二。\n\n" +
		"第二卷　戊己庚辛\n\n" +
		"第三章　申酉戌亥\n正文三。\n\n" +
		"第四章　春夏秋冬\n正文四。\n"
	out := ParseChapters(text, nil)
	if len(out) != 6 {
		t.Fatalf("want 6 entries (2 vols + 4 chaps), got %d: %+v", len(out), out)
	}
	wantLevels := []int{0, 1, 1, 0, 1, 1}
	for i, c := range out {
		if c.Level != wantLevels[i] {
			t.Errorf("entry %d (%q): level=%d, want %d", i, c.Title, c.Level, wantLevels[i])
		}
		if c.Idx != i+1 {
			t.Errorf("entry %d: Idx=%d, want %d", i, c.Idx, i+1)
		}
	}
}

func TestParseChapters_ChaptersBeforeFirstVolume(t *testing.T) {
	// Some books begin with chapters because the author hadn't
	// introduced volumes yet; explicit 卷 markers appear later. The
	// parser keeps every chapter in document order and assignDepth
	// rewrites Level to tree-position depth: pre-volume chapters
	// (no ancestor in scope) are roots at depth 0 — same outer level
	// as the volume that follows them. Chapters that fall under a
	// volume nest one deeper.
	//
	// Two volumes here so the volume tier clears rankMinKept[1]=2
	// (singleton-volume books are treated as flat, see
	// TestParseChapters_LoneVolumeTreatedAsFlat).
	text := "第一章　甲乙丙丁\n正文。\n\n" +
		"第二章　子丑寅卯\n正文。\n\n" +
		"第二卷　戊己庚辛\n\n" +
		"第三章　申酉戌亥\n正文。\n\n" +
		"第三卷　壬癸\n\n" +
		"第四章　甲乙\n正文。\n"
	out := ParseChapters(text, nil)
	if len(out) != 6 {
		t.Fatalf("want 6, got %d: %+v", len(out), out)
	}
	wantLevels := []int{0, 0, 0, 1, 0, 1}
	for i, want := range wantLevels {
		if out[i].Level != want {
			t.Errorf("entry %d (%q): level=%d, want %d", i, out[i].Title, out[i].Level, want)
		}
	}
}

func TestParseChapters_LoneVolumeTreatedAsFlat(t *testing.T) {
	// A single 第X卷 marker among many chapters is more likely body
	// noise than legit structure — the dominance + rank-min check
	// drops it, leaving the chapters as a flat list. The chapter
	// titles must still all surface.
	text := "第一章　甲\n正文。\n\n" +
		"第二章　乙\n正文。\n\n" +
		"第一卷　丙\n\n" +
		"第三章　丁\n正文。\n"
	out := ParseChapters(text, nil)
	if len(out) != 3 {
		t.Fatalf("want 3 chapters (lone volume dropped), got %d: %+v", len(out), out)
	}
	for _, c := range out {
		if strings.HasPrefix(c.Title, "第一卷") {
			t.Errorf("lone volume leaked through: %+v", c)
		}
		if c.Level != 0 {
			t.Errorf("flat-book chapter should be level=0, got %+v", c)
		}
	}
}

func TestParseChapterNumber(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"第一章 起", 1, true},
		{"第二章　承", 2, true},
		{"第十章", 10, true},
		{"第十一章", 11, true},
		{"第二十章", 20, true},
		{"第二十一章", 21, true},
		{"第一百章", 100, true},
		{"第一百零一章", 101, true},
		{"第一千二百三十四回", 1234, true},
		{"第12章 起", 12, true},
		{"第123章", 123, true},
		{"Chapter 7 — Title", 7, true},
		{"CHAPTER 42", 42, true},
		{"楔子", 0, false},     // named — no numeral
		{"序　章", 0, false},   // named — no numeral
		{"（一）", 1, true},     // bracketed numeral
		{"「二十」", 20, true}, // another bracket form
		{"五、子曰", 5, true},  // enumerated
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseChapterNumber(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseChapterNumber(%q) = (%d, %v), want (%d, %v)",
				tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestConsecutiveRatio(t *testing.T) {
	mk := func(titles ...string) []Chapter {
		out := make([]Chapter, len(titles))
		for i, t := range titles {
			out[i] = Chapter{Title: t}
		}
		return out
	}
	cases := []struct {
		name string
		in   []Chapter
		want float64
	}{
		{"empty", mk(), 1.0},
		{"single", mk("第一章"), 1.0},
		{"perfect run", mk("第一章", "第二章", "第三章"), 1.0},
		{"resets per volume — 1,2,3,1,2,3 — 4/5 pairs", mk("第一章", "第二章", "第三章", "第一章", "第二章", "第三章"), 4.0 / 5.0},
		{"random", mk("第三章", "第十七章", "第五章"), 0.0},
		{"no parseable nums", mk("楔子", "序章"), 1.0}, // vacuous: <2 numbers
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := consecutiveRatio(tc.in)
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseChapters_BracketedDominatesUnitNoise(t *testing.T) {
	// Bracketed-numeral book with a stray `第X节<subtitle>` body line that
	// got peeled out as a pseudo-title earlier (FormatText's title/body
	// 3+3 split fires on body prose with a symmetric Han comma rhythm).
	// Without per-unit scoring, that one line matches the chapter-tier
	// `第X节` pattern and smuggles itself into the TOC. With it,
	// ch-jie's count=1 loses to bracket's count>=3 and gets dropped.
	// Synthetic CJK placeholders only.
	text := "（一）\n　　正文一段。\n\n" +
		"（二）\n　　正文二段。\n\n" +
		"（三）\n　　正文三段。\n\n" +
		"第二节甲乙丙，丁戊己\n　　这其实是上一章里被 FormatText 切出来的伪标题，应该被打分系统识破。\n\n" +
		"（四）\n　　正文四段。\n"
	out := ParseChapters(text, nil)
	for _, c := range out {
		if strings.HasPrefix(c.Title, "第二节") {
			t.Errorf("body-derived pseudo-title leaked into TOC: %+v\nfull list: %+v", c, out)
		}
	}
	// All four (X) markers should survive.
	if len(out) != 4 {
		t.Errorf("want 4 bracketed entries, got %d: %+v", len(out), out)
	}
}

func TestParseChapters_UnitDominanceDropsStrayJie(t *testing.T) {
	// A 第X章 book with a single stray `第X节` body line that survived
	// titleBodySplitPattern. ch-zhang (3 matches, consecutive) scores
	// 3; ch-jie (1 match, vacuous ratio) scores 1. ch-jie's score is
	// below ch-zhang's score × dominanceRatio(0.5), so ch-jie gets
	// dropped — only the three 第X章 entries survive. Synthetic CJK
	// placeholders only.
	text := "第一章　起\n　　正文。\n\n" +
		"第二章　承\n　　正文。\n\n" +
		"第二节甲乙丙，丁戊己\n　　body remainder.\n\n" +
		"第三章　转\n　　正文。\n"
	out := ParseChapters(text, nil)
	if len(out) != 3 {
		t.Fatalf("want 3 (stray `第二节` dropped), got %d: %+v", len(out), out)
	}
	for _, c := range out {
		if strings.HasPrefix(c.Title, "第二节") {
			t.Errorf("stray `第二节` leaked: %+v", c)
		}
	}
}

func TestParseChapters_LonePartTreatedAsBody(t *testing.T) {
	// A standalone `第一部<subtitle>` short paragraph (could be a chapter
	// title; could be body text whose first word happens to fit the
	// shape) with no second part marker should be dropped by
	// rankMinKept[0]=2. The other markers (chapters) survive. Synthetic
	// CJK placeholders only.
	text := "第一部甲乙\n\n" +
		"第一章　起\n　　正文。\n\n" +
		"第二章　承\n　　正文。\n\n" +
		"第三章　转\n　　正文。\n"
	out := ParseChapters(text, nil)
	for _, c := range out {
		if strings.HasPrefix(c.Title, "第一部") {
			t.Errorf("singleton `第一部` leaked: %+v", c)
		}
	}
	if len(out) != 3 {
		t.Errorf("want 3 chapters (part dropped), got %d: %+v", len(out), out)
	}
}

func TestParseChapters_StrictBeatsEnumeratedNoise(t *testing.T) {
	// Mixed shape: structured `第X章` dominates as the real chapter
	// marker; a stray `六、子曰` on its own line in the body must not
	// leak into the TOC. The two-phase scan should count strict
	// matches at the chapter rank (3 from ChapterPattern) and drop
	// non-strict EnumeratedNumeralPattern matches (which would
	// otherwise emit `六、子曰` as a fourth "chapter").
	text := "第一章　甲乙丙丁\n　　正文一段。\n\n" +
		"第二章　子丑寅卯\n　　正文二段。\n\n" +
		"六、子曰\n　　这是一段引用孔子的正文，因为整行匹配的关系，被 EnumeratedNumeralPattern 抓住，但其实是书中引文。\n\n" +
		"第三章　申酉戌亥\n　　正文三段。\n"
	out := ParseChapters(text, nil)
	if len(out) != 3 {
		t.Fatalf("want 3 (enumerated `六、` filtered out), got %d: %+v", len(out), out)
	}
	for _, c := range out {
		if strings.HasPrefix(c.Title, "六、") {
			t.Errorf("permissive `六、` leaked through despite 3 strict chapter matches: %+v", c)
		}
	}
}

func TestParseChapters_EnumeratedAloneWins(t *testing.T) {
	// Pure enumerated book: no strict matches at the chapter rank,
	// so EnumeratedNumeralPattern (non-strict) is the only signal.
	// It must still be emitted — the strict-vs-permissive rule only
	// kicks in when strict matches exist to compete with.
	text := "一、起头\n　　正文。\n\n" +
		"二、中段\n　　正文。\n\n" +
		"三、结尾\n　　正文。\n"
	out := ParseChapters(text, nil)
	if len(out) != 3 {
		t.Fatalf("want 3, got %d: %+v", len(out), out)
	}
	wantTitles := []string{"一、起头", "二、中段", "三、结尾"}
	for i, c := range out {
		if c.Title != wantTitles[i] {
			t.Errorf("idx %d: title=%q, want %q", i, c.Title, wantTitles[i])
		}
	}
}

func TestParseChapters_VolumeFormVariants(t *testing.T) {
	// Both `第X卷` and `卷X` standalone forms should be detected. Each
	// fixture provides TWO volumes — singleton volumes are dropped by
	// the dominance + rank-min filter (covered in
	// TestParseChapters_LoneVolumeTreatedAsFlat), so to exercise the
	// pattern itself we keep the tier above the floor.
	cases := []struct {
		name  string
		text  string
		title string
	}{
		{
			"第X卷 with subtitle",
			"第一卷　甲乙丙丁\n\n第一章　起\n正文\n\n第二卷　戊己庚辛\n\n第二章　承\n正文\n",
			"第一卷　甲乙丙丁",
		},
		{
			"第X卷 bare",
			"第二卷\n\n第一章　起\n正文\n\n第三卷\n\n第二章　承\n正文\n",
			"第二卷",
		},
		{
			"卷X form",
			"卷三　戊己庚辛\n\n第一章　起\n正文\n\n卷四　壬癸\n\n第二章　承\n正文\n",
			"卷三　戊己庚辛",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ParseChapters(tc.text, nil)
			if len(out) < 1 {
				t.Fatalf("no matches: %+v", out)
			}
			if out[0].Level != 0 {
				t.Errorf("first entry should be volume (level=0), got level=%d", out[0].Level)
			}
			if out[0].Title != tc.title {
				t.Errorf("title=%q, want %q", out[0].Title, tc.title)
			}
		})
	}
}

func TestParseChapters_EnumeratedNumerals(t *testing.T) {
	// `<numeral>、<subtitle>` form used by personal essays + short-story
	// collections. Synthetic CJK placeholders only — no corpus content.
	text := "TTT\n\n作者：AAA\n\n" +
		"一、甲乙丙\n　　正文一段。\n\n" +
		"二、丁戊己\n　　正文二段。\n\n" +
		"三、庚辛壬\n　　正文三段。\n"
	out := ParseChapters(text, nil)
	if len(out) != 3 {
		t.Fatalf("want 3 enumerated chapters, got %d: %+v", len(out), out)
	}
	wantTitles := []string{"一、甲乙丙", "二、丁戊己", "三、庚辛壬"}
	for i, c := range out {
		if c.Title != wantTitles[i] {
			t.Errorf("idx %d: title=%q, want %q", i, c.Title, wantTitles[i])
		}
		// Flat-only book: chapter tier is the only tier present, so
		// normaliseLevels ranks it at 0.
		if c.Level != 0 {
			t.Errorf("idx %d: level=%d, want 0 (flat book)", i, c.Level)
		}
	}
}

func TestParseChapters_EnumeratedNotInsideBody(t *testing.T) {
	// `一、` appearing inline in a body sentence (e.g. an inline
	// enumeration "理由有三：一、…二、…") must NOT match — pattern is
	// whole-line anchored.
	text := "一、起头\n　　正文。\n\n" +
		"　　理由有三：一、子曰；二、孟曰；三、荀曰。\n\n" +
		"二、结尾\n　　正文。\n"
	out := ParseChapters(text, nil)
	if len(out) != 2 {
		t.Fatalf("want 2 (inline enumeration ignored), got %d: %+v", len(out), out)
	}
}

func TestParseChapters_VolumeNotInsideBody(t *testing.T) {
	// "...第二卷..." mentioned inside a body paragraph must NOT match
	// VolumePattern — the pattern is whole-line anchored. Without a
	// volume tier the book is flat (chapter-only), so we assert by
	// title that the body-line volumes never became chapter entries.
	text := "第一章　甲乙丙丁\n他翻到第二卷的目录页查看。\n继续阅读第三卷里的内容。\n第二章　子丑寅卯\n正文\n"
	out := ParseChapters(text, nil)
	if len(out) != 2 {
		t.Fatalf("want 2 chapters (no volume false-positives), got %d: %+v", len(out), out)
	}
	for _, c := range out {
		if strings.Contains(c.Title, "卷") {
			t.Errorf("body-line volume leaked into TOC: %+v", c)
		}
	}
}

func TestParseChapters_PartVolumeChapter(t *testing.T) {
	// Three-tier book: 第X部 → 第X卷 → 第X章. PartPattern, VolumePattern
	// and ChapterPattern each pick their own tier; normaliseLevels then
	// renumbers them to 0/1/2 because all three tiers are present.
	text := "第一部　甲乙\n\n" +
		"第一卷　丙丁\n\n" +
		"第一章　戊己\n正文。\n\n" +
		"第二章　庚辛\n正文。\n\n" +
		"第二卷　壬癸\n\n" +
		"第一章　子丑\n正文。\n\n" +
		"第二部　寅卯\n\n" +
		"第一卷　辰巳\n\n" +
		"第一章　午未\n正文。\n"
	out := ParseChapters(text, nil)
	if len(out) != 9 {
		t.Fatalf("want 9 entries (2 parts + 3 vols + 4 chaps), got %d: %+v", len(out), out)
	}
	want := []struct {
		title string
		level int
	}{
		{"第一部　甲乙", 0},
		{"第一卷　丙丁", 1},
		{"第一章　戊己", 2},
		{"第二章　庚辛", 2},
		{"第二卷　壬癸", 1},
		{"第一章　子丑", 2},
		{"第二部　寅卯", 0},
		{"第一卷　辰巳", 1},
		{"第一章　午未", 2},
	}
	for i, w := range want {
		if out[i].Title != w.title || out[i].Level != w.level {
			t.Errorf("entry %d: got (%q, level=%d), want (%q, level=%d)",
				i, out[i].Title, out[i].Level, w.title, w.level)
		}
	}
}

func TestParseChapters_PartChapterNoVolume(t *testing.T) {
	// Two-tier book that skips 卷: parts directly contain chapters.
	// Only tiers 0 (part) and 2 (chapter) are present, so they rank
	// to Level 0 and Level 1 — no empty "depth hole" at level 1.
	text := "第一部　甲乙\n\n" +
		"第一章　丙丁\n正文。\n\n" +
		"第二章　戊己\n正文。\n\n" +
		"第二部　庚辛\n\n" +
		"第一章　壬癸\n正文。\n"
	out := ParseChapters(text, nil)
	wantLevels := []int{0, 1, 1, 0, 1}
	if len(out) != len(wantLevels) {
		t.Fatalf("want %d entries, got %d: %+v", len(wantLevels), len(out), out)
	}
	for i, want := range wantLevels {
		if out[i].Level != want {
			t.Errorf("entry %d (%q): level=%d, want %d", i, out[i].Title, out[i].Level, want)
		}
	}
}

func TestDetectMetadata(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantTitle  string
		wantAuthor string
	}{
		{"inline by colon", "标题甲 by:作者乙\n第一章\n", "标题甲", "作者乙"},
		{"By capitalized + space — title precedes", "标题丙 By: 作者丁\n", "标题丙", "作者丁"},
		{"BY uppercase + full-width colon, no title", "BY：作者甲\n正文...\n", "", "作者甲"},
		{"skips leading blank lines", "\n\n\n书名 by: 张三\n", "书名", "张三"},
		{"no by anywhere", "纯标题\n第一章\n正文\n", "", ""},
		{"by inside word — not matched", "Tobyrules:notanauthor\n", "", ""},
		{"only scans top of file", strings.Repeat("filler\n", 100) + "by: 太晚了\n", "", ""},
		{"title with extra whitespace gets trimmed", "  奇书   by:  无名  \n", "奇书", "无名"},
		{"Chinese 作者 standalone — author only (title from filename later)",
			"书名甲\n\n作者：作者乙\n\n一、起\n正文\n", "", "作者乙"},
		{"Chinese 作者 with full-width colon", "作者：作者丙\n", "", "作者丙"},
		{"Chinese 作者 with ASCII colon + spaces", "作者: 作者丁\n", "", "作者丁"},
		{"Chinese 作者 inline with title prefix",
			"书名甲作者：作者乙\n", "书名甲", "作者乙"},
		{"Chinese 作者 inline + leading centring whitespace",
			"　　　　　　书名丙作者：作者丁\n", "书名丙", "作者丁"},
		{"作者 with trailing prose — author capture rejected by length cap",
			"作者：AAA加上更多文字补足长度甲乙丙丁戊己庚辛\n", "", ""},
		{"作者 with ASCII handle including underscore + digits",
			"作者：abc_xyz123\n", "", "abc_xyz123"},
		{"by: takes precedence over 作者 (same scan)",
			"标题甲 by:作者甲\n\n作者：另一人\n", "标题甲", "作者甲"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DetectMetadata(tc.in)
			if got.Title != tc.wantTitle || got.Author != tc.wantAuthor {
				t.Errorf("DetectMetadata(%q) = %+v, want {Title:%q Author:%q}",
					tc.in, got, tc.wantTitle, tc.wantAuthor)
			}
		})
	}
}

// End-to-end corpus coverage (`ParseChapters` against a real book +
// the full format → ingest pipeline) lives in
// `TestFormatToCache_Corpus` in format_test.go, driven by the
// `ZREADER_TEST_CORPUS` env var. Keeps real corpus identifiers out of
// the repo entirely.
