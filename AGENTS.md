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

## Privacy — corpus content stays out of the repo

The user's `books/` directory is private. **None of its contents
belong in source control — not in commit messages, not in code, not
in tests, not in docs, not in PR descriptions.** Specifically off
limits:

- **Book titles** and **author names** harvested from the user's
  library — whether from filenames, from `by:` lines, or from
  anywhere else. Treat them like API keys: never echo them back into
  any tracked artefact.
- **Chapter titles** and **body text** from those books. Quoted
  prose, specific 章/折/回 names, sample paragraphs — all private.
- **Per-book numeric fingerprints** that pin a specific source
  (e.g. "the 217-chapter book", "char_count 63833 → 63816").
  Structural invariants are fine when they don't identify the source
  — "≥10 chapters", "char_count drops by the stripped paragraphs"
  works; precise pre/post numbers don't.

When you need a concrete example, use synthetic stand-ins:

- ASCII placeholders — `BookA`, `AuthorX`, `<title>`, `<author>`.
- Synthetic CJK from neutral vocabularies — 天干 (`甲乙丙丁戊己庚辛`),
  地支 (`子丑寅卯辰巳`), 四季方位 (`东南西北春夏秋冬`). Obvious
  composites that wouldn't be mistaken for a real title.
- Generic chapter markers (`第一章`, `「一」`, `楔子`, `Chapter N`)
  are fine — they're structural categories shared across many books,
  not corpus identifiers.

The corpus regression test (`TestFormatToCache_Corpus` in
`format_test.go`) reads its assertions from a JSON config pointed at
by `$ZREADER_TEST_CORPUS`. **That config file is the only place real
corpus identifiers live** — gitignored, kept locally by the user.
See `backend/internal/library/testdata/corpus.example.json` for the
schema.

**Why this matters.** A leak in a commit message is expensive to
undo: even after `git filter-branch` + force-push, GitHub keeps the
unreachable commit reachable by SHA for ~30–90 days. Don't write it
in the first place — it's much cheaper than scrubbing it later.

## Layout

```text
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

```text
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

```text
books/                              ← scan folder (the docker mount)
  <title-A> - <author-X>.txt        ← source (top-level, untouched)
  <title-B> - <author-X>.txt        ← source (top-level, untouched)
  <author-X>/
    <title-A>.txt                   ← formatted cache (overwritten each scan)
    <title-B>.txt                   ← formatted cache (overwritten each scan)
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

End-to-end format → ingest assertions live in
`TestFormatToCache_Corpus` (see `backend/internal/library/format_test.go`).
The test reads a JSON config pointed at by `ZREADER_TEST_CORPUS` and
`t.Skipf`s when the env var is unset, so corpus identifiers stay out
of the repo entirely. See `backend/internal/library/testdata/
corpus.example.json` for the schema; per entry you can assert
`min_chapters` as a floor, exact chapter titles via `contains`, and
prefix matches via `prefixes`. The corpus files themselves live
wherever the user keeps them — `books/` (gitignored) is the typical
choice.

The shapes the corpus has historically exercised (set `path` in
`corpus.json` to a file of each kind to keep coverage):

- **Well-formatted, named + numeric dividers** — one paragraph per
  line, blank-line separators, `楔子` then bare digits `1`..`N` as
  chapter markers. Exercises `NamedChapterPattern` +
  `LooseDigitPattern` merge.
- **Hard-wrapped, structured markers + multi-part chapters** —
  fixed-width hard-wrapping with paragraphs broken across many
  physical lines; `第X章/折` markers glued mid-line after `」` / `）`
  / `。`, plus `（上）（中）（下）（补）` part markers. Exercises
  `FormatText`'s wrap-rejoin + chapter-split, then line-anchored
  detection on the formatted output.
- **Bracketed-numeral chapters** — `「一」`..`「N」` markers on their
  own lines. Exercises `BracketedNumeralPattern` and the regex's CJK
  compound-number coverage (零/百/十/individuals).

When a TXT defeats detection, the first thing to do is `git diff` the
formatted output vs the original — usually a format-side fix, not a
parser-side fix.

## Conventions

- **Privacy first.** Before writing any text that will be committed
  (code, commit message, doc, test, PR body), check it against the
  "corpus content stays out of the repo" rule at the top. When in
  doubt, use synthetic placeholders.
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

## Workflow — you own the regression check

The user grades on the final result, not intermediate logic. **Whatever
you change (bugfix, refactor, new feature) you exercise end-to-end
yourself before declaring done.** A plausible-looking diff is not a
fix; "I think this works" failed before and will fail again.

### The loop

1. **Reproduce** the bug / drive the feature yourself first. Capture
   the exact observable that's wrong (scrollTop value, parsed chapter
   count, API response shape) — that's your acceptance criterion.
2. **Change** the code.
3. **Re-run the same reproduction.** If the observable still mismatches,
   the fix is wrong — go back to step 2. Don't stop at the first
   plausible diff. The TOC-jump bug was a three-step race
   (`scroll-behavior: smooth` × `onScroll` × `extendUp/compensate`)
   hiding behind a one-step symptom; the first fix was correct but
   incomplete.
4. **Lock it in** as a test when cheap: Go unit test against the
   `books/` corpus for parser / formatter bugs; a one-off Playwright
   probe is fine to delete after verifying — only keep it if the
   regression is likely to recur and the probe is short.

This applies to new features too — building the feature without
driving the real flow that uses it counts as half done.

### Test gate policy

- **Before pushing a feature change**, run incremental tests that match the
  changed surface and add or update tests for the feature first. Examples:
  backend-only changes run the touched package tests (or the narrowest useful
  `go test` target); frontend-only changes run typecheck/build or the targeted
  browser probe; parser/import changes run the relevant library tests and any
  available corpus regression for that area.
- **Before creating a release or pushing a release tag**, run the full project
  test gate: `cd backend && go test ./...`, `cd frontend && corepack pnpm
  test:e2e`, plus any available corpus regression or release-specific checks.
  Do not tag until these pass.
- If an expected test cannot run because the local environment is missing a
  dependency or permission, state that explicitly before pushing; do not
  release unless the user accepts the gap.

### Tool chain (Windows host)

- **pnpm** is not on PATH by default. Bring it up via corepack —
  `corepack enable && corepack prepare pnpm@latest --activate`, then
  `cd frontend && pnpm install`.
- **node / npx** live at `C:\Program Files\nodejs\` and are already on
  PATH.
- **Dev backend port conflict**: `:8080` is usually held by
  `docker compose` running the production image. Either `docker
  compose down` first, or run the dev backend on a free port
  (`ZREADER_PORT=18080 go run ./cmd/zreader`) and proxy vite to it.
- **Background processes**: launch dev servers with the Bash tool's
  `run_in_background` and tear them down before finishing.

### Driving the SPA — Playwright probe pattern

Reader bugs (chapter jumps, scroll restore, progress reporting) are
not provable from reading TS source. CSS effects (`scroll-behavior`,
`overflow-anchor`, `offsetParent`, `padding`) interact with React
state batches and async fetches in ways static analysis won't surface.
Drive the real browser:

```bash
# `playwright` is a permanent devDependency — `pnpm install` brings the
# npm package. The chromium browser binary itself is NOT an npm package;
# it lives in a per-user cache (Windows:
# %USERPROFILE%\AppData\Local\ms-playwright\, Linux/mac:
# ~/.cache/ms-playwright/). One-time install per machine:
#
#   pnpm exec playwright install chromium
#
# After that, every subsequent probe reuses the cached binary across
# sessions / reboots — no reinstall needed until the playwright npm
# version bumps to one that requires a different chromium build. Neither
# the dev dep nor the cached binary ship in the docker image (the
# production Dockerfile only copies what `pnpm build` emits, and the
# per-user playwright cache is outside node_modules entirely).

# probe.mjs — minimal pattern:
#   - chromium.launch({ headless: true })
#   - page.on('console', ...) to capture any [zreader] logs you added
#   - page.goto('http://localhost:5173/') and click through the flow
#   - page.evaluate(() => ({ scrollTop, anchorOffsetTop, anchorRectTop,
#       computed paddingTop, chromeHeight, ... }))
#   - assert expected vs observed, exit non-zero on mismatch

node probe.mjs

# Delete probe.mjs before commit unless it becomes a durable, fast
# regression test. Leave playwright in devDependencies — removing it
# just to re-add next session is wasted churn.
```

Add `console.log('[zreader] ...', { ... })` temporarily in the suspect
path while iterating; remove before shipping. Logs alone aren't
enough — without driving the browser you won't see the real timing of
`scroll-behavior: smooth` and async extension.

### Where the proxy points

`vite.config.ts` proxies `/api` to `127.0.0.1:8080`. So vite dev at
`http://localhost:5173` talks to **whatever is on :8080** — usually the
docker container. If you're testing a backend change too, either stop
docker and run `go run ./cmd/zreader` on :8080, or rebuild the docker
image (`docker compose up -d --build`) so the embedded SPA also picks
up your frontend change. Hitting `http://localhost:8080` directly
shows the docker-embedded SPA bundle, **not** vite's live code — this
has bitten the user once already ("还是不对" while testing the wrong
URL).

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
