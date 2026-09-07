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

## Covers — PDFs with no embedded JPEG still fall back

`backend/internal/library/pdf_cover.go`

PDF covers are lifted by scanning the head of the file for stream objects
whose payload starts with a JPEG SOI, which covers scanned PDFs (page 1 *is*
one JPEG) and most text-layer PDFs with cover plates.

Not covered: PDFs that store images as raw Flate-compressed samples rather
than an embedded JPEG. Those aren't a file in any format a browser reads,
and turning them into one means implementing PDF colour-space handling
(DeviceN, Indexed, ICCBased, `/Decode` arrays, SMask alpha) — a lot of
surface for the remaining minority. They fall back to a generated cover.

A page rasteriser would solve both this and any PDF whose page 1 is drawn
rather than scanned, but every pure-Go option is heavy and the CGO ones cost
the single static binary.

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
