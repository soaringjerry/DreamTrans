# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o dreamtrans-backend ./cmd/web

# Run stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/dreamtrans-backend .
COPY --from=builder /app/.env .env

EXPOSE 8080
CMD ["./dreamtrans-backend"]