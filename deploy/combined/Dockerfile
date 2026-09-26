# syntax=docker/dockerfile:1

# One static binary with the web UI embedded, on a distroless base.
# Build context is the repo root.

# ---- Web UI ----
FROM node:26-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
# Vite writes into internal/adapters/web/dist (see vite.config.ts).
RUN mkdir -p ../internal/adapters/web/dist && npm run build

# ---- Go binary ----
FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/adapters/web/dist internal/adapters/web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/stlib ./cmd/stlib \
    && mkdir -p /out/data

# ---- Runtime ----
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/stlib /stlib
# Owned by the nonroot user, so a fresh named volume mounted here is
# writable (Docker copies the ownership into it).
COPY --from=build --chown=65532:65532 /out/data /data
ENV LIBRARY_ROOT=/library \
    DATA_DIR=/data \
    LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/stlib", "serve"]
