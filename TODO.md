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
