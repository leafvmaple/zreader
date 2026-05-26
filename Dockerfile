# --- Stage 1: build the React SPA -------------------------------------------
FROM node:22-alpine AS frontend
WORKDIR /src/frontend

# Install pnpm via the bundled corepack (pinned in package.json's engines if set).
RUN corepack enable && corepack prepare pnpm@9 --activate

COPY frontend/package.json ./
RUN pnpm install --no-frozen-lockfile

# Vite writes its output to ../backend/internal/webui/dist (see vite.config.ts),
# so make sure that destination directory exists in this stage.
RUN mkdir -p /src/backend/internal/webui
COPY frontend/ ./
RUN pnpm build

# --- Stage 2: compile the Go backend with the embedded SPA ------------------
FROM golang:1.25-alpine AS backend
WORKDIR /src/backend

RUN apk add --no-cache git
COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
# Overlay the built SPA so go:embed picks it up.
COPY --from=frontend /src/backend/internal/webui/dist ./internal/webui/dist

ARG VERSION=docker
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.Version=${VERSION}" \
        -o /out/zreader \
        ./cmd/zreader

# --- Stage 3: minimal runtime image -----------------------------------------
FROM alpine:3.20
# UID 1000 / GID 10 ("wheel") matches the default admin user on most consumer
# NASes (UGREEN UGOS Pro, Synology, QNAP). Bind-mounted shared folders are
# typically owned by 1000:10 on those systems, so the container hits them as
# owner — bypassing ACLs that may restrict "other". Override at run time with
# `--user UID:GID` if your host uses different IDs.
RUN apk add --no-cache ca-certificates tzdata wget \
 && addgroup -g 1000 -S zreader \
 && adduser -u 1000 -G zreader -S zreader \
 && addgroup zreader wheel \
 && mkdir -p /data /library \
 && chown -R zreader:zreader /data /library

COPY --from=backend /out/zreader /usr/local/bin/zreader

# OCI image annotations — surfaced by `docker inspect`, `docker scout`,
# GHCR/Docker Hub UIs, and most container registries. BUILD_DATE and
# VCS_REF are passed at build time; VERSION is re-declared here because
# ARG values don't cross FROM boundaries.
ARG VERSION=docker
ARG BUILD_DATE
ARG VCS_REF
LABEL org.opencontainers.image.title="zreader" \
      org.opencontainers.image.description="Self-hosted TXT reader: scans a directory of plain-text books, splits chapters automatically (CJK + bracketed-numeral support), and serves a React SPA for browsing and reading with per-device progress sync." \
      org.opencontainers.image.url="https://github.com/leafvmaple/zreader" \
      org.opencontainers.image.source="https://github.com/leafvmaple/zreader" \
      org.opencontainers.image.documentation="https://github.com/leafvmaple/zreader/blob/main/README.md" \
      org.opencontainers.image.vendor="leafvmaple" \
      org.opencontainers.image.authors="Zohar Lee <leafvmaple@gmail.com>" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.base.name="alpine:3.20"

ENV ZREADER_PORT=8080 \
    ZREADER_DATA_DIR=/data \
    ZREADER_LIBRARY_PATH=/library

USER zreader
WORKDIR /data
EXPOSE 8080
VOLUME ["/data", "/library"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/api/v1/health || exit 1

ENTRYPOINT ["/usr/local/bin/zreader"]
