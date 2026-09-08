# Known Issues / Follow-ups

Running list of small things deferred during normal work. Add a line when
something is worth remembering but not worth fixing inline.

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

## Covers — PDFs drawn rather than scanned still fall back

`backend/internal/library/pdf_cover.go`, `pdf_flate_cover.go`

Two paths now. The cheap one scans the head of the file for stream objects
whose payload starts with a JPEG SOI — scanned PDFs (page 1 *is* one JPEG)
and most text-layer PDFs with cover plates. When that comes up empty, the
second decodes raw Flate-compressed samples: 8-bit DeviceGray / DeviceRGB /
ICCBased(N=1,3), and Indexed over those.

Still not covered, deliberately: DeviceCMYK and Separation (need ink models
to look right), Lab, DeviceN, sub-byte depths, 16-bit samples, `/Decode`
arrays, and images with an SMask. Each would need a guess, and a
wrong-coloured cover is worse than the generated one it replaces.

Also still not covered: a PDF whose page 1 is *drawn* — vector text and
paths, no image object at all. That needs a page rasteriser, and every
pure-Go option is heavy while the CGO ones cost the single static binary.

Follow-up worth doing if a library ever holds many such PDFs: the Flate
path runs per cover request for image-only PDFs (`handlers_books.go`
serves those from source, with an ETag but no stored cover). Inflating and
re-encoding a full-page raster is far more expensive than the JPEG scan it
falls back from. Caching the extracted cover next to the source, the way
`ocr.go` caches its searchable PDF, would fix it.

## Shelf — only the list and grid are windowed

`frontend/src/hooks/useWindowedList.ts`

The "继续阅读" strip is capped at five entries by construction, so it never
needs windowing.

Resolved, and worth recording because the original entry had the diagnosis
backwards. It claimed a row changing height while mounted kept a stale
height until it left and re-entered the window. Measured against a
120-book shelf, that never reproduced: `measure(index)` returned a fresh
closure every render, so React re-ran every visible row's ref callback on
every render and re-measured everything. The staleness was hidden — and
paid for with a forced layout read per visible row per render, 438 of them
across 21 scroll steps.

A single ResizeObserver now reports size changes, the ref callbacks are
stable per index, and the same scroll costs 98. The behaviour that entry
asked for was already there; what it cost is what got fixed.

## Reader — EPUB inline markup is flattened

`backend/internal/library/epub_reader.go`

The flat text the reader serves is paragraphs and nothing else. `<br>`
now splits paragraphs and images leave a marker, but bold, italic, lists
and block quotes all arrive as identical paragraphs, and only the first
heading in a file survives — as the chapter title. A second `<h2>` in the
same chapter is dropped outright.

This is not a bug in the reader so much as a property of the format
between it and the parser. Everything downstream indexes that flat text:
chapter detection is line-anchored on it, search offsets are rune
positions into it, the reader's content endpoint slices it, and the AI
export cleans it. Carrying markup means deciding what a "character
offset" means when characters have markup around them, which is a change
to the contract, not to one function.

Worth doing if illustrated or heavily formatted books become common in a
library. For prose — which is what this corpus is — flat is the right
representation and the cheapest one.
