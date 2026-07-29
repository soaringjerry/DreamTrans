#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

echo "=== DreamTrans backend build verification ==="
echo "Go toolchain: $(go version)"

echo "1. Verifying module files..."
go mod tidy -diff
go mod verify

echo "2. Building the web server..."
go build -buildvcs=false -trimpath -o /dev/null ./cmd/web

echo "3. Building the PCAS provider..."
go build -buildvcs=false -trimpath -o /dev/null ./cmd/pcas-provider

echo "4. Building the event worker..."
go build -buildvcs=false -trimpath -tags=event_worker -o /dev/null ./cmd/event-worker

echo "5. Running all package tests..."
go test ./...
go test -tags=event_worker ./cmd/event-worker

echo "=== All backend builds and tests passed ==="
