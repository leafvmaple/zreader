package library

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeEpubToTemp builds the EPUB into a temp file and returns its path.
// ReadEpub only takes a path (it uses zip.OpenReader), so tests that
// want to exercise the full round-trip stage their bytes on disk first.
func writeEpubToTemp(t *testing.T, title, author, text string, chapters []Chapter) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "book.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer f.Close()
	if _, err := BuildEpub(f, title, author, text, chapters); err != nil {
		t.Fatalf("BuildEpub: %v", err)
	}
	return p
}

func TestReadEpub_RoundTripFlat(t *testing.T) {
	// A flat book: two chapters at Level=1, no volumes. ReadEpub should
	// preserve titles, order, and flat text; offsets should index into
	// FlatText so slicing reproduces each chapter's source.
	text := "第一章　起\n\n正文段一。\n\n正文段二。\n\n第二章　承\n\n正文段三。\n"
	chapters := []Chapter{
		{Idx: 1, Title: "第一章　起", Level: 1, ByteOffset: 0, CharOffset: 0},
		{Idx: 2, Title: "第二章　承", Level: 1, ByteOffset: strings.Index(text, "第二章")},
	}

	p := writeEpubToTemp(t, "示例书", "佚名", text, chapters)

	book, err := ReadEpub(p)
	if err != nil {
		t.Fatalf("ReadEpub: %v", err)
	}
	if book.Title != "示例书" {
		t.Errorf("title = %q, want %q", book.Title, "示例书")
	}
	if book.Author != "佚名" {
		t.Errorf("author = %q, want %q", book.Author, "佚名")
	}
	if len(book.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2; got=%+v", len(book.Chapters), book.Chapters)
	}
	if book.Chapters[0].Title != "第一章　起" || book.Chapters[1].Title != "第二章　承" {
		t.Errorf("chapter titles wrong: %+v", book.Chapters)
	}
	for i, c := range book.Chapters {
		if c.Level != 1 {
			t.Errorf("chapter %d level = %d, want 1 (flat book)", i, c.Level)
		}
	}

	// FlatText should contain both chapter titles and bodies in order.
	for _, want := range []string{"第一章　起", "正文段一。", "正文段二。", "第二章　承", "正文段三。"} {
		if !strings.Contains(book.FlatText, want) {
			t.Errorf("FlatText missing %q\nfull text:\n%s", want, book.FlatText)
		}
	}

	// Slicing FlatText at chapter[1].CharOffset should land at the
	// second chapter's title. Use runes (CharOffset is rune-indexed).
	runes := []rune(book.FlatText)
	if int(book.Chapters[1].CharOffset) > len(runes) {
		t.Fatalf("char offset %d > rune count %d", book.Chapters[1].CharOffset, len(runes))
	}
	at := string(runes[book.Chapters[1].CharOffset:])
	if !strings.HasPrefix(at, "第二章") {
		t.Errorf("chapters[1] CharOffset doesn't land on title; slice starts with %q", trimPreview(at, 40))
	}
}

func TestReadEpub_RoundTripWithVolumes(t *testing.T) {
	// One volume containing two chapters. ReadEpub must classify the
	// volume as Level=0 (has children in nav) and chapters as Level=1.
	text := "第一卷　序卷\n\n卷首一段。\n\n第一章　起\n\n正文段一。\n\n第二章　承\n\n正文段二。\n"
	chapters := []Chapter{
		{Idx: 1, Title: "第一卷　序卷", Level: 0, ByteOffset: 0},
		{Idx: 2, Title: "第一章　起", Level: 1, ByteOffset: strings.Index(text, "第一章")},
		{Idx: 3, Title: "第二章　承", Level: 1, ByteOffset: strings.Index(text, "第二章")},
	}

	p := writeEpubToTemp(t, "示例书", "佚名", text, chapters)
	book, err := ReadEpub(p)
	if err != nil {
		t.Fatalf("ReadEpub: %v", err)
	}
	if len(book.Chapters) != 3 {
		t.Fatalf("chapters = %d, want 3", len(book.Chapters))
	}
	want := []struct {
		title string
		level int
	}{
		{"第一卷　序卷", 0}, // container in nav → Level=0
		{"第一章　起", 1},   // leaf in nav → Level=1
		{"第二章　承", 1},
	}
	for i, w := range want {
		if book.Chapters[i].Title != w.title || book.Chapters[i].Level != w.level {
			t.Errorf("chapter[%d] = (%q, level=%d), want (%q, level=%d)",
				i, book.Chapters[i].Title, book.Chapters[i].Level, w.title, w.level)
		}
	}
}

func TestReadEpub_OffsetsAreConsistent(t *testing.T) {
	// Stricter check: for every chapter, slicing FlatText by its
	// CharOffset must land on the chapter's title (or empty if it's
	// the final chapter at the very end). Catches off-by-one /
	// rune-vs-byte mix-ups.
	text := "第一章　甲\n\n甲段。\n\n第二章　乙\n\n乙段一。\n\n乙段二。\n\n第三章　丙\n\n丙段。\n"
	chapters := []Chapter{
		{Idx: 1, Title: "第一章　甲", Level: 1, ByteOffset: 0},
		{Idx: 2, Title: "第二章　乙", Level: 1, ByteOffset: strings.Index(text, "第二章")},
		{Idx: 3, Title: "第三章　丙", Level: 1, ByteOffset: strings.Index(text, "第三章")},
	}
	p := writeEpubToTemp(t, "示例书", "佚名", text, chapters)
	book, err := ReadEpub(p)
	if err != nil {
		t.Fatalf("ReadEpub: %v", err)
	}
	runes := []rune(book.FlatText)
	for i, c := range book.Chapters {
		if int(c.CharOffset) > len(runes) {
			t.Errorf("chapter %d CharOffset %d out of range (rune count %d)", i, c.CharOffset, len(runes))
			continue
		}
		slice := string(runes[c.CharOffset:])
		if !strings.HasPrefix(slice, c.Title) {
			t.Errorf("chapter %d: slice at CharOffset=%d doesn't start with %q; got %q",
				i, c.CharOffset, c.Title, trimPreview(slice, 30))
		}
	}
}

func TestCollapseSpaces(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "abc"},
		{"  abc  ", " abc "},
		{"a\n  b", "a b"},
		{"中文\t空格", "中文 空格"},
	}
	for _, tc := range cases {
		got := collapseSpaces(tc.in)
		if got != tc.want {
			t.Errorf("collapseSpaces(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// trimPreview returns the first n runes of s for nicer error messages.
func trimPreview(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// Ensure FlatText round-trip is byte-stable (modulo possible whitespace
// collapse on chapter bodies — but our writer never introduces inline
// whitespace, so the output should match the input formatted text).
func TestReadEpub_FlatTextMatchesInput(t *testing.T) {
	text := "第一章　起\n\n正文段一。\n\n正文段二。\n\n第二章　承\n\n正文段三。\n"
	chapters := []Chapter{
		{Idx: 1, Title: "第一章　起", Level: 1, ByteOffset: 0},
		{Idx: 2, Title: "第二章　承", Level: 1, ByteOffset: strings.Index(text, "第二章")},
	}
	p := writeEpubToTemp(t, "示例书", "佚名", text, chapters)
	book, err := ReadEpub(p)
	if err != nil {
		t.Fatalf("ReadEpub: %v", err)
	}
	// The input ends with a trailing newline that the EPUB extractor
	// doesn't reproduce (since we strip per-paragraph). Trim both
	// sides' trailing whitespace before comparing.
	gotTrim := strings.TrimRight(book.FlatText, "\n")
	wantTrim := strings.TrimRight(text, "\n")
	if !bytes.Equal([]byte(gotTrim), []byte(wantTrim)) {
		t.Errorf("FlatText mismatch:\n got %q\nwant %q", gotTrim, wantTrim)
	}
}
