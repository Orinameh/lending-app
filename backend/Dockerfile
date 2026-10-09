# syntax=docker/dockerfile:1
FROM golang:1.27.1-alpine3.20 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY backend/ ./backend/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" -o /api ./backend/cmd/api

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata
RUN adduser -D -H -u 10001 apiuser
WORKDIR /srv
COPY --from=builder /api ./api
COPY --from=builder /src/backend/migrations ./migrations
RUN chown -R apiuser:apiuser /srv
USER apiuser
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/health | grep -q '"status":"ok"' || exit 1
ENTRYPOINT ["./api"]
