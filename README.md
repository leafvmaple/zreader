# zreader

A self-hosted TXT reader. Point it at a directory of `.txt` files; it scans,
detects encoding, parses chapters, and gives you a web reader with
cross-device progress sync. Single binary, single container.

> Status: **MVP**. TXT only for now. EPUB / PDF / MOBI are on the roadmap.

## Quick start

```bash
docker run -d \
  --name zreader \
  -p 8080:8080 \
  -v $(pwd)/data:/data \
  -v /path/to/your/books:/library:ro \
  ghcr.io/leafvmaple/zreader:latest
```

Open <http://localhost:8080>, click the scan button, start reading.

### docker compose

```yaml
services:
  zreader:
    image: ghcr.io/leafvmaple/zreader:latest
    ports: ["8080:8080"]
    volumes:
      - ./data:/data
      - ./books:/library:ro
    restart: unless-stopped
```

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
  ghcr.io/leafvmaple/zreader:latest
```

## What works

- TXT only (UTF-8 BOM / UTF-8 / GBK / GB18030 / pure ASCII auto-detect)
- Chapter parsing: Chinese `第X章/节/回/卷`, English `Chapter N`. Falls back to
  a single "正文" chapter when no markers are found.
- Library scan: incremental — re-scanning skips files whose mtime+size match.
- Reading view: scroll reading, chapter drawer, 4 themes × 4 font sizes,
  progress auto-sync (with stale-write protection).
- Keyboard: ←/→/PageUp/PageDown/Space turn pages, Home/End jump, Esc closes.

## What's missing

- No authentication. **Do not expose this to the public internet without a
  reverse proxy that handles auth** (Caddy / Nginx Basic Auth, Authelia,
  Tailscale, etc.). Inside your homelab on a trusted network it's fine.
- Single user. The schema has a `user_id` column but everyone is `default`
  in this mode.
- EPUB / PDF / MOBI not supported yet.

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

### Building the image yourself

```bash
docker build -t zreader:dev .
docker run --rm -p 8080:8080 -v $(pwd)/testbooks:/library:ro zreader:dev
```

## License

MIT — see [LICENSE](LICENSE).
