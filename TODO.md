# Known Issues / Follow-ups

Running list of small things deferred during normal work. Add a line when
something is worth remembering but not worth fixing inline.

## Format — asymmetric subtitle over-splits

`backend/internal/library/format.go`

`titleBodySplitPattern` only accepts symmetric subtitles (4+4, 5+5,
3+3). A real 4+3 subtitle is matched as 4+4 by the greedy engine,
which steals one body char into the title.

**Example.** 《照日天劫》第六折's subtitle is `连天铁障，将军箓`
(4+3). The matcher takes `连天铁障，将军箓法` (4+4) so the split
produces:

- title: `第六折连天铁障，将军箓法`
- body: `文、商二姝相偕入观。`

The body reads correctly minus its first character. 11 of the book's
12 折 chapters split cleanly; this is the one outlier.

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
