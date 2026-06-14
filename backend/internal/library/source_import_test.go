package library

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/leafvmaple/zreader/internal/store"
)

func TestFormatPDFToCache_TextLayer(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "FileTitle - FileAuthor.pdf")
	writeSimplePDF(t, src, "PDFTitle", "PDFAuthor", []string{
		"Chapter 1",
		"Alpha beta paragraph for zreader import.",
	})

	cr, err := FormatPDFToCache(tmp, src)
	if err != nil {
		t.Fatalf("FormatPDFToCache: %v", err)
	}
	if cr.Title != "PDFTitle" || cr.Author != "PDFAuthor" {
		t.Fatalf("metadata = (%q, %q), want PDFTitle/PDFAuthor", cr.Title, cr.Author)
	}
	if cr.SourceEnc != "pdf-text" {
		t.Fatalf("SourceEnc = %q, want pdf-text", cr.SourceEnc)
	}

	book, err := ReadEpub(cr.Path)
	if err != nil {
		t.Fatalf("read cached epub: %v", err)
	}
	for _, want := range []string{"Chapter 1", "Alpha beta paragraph"} {
		if !strings.Contains(book.FlatText, want) {
			t.Fatalf("cached flat text missing %q: %q", want, book.FlatText)
		}
	}
	if len(book.Chapters) != 1 || book.Chapters[0].Title != "Chapter 1" {
		t.Fatalf("chapters = %+v, want one Chapter 1", book.Chapters)
	}
}

func TestFormatPDFToCache_CJKToUnicodeTextLayer(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "SyntheticCJK.pdf")
	writeCMapPDF(t, src, "CJKTitle", "AuthorX", []string{
		"第一章　甲乙丙丁",
		"子丑寅卯辰巳午未申酉戌亥春夏秋冬。",
	})

	cr, err := FormatPDFToCache(tmp, src)
	if err != nil {
		t.Fatalf("FormatPDFToCache: %v", err)
	}
	if cr.Title != "CJKTitle" || cr.Author != "AuthorX" {
		t.Fatalf("metadata = (%q, %q), want CJKTitle/AuthorX", cr.Title, cr.Author)
	}

	book, err := ReadEpub(cr.Path)
	if err != nil {
		t.Fatalf("read cached epub: %v", err)
	}
	for _, want := range []string{"第一章 甲乙丙丁", "子丑寅卯辰巳午未申酉戌亥春夏秋冬。"} {
		if !strings.Contains(book.FlatText, want) {
			t.Fatalf("cached flat text missing %q: %q", want, book.FlatText)
		}
	}
	if len(book.Chapters) != 1 || book.Chapters[0].Title != "第一章 甲乙丙丁" {
		t.Fatalf("chapters = %+v, want one CJK chapter", book.Chapters)
	}
}

func TestExtractPDFText_NoTextLayer(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "ImageOnly.pdf")
	writeSimplePDF(t, src, "ImageOnly", "AuthorX", nil)

	_, err := ExtractPDFText(src)
	if !errors.Is(err, ErrPDFNoText) {
		t.Fatalf("ExtractPDFText err = %v, want ErrPDFNoText", err)
	}
}

func TestImportEpubToCache(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "source.epub")
	text := "Chapter 1\n\nAlpha paragraph.\n\nChapter 2\n\nBeta paragraph.\n"
	chapters := []Chapter{
		{Idx: 1, Title: "Chapter 1", Level: 0, ByteOffset: 0},
		{Idx: 2, Title: "Chapter 2", Level: 0, ByteOffset: strings.Index(text, "Chapter 2")},
	}
	writeEpubFile(t, src, "EpubTitle", "EpubAuthor", text, chapters)

	cr, err := ImportEpubToCache(tmp, src)
	if err != nil {
		t.Fatalf("ImportEpubToCache: %v", err)
	}
	if cr.Title != "EpubTitle" || cr.Author != "EpubAuthor" {
		t.Fatalf("metadata = (%q, %q), want EpubTitle/EpubAuthor", cr.Title, cr.Author)
	}
	if cr.SourceEnc != "epub" {
		t.Fatalf("SourceEnc = %q, want epub", cr.SourceEnc)
	}
	book, err := ReadEpub(cr.Path)
	if err != nil {
		t.Fatalf("read imported epub cache: %v", err)
	}
	if len(book.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(book.Chapters))
	}
}

func TestScannerScanFolder_SupportedSourceFormats(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	bookDir := t.TempDir()

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	folder, err := st.AddFolder(ctx, bookDir)
	if err != nil {
		t.Fatalf("add folder: %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(bookDir, "TxtTitle - TxtAuthor.txt"),
		[]byte("Chapter 1\n\nAlpha text paragraph.\n"),
		0o644,
	); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	epubText := "Chapter 1\n\nEpub body paragraph.\n"
	writeEpubFile(t,
		filepath.Join(bookDir, "EpubSource.epub"),
		"EpubTitle",
		"EpubAuthor",
		epubText,
		[]Chapter{{Idx: 1, Title: "Chapter 1", Level: 0, ByteOffset: 0}},
	)
	writeSimplePDF(t,
		filepath.Join(bookDir, "PdfSource.pdf"),
		"PDFTitle",
		"PDFAuthor",
		[]string{"Chapter 1", "PDF body paragraph for scanning."},
	)

	scanner := &Scanner{Store: st}
	res, err := scanner.ScanFolder(ctx, folder)
	if err != nil {
		t.Fatalf("ScanFolder: %v", err)
	}
	if res.Added != 3 || res.Updated != 0 || len(res.Failed) != 0 {
		t.Fatalf("scan result = %+v, want 3 added and no failures", res)
	}

	books, err := st.ListBooks(ctx, folder.ID)
	if err != nil {
		t.Fatalf("list books: %v", err)
	}
	got := map[string]bool{}
	for _, b := range books {
		got[b.Title] = true
		if b.Format != "epub" {
			t.Fatalf("book %q format = %q, want epub", b.Title, b.Format)
		}
	}
	for _, title := range []string{"TxtTitle", "EpubTitle", "PDFTitle"} {
		if !got[title] {
			t.Fatalf("missing scanned title %q; got=%v", title, got)
		}
	}
}

func writeEpubFile(t *testing.T, path, title, author, text string, chapters []Chapter) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir epub dir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create epub: %v", err)
	}
	defer f.Close()
	if _, err := BuildEpub(f, title, author, text, chapters); err != nil {
		t.Fatalf("BuildEpub: %v", err)
	}
}

func writeSimplePDF(t *testing.T, path, title, author string, lines []string) {
	t.Helper()
	var content bytes.Buffer
	content.WriteString("BT /F1 12 Tf\n")
	for lineIdx, line := range lines {
		y := 720 - lineIdx*24
		words := strings.Fields(line)
		if len(words) == 0 {
			continue
		}
		for wordIdx, word := range words {
			x := 72 + wordIdx*54
			fmt.Fprintf(&content, "1 0 0 1 %d %d Tm (%s) Tj\n", x, y, escapePDFString(word))
		}
	}
	content.WriteString("ET\n")

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", content.Len(), content.String()),
		fmt.Sprintf("<< /Title (%s) /Author (%s) >>", escapePDFString(title), escapePDFString(author)),
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(objects))
	for i, obj := range objects {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objects)+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R /Info 6 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
}

func writeCMapPDF(t *testing.T, path, title, author string, lines []string) {
	t.Helper()
	type runeCode struct {
		r    rune
		code byte
	}
	codeByRune := map[rune]byte{}
	var codes []runeCode
	nextCode := byte(1)
	for _, line := range lines {
		for _, r := range line {
			if _, ok := codeByRune[r]; ok {
				continue
			}
			if nextCode == 0 {
				t.Fatalf("synthetic PDF exceeded one-byte code space")
			}
			codeByRune[r] = nextCode
			codes = append(codes, runeCode{r: r, code: nextCode})
			nextCode++
		}
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i].code < codes[j].code })

	var content bytes.Buffer
	content.WriteString("BT /F1 12 Tf\n")
	for lineIdx, line := range lines {
		y := 720 - lineIdx*24
		fmt.Fprintf(&content, "1 0 0 1 72 %d Tm <%s> Tj\n", y, cmapPDFLineHex(line, codeByRune))
	}
	content.WriteString("ET\n")

	var cmap bytes.Buffer
	cmap.WriteString("/CIDInit /ProcSet findresource begin\n")
	cmap.WriteString("12 dict begin\n")
	cmap.WriteString("begincmap\n")
	cmap.WriteString("/CIDSystemInfo << /Registry (Adobe) /Ordering (UCS) /Supplement 0 >> def\n")
	cmap.WriteString("/CMapName /SyntheticCJK def\n")
	cmap.WriteString("/CMapType 2 def\n")
	cmap.WriteString("1 begincodespacerange\n")
	cmap.WriteString("<01> <FF>\n")
	cmap.WriteString("endcodespacerange\n")
	fmt.Fprintf(&cmap, "%d beginbfchar\n", len(codes))
	for _, rc := range codes {
		fmt.Fprintf(&cmap, "<%02X> <%s>\n", rc.code, utf16BEHex(string(rc.r)))
	}
	cmap.WriteString("endbfchar\n")
	cmap.WriteString("endcmap\n")
	cmap.WriteString("CMapName currentdict /CMap defineresource pop\n")
	cmap.WriteString("end\nend\n")

	widths := strings.Repeat("1000 ", len(codes))
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>",
		fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /FirstChar 1 /LastChar %d /Widths [%s] /ToUnicode 6 0 R >>", len(codes), widths),
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", content.Len(), content.String()),
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", cmap.Len(), cmap.String()),
		fmt.Sprintf("<< /Title (%s) /Author (%s) >>", escapePDFString(title), escapePDFString(author)),
	}

	writePDFObjects(t, path, objects, 7)
}

func cmapPDFLineHex(line string, codeByRune map[rune]byte) string {
	var b strings.Builder
	for _, r := range line {
		fmt.Fprintf(&b, "%02X", codeByRune[r])
	}
	return b.String()
}

func utf16BEHex(s string) string {
	var b strings.Builder
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, "%04X", u)
	}
	return b.String()
}

func writePDFObjects(t *testing.T, path string, objects []string, infoObject int) {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, 0, len(objects))
	for i, obj := range objects {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objects)+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R", len(objects)+1)
	if infoObject > 0 {
		fmt.Fprintf(&buf, " /Info %d 0 R", infoObject)
	}
	fmt.Fprintf(&buf, " >>\nstartxref\n%d\n%%%%EOF\n", xref)

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
}

func escapePDFString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `(`, `\(`)
	s = strings.ReplaceAll(s, `)`, `\)`)
	return s
}
