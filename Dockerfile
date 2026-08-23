# Build stage
# BUILDPLATFORM/TARGETOS/TARGETARCH are provided by buildx for multi-arch builds;
# pinning the builder to BUILDPLATFORM keeps the toolchain native and uses
# Go's own cross-compilation (CGO_ENABLED=0, all deps are pure Go).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -ldflags "-s -w -X main.Version=${VERSION}" \
    -o pgmanager ./cmd/pgmanager

# Final stage
FROM alpine:3.21

# postgresql17-client provides pg_dump/pg_restore matching the postgres:17
# image used in examples/deploy — a mismatched major version fails loudly at
# runtime rather than silently, but we pin here to avoid that entirely.
RUN apk add --no-cache ca-certificates tzdata postgresql17-client

COPY --from=builder /app/pgmanager /usr/local/bin/pgmanager

# Static SPA (optional). Served from ./web at runtime if present. Only the
# built output is copied — the Vite sources in web/ are not needed at runtime,
# and dist is committed so no Node stage is required here.
COPY web/dist/ /app/web/dist/

# Data directory for the bootstrap-token file and any future on-disk state.
ENV PGMANAGER_DATA_DIR=/var/lib/pgmanager
RUN mkdir -p /var/lib/pgmanager

WORKDIR /app

EXPOSE 8080

CMD ["pgmanager", "serve"]
