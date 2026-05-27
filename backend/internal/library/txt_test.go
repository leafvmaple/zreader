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

func TestParseChapters_LooseAloneNotEnough(t *testing.T) {
	// Single bare digit on its own — below loose threshold, should fall through to synthetic.
	text := "some prose\n\n7\n\nmore prose\n"
	out := ParseChapters(text, nil)
	if len(out) != 1 || out[0].Title != "正文" {
		t.Fatalf("want synthetic, got %+v", out)
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
