package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatText_IndentedRejoin(t *testing.T) {
	in := "　　第一段开头\n硬包到第二行\n硬包到第三行\n\n　　第二段开头\n硬包行\n"
	// Output paragraphs are flush-left — the reader handles visual indent.
	want := "第一段开头硬包到第二行硬包到第三行\n\n第二段开头硬包行\n"
	got := FormatText(in, "", "")
	if got != want {
		t.Errorf("FormatText:\n got %q\nwant %q", got, want)
	}
}

func TestFormatText_VolumeFormNormalised(t *testing.T) {
	// `卷X[subtitle]` rewritten to `第X卷　[subtitle]`. The trailing
	// whitespace between marker and Han subtitle collapses to exactly
	// one full-width `　` (chapterTitleSpacingPattern, same convention
	// as 章/折).
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "卷X glued to Han subtitle",
			in:   "　　卷四十甲乙丙丁\n\n　　正文段落一。\n",
			want: "第四十卷　甲乙丙丁\n\n正文段落一。\n",
		},
		{
			name: "卷X with existing space",
			in:   "　　卷三　戊己庚辛\n\n　　正文。\n",
			want: "第三卷　戊己庚辛\n\n正文。\n",
		},
		{
			name: "bare 卷X no subtitle",
			in:   "　　卷二\n\n　　正文。\n",
			want: "第二卷\n\n正文。\n",
		},
		{
			name: "卷 inside body sentence — not touched",
			in:   "　　他翻到下一卷的目录页查看。\n",
			want: "他翻到下一卷的目录页查看。\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatText(tc.in, "", "")
			if got != tc.want {
				t.Errorf("FormatText:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestFormatText_StripsHTMLTags(t *testing.T) {
	// Inline rich-text markup that some scraped TXTs embed (<center>,
	// <img src=…>, <br>, …) gets stripped. Paragraphs that become empty
	// after the strip are dropped entirely.
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "standalone HTML-only paragraph dropped",
			in:   "　　正文一。\n\n　　<center><img src=../img/x.jpg></center>\n\n　　正文二。\n",
			want: "正文一。\n\n正文二。\n",
		},
		{
			name: "inline HTML in body kept-prose",
			in:   "　　他望着画卷<img src=../img/y.jpg>沉默良久。\n",
			want: "他望着画卷沉默良久。\n",
		},
		{
			name: "broken HTML with no inner space",
			in:   "　　<center><imgsrc=../txt/40.jpg></center>\n\n　　继续。\n",
			want: "继续。\n",
		},
		{
			name: "leaves text containing < without > untouched",
			in:   "　　数学符号 a < b 在书里出现过。\n",
			want: "数学符号 a < b 在书里出现过。\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatText(tc.in, "", "")
			if got != tc.want {
				t.Errorf("FormatText:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestFormatText_OneLinePerParagraph_NoBlanks(t *testing.T) {
	// Some files use the "every line is one paragraph, all indented,
	// no blank-line gaps" convention — a bare title on line 1 followed
	// by `　　…` paragraphs. The format pipeline must still run (drop
	// the title metadata line, normalise chapter title spacing, etc.)
	// instead of returning the file unchanged.
	in := "TTT\n" +
		"　　第一折甲乙丙丁，戊己庚辛\n" +
		"　　子丑寅卯辰巳午未申酉\n" +
		"　　东南西北春夏秋冬。\n"
	want := "第一折　甲乙丙丁，戊己庚辛\n\n" +
		"子丑寅卯辰巳午未申酉\n\n" +
		"东南西北春夏秋冬。\n"
	got := FormatText(in, "TTT", "AAA")
	if got != want {
		t.Errorf("FormatText:\n got %q\nwant %q", got, want)
	}
}

func TestFormatText_NoIndentButSentenceTerminators(t *testing.T) {
	// One-line-per-paragraph layout: no indent, no blank-line gaps,
	// but every line ends with a sentence terminator. Format pipeline
	// should still kick in so per-paragraph transforms (HTML strip,
	// metadata drop, chapter spacing) get to run, and emit each line
	// as its own paragraph separated by `\n\n`.
	in := "TTT作者：AAA\n" +
		"楔　子\n" +
		"甲乙丙丁戊己庚辛壬癸。\n" +
		"子丑寅卯辰巳午未申酉戌亥。\n" +
		"东南西北春夏秋冬。\n"
	want := "楔　子\n\n" +
		"甲乙丙丁戊己庚辛壬癸。\n\n" +
		"子丑寅卯辰巳午未申酉戌亥。\n\n" +
		"东南西北春夏秋冬。\n"
	got := FormatText(in, "TTT", "AAA")
	if got != want {
		t.Errorf("FormatText:\n got %q\nwant %q", got, want)
	}
}

func TestFormatText_NoIndentConvention_NoOp(t *testing.T) {
	// Without indented paragraph starts we can't distinguish wrap from
	// real breaks — return text unchanged.
	in := "line one\nline two\nline three\n"
	if got := FormatText(in, "", ""); got != in {
		t.Errorf("expected no-op for unindented file, got %q", got)
	}
}

func TestFormatText_CRLFCollapsedToLF(t *testing.T) {
	in := "　　段落一\r\n硬包\r\n\r\n　　段落二\r\n"
	want := "段落一硬包\n\n段落二\n"
	if got := FormatText(in, "", ""); got != want {
		t.Errorf("FormatText:\n got %q\nwant %q", got, want)
	}
}

func TestFormatText_ExtraBlanksNormalised(t *testing.T) {
	in := "　　段落一\n\n\n\n　　段落二\n"
	want := "段落一\n\n段落二\n"
	if got := FormatText(in, "", ""); got != want {
		t.Errorf("FormatText:\n got %q\nwant %q", got, want)
	}
}

func TestFormatText_DropsMetadataHeader(t *testing.T) {
	// Synthetic title/author tokens — keeps the fixtures self-contained
	// without leaking real corpus identifiers into the test corpus.
	const title, author = "TTT", "AAA"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "title alone dropped",
			in:   "　　TTT\n\n　　楔子\n\n　　正文第一段。\n",
			want: "楔子\n\n正文第一段。\n",
		},
		{
			name: "author alone dropped",
			in:   "　　AAA\n\n　　楔子\n",
			want: "楔子\n",
		},
		{
			name: "title + space + author dropped",
			in:   "　　TTT AAA\n\n　　楔子\n",
			want: "楔子\n",
		},
		{
			name: "title + by:author dropped",
			in:   "　　TTT by:AAA\n\n　　楔子\n",
			want: "楔子\n",
		},
		{
			name: "bare by:author dropped",
			in:   "　　by:AAA\n\n　　第一章\n",
			want: "第一章\n",
		},
		{
			name: "full-width colon variant dropped",
			in:   "　　TTT by：AAA\n\n　　楔子\n",
			want: "楔子\n",
		},
		{
			name: "title bare-colon author dropped",
			in:   "　　TTT：AAA\n\n　　楔子\n",
			want: "楔子\n",
		},
		{
			name: "title 作者：author dropped",
			in:   "　　TTT 作者：AAA\n\n　　楔子\n",
			want: "楔子\n",
		},
		{
			name: "Author: english label dropped",
			in:   "　　TTT Author:AAA\n\n　　楔子\n",
			want: "楔子\n",
		},
		{
			name: "title prefix with extra body preserved",
			in:   "　　TTT这是一段正文。\n",
			want: "TTT这是一段正文。\n",
		},
		{
			name: "wrong author after by: marker preserved",
			in:   "　　by:别人\n",
			want: "by:别人\n",
		},
		{
			name: "by: substring inside body sentence preserved",
			in:   "　　她在路上听见有人喊 by: 是英文片段，但绝不应该被当作元数据。\n",
			want: "她在路上听见有人喊 by: 是英文片段，但绝不应该被当作元数据。\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatText(tc.in, title, author)
			if got != tc.want {
				t.Errorf("FormatText:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestFormatText_EnumeratedTitleSplitsFromBody(t *testing.T) {
	// `<numeral>、<symmetric-subtitle>` glued to body — same shape as
	// the existing 第X章 splitter, just with `、` instead of the章/折
	// marker character. The asymmetric 3+5 / 4+3 limitation
	// documented for the structured form applies equally here.
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "<numeral>、 + 4+4 subtitle glued to body",
			in:   "　　三十一、甲乙丙丁，戊己庚辛主角醒了过来。\n",
			want: "三十一、甲乙丙丁，戊己庚辛\n\n主角醒了过来。\n",
		},
		{
			name: "<numeral>、 + 3+3 subtitle glued to body",
			in:   "　　二、甲乙丙，戊己庚某天下午发生的事。\n",
			want: "二、甲乙丙，戊己庚\n\n某天下午发生的事。\n",
		},
		{
			name: "<numeral>、 + 5+5 subtitle glued to body",
			in:   "　　12、子丑寅卯辰，巳午未申酉接下来的故事。\n",
			want: "12、子丑寅卯辰，巳午未申酉\n\n接下来的故事。\n",
		},
		{
			// Mirrors the 4+3 over-split documented for the structured
			// form: 3+5 falls back to a greedy 3+3 split, stealing two
			// chars from the body. Chapter detection still succeeds
			// (the truncated title matches EnumeratedNumeralPattern),
			// just with a slightly corrupted title — worth accepting
			// to avoid losing the chapter entry entirely.
			name: "<numeral>、 + asymmetric 3+5 — over-splits as 3+3 (known)",
			in:   "　　二十、甲乙丙，丁戊己庚辛后续的正文文本。\n",
			want: "二十、甲乙丙，丁戊己\n\n庚辛后续的正文文本。\n",
		},
		{
			name: "standalone <numeral>、subtitle — unchanged",
			in:   "　　一、甲乙丙\n",
			want: "一、甲乙丙\n",
		},
		{
			// User edits the source to put a 3+5-shaped title on its
			// own line. Without the body-tail threshold the symmetric
			// 3+3 arm would still greedily match the prefix and orphan
			// the last 2 runes as a separate paragraph — re-introducing
			// the over-split the manual fix was meant to undo.
			name: "standalone <numeral>、 + 3+5 subtitle — short tail preserves whole title",
			in:   "　　二十、甲乙丙，丁戊己庚辛\n",
			want: "二十、甲乙丙，丁戊己庚辛\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatText(tc.in, "", "")
			if got != tc.want {
				t.Errorf("FormatText:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestFormatText_TitleSplitsFromBody(t *testing.T) {
	// Subtitles use synthetic 4-char chunks drawn from 天干 / 地支 /
	// 四季 — keeps the 4+4 structural shape the splitter checks for
	// without embedding any real corpus chapter titles.
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "4+4 subtitle glued to body",
			in:   "　　第一章　甲乙丙丁，戊己庚辛这是一段被错误拼接到标题后的正文内容。\n",
			want: "第一章　甲乙丙丁，戊己庚辛\n\n这是一段被错误拼接到标题后的正文内容。\n",
		},
		{
			name: "4+4 + (上) part marker glued to body",
			in:   "　　第九章　子丑寅卯，辰巳午未（上）主角醒了过来。\n",
			want: "第九章　子丑寅卯，辰巳午未（上）\n\n主角醒了过来。\n",
		},
		{
			name: "no space between marker and 4+4 subtitle — space inserted",
			in:   "　　第五章东南西北，春夏秋冬主角走出院落。\n",
			want: "第五章　东南西北，春夏秋冬\n\n主角走出院落。\n",
		},
		{
			name: "multiple spaces between marker and subtitle — collapsed to one",
			in:   "　　第七章　　　风花雪月，松竹梅兰高台之上。\n",
			want: "第七章　风花雪月，松竹梅兰\n\n高台之上。\n",
		},
		{
			name: "marker with part marker directly attached — no space inserted",
			in:   "　　第九章（上）\n",
			want: "第九章（上）\n",
		},
		{
			name: "chapter title with no body glued — unchanged",
			in:   "　　第九章　子丑寅卯，辰巳午未（上）\n",
			want: "第九章　子丑寅卯，辰巳午未（上）\n",
		},
		{
			// Documents a known limitation: a true 4+3 subtitle gets
			// over-split as 4+4, stealing one char from the body. See
			// TODO.md — fixing this needs semantic context (or per-book
			// override) that the structural matcher doesn't have.
			//
			// Synthetic 4+3 input:    `甲乙丙丁，戊己庚` followed by
			//                         body that starts with `辛`.
			// Greedy 4+4 takes:       `甲乙丙丁，戊己庚辛` (steals `辛`).
			name: "asymmetric 4+3 subtitle — over-splits as 4+4 (known)",
			in:   "　　第六章甲乙丙丁，戊己庚辛壬癸子丑寅卯辰。\n",
			want: "第六章　甲乙丙丁，戊己庚辛\n\n壬癸子丑寅卯辰。\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatText(tc.in, "", "")
			if got != tc.want {
				t.Errorf("FormatText:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestParseFilenameMeta(t *testing.T) {
	cases := []struct {
		in, wantTitle, wantAuthor string
	}{
		{"BookA - AuthorX.txt", "BookA", "AuthorX"},
		{"BookB - AuthorY.TXT", "BookB", "AuthorY"},
		{"汉字标题 - 汉字作者.txt", "汉字标题", "汉字作者"},
		{"no-separator.txt", "no-separator", ""},
		{"  spaced - author  .txt", "spaced", "author"},
		// Title contains " - " — the LAST separator splits.
		{"Foo - Bar - Author.txt", "Foo - Bar", "Author"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			title, author := parseFilenameMeta(tc.in)
			if title != tc.wantTitle || author != tc.wantAuthor {
				t.Errorf("parseFilenameMeta(%q) = (%q, %q), want (%q, %q)",
					tc.in, title, author, tc.wantTitle, tc.wantAuthor)
			}
		})
	}
}

func TestResolveMetadata(t *testing.T) {
	cases := []struct {
		name       string
		filename   string
		fromText   TxtMetadata
		wantTitle  string
		wantAuthor string
	}{
		{"by-line wins over filename",
			"FileTitle - FileAuthor.txt", TxtMetadata{Title: "ByTitle", Author: "ByAuthor"},
			"ByTitle", "ByAuthor"},
		{"filename used when by-line empty",
			"FileTitle - FileAuthor.txt", TxtMetadata{},
			"FileTitle", "FileAuthor"},
		{"default author when both empty",
			"no-author.txt", TxtMetadata{},
			"no-author", DefaultAuthor},
		{"partial by-line — title only, author from filename",
			"FileTitle - FileAuthor.txt", TxtMetadata{Title: "NewTitle"},
			"NewTitle", "FileAuthor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			title, author := ResolveMetadata(tc.filename, tc.fromText)
			if title != tc.wantTitle || author != tc.wantAuthor {
				t.Errorf("got (%q, %q), want (%q, %q)",
					title, author, tc.wantTitle, tc.wantAuthor)
			}
		})
	}
}

func TestSanitizePathComponent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"normal", "normal"},
		{"汉字测试", "汉字测试"},
		{"with/slash", "with_slash"},
		{"a:b*c?d", "a_b_c_d"},
		{"  trimmed  ", "trimmed"},
		{"", "_"},
		{"///", "___"},
	}
	for _, tc := range cases {
		got := sanitizePathComponent(tc.in)
		if got != tc.want {
			t.Errorf("sanitizePathComponent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatToCache_LeavesSourceUntouched(t *testing.T) {
	tmp := t.TempDir()
	const wantTitle, wantAuthor = "BookSample", "AuthorSample"
	src := filepath.Join(tmp, wantTitle+" - "+wantAuthor+".txt")
	original := "　　第一章　甲乙丙丁，戊己庚辛这是一段被错误拼接到标题后的正文内容。\n\n　　第二段正文示例。\n"
	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	srcInfo, _ := os.Stat(src)
	srcMtime := srcInfo.ModTime()

	cp, title, author, err := FormatToCache(tmp, src)
	if err != nil {
		t.Fatal(err)
	}
	if title != wantTitle || author != wantAuthor {
		t.Errorf("meta = (%q, %q), want (%q, %q)", title, author, wantTitle, wantAuthor)
	}
	wantCachedPath := filepath.Join(tmp, wantAuthor, wantTitle+".txt")
	if cp != wantCachedPath {
		t.Errorf("cached path = %q, want %q", cp, wantCachedPath)
	}

	// Source bytes + mtime unchanged.
	srcAfter, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(srcAfter) != original {
		t.Error("source content changed — should be untouched")
	}
	if info, _ := os.Stat(src); info.ModTime() != srcMtime {
		t.Error("source mtime changed — should be untouched")
	}

	// Cached file exists and is correctly formatted.
	cachedBytes, err := os.ReadFile(cp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cachedBytes), "第一章　甲乙丙丁，戊己庚辛") {
		t.Errorf("cached content missing expected title; got %q", cachedBytes)
	}
	if !strings.Contains(string(cachedBytes), "这是一段") {
		t.Errorf("cached content missing body; got %q", cachedBytes)
	}
}

func TestFormatToCache_OverwritesOnRerun(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "book - author.txt")
	if err := os.WriteFile(src, []byte("　　hello\n硬包\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp, _, _, err := FormatToCache(tmp, src)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper with cached output; FormatToCache should overwrite on the
	// next run (this is the "easy to re-format after bug fix" guarantee).
	if err := os.WriteFile(cp, []byte("STALE — should be replaced"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := FormatToCache(tmp, src); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(cp)
	if strings.Contains(string(after), "STALE") {
		t.Errorf("cached file was not overwritten on re-format; got %q", after)
	}
}

func TestRestoreBackups(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "book.txt")
	if err := os.WriteFile(src, []byte("mutated content"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src+BackupSuffix, []byte("pristine original"), 0o644); err != nil {
		t.Fatal(err)
	}

	n, err := RestoreBackups(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("restored = %d, want 1", n)
	}
	got, _ := os.ReadFile(src)
	if string(got) != "pristine original" {
		t.Errorf(".txt = %q, want pristine restored content", got)
	}
	if _, err := os.Stat(src + BackupSuffix); !os.IsNotExist(err) {
		t.Errorf(".bak should have been removed, err=%v", err)
	}

	// Second run with no .bak files — must be a no-op.
	n2, err := RestoreBackups(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if n2 != 0 {
		t.Errorf("second run restored = %d, want 0", n2)
	}
}

// corpusEntry is one source file the corpus test exercises, plus the
// regression assertions to check against the parsed chapter set. The
// JSON config lives under whatever path `ZREADER_TEST_CORPUS` points at
// (gitignored) — so book titles, chapter titles, and per-corpus counts
// never end up in the repo.
type corpusEntry struct {
	// Path is the source TXT location. Relative paths resolve against
	// the directory containing the config file; absolute paths are
	// used verbatim.
	Path string `json:"path"`
	// MinChapters is the lower bound for ParseChapters' output. 0 = no
	// floor check.
	MinChapters int `json:"min_chapters,omitempty"`
	// Contains is a list of chapter titles that must appear verbatim in
	// the parsed set (full-string match).
	Contains []string `json:"contains,omitempty"`
	// Prefixes is a list of chapter-title prefixes that must each match
	// at least one parsed chapter (HasPrefix match).
	Prefixes []string `json:"prefixes,omitempty"`
}

// TestFormatToCache_Corpus drives the full FormatToCache + ParseChapters
// pipeline against a user-supplied corpus. Set
// `ZREADER_TEST_CORPUS=<path>/corpus.json`; the file lists books and
// per-book assertions (see testdata/corpus.example.json for the
// schema). Skips entirely when the env var is unset, and skips per
// entry when its source path can't be read — keeps the test runnable
// on fresh clones and in CI without privately-held corpus data.
func TestFormatToCache_Corpus(t *testing.T) {
	cfgPath := os.Getenv("ZREADER_TEST_CORPUS")
	if cfgPath == "" {
		t.Skip("set ZREADER_TEST_CORPUS=<path to corpus.json> to run end-to-end corpus assertions")
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read corpus config %s: %v", cfgPath, err)
	}
	var entries []corpusEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("parse %s: %v", cfgPath, err)
	}
	baseDir := filepath.Dir(cfgPath)

	for _, e := range entries {
		src := e.Path
		if !filepath.IsAbs(src) {
			src = filepath.Join(baseDir, src)
		}
		t.Run(filepath.Base(src), func(t *testing.T) {
			rawSrc, err := os.ReadFile(src)
			if err != nil {
				t.Skipf("source missing: %v", err)
			}
			tmp := t.TempDir()
			dst := filepath.Join(tmp, filepath.Base(src))
			if err := os.WriteFile(dst, rawSrc, 0o644); err != nil {
				t.Fatal(err)
			}

			cp, _, _, err := FormatToCache(tmp, dst)
			if err != nil {
				t.Fatalf("format: %v", err)
			}
			formatted, err := os.ReadFile(cp)
			if err != nil {
				t.Fatal(err)
			}
			_, text, err := DetectAndDecode(formatted)
			if err != nil {
				t.Fatalf("decode formatted: %v", err)
			}
			chapters := ParseChapters(text, nil)

			if e.MinChapters > 0 && len(chapters) < e.MinChapters {
				t.Errorf("post-format chapters = %d, want >= %d", len(chapters), e.MinChapters)
			}
			for _, want := range e.Contains {
				found := false
				for _, c := range chapters {
					if c.Title == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected exact chapter title %q missing after format", want)
				}
			}
			for _, want := range e.Prefixes {
				found := false
				for _, c := range chapters {
					if strings.HasPrefix(c.Title, want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("no chapter starting with %q after format", want)
				}
			}
		})
	}
}
