# Build stage
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=1 GOOS=linux go build -a -ldflags '-linkmode external -extldflags "-static"' -o pgmanager ./cmd/pgmanager

# Final stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/pgmanager /usr/local/bin/pgmanager

# Copy config and web files
COPY config.yaml /etc/pgmanager/config.yaml
COPY web/ /app/web/

# Create data directory
RUN mkdir -p /data

# Set environment variables
ENV PGMANAGER_SQLITE_PATH=/data/pgmanager.db

EXPOSE 8080

# Default command runs the API server
CMD ["pgmanager", "-c", "/etc/pgmanager/config.yaml", "serve"]
