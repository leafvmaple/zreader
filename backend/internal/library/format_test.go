package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatText_IndentedRejoin(t *testing.T) {
	in := "　　第一段开头\n硬包到第二行\n硬包到第三行\n\n　　第二段开头\n硬包行\n"
	// Output paragraphs are flush-left — the reader handles visual indent.
	want := "第一段开头硬包到第二行硬包到第三行\n\n第二段开头硬包行\n"
	got := FormatText(in)
	if got != want {
		t.Errorf("FormatText:\n got %q\nwant %q", got, want)
	}
}

func TestFormatText_NoIndentConvention_NoOp(t *testing.T) {
	// Without indented paragraph starts we can't distinguish wrap from
	// real breaks — return text unchanged.
	in := "line one\nline two\nline three\n"
	if got := FormatText(in); got != in {
		t.Errorf("expected no-op for unindented file, got %q", got)
	}
}

func TestFormatText_CRLFCollapsedToLF(t *testing.T) {
	in := "　　段落一\r\n硬包\r\n\r\n　　段落二\r\n"
	want := "段落一硬包\n\n段落二\n"
	if got := FormatText(in); got != want {
		t.Errorf("FormatText:\n got %q\nwant %q", got, want)
	}
}

func TestFormatText_ExtraBlanksNormalised(t *testing.T) {
	in := "　　段落一\n\n\n\n　　段落二\n"
	want := "段落一\n\n段落二\n"
	if got := FormatText(in); got != want {
		t.Errorf("FormatText:\n got %q\nwant %q", got, want)
	}
}

func TestFormatText_TitleSplitsFromBody(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "4+4 subtitle glued to body",
			in:   "　　第一折　七禽六兽，十三衣冠锦幄犹温，兽香袅袅，黄花梨木精雕的大床。\n",
			want: "第一折　七禽六兽，十三衣冠\n\n锦幄犹温，兽香袅袅，黄花梨木精雕的大床。\n",
		},
		{
			name: "4+4 + (上) part marker glued to body",
			in:   "　　第九折　升仙大道，紫电冲霄（上）劫兆醒过来。\n",
			want: "第九折　升仙大道，紫电冲霄（上）\n\n劫兆醒过来。\n",
		},
		{
			name: "no space between marker and 4+4 subtitle — space inserted",
			in:   "　　第五折云梦之身，幻影剑式劫兆与岳盈盈行出大院。\n",
			want: "第五折　云梦之身，幻影剑式\n\n劫兆与岳盈盈行出大院。\n",
		},
		{
			name: "multiple spaces between marker and subtitle — collapsed to one",
			in:   "　　第七折　　　道圣智绝，无用相思丹墀之上。\n",
			want: "第七折　道圣智绝，无用相思\n\n丹墀之上。\n",
		},
		{
			name: "marker with part marker directly attached — no space inserted",
			in:   "　　第九折（上）\n",
			want: "第九折（上）\n",
		},
		{
			name: "chapter title with no body glued — unchanged",
			in:   "　　第九折　升仙大道，紫电冲霄（上）\n",
			want: "第九折　升仙大道，紫电冲霄（上）\n",
		},
		{
			// Documents a known limitation: a true 4+3 subtitle gets
			// over-split as 4+4, stealing one char from the body. See
			// TODO.md — fixing this needs semantic context (or per-book
			// override) that the structural matcher doesn't have.
			name: "asymmetric 4+3 subtitle — over-splits as 4+4 (known)",
			in:   "　　第六折连天铁障，将军箓法文、商二姝相偕入观。\n",
			want: "第六折　连天铁障，将军箓法\n\n文、商二姝相偕入观。\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatText(tc.in)
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
		{"照日天劫 - 佚名.txt", "照日天劫", "佚名"},
		{"铸蝉记 - 轩辕悬.TXT", "铸蝉记", "轩辕悬"},
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
			"照日天劫 - 佚名.txt", TxtMetadata{Title: "铸蝉记", Author: "轩辕悬"},
			"铸蝉记", "轩辕悬"},
		{"filename used when by-line empty",
			"照日天劫 - 佚名.txt", TxtMetadata{},
			"照日天劫", "佚名"},
		{"default author when both empty",
			"no-author.txt", TxtMetadata{},
			"no-author", DefaultAuthor},
		{"partial by-line — title only, author from filename",
			"照日天劫 - 佚名.txt", TxtMetadata{Title: "新名字"},
			"新名字", "佚名"},
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
		{"轩辕悬", "轩辕悬"},
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
	src := filepath.Join(tmp, "照日天劫 - 佚名.txt")
	original := "　　第一折　七禽六兽，十三衣冠锦幄犹温，兽香袅袅，大床。\n\n　　第二段。\n"
	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	srcInfo, _ := os.Stat(src)
	srcMtime := srcInfo.ModTime()

	cp, title, author, err := FormatToCache(tmp, src)
	if err != nil {
		t.Fatal(err)
	}
	if title != "照日天劫" || author != "佚名" {
		t.Errorf("meta = (%q, %q), want (照日天劫, 佚名)", title, author)
	}
	wantCachedPath := filepath.Join(tmp, "佚名", "照日天劫.txt")
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
	if !strings.Contains(string(cachedBytes), "第一折　七禽六兽，十三衣冠") {
		t.Errorf("cached content missing expected title; got %q", cachedBytes)
	}
	if !strings.Contains(string(cachedBytes), "锦幄犹温") {
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

// Smoke test against the real 照日天劫 — full FormatToCache flow in a
// temp copy. The cached file should be parseable and yield the full
// post-format chapter set.
// 《十景缎》end-to-end: every `「N」` marker should make it out, including
// the 7 that are glued mid-paragraph in the source (some after `…` /
// `"`, some with no preceding punctuation at all).
func TestFormatToCache_ShiJingDuan(t *testing.T) {
	const src = "../../../books/十景缎 - 佚名.txt"
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("test book not present: %v", err)
	}

	tmp := t.TempDir()
	dst := filepath.Join(tmp, "十景缎 - 佚名.txt")
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cp, title, author, err := FormatToCache(tmp, dst)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if title != "十景缎" || author != "佚名" {
		t.Errorf("meta = (%q, %q), want (十景缎, 佚名)", title, author)
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
	if len(chapters) < 217 {
		t.Errorf("post-format chapters = %d, want >= 217", len(chapters))
	}
	for _, want := range []string{"「九十六」", "「一百零五」", "「一百二十四」", "「一百二十六」", "「一百八十四」", "「一百八十九」", "「一百九十九」"} {
		found := false
		for _, c := range chapters {
			if c.Title == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("inline-glued chapter %q missing after format", want)
		}
	}
}

func TestFormatToCache_ZhaoRiTianJie(t *testing.T) {
	const src = "../../../books/照日天劫 - 佚名.txt"
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("test book not present: %v", err)
	}

	tmp := t.TempDir()
	dst := filepath.Join(tmp, "照日天劫 - 佚名.txt")
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	cp, title, author, err := FormatToCache(tmp, dst)
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if title != "照日天劫" || author != "佚名" {
		t.Errorf("meta = (%q, %q), want (照日天劫, 佚名)", title, author)
	}
	want := filepath.Join(tmp, "佚名", "照日天劫.txt")
	if cp != want {
		t.Errorf("cached path = %q, want %q", cp, want)
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
	if len(chapters) < 19 {
		t.Errorf("post-format chapters = %d, want >= 19", len(chapters))
	}
	for _, want := range []string{"第一折", "第十一折", "第十二折"} {
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
}
