package library

// Cover extraction.
//
// EPUB sources almost always embed a cover image; MOBI files usually carry
// one as a Palm image record (mobi_cover.go); TXT sources never do.
// The pipeline treats the cover like any other piece of book content:
// it is pulled out of the source at format time and written into the
// cached EPUB (epub_export.go), so the cache stays the single source of
// truth and a re-scan re-applies any extraction fix with no migration.
//
// Serving reads it back out of that cached EPUB on demand — covers are
// a few hundred KB at most and the OS page cache makes the repeat reads
// free, which is cheaper than maintaining a parallel image directory
// that can drift out of sync with the library.

import (
	"archive/zip"
	"fmt"
	"path"
	"strings"
)

// Cover is a decoded cover image plus the media type to serve it as.
type Cover struct {
	Data      []byte
	MediaType string
}

// maxCoverBytes caps what we're willing to copy into the cache. Real
// covers land well under 1 MB; anything larger is a full-page scan that
// would bloat every cached EPUB for no visual gain.
const maxCoverBytes = 4 << 20

// coverMediaTypes maps the image formats browsers render natively to
// their canonical media type. Anything outside this set is skipped
// rather than passed through — an unrecognised blob would only produce
// a broken <img>.
var coverMediaTypes = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".svg":  "image/svg+xml",
}

// ExtractCover opens the EPUB at epubPath and returns its cover image,
// or (nil, nil) when the book has none. Only genuinely broken archives
// produce an error; a missing or unreadable cover degrades to "no
// cover" so one odd file never fails a whole scan.
func ExtractCover(epubPath string) (*Cover, error) {
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

	href := findCoverHref(opf)
	if href == "" {
		return nil, nil
	}
	full := joinEpubPath(path.Dir(opfPath), href)
	data, err := readZipFile(&zrc.Reader, full)
	if err != nil || len(data) == 0 || len(data) > maxCoverBytes {
		return nil, nil
	}
	media := coverMediaTypes[strings.ToLower(path.Ext(href))]
	if media == "" {
		return nil, nil
	}
	return &Cover{Data: data, MediaType: media}, nil
}

// findCoverHref resolves the manifest href of the cover image, trying
// the three conventions real-world EPUBs use, in descending order of
// reliability:
//
//  1. EPUB 3 — manifest item carrying properties="cover-image".
//  2. EPUB 2 — <meta name="cover" content="<manifest-id>"/>.
//  3. Neither — an image item whose href looks like a cover. Calibre and
//     several Chinese converters emit no cover metadata at all but do
//     name the file cover.jpeg, so this recovers a real cover often
//     enough to be worth the small risk of picking an illustration.
//
// Returns "" when nothing matches.
func findCoverHref(opf *opfPackage) string {
	byID := make(map[string]string, len(opf.Manifest.Items))
	for _, it := range opf.Manifest.Items {
		byID[it.ID] = it.Href
		if strings.Contains(it.Properties, "cover-image") {
			return it.Href
		}
	}
	for _, m := range opf.Metadata.Metas {
		if strings.EqualFold(m.Name, "cover") && m.Content != "" {
			if href, ok := byID[m.Content]; ok {
				return href
			}
		}
	}
	for _, it := range opf.Manifest.Items {
		if !strings.HasPrefix(it.MediaType, "image/") {
			continue
		}
		if strings.Contains(strings.ToLower(path.Base(it.Href)), "cover") {
			return it.Href
		}
	}
	return ""
}

// HasCover reports whether the EPUB at epubPath declares a cover image
// we can serve. It stops at the manifest rather than reading the image
// bytes, so the scanner can record the flag for every book without
// paying to decompress art it isn't going to send anywhere.
func HasCover(epubPath string) bool {
	zrc, err := zip.OpenReader(epubPath)
	if err != nil {
		return false
	}
	defer zrc.Close()

	opfPath, err := readContainerXML(&zrc.Reader)
	if err != nil {
		return false
	}
	opf, err := readOPF(&zrc.Reader, opfPath)
	if err != nil {
		return false
	}
	href := findCoverHref(opf)
	return href != "" && coverMediaTypes[strings.ToLower(path.Ext(href))] != ""
}
