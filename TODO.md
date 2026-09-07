# Known Issues / Follow-ups

Running list of small things deferred during normal work. Add a line when
something is worth remembering but not worth fixing inline.

## PDF — image-only sources still need OCR

`backend/internal/library/pdf.go`

Text-layer PDFs import through the normal text pipeline. Scanned/image-only
PDFs are readable through the source-backed page reader, but they are not
searchable because there is no OCR text layer yet.

## Format — asymmetric subtitle over-splits

`backend/internal/library/format.go`

`titleBodySplitPattern` only accepts symmetric subtitles (4+4, 5+5,
3+3). A real 4+3 subtitle is matched as 4+4 by the greedy engine,
which steals one body char into the title.

**Example.** Synthetic text shaped like `AAAA，BBB` can be consumed as a
4+4 title boundary (`AAAA，BBBC`), stealing the body's first character into
the title.

**Why this is hard to fix structurally:** distinguishing 4+3 from 4+4
requires knowing the subtitle's lexical boundaries, which a regex can't
infer without semantic context. The user explicitly opted out of NLP
for now.

**Possible fixes when revisited:**
- Per-book override hint in the scanner (e.g. metadata `subtitle_form:
  4+3`).
- A pluggable Chinese word segmenter (jieba / gse) used only at the
  title boundary — heavy dep for one case.
- Manual `<filename>.chapters.json` override file in `books/` for any
  book where automatic split is wrong.

## Source — bracketed-numeral source markers can have gaps

Some corpus fixtures used to validate `BracketedNumeralPattern` have missing
numbered markers even though the surrounding prose flows continuously. This
looks like source typo/transcription damage rather than a parser miss.

The parser is doing all it can when the marker is absent; fixing this requires
either a cleaner source or a manual `<filename>.chapters.json` override
(cross-referenced with the "asymmetric subtitle" entry above as a candidate
use case for the same mechanism).

## Covers — PDF sources get no cover art

`backend/internal/library/cover.go`

EPUB sources (and MOBI/AZW, which convert to EPUB first) have their cover
extracted at format time and carried into the cached EPUB. PDFs don't: the
obvious source would be a raster of page 1, and `rsc.io/pdf` cannot do it —
`Value.Reader()` *panics* on any filter it doesn't implement, and page images
are almost always `DCTDecode`, so even lifting the embedded JPEG out of an
image-only PDF's XObject is not reachable through the exported API.

Options when revisited:
- A tiny hand-rolled PDF object scanner that finds the first `DCTDecode`
  stream and passes the bytes through as `image/jpeg`. Covers scanned PDFs
  (where page 1 *is* one image) and needs no new dependency.
- A real rasteriser for text-layer PDFs. Every pure-Go option is heavy and
  the CGO ones break the "single ~23 MB container" property.

Until then PDFs fall back to the generated cover, same as TXT.
