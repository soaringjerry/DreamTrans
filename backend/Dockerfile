# Build stage
FROM golang:1.26.5-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY public ./public
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /app/dreamtrans-backend ./cmd/web

# Run stage
FROM alpine:3.22
RUN apk --no-cache add ca-certificates \
    && addgroup -S -g 10001 dreamtrans \
    && adduser -S -D -H -u 10001 -G dreamtrans dreamtrans \
    && mkdir -p /app/data \
    && chown -R dreamtrans:dreamtrans /app
WORKDIR /app
COPY --from=builder --chown=dreamtrans:dreamtrans /app/dreamtrans-backend ./dreamtrans-backend
COPY --from=builder --chown=dreamtrans:dreamtrans /app/public ./public

ENV RAG_DB_PATH=/app/data/rag.db
ENV DREAMTRANS_CONFIG_PATH=/app/data/dreamtrans.config.json
VOLUME ["/app/data"]
USER dreamtrans

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=12 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/readyz || exit 1
CMD ["./dreamtrans-backend"]
