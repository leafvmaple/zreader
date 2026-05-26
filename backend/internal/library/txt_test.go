package library

import (
	"os"
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
	// 「一」/「二」/「三」 — the chapter convention used by 《十景缎》.
	// Bracketed CJK numerals should land in the primary tier without
	// needing the loose fallback.
	text := "十景缎\n\n「一」\n第一段内容。\n\n「二」\n第二段内容。\n\n「三」\n第三段内容。\n"
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
		{"inline by colon", "铸蝉记 by:轩辕悬\n第一章\n", "铸蝉记", "轩辕悬"},
		{"By capitalized + space — title precedes", "三体 By: 大刘\n", "三体", "大刘"},
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

// Smoke test against the actual test book in books/ if present. Skipped
// when the file is absent (e.g. CI, fresh clones — books/ is gitignored).
func TestParseChapters_ZhuChanJi(t *testing.T) {
	const path = "../../../books/铸蝉记 - 佚名.txt"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("test book not present: %v", err)
	}
	_, text, err := DetectAndDecode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := ParseChapters(text, nil)
	t.Logf("got %d chapters", len(out))
	for _, c := range out {
		t.Logf("  [%d] @%d  %q", c.Idx, c.ByteOffset, c.Title)
	}
	if len(out) < 10 {
		t.Fatalf("expected at least 10 chapters (楔子 + 1..10), got %d", len(out))
	}
	meta := DetectMetadata(text)
	if meta.Title != "铸蝉记" || meta.Author != "轩辕悬" {
		t.Fatalf("expected {铸蝉记, 轩辕悬}, got %+v", meta)
	}
}

// Smoke test against 《十景缎》 — bracketed CJK-numeral chapter markers
// (「一」/「二」/...). Skipped when the file is absent.
func TestParseChapters_ShiJingDuan(t *testing.T) {
	const path = "../../../books/十景缎 - 佚名.txt"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("test book not present: %v", err)
	}
	_, text, err := DetectAndDecode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := ParseChapters(text, nil)
	t.Logf("got %d chapters", len(out))
	for _, c := range out {
		t.Logf("  [%d] @%d  %q", c.Idx, c.ByteOffset, c.Title)
	}
	// Raw-parse only catches markers that already stand on their own
	// line (~210 of 217 in this book). The remaining ~7 are glued to
	// the prior paragraph and only get split out by FormatText —
	// asserted end-to-end in TestFormatToCache_ShiJingDuan below.
	if len(out) < 200 {
		t.Fatalf("expected at least 200 bracketed chapters, got %d", len(out))
	}
	if out[0].Title != "「一」" {
		t.Fatalf("expected first chapter title to be 「一」, got %q", out[0].Title)
	}
}

// 照日天劫's post-format chapter coverage is asserted in
// TestFormatBook_ZhaoRiTianJie — that's the path the scanner actually
// takes. ParseChapters is a pure function operating on already-formatted
// input; testing it directly on raw 照日天劫 would just re-test the
// missing-format case.
