# Build
FROM golang:1.26-alpine AS build

# CGO stays off: the sqlite driver in use is uptrace/bun's sqliteshim, which
# selects the pure-Go modernc.org/sqlite backend when cgo is unavailable. That
# keeps the binary static and the runtime image free of libc concerns.
ENV CGO_ENABLED=0 GOOS=linux

WORKDIR /src

# Copy manifests first so dependency download is cached independently of source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# -trimpath drops local filesystem paths; -s -w strips the symbol and DWARF
# tables. Templates and static assets are embedded via web/embed.go, so the
# resulting binary is self-contained.
RUN go build -trimpath -ldflags='-s -w' -o /out/ai-self-service ./cmd/server

# Runtime
FROM alpine:3.24

# ca-certificates: outbound TLS to LiteLLM and the OIDC provider.
# tzdata: SESSION_DURATION and key expiry render in local time.
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 10001 app

COPY --from=build /out/ai-self-service /usr/local/bin/ai-self-service

# The SQLite database lives on /data; mount a volume there to keep it. An
# explicitly set DB_PATH still wins over this default.
ENV DB_PATH=/data/data.db
RUN mkdir -p /data && chown app:app /data
VOLUME ["/data"]

USER app
EXPOSE 8080

# Docker health status via the binary's own /healthz probe; it follows
# LISTEN_ADDR and needs no shell. Kubernetes ignores this and uses its own
# probes on /healthz and /readyz.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/ai-self-service", "-healthcheck"]

ENTRYPOINT ["/usr/local/bin/ai-self-service"]
