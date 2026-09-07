package export

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// buildFixture assembles a synthetic book in the canonical flat-text shape
// (paragraphs separated by blank lines, chapter title as its own paragraph)
// and returns the text plus the chapter list indexing into it.
func buildFixture(t *testing.T, chapters ...[]string) (string, []Chapter) {
	t.Helper()
	var b strings.Builder
	var out []Chapter
	for i, paras := range chapters {
		out = append(out, Chapter{
			Idx:        i + 1,
			Title:      paras[0],
			CharOffset: len([]rune(b.String())),
		})
		for _, p := range paras {
			b.WriteString(p)
			b.WriteString("\n\n")
		}
	}
	return b.String(), out
}

func allRules() Options {
	return Options{
		Rules:      Rules{Promo: true, EdgeLines: true, Normalise: true, AuthorNotes: true},
		ChunkChars: 500,
	}
}

func joinText(chunks []Chunk) string {
	var parts []string
	for _, c := range chunks {
		parts = append(parts, c.Text)
	}
	return strings.Join(parts, "\n\n")
}

func TestBuild_DropsPromoLines(t *testing.T) {
	flat, chapters := buildFixture(t,
		[]string{
			"第一章　子丑寅卯",
			"甲乙丙丁，戊己庚辛。东南西北，春夏秋冬。",
			"更多精彩小说尽在 www.example-invalid.test，请记住本站域名！",
			"本书由甲乙丙整理制作",
			"壬癸子丑，寅卯辰巳。午未申酉，戌亥天干。",
		},
	)
	chunks, stats := Build(Meta{Title: "示例书"}, chapters, flat, allRules(), nil)
	got := joinText(chunks)

	for _, gone := range []string{"example-invalid.test", "记住本站", "整理制作"} {
		if strings.Contains(got, gone) {
			t.Errorf("promo text %q survived the export", gone)
		}
	}
	for _, kept := range []string{"甲乙丙丁", "壬癸子丑"} {
		if !strings.Contains(got, kept) {
			t.Errorf("body text %q was removed", kept)
		}
	}
	if stats.DroppedParas < 2 {
		t.Errorf("DroppedParas = %d, want at least the two promo lines", stats.DroppedParas)
	}
}

// A long paragraph that merely contains a URL is prose with an injection,
// not an ad — it should lose the URL and keep the sentence.
func TestBuild_PromoInLongParagraphKeepsProse(t *testing.T) {
	long := "甲乙丙丁戊己庚辛壬癸，子丑寅卯辰巳午未申酉戌亥。东南西北，春夏秋冬，风花雪月，山川湖海。" +
		"访问 www.example-invalid.test 继续阅读。" +
		"甲乙丙丁戊己庚辛壬癸，子丑寅卯辰巳午未申酉戌亥。东南西北，春夏秋冬，风花雪月，山川湖海。"
	flat, chapters := buildFixture(t, []string{"第一章　子丑寅卯", long})

	chunks, _ := Build(Meta{Title: "示例书"}, chapters, flat, allRules(), nil)
	got := joinText(chunks)

	if strings.Contains(got, "example-invalid.test") {
		t.Error("the injected URL survived")
	}
	if !strings.Contains(got, "风花雪月，山川湖海") {
		t.Error("the surrounding prose was dropped along with the URL")
	}
}

func TestBuild_DropsAuthorNotesToEndOfChapter(t *testing.T) {
	flat, chapters := buildFixture(t,
		[]string{
			"第一章　子丑寅卯",
			"甲乙丙丁，戊己庚辛。",
			"壬癸子丑，寅卯辰巳。",
			"作者有话说：这一章写得很辛苦。",
			"求推荐票，求收藏，谢谢大家。",
		},
	)
	chunks, _ := Build(Meta{Title: "示例书"}, chapters, flat, allRules(), nil)
	got := joinText(chunks)

	if strings.Contains(got, "作者有话说") || strings.Contains(got, "求推荐票") {
		t.Errorf("author note block survived:\n%s", got)
	}
	if !strings.Contains(got, "壬癸子丑") {
		t.Error("the prose before the author note was dropped too")
	}
}

func TestBuild_DropsRepeatedShortEdgeLines(t *testing.T) {
	var chapters [][]string
	for i := 1; i <= 6; i++ {
		chapters = append(chapters, []string{
			"第" + string(rune('０'+i)) + "章　子丑寅卯",
			"甲乙丙小说网",
			"正文第" + string(rune('０'+i)) + "段，天干地支，东南西北。",
			"甲乙丙小说网",
		})
	}
	flat, chs := buildFixture(t, chapters...)
	chunks, _ := Build(Meta{Title: "示例书"}, chs, flat, allRules(), nil)
	got := joinText(chunks)

	if strings.Contains(got, "甲乙丙小说网") {
		t.Errorf("repeated header/footer survived:\n%s", got)
	}
	if !strings.Contains(got, "天干地支") {
		t.Error("body text was removed with the edge lines")
	}
}

// The repetition heuristic must not delete a long recurring paragraph —
// that shape is a refrain, not a header, and losing it is silent data loss.
func TestBuild_KeepsLongRepeatedParagraph(t *testing.T) {
	refrain := "甲乙丙丁戊己庚辛壬癸，子丑寅卯辰巳午未申酉戌亥，东南西北中，春夏秋冬至，风花雪月夜。"
	var chapters [][]string
	for i := 1; i <= 6; i++ {
		chapters = append(chapters, []string{
			"第" + string(rune('０'+i)) + "章　子丑寅卯",
			refrain,
			"正文第" + string(rune('０'+i)) + "段。",
		})
	}
	flat, chs := buildFixture(t, chapters...)
	chunks, _ := Build(Meta{Title: "示例书"}, chs, flat, allRules(), nil)

	if !strings.Contains(joinText(chunks), refrain) {
		t.Error("a long repeated paragraph was deleted as if it were a header")
	}
}

func TestBuild_NormalisesPunctuationAndSpacing(t *testing.T) {
	flat, chapters := buildFixture(t,
		[]string{
			"第一章　子丑寅卯",
			"　　甲乙丙丁。。。戊己庚辛！！！壬癸,子丑   寅卯。",
		},
	)
	chunks, _ := Build(Meta{Title: "示例书"}, chapters, flat, allRules(), nil)
	got := joinText(chunks)

	if !strings.Contains(got, "……") {
		t.Errorf("run of periods was not folded into an ellipsis: %q", got)
	}
	if strings.Contains(got, "！！！") {
		t.Errorf("three repeated marks were not collapsed: %q", got)
	}
	if strings.Contains(got, "癸,") {
		t.Errorf("half-width comma between CJK was not widened: %q", got)
	}
	if strings.Contains(got, "   ") || strings.Contains(got, "　　") {
		t.Errorf("indentation/extra spaces survived: %q", got)
	}
}

// Two marks are a stylistic choice and must survive; three are damage.
// RE2 has no backreferences, so this is the behaviour collapseRepeatedMarks
// exists to provide — a pattern would have folded 「？！」 too.
func TestCollapseRepeatedMarks(t *testing.T) {
	cases := []struct{ in, want string }{
		{"甲！！乙", "甲！！乙"},
		{"甲！！！乙", "甲！乙"},
		{"甲！！！！！乙", "甲！乙"},
		{"甲？！乙", "甲？！乙"},
		{"甲？？？乙！！！丙", "甲？乙！丙"},
		{"甲乙丙", "甲乙丙"},
	}
	for _, c := range cases {
		if got := collapseRepeatedMarks(c.in); got != c.want {
			t.Errorf("collapseRepeatedMarks(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Turning every rule off must be lossless apart from the chapter-title
// paragraph, which the record carries in its Title field instead.
func TestBuild_RulesOffKeepsEverything(t *testing.T) {
	flat, chapters := buildFixture(t,
		[]string{"第一章　子丑寅卯", "更多精彩小说尽在 www.example-invalid.test", "作者有话说：辛苦了。"},
	)
	chunks, _ := Build(Meta{Title: "示例书"}, chapters, flat, Options{ChunkChars: 500}, nil)
	got := joinText(chunks)

	for _, kept := range []string{"example-invalid.test", "作者有话说"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q was removed even with every rule disabled", kept)
		}
	}
}

func TestBuild_ChunksStayInsideChapterAndTarget(t *testing.T) {
	body := strings.Repeat("甲乙丙丁戊己庚辛壬癸，子丑寅卯辰巳午未申酉戌亥。", 8)
	flat, chapters := buildFixture(t,
		[]string{"第一章　子丑寅卯", body, body, body},
		[]string{"第二章　辰巳午未", body},
	)
	opts := allRules()
	opts.ChunkChars = 300
	chunks, stats := Build(Meta{Title: "示例书", Author: "佚名"}, chapters, flat, opts, nil)

	if len(chunks) < 2 {
		t.Fatalf("expected the book to split into several chunks, got %d", len(chunks))
	}
	lastOffset := -1
	for _, c := range chunks {
		if c.Book != "示例书" || c.Author != "佚名" {
			t.Errorf("chunk is missing book identity: %+v", c)
		}
		if c.Title == "" {
			t.Errorf("chunk %d has no chapter title", c.Chapter)
		}
		if c.Offset <= lastOffset {
			t.Errorf("offsets are not increasing: %d after %d", c.Offset, lastOffset)
		}
		lastOffset = c.Offset
		if c.Chars != len([]rune(c.Text)) {
			t.Errorf("Chars=%d disagrees with the text length %d", c.Chars, len([]rune(c.Text)))
		}
	}
	if stats.Chunks != len(chunks) {
		t.Errorf("Stats.Chunks = %d, want %d", stats.Chunks, len(chunks))
	}
}

// Offsets must index the ORIGINAL flat text, not the cleaned output —
// that's what lets an exported chunk be traced back to a reading position.
func TestBuild_OffsetsIndexOriginalText(t *testing.T) {
	flat, chapters := buildFixture(t,
		[]string{"第一章　子丑寅卯", "更多精彩小说尽在 www.example-invalid.test", "甲乙丙丁，戊己庚辛。"},
	)
	chunks, _ := Build(Meta{Title: "示例书"}, chapters, flat, allRules(), nil)
	if len(chunks) == 0 {
		t.Fatal("no chunks produced")
	}
	runes := []rune(flat)
	off := chunks[0].Offset
	if off < 0 || off >= len(runes) {
		t.Fatalf("offset %d is outside the flat text (%d runes)", off, len(runes))
	}
	if got := string(runes[off : off+4]); got != "甲乙丙丁" {
		t.Errorf("offset %d points at %q, want the surviving paragraph's start", off, got)
	}
}

// Cleaning only ever removes, so the reported output must never exceed the
// input — the UI turns the difference into a "removed %".
func TestBuild_CharsOutNeverExceedsCharsIn(t *testing.T) {
	body := strings.Repeat("甲乙丙丁戊己庚辛壬癸，子丑寅卯辰巳午未申酉戌亥。", 4)
	flat, chapters := buildFixture(t,
		[]string{"第一章　子丑寅卯", body, body},
		[]string{"第二章　辰巳午未", body},
	)
	for name, opts := range map[string]Options{
		"all rules":  allRules(),
		"no rules":   {ChunkChars: 500},
		"promo only": {Rules: Rules{Promo: true}, ChunkChars: 500},
	} {
		_, stats := Build(Meta{Title: "示例书"}, chapters, flat, opts, nil)
		if stats.CharsOut > stats.CharsIn {
			t.Errorf("%s: CharsOut %d exceeds CharsIn %d", name, stats.CharsOut, stats.CharsIn)
		}
	}
}

func TestWriteJSONL_OneObjectPerLine(t *testing.T) {
	chunks := []Chunk{
		{Book: "示例书", Chapter: 1, Title: "第一章", Offset: 0, Chars: 4, Text: "甲乙丙丁"},
		{Book: "示例书", Chapter: 2, Title: "第二章", Offset: 9, Chars: 4, Text: "戊己庚辛"},
	}
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, chunks); err != nil {
		t.Fatalf("WriteJSONL: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), buf.String())
	}
	for i, line := range lines {
		var back Chunk
		if err := json.Unmarshal([]byte(line), &back); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", i, err)
		}
		if back.Text != chunks[i].Text {
			t.Errorf("line %d round-tripped to %q, want %q", i, back.Text, chunks[i].Text)
		}
	}
	// CJK must not be escaped to \uXXXX — the file is meant to be read.
	if strings.Contains(buf.String(), `\u`) {
		t.Errorf("output escaped non-ASCII:\n%s", buf.String())
	}
}

func TestBuildCorpus_EmitsOneVersionedRecordPerChapter(t *testing.T) {
	flat, chapters := buildFixture(t,
		[]string{"第一章　子丑寅卯", "甲乙丙丁，戊己庚辛。", "壬癸子丑，寅卯辰巳。"},
		[]string{"第二章　辰巳午未", "午未申酉，戌亥天干。"},
	)
	records, stats := BuildCorpus(
		Meta{Title: "示例书", Author: "佚名"},
		chapters,
		flat,
		allRules().Rules,
		nil,
	)

	if len(records) != 2 {
		t.Fatalf("got %d records, want one per chapter", len(records))
	}
	if stats.Records != len(records) || stats.Chapters != len(chapters) {
		t.Fatalf("stats = %+v, want 2 records and 2 chapters", stats)
	}
	if records[0].SchemaVersion != CorpusSchemaVersion {
		t.Errorf("schema version = %d, want %d", records[0].SchemaVersion, CorpusSchemaVersion)
	}
	if records[0].DocumentID == "" || records[0].ID != records[0].DocumentID+"/chapter-0001" {
		t.Errorf("unstable record identity: %+v", records[0])
	}
	if records[0].DocumentID != records[1].DocumentID {
		t.Error("records from one document have different document IDs")
	}
	if records[0].ChapterID != "chapter-0001" || records[0].ChapterIndex != 1 {
		t.Errorf("chapter identity = %q/%d", records[0].ChapterID, records[0].ChapterIndex)
	}
	if records[0].Metadata.Book != "示例书" || records[0].Metadata.Author != "佚名" {
		t.Errorf("metadata = %+v", records[0].Metadata)
	}
	if records[0].Text != "甲乙丙丁，戊己庚辛。\n\n壬癸子丑，寅卯辰巳。" {
		t.Errorf("chapter text was not kept whole: %q", records[0].Text)
	}
	if len(records[0].SourceSHA256) != 64 || len(records[0].CleanedSHA256) != 64 {
		t.Errorf("hashes are not SHA-256 hex: %+v", records[0])
	}
	if records[0].Chars != len([]rune(records[0].Text)) {
		t.Errorf("Chars=%d disagrees with text length", records[0].Chars)
	}
	var buf bytes.Buffer
	if err := WriteCorpusJSONL(&buf, records); err != nil {
		t.Fatalf("WriteCorpusJSONL: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != len(chapters) {
		t.Fatalf("JSONL has %d lines, want %d", len(lines), len(chapters))
	}
	if strings.Contains(buf.String(), `\u`) {
		t.Errorf("corpus output escaped non-ASCII:\n%s", buf.String())
	}
}

func TestBuildCorpus_CleaningChangesOnlyCleanedHash(t *testing.T) {
	flat, chapters := buildFixture(t,
		[]string{"第一章　子丑寅卯", "甲乙丙丁。。。戊己庚辛。"},
	)
	cleaned, _ := BuildCorpus(Meta{Title: "示例书"}, chapters, flat, Rules{Normalise: true}, nil)
	raw, _ := BuildCorpus(Meta{Title: "示例书"}, chapters, flat, Rules{}, nil)

	if cleaned[0].ID != raw[0].ID || cleaned[0].DocumentID != raw[0].DocumentID ||
		cleaned[0].SourceSHA256 != raw[0].SourceSHA256 {
		t.Error("source identity changed when only cleaning rules changed")
	}
	if cleaned[0].CleanedSHA256 == raw[0].CleanedSHA256 {
		t.Error("cleaned hash did not change with the cleaned text")
	}
}

func TestBuildCorpus_ReportsReplacementCharacterAndGenericStructure(t *testing.T) {
	flat, chapters := buildFixture(t,
		[]string{"正文", "甲乙丙丁。\uFFFD"},
	)
	records, stats := BuildCorpus(Meta{Title: "示例书"}, chapters, flat, Rules{}, nil)

	if stats.ReplacementCharacters != 1 {
		t.Errorf("replacement characters = %d, want 1", stats.ReplacementCharacters)
	}
	if !stats.ChapterStructureWarning {
		t.Error("single generic chapter did not raise a structure warning")
	}
	if len(records) != 1 || len(records[0].Cleaning.Warnings) != 1 ||
		records[0].Cleaning.Warnings[0] != "replacement_character" {
		t.Errorf("record warnings = %+v", records)
	}
}

func TestWriteCorpusJSONL_RejectsReplacementCharacterBeforeWriting(t *testing.T) {
	records := []CorpusRecord{
		{SchemaVersion: CorpusSchemaVersion, Text: "甲乙丙丁。"},
		{SchemaVersion: CorpusSchemaVersion, Text: "戊己\uFFFD庚辛。"},
	}
	var buf bytes.Buffer
	if err := WriteCorpusJSONL(&buf, records); err == nil {
		t.Fatal("WriteCorpusJSONL accepted a replacement character")
	}
	if buf.Len() != 0 {
		t.Errorf("writer emitted a partial export before validation: %q", buf.String())
	}
}
