# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS builder
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS=-mod=readonly
# Dependency layer first: invalidated only when go.mod/go.sum change.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
# Source layer: BuildKit caches modules + compiler output across rebuilds,
# so code-only changes skip redownloading and recompiling dependencies.
COPY backend/ ./backend/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-w -s" -o /api ./backend/cmd/api

FROM alpine:3.23
# Single layer: certs + tzdata + non-root user.
RUN apk --no-cache add ca-certificates tzdata \
 && adduser -D -H -u 10001 apiuser
WORKDIR /srv
# --chown avoids a separate chown layer (which would duplicate file bytes).
COPY --from=builder --chown=apiuser:apiuser /api ./api
COPY --from=builder --chown=apiuser:apiuser /src/backend/migrations ./migrations
USER apiuser
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/health | grep -q '"status":"ok"' || exit 1
ENTRYPOINT ["./api"]
