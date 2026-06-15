# zreader

A self-hosted ebook reader. Point it at a directory of `.txt`, `.epub`, or
text-layer `.pdf` files; it scans, detects/normalises text, parses chapters,
and gives you a web reader with
cross-device progress sync. Single binary, single ~23 MB container.

> Status: **MVP**. TXT, EPUB, and text-layer PDF are supported. Image-only
> PDFs need OCR or a page-image reader mode and are not imported yet.

## Quick start

```bash
docker run -d \
  --name zreader \
  -p 8080:8080 \
  -v $(pwd)/data:/data \
  -v /path/to/your/books:/library \
  leafvmaple/zreader:latest
```

Open <http://localhost:8080>, click the scan button, start reading.

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

Multiple library roots:

```bash
docker run ... \
  -v /mnt/novels:/novels:ro \
  -v /mnt/tech-books:/tech:ro \
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
- Chapter parsing: Chinese `第X章/节/回/卷`, English `Chapter N`, bracketed
  CJK numerals (`「一」`, `【3】`, `〈12〉`). Falls back to a single "正文"
  chapter when no markers are found.
- Library scan: incremental — re-scanning skips files whose mtime+size match.
- Reading view: per-chapter lazy load, chapter drawer, 4 themes × 4 font
  sizes, font picker (system / 思源宋体 / 霞鹜文楷), progress auto-sync
  with stale-write protection.
- Keyboard: ←/→/PageUp/PageDown/Space turn pages, Home/End jump, Esc closes.

## What's missing

- No authentication. **Do not expose this to the public internet without a
  reverse proxy that handles auth** (Caddy / Nginx Basic Auth, Authelia,
  Tailscale, etc.). Inside your homelab on a trusted network it's fine.
- Single user. The schema has a `user_id` column but everyone is `default`
  in this mode.
- Image-only/scanned PDFs are detected but not imported; they need local OCR
  or a dedicated page-image reading mode.
- MOBI not supported yet.

## Roadmap

The current release is v0.7: it imports TXT, EPUB, and text-layer PDF sources,
caches them as EPUB, parses chapters, serves a web reader, syncs single-user
reading progress, and adds library-management workflows for larger shelves. The
next milestones focus on expanding format coverage and deployment safety.

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

### v0.8 — Format Coverage

Goal: improve import success rate and fidelity.

- Image-only PDF support, starting with a page-image reader mode; OCR can be
  added after the reading/progress model is in place.
- Better EPUB fidelity for covers, images, footnotes, and nested navigation.
- MOBI/AZW3 import.
- Manual chapter override sidecars for books whose automatic parsing is wrong.
- Continued PDF text cleanup: reading order, page header/footer filtering, and
  footnote handling.

### v0.9 — Users and Safety

Goal: make the app safe to share inside a household or small private group.

- Built-in authentication.
- Multi-user progress, bookmarks, notes, and settings.
- Admin/user roles.
- Database and configuration backup/restore.
- Hardened reverse-proxy deployment docs and safer defaults.

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

Requires Go 1.25+, Node 22+, pnpm 9+.

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
