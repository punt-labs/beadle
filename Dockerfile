# syntax=docker/dockerfile:1

# ---- Build stage ----
# Pin by digest -- replace placeholder digests at build time with
# current values from: docker pull golang:1.24-bookworm && docker inspect --format='{{.RepoDigests}}'
FROM golang:1.24-bookworm AS builder

# Version injected from Makefile (git describe). Falls back to "dev"
# when not provided. .git is excluded via .dockerignore, so git
# describe cannot run inside the build context.
ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux \
    go build -ldflags="-s -w -X main.version=${VERSION}" \
    -o /beadle-email ./cmd/beadle-email/

# ---- Runtime stage ----
# Pin by digest -- replace placeholder digests at build time with
# current values from: docker pull debian:bookworm-slim && docker inspect --format='{{.RepoDigests}}'
FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      gnupg \
      ca-certificates && \
    rm -rf /var/lib/apt/lists/*

# Create unprivileged user
RUN groupadd -r beadle && \
    useradd -r -g beadle -m -d /home/beadle -s /bin/false beadle

# Create required directory structure
RUN mkdir -p /home/beadle/.punt-labs/beadle/identities \
             /home/beadle/.punt-labs/beadle/secrets \
             /home/beadle/.punt-labs/ethos/identities \
             /home/beadle/.gnupg && \
    chown -R beadle:beadle /home/beadle

# Copy binary from builder
COPY --from=builder /beadle-email /beadle-email

# Entrypoint script: copies GPG keyring from read-only source
# mount to tmpfs-backed ~/.gnupg, then execs the main binary.
# This ensures private key material exists only in memory.
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

USER beadle
WORKDIR /home/beadle

# Default MCP WebSocket port
EXPOSE 8420

# Health check: built-in subcommand, no wget dependency
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD ["/beadle-email", "health", "--port", "8420"]

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/beadle-email", "serve", "--transport", "ws", "--port", "8420"]
