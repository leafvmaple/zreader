# Known Issues / Follow-ups

Running list of small things deferred during normal work. Add a line when
something is worth remembering but not worth fixing inline.

## Format — asymmetric subtitle over-splits

`backend/internal/library/format.go`

`titleBodySplitPattern` only accepts symmetric subtitles (4+4, 5+5,
3+3). A real 4+3 subtitle is matched as 4+4 by the greedy engine,
which steals one body char into the title.

**Example.** A corpus book has one 4+3 subtitle in its chapter set
(`AAAA，BBB`). The greedy matcher consumes `AAAA，BBBC` (4+4),
stealing the body's first character `C` into the title; the body
then reads correctly minus that first character. 11 of the book's
12 chapters split cleanly; this is the one outlier.

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

## Source — bracketed-numeral corpus has 4 missing chapter numbers

One of the corpus books used to validate `BracketedNumeralPattern` is
numbered `「一」`..`「二百二十一」` but is missing markers for chapters
**50, 103, 135, 180** — the sequence jumps directly from 49→51,
102→104, 134→136, 179→181. Surrounding prose flows continuously
across each gap, so this is almost certainly a typo/transcription
error in the source rather than missing content.

Net result: 217 detectable chapters out of a nominal 221. The parser
is doing all it can — fixing this requires either a cleaner copy of
the source or a manual `<filename>.chapters.json` override
(cross-referenced with the "asymmetric subtitle" entry above as a
candidate use case for the same mechanism).
