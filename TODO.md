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

## Shelf — windowing measures on scroll, not on resize

`frontend/src/hooks/useWindowedList.ts`

Row heights are measured when a row renders and cached by index. A row that
changes height *while mounted* without a re-render — a webfont finishing its
swap, say — keeps its stale height until it leaves and re-enters the window.
In practice the drift is a pixel or two and self-corrects on the next pass;
a ResizeObserver per row would fix it exactly, at the cost of an observer
per visible row.

Also: only the list and grid are windowed. The "继续阅读" strip is capped at
five entries by construction, so it never needs it.
