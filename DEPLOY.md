# DreamTrans Deployment Guide

The supported production path is Docker Compose. It runs PostgreSQL, applies
all pending migrations, starts DreamTrans only after the schema is ready, and
keeps the application port bound to loopback by default.

## Requirements

- Docker Engine with Docker Compose v2
- A Speechmatics API key
- An OpenAI-compatible API key only if translation, chat, or RAG is needed

For source development, use Go 1.26.5 and Node.js 24.18.0 LTS. The Go module
declares its toolchain, so a compatible Go installation can download the exact
compiler automatically.

## One-click installation

```bash
curl -fsSL \
  https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh |
  bash
```

The installer writes a permission-restricted `.env`, generates independent
database/access-token/refresh-token secrets, creates the initial administrator,
and waits for `/readyz` before reporting success. Migration files are extracted
from the exact pulled application image rather than from a mutable Git branch.

Useful commands:

```bash
# Update the existing tag.
curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh |
  bash -s -- --update

# Switch to a specific release tag and update.
curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh |
  bash -s -- --update --tag 1.2.3

# Status and logs.
curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh |
  bash -s -- --status
curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh |
  bash -s -- --logs
```

## Repository-based Compose deployment

Check out the same release as the application image tag. This keeps the
repository migration bundle and image schema expectations aligned.

```bash
git clone https://github.com/soaringjerry/DreamTrans.git
cd DreamTrans
git checkout v1.2.3

cp backend/.env.example .env
chmod 600 .env
```

Fill in at least:

```dotenv
IMAGE_TAG=1.2.3
SM_API_KEY=...
POSTGRES_PASSWORD=...
JWT_SECRET=...
JWT_REFRESH_SECRET=...
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=...
```

Use different random values for all three secrets. `ADMIN_EMAIL` and
`ADMIN_PASSWORD` are needed on a fresh database unless self-registration is
deliberately enabled.

```bash
docker compose up -d
docker compose ps
curl --fail http://127.0.0.1:16002/healthz
curl --fail http://127.0.0.1:16002/readyz
```

`/healthz` is a process liveness check. `/readyz` also verifies PostgreSQL when
database mode is configured. Neither endpoint calls Speechmatics or OpenAI.

To update, first move the checkout and image tag to the same release:

```bash
git fetch --tags
git checkout v1.2.4
# Set IMAGE_TAG=1.2.4 in .env.
docker compose pull
docker compose up -d
```

Migrations are transactional and recorded in `schema_migrations`. Back up the
PostgreSQL volume before a production upgrade.

## Network exposure and TLS

Compose defaults to:

```dotenv
BIND_ADDRESS=127.0.0.1
PORT=16002
```

Keep that loopback binding when an Nginx, Caddy, or other same-host reverse
proxy terminates HTTPS. If direct network publication is intentional, set
`BIND_ADDRESS=0.0.0.0` and enforce TLS/firewall controls externally.

Proxy `/`, `/api/`, and `/ws/` to the same DreamTrans origin. WebSocket proxying
must preserve the `Upgrade` and `Connection` headers. Browser microphone access
requires HTTPS outside localhost.

Anonymous provider access and self-registration are disabled by default. Do
not expose `ALLOW_ANONYMOUS_API=true` outside a trusted loopback development
environment.

## Local source development

Backend-only Classic UI development:

```bash
cd backend
SM_API_KEY=... ALLOW_ANONYMOUS_API=true go run ./cmd/web
```

Keep this anonymous mode on localhost. For frontend development:

```bash
cd frontend
npm ci
VITE_BACKEND_URL=http://127.0.0.1:8080 \
VITE_BACKEND_WS_URL=ws://127.0.0.1:8080 \
npm run dev
```

Production frontend assets are served by the Go application; a separate PM2 or
frontend container is not required.

## Troubleshooting

```bash
docker compose ps
docker compose logs migrate
docker compose logs dreamtrans
docker inspect --format '{{.State.Health.Status}}' dreamtrans
```

- A failed migration is rolled back and prevents the application from starting.
- `401` from provider-backed endpoints means JWT/service-key authentication is
  working as configured; it is not a health-check failure.
- If `/healthz` succeeds but `/readyz` returns `503`, inspect PostgreSQL and
  migration logs.
- The server allows up to 20 seconds to drain active requests and WebSockets;
  Compose grants a 30-second stop window.
