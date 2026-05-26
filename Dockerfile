# syntax=docker/dockerfile:1.7

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
RUN apk add --no-cache ca-certificates tzdata wget \
 && addgroup -S zreader && adduser -S zreader -G zreader \
 && mkdir -p /data /library \
 && chown -R zreader:zreader /data /library

COPY --from=backend /out/zreader /usr/local/bin/zreader

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
