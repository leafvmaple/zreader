# AGENTS.md

Guidance for AI coding assistants (Claude, etc.) working in this repo.
Humans should read [README.md](README.md) for what the app is and how to
run it; this file is the orientation a fresh agent session needs to be
productive in 30 seconds.

---

## What this is

**zreader** is a self-hosted Chinese e-book (TXT) reader. Go backend +
embedded React SPA, single binary / single container, sqlite-backed.
Single-user by default; the user is whoever the request comes from.

## Layout

```
backend/
  cmd/zreader/         entrypoint
  cmd/decodetest/      one-off encoding-detection CLI for debugging
  internal/
    config/            paths + env wiring
    library/           file scanning, encoding detection, TXT formatting,
                       chapter parsing, author metadata
    server/            HTTP router + handlers (net/http stdlib mux)
    store/             sqlite layer (modernc.org/sqlite — CGO-free)
    webui/             embed.FS for the built SPA
frontend/              Vite + React + TypeScript SPA
data/                  sqlite db (host-side compose mount, gitignored)
books/                 user's library (host-side compose mount, gitignored)
TODO.md                cross-cutting follow-ups noted during normal work
```

## Pipeline: scan → format → parse

This is the most important architectural decision in the codebase. Read
it before touching anything in `internal/library/`.

```
ScanFolder(folder)
  ├─ Phase 0 — RestoreBackups       ← migrate legacy *.txt.bak (one-time
  │                                    leftover from old in-place format)
  ├─ Phase 1 — for each top-level *.txt source:
  │     FormatToCache(folder, source)
  │       ├─ DetectAndDecode(raw)
  │       ├─ DetectMetadata(text)
  │       ├─ ResolveMetadata(...)   ← by:line ▸ filename ▸ defaults
  │       ├─ FormatText(text)
  │       │    ├─ buildLogicalLines (rejoin wrap breaks)
  │       │    ├─ splitAtChapters   (promote 第X章 to own paragraph)
  │       │    └─ splitTitleFromBody (peel title off glued body)
  │       └─ write → <folder>/<author>/<title>.txt (atomic)
  ├─ Phase 2 — for each cached path:
  │     ingestFile(cachedPath, title, author)
  │       ├─ DetectAndDecode(cached)
  │       ├─ ParseChapters(text, nil)  ← only sees normalised input
  │       └─ store.UpsertBook + store.ReplaceChapters
  └─ Phase 3 — store.DeleteBooksMissing(cached paths)
```

**Source files are never modified.** The user's TXT at the scan
folder's top level is read-only conceptually; the formatted output
lives separately under `<folder>/<author>/<title>.txt`. Re-running
`FormatToCache` overwrites the cached file on every scan, so a bug
fix in `FormatText` takes effect on the next scan with no manual
intervention.

**The contract `FormatText` enforces for downstream chapter detection:**
- Wrap-only line breaks are removed (paragraphs reconstructed by the
  indented-paragraph convention).
- Structured chapter markers (`第X章/折/节/回/卷/篇/集/部`, `Chapter N`)
  glued mid-paragraph after sentence/quote-end punctuation get promoted
  to their own paragraph.
- Symmetric `XXXX，XXXX` subtitles glued to body get split off (the
  4+3 outlier is a known limitation — see TODO.md).
- Exactly one full-width `　` between chapter marker and subtitle.
- Every chapter marker stands at the **start of a line**.

**Therefore `txt.go`'s `ChapterPattern` is line-anchored only** — no
sub-line / preceding-punctuation gymnastics. If a real-world TXT
defeats chapter detection, **fix `FormatText` first**, not the chapter
pattern. Growing the pattern with format-specific fallbacks defeats the
whole separation.

### Source vs cached file layout

```
books/                          ← scan folder (the docker mount)
  照日天劫 - 佚名.txt           ← source (top-level, untouched)
  铸蝉记 - 佚名.txt             ← source (top-level, untouched)
  佚名/
    照日天劫.txt                ← formatted cache (overwritten each scan)
  轩辕悬/
    铸蝉记.txt                  ← formatted cache (overwritten each scan)
```

Only **top-level** `*.txt` files are treated as sources. Anything in a
subdirectory is left alone by the format pass (Phase 1 ignores it) and
ingested as-is if discovered (Phase 2's loop only sees what Phase 1
produced, so manual content under subdirs becomes an "orphan" — not
auto-tracked). Don't put sources in subfolders; rename or move them to
the top level.

### Migration from the legacy in-place flow

Older builds rewrote the source TXT in place and saved the original as
`<path>.bak`. Phase 0 (`RestoreBackups`) detects these and reverses the
mutation: `<path>.bak` becomes `<path>` again, `.bak` is removed. From
then on the source is pristine and the cached file under
`<author>/<title>.txt` becomes the only file the reader serves.

## Chapter detection — tiered

`ParseChapters` runs four line-anchored patterns and merges:

1. **`ChapterPattern`** — structured `第X章/折/…` + `Chapter N`. Capture
   group 1 is the title; subtitle arm prefers a parenthesised part
   marker (`（上）/（中）/（下）/（补）/（外传）/…`) when present,
   otherwise caps at 10 chars.
2. **`NamedChapterPattern`** — `楔子/序章/序言/序篇/引子/前言/尾声/后记/
   番外/终章/结语`. Line-anchored with a small slop; these words appear
   in body text so we don't accept them inline.
3. **`BracketedNumeralPattern`** — `「一」/【3】/〈12〉/[二十]` style.
   Whole-line bracketed numeral (CJK or Arabic). Bracket set restricted
   to `「『【〈[` / `」』】〉]` — `《》` / `（）` / `()` excluded
   (book-title / inline use, too noisy). Treated as primary tier — the
   bracket pairing is a strong signal on its own, so no threshold gate.
4. **`LooseDigitPattern`** — bare numeric divider lines (`1`, `12`).
   Merged in only when the primary tiers are sparse (< 3 matches);
   stray numeric lines in body text would false-positive otherwise.

If everything misses → synthetic `正文` chapter at offset 0.

## Author + title from `by:` line

`DetectMetadata` scans the first 30 non-empty lines for an inline
`by:` tag (`Title by:Author`, ASCII or full-width colon, case-
insensitive on `by`). Captured author overrides nothing; captured title
overrides the filename-derived title when present.

## Test corpus

Three real books in `books/` (gitignored — won't be in fresh clones,
the tests `t.Skipf` when absent):

- **《铸蝉记》** — well-formatted: every paragraph is one line, blank
  lines between, chapter dividers are `楔子` then bare digits `1`..`10`.
  Exercises `NamedChapterPattern` + `LooseDigitPattern` merge.
- **《照日天劫》** — hard-wrapped at fixed width with paragraphs broken
  across many physical lines. Chapter markers (`第X折`) glued mid-line
  after `」` / `）` / `。`, plus `（上）（中）（下）（补）` multi-part
  markers. Exercises `FormatText`'s wrap-rejoin + chapter-split, then
  line-anchored detection on the formatted output.
- **《十景缎》** — bracketed-numeral chapter markers `「一」`..`「二百
  二十一」` on their own lines. Exercises `BracketedNumeralPattern` and
  the regex's CJK compound-number coverage (零/百/十/individuals).

When a TXT defeats detection, the first thing to do is `git diff` the
formatted output vs the original — usually a format-side fix, not a
parser-side fix.

## Conventions

- **Commits** are conventional (`feat(scope): …`, `docs(scope): …`,
  `chore: …`). See git log for style.
- **Comments**: don't add comments unless the WHY is non-obvious.
  Don't narrate WHAT the code does — names already do that. The
  existing `txt.go` / `format.go` use somewhat richer doc comments
  because the regex/heuristic intent is otherwise opaque; follow that
  pattern when adding similar logic.
- **No emojis** in code or commits.
- **Don't create `.md` docs** unless asked. AGENTS.md, README.md, and
  TODO.md are the only Markdown files that should exist.
- **`TODO.md`** is the catch-all for follow-ups deferred during normal
  work (one-line entries with file path + 1-2 sentence why). Update it
  when shipping a partial fix; clear it when fixed.

## Running things

```bash
# Backend tests (must cd into backend/ — go modules root is there)
cd backend && go test ./...
cd backend && go test ./internal/library/ -v   # noisy detail

# Build the whole binary
cd backend && go build ./...

# Run dev backend (frontend separately via pnpm dev, or use docker compose)
cd backend && go run ./cmd/zreader

# Docker (default: build from local checkout — see README "Docker workflow")
docker compose up -d --build
```

## Things easy to get wrong

- **Scanner re-runs the full format → ingest pipeline unconditionally**
  every scan. Cached files under `<author>/<title>.txt` are overwritten,
  and `ReplaceChapters` wipes + re-inserts. So if you change
  `FormatText` or `ParseChapters` and want the DB to reflect it, just
  trigger a re-scan — no migration needed.
- **`book.Path` is the source of truth for file location**; database
  IDs are stable across scans because path is `UNIQUE`. Don't rely on
  IDs being densely numbered or insertion-ordered.
- **`CharOffset` is rune-indexed, `ByteOffset` is byte-indexed** into
  the decoded UTF-8 string. The reader endpoint slices by rune.
- **Encoding**: `DetectAndDecode` (uses `chardet` + `golang.org/x/text`)
  handles GBK / GB18030 / UTF-8 / etc. After `FormatBook` writes, the
  file is always UTF-8 (without BOM).
- **`books/` and `data/`** at repo root are docker-compose host mounts.
  Don't commit anything that lands there. They're gitignored.
- **PowerShell on Windows**: the Bash tool works for git/build commands
  cross-platform; PowerShell-only constructs (`$null`, `2>$null`) only
  matter when using the PowerShell tool. Stick to Bash for portability
  unless you specifically need a Windows-only call.
