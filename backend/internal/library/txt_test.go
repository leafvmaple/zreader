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
	// introduced volumes yet; the first explicit 卷 marker appears
	// only part way through the book.
	// Parser must emit chapters in document order — the missing 第一卷
	// is a frontend rendering concern, not a parser concern. We assert
	// no synthetic level=0 leak from the parser.
	text := "第一章　甲乙丙丁\n正文。\n\n" +
		"第二章　子丑寅卯\n正文。\n\n" +
		"第二卷　戊己庚辛\n\n" +
		"第三章　申酉戌亥\n正文。\n"
	out := ParseChapters(text, nil)
	if len(out) != 4 {
		t.Fatalf("want 4, got %d: %+v", len(out), out)
	}
	if out[0].Level != 1 || out[1].Level != 1 {
		t.Errorf("pre-volume chapters should be level=1; got %+v, %+v", out[0], out[1])
	}
	if out[2].Level != 0 {
		t.Errorf("volume marker should be level=0; got %+v", out[2])
	}
	if out[3].Level != 1 {
		t.Errorf("post-volume chapter should be level=1; got %+v", out[3])
	}
}

func TestParseChapters_VolumeFormVariants(t *testing.T) {
	// Both `第X卷` and `卷X` standalone forms should be detected.
	cases := []struct {
		name  string
		text  string
		title string
	}{
		{"第X卷 with subtitle", "第一卷　甲乙丙丁\n\n第一章　起\n正文\n", "第一卷　甲乙丙丁"},
		{"第X卷 bare", "第二卷\n\n第一章　起\n正文\n", "第二卷"},
		{"卷X form", "卷三　戊己庚辛\n\n第一章　起\n正文\n", "卷三　戊己庚辛"},
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
		if c.Level != 1 {
			t.Errorf("idx %d: level=%d, want 1", i, c.Level)
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
	// VolumePattern — the pattern is whole-line anchored.
	text := "第一章　甲乙丙丁\n他翻到第二卷的目录页查看。\n继续阅读第三卷里的内容。\n第二章　子丑寅卯\n正文\n"
	out := ParseChapters(text, nil)
	for _, c := range out {
		if c.Level == 0 {
			t.Errorf("volume false-positive inside body: %+v", c)
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
