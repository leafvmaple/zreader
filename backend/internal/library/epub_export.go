package library

// EPUB export.
//
// BuildEpub turns the same (formattedText, chapters) pair the scanner
// already produces (see scanner.go → FormatToCache + ParseChapters) into a
// valid EPUB 3 archive, with an EPUB 2 NCX alongside for older readers.
// Pure stdlib — archive/zip + a few string templates. The whole writer
// lives in this file; there's no third-party dependency to chase when
// something needs adjusting.
//
// TOC nesting reflects Chapter.Level: a Level=N entry becomes a child of
// the most recent unclosed ancestor with Level < N. The current parser
// only emits 0 (volume) and 1 (chapter), so the produced TOC is at most
// two deep — but the tree builder handles arbitrary depth, so when the
// parser grows finer levels (部 / 篇 / 节) or EPUB import lands, no
// changes are needed here.
//
// Layout produced:
//
//	mimetype                       (stored, uncompressed, first entry)
//	META-INF/container.xml         (points to EPUB/content.opf)
//	EPUB/content.opf               (package — metadata, manifest, spine)
//	EPUB/nav.xhtml                 (EPUB 3 navigation document)
//	EPUB/toc.ncx                   (EPUB 2 navigation, backward-compat)
//	EPUB/xhtml/chap-NNNN.xhtml     (one per chapter, in spine order)

import (
	"archive/zip"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	epubMimetype = "application/epub+zip"
	epubRootDir  = "EPUB"
	epubXhtmlDir = "xhtml"
	opfPath      = epubRootDir + "/content.opf"
	navPath      = epubRootDir + "/nav.xhtml"
	ncxPath      = epubRootDir + "/toc.ncx"
)

// epubSection is one node in the per-book TOC tree. The flat input
// Chapter slice is folded into a tree (children populated according to
// Level) before serialisation so the EPUB nav reflects whatever depth
// the parser emitted.
type epubSection struct {
	title    string
	filename string // basename only, e.g. "chap-0001.xhtml"
	body     string // XHTML body content (children of <body>)
	level    int
	children []*epubSection
}

// BuildEpub writes the EPUB archive for the given book to w. chapters
// must be ordered by ByteOffset (the order ParseChapters produces) and
// their offsets must index into formattedText.
//
// Returns bytes written + first error encountered. On error a partial
// archive may have been written to w; callers should treat any error
// return as "the EPUB is not valid, discard it".
func BuildEpub(w io.Writer, title, author, formattedText string, chapters []Chapter) (int64, error) {
	if title == "" {
		title = "未命名"
	}
	if author == "" {
		author = DefaultAuthor
	}
	if len(chapters) == 0 {
		chapters = []Chapter{{Idx: 1, Title: title, Level: 0, ByteOffset: 0, CharOffset: 0}}
	}

	roots, flat := buildSections(formattedText, chapters)
	bookID := "urn:uuid:" + uuid.NewString()
	modified := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	counter := &countingWriter{w: w}
	zw := zip.NewWriter(counter)

	// mimetype MUST be the first entry, stored uncompressed, with no
	// extra field. EPUB 3 §4.3.1; readers / epubcheck verify this.
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return counter.n, fmt.Errorf("create mimetype: %w", err)
	}
	if _, err := mw.Write([]byte(epubMimetype)); err != nil {
		return counter.n, fmt.Errorf("write mimetype: %w", err)
	}

	if err := zipFile(zw, "META-INF/container.xml", containerXML()); err != nil {
		return counter.n, err
	}
	if err := zipFile(zw, opfPath, packageOpfXML(title, author, bookID, modified, flat)); err != nil {
		return counter.n, err
	}
	if err := zipFile(zw, navPath, navXhtmlXML(title, roots)); err != nil {
		return counter.n, err
	}
	if err := zipFile(zw, ncxPath, tocNcxXML(title, bookID, roots)); err != nil {
		return counter.n, err
	}
	for _, s := range flat {
		path := epubRootDir + "/" + epubXhtmlDir + "/" + s.filename
		if err := zipFile(zw, path, chapterXhtmlXML(s.title, s.body)); err != nil {
			return counter.n, err
		}
	}

	if err := zw.Close(); err != nil {
		return counter.n, fmt.Errorf("close zip: %w", err)
	}
	return counter.n, nil
}

// buildSections folds the flat chapter list into a nested tree using
// the stack-of-ancestors approach: each new node closes any ancestor
// whose level is >= its own, then attaches under whatever remains at
// the top (or as a root if the stack is empty).
//
// Returns (root sections, every section in spine/manifest order). The
// flat slice mirrors ParseChapters ordering — that's the natural
// reading order, which is also what the EPUB spine wants.
func buildSections(formattedText string, chapters []Chapter) (roots, flat []*epubSection) {
	stack := make([]*epubSection, 0, 4)
	for i, ch := range chapters {
		end := len(formattedText)
		if i+1 < len(chapters) {
			end = chapters[i+1].ByteOffset
		}
		s := &epubSection{
			title:    ch.Title,
			filename: fmt.Sprintf("chap-%04d.xhtml", i+1),
			body:     chapterBodyXHTML(ch.Title, formattedText[ch.ByteOffset:end]),
			level:    ch.Level,
		}
		for len(stack) > 0 && stack[len(stack)-1].level >= s.level {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			roots = append(roots, s)
		} else {
			parent := stack[len(stack)-1]
			parent.children = append(parent.children, s)
		}
		stack = append(stack, s)
		flat = append(flat, s)
	}
	return roots, flat
}

// chapterBodyXHTML renders one chapter's body content (the <body>'s
// children — not a full document). The slice is expected to begin with
// the title on its own paragraph (canonical FormatText output); that
// paragraph is stripped before splitting so the title appears once,
// in <h1>, rather than twice.
func chapterBodyXHTML(title, slice string) string {
	var b strings.Builder
	b.Grow(len(slice) + 64)
	b.WriteString("<h1>")
	b.WriteString(html.EscapeString(title))
	b.WriteString("</h1>\n")

	body := ""
	if idx := strings.Index(slice, "\n\n"); idx >= 0 {
		body = slice[idx+2:]
	}
	for _, para := range strings.Split(body, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}
		b.WriteString("<p>")
		b.WriteString(html.EscapeString(para))
		b.WriteString("</p>\n")
	}
	return b.String()
}

// --- XML templates ---------------------------------------------------------

func containerXML() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="EPUB/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>
`
}

// packageOpfXML emits EPUB/content.opf. The OPF lists every resource
// (manifest), declares reading order (spine), and carries metadata.
// dcterms:modified is required by EPUB 3 — its format is the strict
// CCYY-MM-DDThh:mm:ssZ (no fractional seconds, always UTC).
func packageOpfXML(title, author, bookID, modified string, flat []*epubSection) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" xml:lang="zh" unique-identifier="bookid">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="bookid">`)
	b.WriteString(html.EscapeString(bookID))
	b.WriteString(`</dc:identifier>
    <dc:title>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</dc:title>
    <dc:creator>`)
	b.WriteString(html.EscapeString(author))
	b.WriteString(`</dc:creator>
    <dc:language>zh</dc:language>
    <meta property="dcterms:modified">`)
	b.WriteString(modified)
	b.WriteString(`</meta>
  </metadata>
  <manifest>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
`)
	for i, s := range flat {
		fmt.Fprintf(&b, "    <item id=\"chap%d\" href=\"xhtml/%s\" media-type=\"application/xhtml+xml\"/>\n",
			i+1, html.EscapeString(s.filename))
	}
	b.WriteString(`  </manifest>
  <spine toc="ncx">
`)
	for i := range flat {
		fmt.Fprintf(&b, "    <itemref idref=\"chap%d\"/>\n", i+1)
	}
	b.WriteString(`  </spine>
</package>
`)
	return b.String()
}

func navXhtmlXML(title string, roots []*epubSection) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" xml:lang="zh">
<head><title>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</title></head>
<body>
<nav epub:type="toc" id="toc">
<h1>目录</h1>
`)
	writeNavList(&b, roots)
	b.WriteString(`</nav>
</body>
</html>
`)
	return b.String()
}

// writeNavList emits a nested `<ol>` mirroring the section tree.
// Recursion handles arbitrary depth — no hard-coded volume/chapter
// distinction.
func writeNavList(b *strings.Builder, sections []*epubSection) {
	if len(sections) == 0 {
		return
	}
	b.WriteString("<ol>\n")
	for _, s := range sections {
		b.WriteString(`<li><a href="xhtml/`)
		b.WriteString(html.EscapeString(s.filename))
		b.WriteString(`">`)
		b.WriteString(html.EscapeString(s.title))
		b.WriteString(`</a>`)
		if len(s.children) > 0 {
			b.WriteString("\n")
			writeNavList(b, s.children)
		}
		b.WriteString("</li>\n")
	}
	b.WriteString("</ol>\n")
}

func tocNcxXML(title, bookID string, roots []*epubSection) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1" xml:lang="zh">
<head>
  <meta name="dtb:uid" content="`)
	b.WriteString(html.EscapeString(bookID))
	b.WriteString(`"/>
  <meta name="dtb:depth" content="`)
	b.WriteString(strconv.Itoa(treeDepth(roots)))
	b.WriteString(`"/>
  <meta name="dtb:totalPageCount" content="0"/>
  <meta name="dtb:maxPageNumber" content="0"/>
</head>
<docTitle><text>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</text></docTitle>
<navMap>
`)
	playOrder := 0
	writeNcxPoints(&b, roots, &playOrder)
	b.WriteString(`</navMap>
</ncx>
`)
	return b.String()
}

func writeNcxPoints(b *strings.Builder, sections []*epubSection, playOrder *int) {
	for _, s := range sections {
		*playOrder++
		n := *playOrder
		fmt.Fprintf(b, "<navPoint id=\"navpoint-%d\" playOrder=\"%d\">\n", n, n)
		b.WriteString(`  <navLabel><text>`)
		b.WriteString(html.EscapeString(s.title))
		b.WriteString(`</text></navLabel>
  <content src="xhtml/`)
		b.WriteString(html.EscapeString(s.filename))
		b.WriteString(`"/>
`)
		if len(s.children) > 0 {
			writeNcxPoints(b, s.children, playOrder)
		}
		b.WriteString("</navPoint>\n")
	}
}

// treeDepth returns the maximum nesting depth of the tree. Used to
// populate `dtb:depth` in the NCX header (informational; readers don't
// strictly require an accurate value but epubcheck flags mismatches).
func treeDepth(sections []*epubSection) int {
	if len(sections) == 0 {
		return 0
	}
	max := 1
	for _, s := range sections {
		if d := 1 + treeDepth(s.children); d > max {
			max = d
		}
	}
	return max
}

func chapterXhtmlXML(title, body string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="zh">
<head><title>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</title></head>
<body>
`)
	b.WriteString(body)
	b.WriteString(`</body>
</html>
`)
	return b.String()
}

// --- zip helpers ----------------------------------------------------------

func zipFile(zw *zip.Writer, name, content string) error {
	fw, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("create %s: %w", name, err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

// countingWriter wraps an io.Writer so BuildEpub can report bytes
// written without buffering the whole archive in memory.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
