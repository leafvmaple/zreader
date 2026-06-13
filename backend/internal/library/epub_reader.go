package library

// EPUB reader.
//
// ReadEpub is the inverse of BuildEpub (epub_export.go). Given a path to
// a cached EPUB it returns the canonical metadata + chapter list plus
// the flat plain-text view that handleBookContent serves slices of.
//
// Conventions matched to BuildEpub:
//   - One chapter per spine item; no fragment hrefs in nav.
//   - Chapter body XHTML contains a single `<h1>` (title) followed by
//     `<p>` paragraphs. Inline children are collapsed to text.
//   - The flat text reconstructs the canonical TXT shape: chapter title
//     paragraph, blank line, body paragraphs separated by blank lines,
//     blank line between chapters. Chapter byte / char offsets index
//     into this string — the same scheme handleBookContent expects.
//
// Level extraction tracks the actual `<li>` nesting depth in the nav
// `<ol>` tree: outermost `<li>` → Level=0, one level nested → Level=1,
// and so on. The convention is 0-indexed depth from the outer list,
// matching what BuildEpub writes — so a (text, chapters) → BuildEpub
// → ReadEpub round-trip preserves Level values exactly. Downstream
// code (frontend TOC) treats Level as nesting depth, so books with
// arbitrarily many tiers render correctly.

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"unicode/utf8"
)

// EpubBook is the parsed-from-disk view of an EPUB. Chapter offsets
// index into FlatText (byte and rune positions).
type EpubBook struct {
	Title    string
	Author   string
	Chapters []Chapter
	FlatText string
}

// ReadEpub opens the EPUB at epubPath and decodes it into an EpubBook.
// Errors are returned for: zip-level failures, missing container.xml /
// OPF, malformed XML, or a spine entry referencing an unknown manifest
// id. Non-fatal issues (no nav doc, chapter XHTML with no <h1>) are
// tolerated — they degrade level/title rather than aborting.
func ReadEpub(epubPath string) (*EpubBook, error) {
	zrc, err := zip.OpenReader(epubPath)
	if err != nil {
		return nil, fmt.Errorf("open epub %s: %w", epubPath, err)
	}
	defer zrc.Close()

	opfPath, err := readContainerXML(&zrc.Reader)
	if err != nil {
		return nil, err
	}
	opf, err := readOPF(&zrc.Reader, opfPath)
	if err != nil {
		return nil, err
	}
	opfDir := path.Dir(opfPath)

	idToHref := make(map[string]string, len(opf.Manifest.Items))
	var navHref string
	for _, it := range opf.Manifest.Items {
		idToHref[it.ID] = it.Href
		if strings.Contains(it.Properties, "nav") {
			navHref = it.Href
		}
	}

	// hrefNav maps a manifest href (the EXACT string in OPF) to its
	// nav-derived depth/title. Missing entries default to Level=0 and
	// chapter-local heading titles.
	hrefNav := map[string]epubNavEntry{}
	if navHref != "" {
		navFullPath := joinEpubPath(opfDir, navHref)
		navItems, err := readNavItems(&zrc.Reader, navFullPath)
		if err == nil {
			hrefNav = navItems
		}
	}

	var flat strings.Builder
	var chapters []Chapter
	charOffset := 0

	for i, ref := range opf.Spine.Itemrefs {
		href, ok := idToHref[ref.IDRef]
		if !ok {
			return nil, fmt.Errorf("spine refers to unknown idref %q", ref.IDRef)
		}
		chapPath := joinEpubPath(opfDir, href)
		title, paras, err := readChapterXHTML(&zrc.Reader, chapPath)
		if err != nil {
			return nil, fmt.Errorf("read chapter %s: %w", chapPath, err)
		}
		if title == "" {
			if nav, ok := hrefNav[href]; ok {
				title = nav.Title
			}
		}
		if title == "" {
			title = fmt.Sprintf("Chapter %d", i+1)
		}

		if i > 0 {
			flat.WriteString("\n\n")
			charOffset += 2
		}
		chapByteOffset := flat.Len()
		chapCharOffset := charOffset

		flat.WriteString(title)
		charOffset += utf8.RuneCountInString(title)
		for _, p := range paras {
			flat.WriteString("\n\n")
			flat.WriteString(p)
			charOffset += 2 + utf8.RuneCountInString(p)
		}

		level := 0
		if nav, ok := hrefNav[href]; ok {
			level = nav.Level
		}

		chapters = append(chapters, Chapter{
			Idx:        i + 1,
			Title:      title,
			Level:      level,
			ByteOffset: chapByteOffset,
			CharOffset: chapCharOffset,
		})
	}

	return &EpubBook{
		Title:    opf.Metadata.Title,
		Author:   opf.Metadata.Creator,
		Chapters: chapters,
		FlatText: flat.String(),
	}, nil
}

// joinEpubPath joins a base directory (within the EPUB archive) with a
// relative href and normalises the result so it can be looked up in
// the zip's flat name list. Always uses `/` (zip-internal convention).
func joinEpubPath(dir, href string) string {
	if dir == "" || dir == "." {
		return path.Clean(href)
	}
	return path.Clean(path.Join(dir, href))
}

// readZipFile reads the named entry from z and returns its raw bytes.
// Entry names in zips are forward-slash separated; callers should pass
// the same form.
func readZipFile(z *zip.Reader, name string) ([]byte, error) {
	for _, f := range z.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("not in epub: %s", name)
}

// readContainerXML pulls the OPF path out of META-INF/container.xml.
func readContainerXML(z *zip.Reader) (string, error) {
	raw, err := readZipFile(z, "META-INF/container.xml")
	if err != nil {
		return "", fmt.Errorf("read container.xml: %w", err)
	}
	var c struct {
		Rootfiles struct {
			Rootfile struct {
				FullPath string `xml:"full-path,attr"`
			} `xml:"rootfile"`
		} `xml:"rootfiles"`
	}
	if err := xml.Unmarshal(raw, &c); err != nil {
		return "", fmt.Errorf("parse container.xml: %w", err)
	}
	if c.Rootfiles.Rootfile.FullPath == "" {
		return "", fmt.Errorf("container.xml has no rootfile full-path")
	}
	return c.Rootfiles.Rootfile.FullPath, nil
}

// opfPackage mirrors the subset of the EPUB OPF schema we read.
// Namespaced elements (`dc:title`, `dc:creator`) are matched by local
// name — Go's xml decoder accepts this by default.
type opfPackage struct {
	Metadata struct {
		Title   string `xml:"title"`
		Creator string `xml:"creator"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Itemrefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func readOPF(z *zip.Reader, opfPath string) (*opfPackage, error) {
	raw, err := readZipFile(z, opfPath)
	if err != nil {
		return nil, fmt.Errorf("read opf: %w", err)
	}
	var p opfPackage
	if err := xml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse opf: %w", err)
	}
	return &p, nil
}

type epubNavEntry struct {
	Level int
	Title string
}

// readNavItems walks the nav.xhtml token stream and returns href-indexed
// depth/title metadata. Depth is the count of open `<li>` ancestors at the
// time the `<a href>` is encountered, minus one. Hrefs are returned exactly as
// written in the nav doc, with URL fragments stripped, so the caller can match
// them against manifest entries directly.
//
// When the same href appears more than once, the first occurrence wins.
func readNavItems(z *zip.Reader, navPath string) (map[string]epubNavEntry, error) {
	raw, err := readZipFile(z, navPath)
	if err != nil {
		return nil, fmt.Errorf("read nav: %w", err)
	}

	out := map[string]epubNavEntry{}
	liDepth := 0 // count of currently-open <li> ancestors
	captureHref := ""
	captureLevel := 0
	var captureText strings.Builder

	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse nav: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch strings.ToLower(t.Name.Local) {
			case "li":
				liDepth++
			case "a":
				if liDepth > 0 {
					for _, a := range t.Attr {
						if strings.ToLower(a.Name.Local) == "href" {
							href := a.Value
							if i := strings.IndexByte(href, '#'); i >= 0 {
								href = href[:i]
							}
							if href != "" {
								captureHref = href
								captureLevel = liDepth - 1
								captureText.Reset()
							}
						}
					}
				}
			}
		case xml.CharData:
			if captureHref != "" {
				captureText.Write(t)
			}
		case xml.EndElement:
			switch strings.ToLower(t.Name.Local) {
			case "a":
				if captureHref != "" {
					if _, exists := out[captureHref]; !exists {
						out[captureHref] = epubNavEntry{
							Level: captureLevel,
							Title: strings.TrimSpace(collapseSpaces(captureText.String())),
						}
					}
					captureHref = ""
					captureText.Reset()
				}
			case "li":
				if liDepth > 0 {
					liDepth--
				}
			}
		}
	}
	return out, nil
}

// readChapterXHTML returns the chapter title (text of the first heading)
// and the ordered list of paragraph texts (one entry per <p>). Inline
// children inside headings/<p> collapse to their text content. Heading-less
// chapters return "" as the title — caller can fall back to whatever
// the manifest / nav offers.
func readChapterXHTML(z *zip.Reader, xhtmlPath string) (title string, paras []string, err error) {
	raw, err := readZipFile(z, xhtmlPath)
	if err != nil {
		return "", nil, fmt.Errorf("read xhtml: %w", err)
	}

	dec := xml.NewDecoder(bytes.NewReader(raw))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity

	var buf strings.Builder
	capture := "" // "h1" or "p" while accumulating an outermost block

	for {
		tok, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", nil, fmt.Errorf("parse xhtml: %w", e)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if capture == "" {
				name := strings.ToLower(t.Name.Local)
				if isHTMLHeading(name) || name == "p" {
					capture = name
					buf.Reset()
				}
			}
		case xml.EndElement:
			if capture != "" && strings.ToLower(t.Name.Local) == capture {
				txt := strings.TrimSpace(collapseSpaces(buf.String()))
				if txt != "" {
					if isHTMLHeading(capture) && title == "" {
						title = txt
					} else if capture == "p" {
						paras = append(paras, txt)
					}
				}
				capture = ""
			}
		case xml.CharData:
			if capture != "" {
				buf.Write(t)
			}
		}
	}
	return title, paras, nil
}

func isHTMLHeading(name string) bool {
	switch name {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return true
	default:
		return false
	}
}

// --- Flat-text cache -----------------------------------------------------

// flatCacheEntry stores the rune-indexed view of a cached EPUB plus
// the (mtime, size) fingerprint that gates invalidation.
type flatCacheEntry struct {
	mtime int64
	size  int64
	runes []rune
}

var (
	flatCacheMu sync.RWMutex
	flatCache   = map[string]flatCacheEntry{}
)

// GetFlatRunes returns the rune view of the EPUB at path — the flat
// plain-text reconstruction the content API serves slices of. The
// result is cached in-process keyed by absolute path; the entry is
// considered stale and refreshed when mtime or byte size changes.
//
// The returned slice is shared with the cache; callers MUST treat it
// as immutable (use string conversion to copy out subslices).
//
// Cache is unbounded. For personal libraries (tens to low-hundreds of
// books, each a few MB of plain text) this stays well under
// hundred-megabyte budget. Add an LRU bound if that changes.
func GetFlatRunes(epubPath string) ([]rune, error) {
	info, err := os.Stat(epubPath)
	if err != nil {
		return nil, err
	}
	mtime := info.ModTime().UnixNano()
	size := info.Size()

	flatCacheMu.RLock()
	if e, ok := flatCache[epubPath]; ok && e.mtime == mtime && e.size == size {
		runes := e.runes
		flatCacheMu.RUnlock()
		return runes, nil
	}
	flatCacheMu.RUnlock()

	book, err := ReadEpub(epubPath)
	if err != nil {
		return nil, err
	}
	runes := []rune(book.FlatText)

	flatCacheMu.Lock()
	flatCache[epubPath] = flatCacheEntry{mtime: mtime, size: size, runes: runes}
	flatCacheMu.Unlock()
	return runes, nil
}

// collapseSpaces reduces runs of ASCII whitespace introduced by inline
// markup gaps (e.g. `<em>foo</em><em>bar</em>` produces `foobar`, but
// `<em>foo </em> <em> bar</em>` would yield "foo  bar" without this
// pass). CJK content rarely needs this but it keeps cross-source EPUBs
// from rendering with stuttering whitespace.
func collapseSpaces(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}
