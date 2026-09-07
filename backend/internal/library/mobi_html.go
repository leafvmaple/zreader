package library

// MOBI markup → plain text.
//
// The decompressed MOBI payload is HTML, but not well-formed enough for an
// XML parser: unclosed <p>, bare <br>, stray < in prose, entities that never
// got escaped. So this is a deliberate scanner rather than a parse — it only
// needs to answer "where do paragraphs break, and what is the text between
// them", which is all the downstream FormatText → ParseChapters pipeline
// consumes.

import (
	"html"
	"strings"
)

// blockTags end a paragraph when opened or closed. Everything else is
// inline and its text flows into the current paragraph.
var blockTags = map[string]bool{
	"p": true, "div": true, "br": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "ul": true, "ol": true, "tr": true, "td": true, "th": true,
	"blockquote": true, "section": true, "article": true, "table": true,
	"body": true, "center": true, "pre": true,
}

// dropTags have their entire contents discarded, not just their markup.
var dropTags = map[string]bool{
	"script": true, "style": true, "head": true, "title": true,
}

// MobiHTMLToText flattens MOBI markup into paragraphs separated by blank
// lines — the same shape FormatText expects from a .txt source.
func MobiHTMLToText(src string) string {
	var out strings.Builder
	var para strings.Builder

	flush := func() {
		s := strings.TrimSpace(collapseSpaces(para.String()))
		para.Reset()
		if s == "" {
			return
		}
		out.WriteString(s)
		out.WriteString("\n\n")
	}

	skipUntil := ""
	for i := 0; i < len(src); {
		c := src[i]
		if c != '<' {
			if skipUntil == "" {
				para.WriteByte(c)
			}
			i++
			continue
		}

		end := strings.IndexByte(src[i:], '>')
		if end < 0 {
			// Unterminated tag: the rest of the file is text, not markup.
			if skipUntil == "" {
				para.WriteString(src[i:])
			}
			break
		}
		tag := src[i+1 : i+end]
		i += end + 1

		name, closing := tagName(tag)
		if skipUntil != "" {
			if closing && name == skipUntil {
				skipUntil = ""
			}
			continue
		}
		if !closing && dropTags[name] {
			// Self-closing <tag/> drops nothing.
			if !strings.HasSuffix(tag, "/") {
				skipUntil = name
			}
			continue
		}
		if blockTags[name] {
			flush()
		}
	}
	flush()

	// Entities are decoded once at the end rather than per-chunk, so a
	// numeric entity split across the tag scanner still resolves.
	return html.UnescapeString(out.String())
}

// tagName extracts the lower-cased element name from a tag's inner text and
// reports whether it was a closing tag.
func tagName(tag string) (name string, closing bool) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", false
	}
	if tag[0] == '/' {
		closing = true
		tag = tag[1:]
	}
	// Comments and processing instructions have no name worth reporting.
	if strings.HasPrefix(tag, "!") || strings.HasPrefix(tag, "?") {
		return "", closing
	}
	if i := strings.IndexAny(tag, " \t\r\n/>"); i >= 0 {
		tag = tag[:i]
	}
	return strings.ToLower(tag), closing
}
