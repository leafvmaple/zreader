package library

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"
)

// readEpubFile returns the raw bytes of `name` inside the EPUB archive
// represented by buf. Returns "" if not found.
func readEpubFile(t *testing.T, buf []byte, name string) string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		t.Fatalf("open epub zip: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("open %s in epub: %v", name, err)
			}
			defer rc.Close()
			b, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("read %s in epub: %v", name, err)
			}
			return string(b)
		}
	}
	return ""
}

// epubFileNames returns every entry's name. Used to spot-check that
// per-chapter XHTML files were emitted.
func epubFileNames(t *testing.T, buf []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		t.Fatalf("open epub zip: %v", err)
	}
	out := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out
}

func TestBuildEpub_BasicShape(t *testing.T) {
	// Two flat chapters, ByteOffsets line up with the formatted text.
	text := "第一章　起\n\n正文段一。\n\n正文段二。\n\n第二章　承\n\n正文段三。\n"
	chapters := []Chapter{
		{Idx: 1, Title: "第一章　起", Level: 1, ByteOffset: 0, CharOffset: 0},
		{Idx: 2, Title: "第二章　承", Level: 1, ByteOffset: strings.Index(text, "第二章"), CharOffset: 0},
	}

	var buf bytes.Buffer
	n, err := BuildEpub(&buf, "示例书", "佚名", text, chapters)
	if err != nil {
		t.Fatalf("BuildEpub: %v", err)
	}
	if n <= 0 {
		t.Fatalf("BuildEpub wrote %d bytes", n)
	}

	// mimetype must be the first entry and stored uncompressed (EPUB rule).
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open epub zip: %v", err)
	}
	if len(zr.File) == 0 || zr.File[0].Name != "mimetype" {
		t.Errorf("first entry = %q, want %q", zr.File[0].Name, "mimetype")
	}
	if zr.File[0].Method != zip.Store {
		t.Errorf("mimetype compression = %v, want zip.Store", zr.File[0].Method)
	}

	// Both chapter titles must surface somewhere in the per-section
	// XHTML (we don't pin a specific filename — the lib auto-assigns).
	names := epubFileNames(t, buf.Bytes())
	var bodies []string
	for _, n := range names {
		if strings.HasSuffix(n, ".xhtml") {
			bodies = append(bodies, readEpubFile(t, buf.Bytes(), n))
		}
	}
	joined := strings.Join(bodies, "\n")
	for _, want := range []string{"第一章　起", "第二章　承", "正文段一。", "正文段三。"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in some xhtml body; bodies=\n%s", want, joined)
		}
	}
}

func TestBuildEpub_VolumeNesting(t *testing.T) {
	// One volume header + two chapters under it. Verify the nav
	// (nav.xhtml — EPUB 3 toc) renders the volume as an outer entry
	// with both chapters listed beneath.
	text := "第一卷　序卷\n\n卷首语段。\n\n第一章　起\n\n正文段一。\n\n第二章　承\n\n正文段二。\n"
	chapters := []Chapter{
		{Idx: 1, Title: "第一卷　序卷", Level: 0, ByteOffset: 0},
		{Idx: 2, Title: "第一章　起", Level: 1, ByteOffset: strings.Index(text, "第一章")},
		{Idx: 3, Title: "第二章　承", Level: 1, ByteOffset: strings.Index(text, "第二章")},
	}

	var buf bytes.Buffer
	if _, err := BuildEpub(&buf, "示例书", "佚名", text, chapters); err != nil {
		t.Fatalf("BuildEpub: %v", err)
	}

	nav := readEpubFile(t, buf.Bytes(), "EPUB/nav.xhtml")
	if nav == "" {
		t.Fatalf("nav.xhtml missing from epub; entries=%v", epubFileNames(t, buf.Bytes()))
	}

	// Volume should appear, both chapters should appear, and the chapter
	// `<ol>` (nested list) must sit inside the volume's `<li>`. We check
	// the literal nest by finding the volume's `<li>`...`</li>` span and
	// asserting both chapters fall inside it.
	volIdx := strings.Index(nav, "第一卷　序卷")
	ch1Idx := strings.Index(nav, "第一章　起")
	ch2Idx := strings.Index(nav, "第二章　承")
	if volIdx < 0 || ch1Idx < 0 || ch2Idx < 0 {
		t.Fatalf("missing entries in nav: vol=%d ch1=%d ch2=%d\nnav=\n%s", volIdx, ch1Idx, ch2Idx, nav)
	}

	// Find the volume <li> and verify both chapters are nested inside it.
	// Our writer emits one `<li>...<ol>children</ol></li>` per parent
	// section; we look for the inner `<ol>` that opens after the volume
	// title and confirm both chapter titles appear before it closes.
	innerOLStart := strings.Index(nav[volIdx:], "<ol>")
	if innerOLStart < 0 {
		t.Fatalf("no nested <ol> after volume title; nav=\n%s", nav)
	}
	innerOLStart += volIdx
	innerOLEnd := strings.Index(nav[innerOLStart:], "</ol>")
	if innerOLEnd < 0 {
		t.Fatalf("inner <ol> not closed; nav=\n%s", nav)
	}
	innerOLEnd += innerOLStart
	if !(innerOLStart < ch1Idx && ch1Idx < innerOLEnd) {
		t.Errorf("第一章 not inside volume's inner <ol> [%d,%d]; ch1=%d", innerOLStart, innerOLEnd, ch1Idx)
	}
	if !(innerOLStart < ch2Idx && ch2Idx < innerOLEnd) {
		t.Errorf("第二章 not inside volume's inner <ol> [%d,%d]; ch2=%d", innerOLStart, innerOLEnd, ch2Idx)
	}
}

func TestChapterXHTML_DropsTitleParagraph(t *testing.T) {
	// The chapter's slice starts with the title on its own line; the
	// helper must drop that paragraph from the body so the title only
	// appears in <h1>.
	got := chapterBodyXHTML("第一章　起", "第一章　起\n\n正文一。\n\n正文二。\n")
	if !strings.Contains(got, "<h1>第一章　起</h1>") {
		t.Errorf("missing <h1>: %s", got)
	}
	if strings.Count(got, "第一章　起") != 1 {
		t.Errorf("title appears %d times, want 1: %s",
			strings.Count(got, "第一章　起"), got)
	}
	for _, want := range []string{"<p>正文一。</p>", "<p>正文二。</p>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestChapterXHTML_EscapesHTML(t *testing.T) {
	got := chapterBodyXHTML("章 <Title>", "章 <Title>\n\n包含 <p> 与 & 的正文。\n")
	if !strings.Contains(got, "<h1>章 &lt;Title&gt;</h1>") {
		t.Errorf("title not escaped: %s", got)
	}
	if !strings.Contains(got, "包含 &lt;p&gt; 与 &amp; 的正文。") {
		t.Errorf("body not escaped: %s", got)
	}
}
