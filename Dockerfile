# Build stage
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=1 GOOS=linux go build \
    -a -ldflags "-linkmode external -extldflags '-static' -X main.Version=${VERSION}" \
    -o pgmanager ./cmd/pgmanager

# Final stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /app/pgmanager /usr/local/bin/pgmanager

# Static SPA (optional). Served from ./web at runtime if present.
COPY web/ /app/web/

# Data directory for the bootstrap-token file and any future on-disk state.
ENV PGMANAGER_DATA_DIR=/var/lib/pgmanager
RUN mkdir -p /var/lib/pgmanager

WORKDIR /app

EXPOSE 8080

CMD ["pgmanager", "serve"]
