# zreader

A self-hosted ebook reader. Point it at a directory of `.txt`, `.epub`, `.pdf`,
`.mobi`, or `.azw3` files; it scans, detects/normalises text where available,
parses chapters, and gives you a web reader with cross-device progress sync.
Single binary, single ~23 MB container.

> Status: **MVP**. TXT, EPUB, text-layer PDF, image-only PDF (paged, or
> searchable with optional OCR), and converter-backed MOBI/AZW/AZW3 import are
> supported.

## Quick start

```bash
docker run -d \
  --name zreader \
  -p 8080:8080 \
  -v $(pwd)/data:/data \
  -v /path/to/your/books:/library \
  leafvmaple/zreader:latest
```

Open <http://localhost:8080>. The first visit asks you to create an admin
account; everything after that is behind a login. Then click the scan button
and start reading.

The UI presents itself as **枫读**; `zreader` remains the project, image and
API name. Change the display name in `frontend/src/brand.ts` (and the
`<title>` in `frontend/index.html`, which is static so the tab is never
briefly titled something else).

### Where the image is published

| Registry             | Image                               |
| -------------------- | ----------------------------------- |
| Docker Hub (default) | `leafvmaple/zreader:latest`         |
| GHCR                 | `ghcr.io/leafvmaple/zreader:latest` |

Both registries serve the same image. Docker Hub is the default `docker pull`
target — no `--registry` flag needed. Architectures: `linux/amd64`,
`linux/arm64`.

### docker compose

The repo ships a [`docker-compose.yml`](docker-compose.yml) configured to
**build from source by default** — clone, drop your books into `./books/`,
run `docker compose up -d --build`, done. See
[Docker workflow](#docker-workflow) below for switching to the published
image instead.

## Configuration

All optional. The defaults match the volume layout above.

| Env var                | Default     | Notes                                                                 |
| ---------------------- | ----------- | --------------------------------------------------------------------- |
| `ZREADER_PORT`         | `8080`      | HTTP listen port.                                                     |
| `ZREADER_DATA_DIR`     | `/data`     | SQLite database (`library.db`) lives here. Persist this.              |
| `ZREADER_LIBRARY_PATH` | `/library`  | One or more book roots, OS-listsep separated (`:` on Linux).          |
| `ZREADER_EBOOK_CONVERT`| unset       | Optional path to Calibre `ebook-convert`, used only as a fallback for MOBI files the native reader declines. |
| `ZREADER_OCR`          | unset       | Set to `1` to OCR scanned PDFs on import. See [OCR](#ocr-for-scanned-pdfs). |
| `ZREADER_OCR_CMD`      | unset       | Path to `ocrmypdf`. Setting it also enables OCR.                      |
| `ZREADER_OCR_LANG`     | `chi_sim+eng` | Tesseract language codes passed to `ocrmypdf -l`.                   |
| `ZREADER_OCR_TIMEOUT`  | `30m`       | Per-file OCR bound, as a Go duration (`45m`, `2h`).                   |

**Library roots must be writable.** Formatted output is cached inside each
root as `<author>/<title>.epub` (see [file layout](AGENTS.md#source-vs-cached-file-layout)),
so a read-only mount fails every scan.

Multiple library roots:

```bash
docker run ... \
  -v /mnt/novels:/novels \
  -v /mnt/tech-books:/tech \
  -e ZREADER_LIBRARY_PATH=/novels:/tech \
  leafvmaple/zreader:latest
```

## NAS deployment

The container runs as **UID 1000 / GID 1000**, with `wheel` (GID 10) as a
supplementary group. This matches the default admin user on UGREEN UGOS Pro,
Synology, and QNAP — bind-mounted shared folders (typically owned by
`1000:10`) are accessible out of the box even when POSIX ACLs restrict
"other" access.

If your host uses a different admin UID, override at run time:

```yaml
services:
  zreader:
    image: leafvmaple/zreader:latest
    user: "1005:100"     # match your host owner's UID:GID
    ...
```

The `./data` host directory must also be owned by the running UID so SQLite
can write to it.

## What works

- TXT (UTF-8 BOM / UTF-8 / GBK / GB18030 / pure ASCII auto-detect)
- EPUB import via the same cached-EPUB reader used internally.
- Text-layer PDF import: extracted text is normalised through the TXT chapter
  parser, then cached as EPUB.
- Image-only/scanned PDF import: readable as pages out of the box, and
  fully searchable when OCR is enabled — see [OCR](#ocr-for-scanned-pdfs).
- MOBI/AZW/AZW3 import with no external tools. HUFF/CDIC-compressed files
  (uncommon) fall back to Calibre `ebook-convert` when one is configured.
- Manual chapter override sidecars: put `<book>.chapters.json` next to a source
  file to replace automatic chapter parsing.
- EPUB text fidelity: nested navigation, readable list/blockquote/footnote
  blocks, and image alt text are preserved in the flat reader text.
- PDF text cleanup removes repeated page headers/footers before chapter parsing.
- Chapter parsing: Chinese `第X章/节/回/卷`, English `Chapter N`, bracketed
  CJK numerals (`「一」`, `【3】`, `〈12〉`). Falls back to a single "正文"
  chapter when no markers are found.
- Library scan: re-runs format → ingest on each scan so parser/import fixes
  apply as soon as the library is scanned again.
- Cover art: EPUB (and converted MOBI/AZW) covers are extracted at scan time
  and served from the library; everything else gets a generated cover.
- Reading view: per-chapter lazy load, chapter drawer that opens on your
  current chapter, a draggable progress bar, 6 themes × 4 font sizes,
  progress auto-sync with stale-write protection.
- Fonts: three system stacks out of the box, no network calls. Extra
  webfonts are opt-in — see [Reading fonts](#reading-fonts).
- Cleaned export for LLM/RAG use — see [Export for AI](#export-for-ai).
- Accounts with per-user reading progress and bookmarks, admin/user roles,
  and session login — see [Accounts](#accounts-and-login).
- Keyboard: ←/→/PageUp/PageDown/Space turn pages, Home/End jump, Esc closes.

## What's missing

- No rate limiting beyond the login endpoint, and no audit log.
- No password reset by email — an admin resets other people's passwords, and
  a lost sole-admin password needs the recovery step in
  [Accounts](#accounts-and-login).
- Scanned PDFs need an external OCR tool; there is no built-in engine.
- MOBI/AZW/AZW3 files using HUFF/CDIC compression need Calibre
  `ebook-convert`; the native reader handles the `none` and PalmDOC schemes.

### Accounts and login

Every API route except the health check and the auth endpoints requires a
session. The first visit to a fresh install shows a setup screen instead of
the library; the account it creates is an admin.

**Upgrading from v0.9 or earlier:** the first account you create adopts the
reading progress and bookmarks recorded before accounts existed, so your
positions carry over. That adoption happens once, for the first account — so
create yours before handing the URL to anyone else.

Admins manage accounts from the shelf header (the person icon → 用户管理):
add users, switch roles, remove accounts. Removing an account deletes its
progress and bookmarks; the library itself is shared and untouched.

Details worth knowing:

- Passwords are bcrypt hashes. Sessions are HttpOnly cookies, stored
  server-side as a SHA-256 of the token, and last 30 days.
- Changing a password signs that account out everywhere else.
- Failed logins are throttled per username after 5 attempts.
- The last admin cannot be demoted or deleted.
- The cookie is marked `Secure` when the request arrives over HTTPS,
  including via `X-Forwarded-Proto` from a reverse proxy. Over plain HTTP it
  is not, because a `Secure` cookie there would never be sent back.

**Lost the only admin password?** There is no email reset. Stop the
container, delete the `users` row with any SQLite client
(`DELETE FROM users;` in `<data>/library.db`), and restart — the setup screen
comes back. Reading progress survives, since it is keyed by user id and the
new account re-adopts nothing; back up `library.db` first.

### OCR for scanned PDFs

A scanned PDF has no text layer, so it can't be searched, chapter-parsed or
exported — only paged through. With OCR enabled, zreader turns it into an
ordinary text-layer PDF on import and everything downstream works normally.

The work goes to [ocrmypdf](https://ocrmypdf.readthedocs.io/), the same way
MOBI import goes to Calibre. There's no built-in engine: no pure-Go OCR is
good enough for Chinese, and the CGO bindings to Tesseract would cost the
single static binary and the ~23 MB image.

**It is off by default and does not auto-detect.** Unlike `ebook-convert`,
OCR takes minutes per book, and silently adding half an hour to a library
scan because a tool happens to be on `PATH` is not a pleasant surprise:

```bash
docker run ... -e ZREADER_OCR=1 ...
```

Results are cached beside the cached EPUB as `<author>/<title>.ocr.pdf` and
reused until the source file changes, so only the first scan pays the cost.
A failed or fruitless OCR leaves the book readable as pages — the behaviour
you get without OCR at all.

The published image does **not** ship ocrmypdf; Tesseract, Ghostscript and
the Chinese language data together are an order of magnitude larger than
zreader itself. Layer them on if you want it:

```dockerfile
FROM leafvmaple/zreader:latest
USER root
RUN apk add --no-cache ocrmypdf tesseract-ocr-data-chi_sim
ENV ZREADER_OCR=1
```

### Export for AI

Any text-backed book can be exported as JSONL through the shelf's per-book
`⋯` menu, or directly:

```bash
curl -OJ 'http://localhost:8080/api/v1/books/12/export?chunk=2000'
```

One JSON object per line, cut on paragraph boundaries and never spanning
two chapters:

```json
{"book":"BookA","author":"AuthorX","chapter":3,"title":"第三章 起","offset":8412,"chars":1873,"text":"…"}
```

`offset` is a rune offset into the book's own text — the same coordinate the
reader and the progress API use — so a chunk can be traced back to a reading
position.

Four cleaning passes run by default, each switchable via a query parameter
(`promo`, `edges`, `normalise`, `notes`; `0` disables):

| Pass        | Removes                                                              |
| ----------- | -------------------------------------------------------------------- |
| `promo`     | Pirate-site advertising injected into the prose (URLs, 「记住本站」…) |
| `edges`     | Per-chapter headers/footers, detected by cross-chapter repetition     |
| `normalise` | Indentation, runs of spaces, `。。。`→`……`, half-width punctuation    |
| `notes`     | 「作者有话说」/「求推荐票」blocks and the rest of the chapter after them |

Add `preview=1` to get statistics plus the first few chunks instead of a
download — the UI uses this to show what a rule combination would strip
before you commit to it.

#### Extending the cleaning rules

The built-in patterns are conservative on purpose: a false positive deletes
a line of your book. To add your own, drop a `clean-rules.json` in
`ZREADER_DATA_DIR` — the patterns are appended to the built-ins, so you
never lose the defaults by adding to them:

```json
{
  "promo":        ["^本站永久域名"],
  "author_notes": ["^本章说"],
  "replace":      [["俩", "两"]]
}
```

`promo` and `author_notes` are Go regexes; `replace` is literal
search-and-replace applied during normalisation. A malformed file is
reported in the preview response and ignored — the defaults still run.

### Reading fonts

The three built-in choices (宋体 / 黑体 / 楷体) resolve entirely from fonts
the device already has, so the reader makes no external requests. Bundling
CJK webfonts isn't practical — a subsetted 霞鹜文楷 alone is around 19 MB
against a ~23 MB image — so extra fonts are opt-in: put `.woff2` / `.ttf`
files in `<ZREADER_DATA_DIR>/fonts/` and they appear in the reader's font
picker, served from your own host.

```bash
mkdir -p ./data/fonts
cp LXGWWenKaiScreen.ttf ./data/fonts/霞鹜文楷.ttf
```

The filename (without extension) is the label shown in the picker.

### Manual chapter sidecars

Create a JSON file next to the source, using the source stem plus
`.chapters.json`:

```json
{
  "chapters": [
    { "title": "Manual A", "match": "Alpha opening", "level": 0 },
    { "title": "Manual B", "match": "Beta opening", "level": 0 }
  ]
}
```

Use `match` to find the chapter start in the normalised text, or use
`char_offset` for an exact rune offset. Sidecar chapters replace automatic
chapter detection for that source.

## Roadmap

The current release is v0.9: everything v0.8 imported, plus OCR for scanned
PDFs, real cover art, a cleaned JSONL export for feeding a book to a language
model, a reworked shelf and reader, and a shelf that stays responsive at
several hundred books. The next milestones focus on users, safety, and
deployment predictability.

### v0.6 — Daily Reader (implemented)

Delivered: normal reading sessions now feel complete enough for daily use.

- In-book search.
- Bookmarks.
- More reading layout controls: line height, paragraph spacing, page width,
  margins, and indentation.
- Better continue-reading and recently-read surfaces.
- Per-book delete and re-parse actions.
- Clear upload/import failure messages in the UI.
- Mobile reading interaction polish: tap zones and immersive chrome toggle.

### v0.7 — Library Management (implemented)

Delivered: tens or hundreds of books can now be managed without falling back to
the filesystem for common tasks.

- Deterministic default covers for books without embedded cover assets.
- Editable metadata: title, author, and description.
- Tags, categories, favorites, and reading status.
- Duplicate detection.
- Batch operations for delete, re-scan, and tagging.
- Import/scan/batch job history with retry, result counts, and failure details.

### v0.8 — Format Coverage (implemented)

Delivered: import success rate and fidelity improved without adding heavyweight
runtime dependencies to the container.

- Image-only PDF support via a source-backed PDF page reader mode (OCR landed
  in v0.9).
- Better EPUB fidelity for image alt text, list/blockquote/footnote blocks, and
  nested navigation.
- MOBI/AZW/AZW3 import through optional Calibre `ebook-convert` (a native
  reader replaced this in v0.10).
- Manual chapter override sidecars for books whose automatic parsing is wrong.
- Continued PDF text cleanup: reading order and repeated page header/footer
  filtering.

### v0.9 — Covers, Cleanup, and Scale (implemented)

Delivered: the shelf earns its name, scanned PDFs stop being second-class, and
the whole app works offline.

- Real cover art: extracted from EPUB (and converted MOBI/AZW) at scan time,
  lifted out of embedded JPEGs for PDFs, and generated typographically for
  everything else.
- OCR for scanned PDFs through optional `ocrmypdf`, routing them into the
  normal text pipeline so they become searchable and chapter-parsed.
- Cleaned JSONL export for LLM/RAG use, with pirate-rip debris removed and
  user-extensible cleaning rules.
- Flat, low-saturation redesign of the shelf and reader; six reader themes
  including an eye-care green and a true-black OLED surface, defaulting to
  whichever matches the shelf.
- No external requests: reading fonts resolve from the system, with optional
  self-hosted webfonts from `<data>/fonts`.
- Reading progress saved when the page is backgrounded, not just on unmount.
- Windowed shelf rendering, so several hundred books stay responsive.
- Destructive actions moved behind an overflow menu; deleting a book no longer
  removes the source file unless you ask it to.

### v0.10 — Users and Safety (implemented)

Delivered: the app is safe to share inside a household or small private group.

- Built-in authentication: setup on first run, session login, no anonymous
  access to anything but the health check.
- Multi-user progress and bookmarks, already keyed by user id in the schema.
- Admin/user roles, with account management in the UI.
- Native MOBI/AZW/AZW3 reading, removing the last external-tool requirement
  from the default install.
- Scan failures report why, instead of a bare count.

Still open for a later milestone:

- Database and configuration backup/restore.
- Per-user reader settings (currently per-browser, in localStorage).

### v1.0 — Stable NAS App

Goal: make upgrades and long-running deployments predictable.

- Stable database migration policy.
- Large-library performance tests.
- End-to-end coverage for upload, scan, reading, and progress recovery.
- Diagnostic log export.
- Clear release notes, upgrade guidance, and rollback guidance.
- Stable Docker image, version, and health-check behavior.

## Reverse proxy example (Caddy)

```caddy
reader.example.com {
    basicauth {
        admin $2a$14$...bcrypt hash...
    }
    reverse_proxy 127.0.0.1:8080
}
```

## Build from source

Requires Go 1.26+, Node 22+, pnpm 9+.

```bash
# Frontend → emits into backend/internal/webui/dist
cd frontend
pnpm install
pnpm build

# Backend with the SPA baked in
cd ../backend
go build -o zreader ./cmd/zreader
ZREADER_LIBRARY_PATH=/tmp/books ./zreader
```

Local dev with hot reload — two terminals:

```bash
# Terminal 1: backend on :8080
cd backend && go run ./cmd/zreader

# Terminal 2: Vite on :5173, proxies /api → :8080
cd frontend && pnpm dev
```

### Docker workflow

The shipped `docker-compose.yml` is set up so the default action is **always
build locally**, never pull from a registry:

```yaml
build: .                                  # use the local Dockerfile
image: docker.io/leafvmaple/zreader:latest  # tag the build with the published name
pull_policy: never                        # refuse to fall back to a registry pull
```

Why all three? `image:` alone tells compose to pull; `build:` alone produces
an unhelpful `<project>_<service>` tag; the combination builds locally **and**
tags the artifact with the same name it would have on Docker Hub, so dev and
prod SHAs stay name-aligned. `pull_policy: never` closes the last hole —
without it, compose's default `missing` policy would try `docker.io` first
when the local image is absent.

The cycle:

1. **Develop** — edit code, then `docker compose up -d --build`. The `--build`
   forces a rebuild from the working copy; without it, compose reuses the
   existing local image. Iteration after the first build is fast (Go and
   pnpm layers cache; only changed source re-runs).
2. **Release** — push a git tag `vX.Y.Z` and let
   [`.github/workflows/docker.yml`](.github/workflows/docker.yml) build &
   push it to Docker Hub + GHCR. Or push manually:

   ```bash
   docker push docker.io/leafvmaple/zreader:vX.Y.Z
   docker push docker.io/leafvmaple/zreader:latest
   ```

3. **Consume** — to run the published image without cloning (e.g. on a NAS),
   either use the `docker run` from [Quick start](#quick-start), or comment
   out the `build:` and `pull_policy:` lines in the compose file.

One-off build without compose:

```bash
docker build -t zreader:dev .
docker run --rm -p 8080:8080 -v $(pwd)/testbooks:/library zreader:dev
```

## License

MIT — see [LICENSE](LICENSE).
