# DreamTrans - A dApp for the Personal Central AI System (PCAS)

[![CI - Code Quality](https://github.com/soaringjerry/DreamTrans/actions/workflows/ci.yml/badge.svg)](https://github.com/soaringjerry/DreamTrans/actions/workflows/ci.yml)
[![Docker Image CI](https://github.com/soaringjerry/DreamTrans/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/soaringjerry/DreamTrans/actions/workflows/docker-publish.yml)

**DreamTrans** is a foundational dApp within the **DreamHub** ecosystem. Its primary role is to provide a powerful, real-time, multilingual transcription and translation service, acting as a core data-ingestion component for the **Personal Central AI System (PCAS)**.

Quick links:
- GitHub repository: https://github.com/soaringjerry/DreamTrans
- One‑command deploy: see below
- User Guide: docs/USER_GUIDE.md
- RAG Guide: docs/RAG.md
- Performance Monitoring: docs/PERFORMANCE_MONITORING.md

## 📱 UI Versions

DreamTrans provides two UI versions:

| | **Pro UI** (Recommended) | **Classic UI** |
|---|---|---|
| **URL** | `/pro` | `/` |
| **Status** | ✅ Active Development | 🔧 Maintenance Only |
| **Features** | Full SaaS features, billing, user accounts | Basic transcription & translation |
| **Design** | Modern glass morphism UI | Simple functional UI |
| **Recommended For** | All users | Legacy/fallback use |

> **💡 Recommendation**: Use **Pro UI** (`/pro`) for the best experience. Classic UI is kept for compatibility but no longer receives new features.

---

This project serves two purposes:
1.  A fully functional, standalone web application for real-time transcription and translation.
2.  A reference implementation of a "headless" service dApp, demonstrating how to integrate with and provide capabilities to the PCAS event bus.

> **Core Architectural Philosophy**: This project is designed based on the "Personal Data Internet" model. Each dApp (like DreamTrans) is an Autonomous System (AS) that provides specific capabilities. PCAS acts as the core BGP backbone, routing events (data packets) between dApps based on a declarative policy. For more details, refer to [ADR-001: The "Personal Data Internet" Model](./docs/adr-001-personal-data-internet-model.md).

## Current Features (Standalone Web App)

- **Real-Time Transcription & Translation**: High-accuracy, low-latency, speaker-separated transcription and translation powered by Speechmatics. Default translation model: `gpt-4.1-mini`.
- **Full Session Persistence**: Never lose your work. The entire session, including audio, original text, and translated text, is automatically saved to your browser's IndexedDB and can be restored after a refresh or crash.
- **Data Export**: Download your full session audio (`.webm`) and transcription (`.txt`) at any time.
- **Robust & Resilient**: Features automatic WebSocket reconnection to handle network interruptions gracefully.
- **RAG Learning Assistant**: Automatic summarize→vectorize pipeline + retrieval-augmented Q&A. Ask questions anytime — the AI uses live context to know “what’s being discussed now”. Default chat/summary model: `gpt-5-chat-latest`.
  - Premium chat UI: bubbles, smooth typing indicator, preserved newlines
  - Assistant messages support Markdown (headings, lists, code blocks, links)
  - Global Settings modal (top-right): override API Base, Model, Prompt, API Key
    - API Key is never shown by default and stored only in your browser (localStorage)
  - Global History modal: browse and clear local chat history
 - Lexicon (Word & Term Frequency): local, real‑time word/bi‑gram counts with filters (All/Unknown/Learning), stopwords, search, AI explain, CSV export.
 - Bilingual Mode (Experimental): one English line paired with one Chinese line for study view; toggle in Settings → Experimental.
 - Dictionary (AI explain): click a word/term or select text to auto‑open Chat and ask for explanation using your current Chat model.

### Pro Edition (SaaS Features)

The Pro edition includes enterprise-grade features for SaaS deployment:

- **Multi-Tenant Architecture**: PostgreSQL-backed user accounts, tenants, and sessions
- **JWT Authentication**: Secure token-based authentication with refresh tokens
- **Cloud Session Storage**: Save transcripts and translations to the cloud
- **Admin Dashboard**: User management, tenant quotas, usage statistics
- **API Traffic Control**: All Speechmatics and OpenAI API calls routed through backend
  - Admin setting to enable/disable user-provided API keys
  - Usage tracking per user/tenant
  - Server-managed API credentials (default: users cannot bypass server APIs)
- **Glass Morphism UI**: Modern, visually stunning Pro interface

**Environment Variables for Pro Features:**
```bash
# PostgreSQL (Compose derives DATABASE_URL internally)
POSTGRES_DB=dreamtrans
POSTGRES_USER=dreamtrans
POSTGRES_PASSWORD=a-url-safe-random-password

# JWT secrets
JWT_SECRET=your-jwt-secret
JWT_REFRESH_SECRET=your-refresh-secret

# Bootstrap administrator (needed for a fresh Pro DB unless registration is enabled)
ADMIN_EMAIL=you@example.com
ADMIN_PASSWORD=a-unique-strong-password

# Self-registration is opt-in
REGISTRATION_ENABLED=false
REGISTRATION_INVITE_CODE=

# System settings
ALLOW_USER_API_KEY=false  # Set to 'true' to allow users to use their own API keys
```

### Productivity & Controls
- **Continue (Resume on same session)**: In addition to starting a New Session, a Continue button resumes on top of the current session (same `session_id`) without clearing text or metrics.
- **Summary Toggle (in the Summary panel)**: Enable/disable summarization right inside the Summary floating window. When off, the backend hard‑disables LLM summarization and refrains from updating session summaries (zero tokens).
- **Embeddings Toggle (Experimental)**: Turn RAG embeddings/retrieval on/off. When off, no embeddings are computed (zero tokens) and Q&A only uses the running summary if available.
- **Settings Quality‑of‑Life**:
  - Show backend default models (Chat/Translate/Summary); one‑click Reset actions for each tab (General/Prompts/Experimental).
  - Compact mode for floating windows (no duplicate headers, more content space).
  - Save feedback: a lightweight “已保存 ✓” hint after saving settings.

### Reliability & Observability
- **LLM Retries**: Translate/Summarize/Chat calls retry transient upstream/proxy errors (503/connection reset, etc.) with fast backoff.
- **Chat Timeout**: Client‑side timeout prevents indefinite hangs; user can cancel a pending request.
- **Performance Panel**: P50/P95/P99 (Translate) latency, per‑kind mini bars, API usage (Requests/Tokens), recent call logs; metrics reset endpoint for fresh sessions.

## The PCAS Ecosystem Vision

DreamTrans is the first step towards a larger ecosystem of interconnected dApps.

- **DreamTrans (This App)**: The **Data Collector**. Its job is to capture the raw, real-time stream of human conversation and convert it into structured, multilingual text data. In the PCAS model, it acts as a "headless" service, providing the `dapp.dreamtrans.translate.stream.v1` capability to the entire ecosystem.
- **DreamNote (Future dApp)**: The **Knowledge Processor**. It will consume data from dApps like DreamTrans, and by leveraging PCAS and Large Language Models (LLMs), it will provide AI-powered summarization, note-taking, and knowledge graph integration.
- **PCAS (The Backbone)**: The central "BGP router" that understands the capabilities of all installed dApps (via their `dapp.yaml` manifests) and routes events between them based on user-defined policies. It transforms simple events into rich, context-aware actions.

### Secure PCAS Workers and Providers

The event worker connects to `PCAS_ADDR` (`127.0.0.1:50051` by default).
Loopback uses plaintext for local development; a non-loopback address uses TLS
by default. Configure `PCAS_CA_CERT` for a private CA,
`PCAS_TLS_SERVER_NAME` when certificate discovery and DNS names differ, and
`PCAS_API_KEY` for Bearer authentication. `PCAS_INSECURE=true` is an explicit
plaintext opt-in, and the worker refuses to send its API key over remote
plaintext. Remote `audio_url` inputs must use HTTPS unless
`AUDIO_URL_ALLOW_HTTP=true`; internal, loopback, link-local, metadata, and
other special IP ranges remain blocked either way.

The streaming provider listens only on `127.0.0.1:50052` by default. A
non-loopback `GRPC_BIND_ADDR` requires a `PCAS_API_KEY` of at least 16
characters and a `PCAS_TLS_CERT`/`PCAS_TLS_KEY` pair. The
`PCAS_ALLOW_INSECURE_REMOTE=true` escape hatch should be limited to an
otherwise protected network. Concurrency defaults to 32 streams through
`PCAS_MAX_CONCURRENT_STREAMS`; server reflection stays off unless
`PCAS_ENABLE_REFLECTION=true`.

Example remote event-worker connection:

```bash
docker run --rm \
  -e SM_API_KEY="..." \
  -e PCAS_ADDR="pcas.example.com:50051" \
  -e PCAS_API_KEY="a-long-independent-service-key" \
  -e PCAS_CA_CERT="/run/secrets/pcas-ca.crt" \
  -v "$PWD/pcas-ca.crt:/run/secrets/pcas-ca.crt:ro" \
  dreamtrans-event
```

Example TLS provider:

```bash
docker build -f backend/Dockerfile.pcas \
  --build-arg MODE=provider -t dreamtrans-pcas .
docker run --rm -p 50052:50052 \
  -e SM_API_KEY="..." \
  -e GRPC_BIND_ADDR="0.0.0.0" \
  -e PCAS_API_KEY="a-long-independent-service-key" \
  -e PCAS_TLS_CERT="/run/secrets/tls.crt" \
  -e PCAS_TLS_KEY="/run/secrets/tls.key" \
  -v "$PWD/tls.crt:/run/secrets/tls.crt:ro" \
  -v "$PWD/tls.key:/run/secrets/tls.key:ro" \
  dreamtrans-pcas
```

## Getting Started & Deployment

This project is fully containerized and designed for easy deployment.

### 🚀 One-Click Installation (Recommended)

The easiest way to get started - just run this command:

```bash
curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh | bash
```

The installer will:
- ✅ Check Docker prerequisites
- ✅ Prompt for your API keys
- ✅ Prompt for an administrator email and generate a unique password if requested
- ✅ Set up PostgreSQL automatically (Pro mode)
- ✅ Generate strong database and JWT secrets in a permission-restricted `.env`
- ✅ Start DreamTrans

**Installation Options:**

```bash
# Basic installation (interactive, default port: 16002)
curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh | bash

# Custom port
curl -fsSL ... | bash -s -- --port 8080

# Update existing installation
curl -fsSL ... | bash -s -- --update

# Management commands
curl -fsSL ... | bash -s -- --stop      # Stop services
curl -fsSL ... | bash -s -- --start     # Start services
curl -fsSL ... | bash -s -- --restart   # Restart services
curl -fsSL ... | bash -s -- --status    # Show status
curl -fsSL ... | bash -s -- --logs      # Show logs (follow mode)

# Uninstall
curl -fsSL ... | bash -s -- --uninstall
```

### Prerequisites
- Docker & Docker Compose
- An API key from [Speechmatics](https://www.speechmatics.com/) (required)
- OpenAI API key (optional, for translation/chat)

### Manual Installation with Docker Compose

1. **Clone the repository:**
   ```bash
   git clone https://github.com/soaringjerry/DreamTrans.git
   cd DreamTrans
   ```

2. **Create environment file:**
   ```bash
   cp backend/.env.example .env
   # Edit .env and set every required API key, database password,
   # JWT secret, and (for a fresh Pro database) a bootstrap administrator pair.
   ```

   There are no production password fallbacks or preinstalled administrator
   credentials. Generate independent secrets, for example with
   `openssl rand -hex 32`.

3. **Start services:**
   ```bash
   docker compose up -d
   ```

   Compose runs the checksummed migration job before starting DreamTrans. This
   also applies missing migrations when `postgres_data` already exists. Keep
   the checkout release and `IMAGE_TAG` aligned.

4. **Access the application:**
   - Classic UI: http://localhost:16002
   - Pro UI: http://localhost:16002/pro

### Production Deployment (Simple Docker Run)

For a local-only Classic UI without PostgreSQL, bind to loopback and explicitly
enable anonymous compatibility mode:

```bash
docker run -d \
  --name dreamtrans \
  -p 127.0.0.1:16002:8080 \
  -e SM_API_KEY="your_speechmatics_api_key" \
  -e OPENAI_API_KEY="your_openai_api_key" \
  -e ALLOW_ANONYMOUS_API=true \
  -v dreamtrans_data:/app/data \
  --restart unless-stopped \
  ghcr.io/soaringjerry/dreamtrans:latest
```

Do not publish that anonymous mode on `0.0.0.0`. For a network-accessible
headless/API deployment, leave `ALLOW_ANONYMOUS_API=false`, generate
`DREAMTRANS_API_KEY` (and a separate `DREAMTRANS_ADMIN_API_KEY`), then send the
service key in `X-DreamTrans-API-Key`; WebSocket clients may use the `api_key`
query parameter. Without a JWT, service key, or explicit anonymous mode,
provider-backed endpoints correctly return `401`.

For browser access over a network, the PostgreSQL-backed Compose deployment is
recommended: log in through Pro and use JWT authentication. Self-registration
is disabled by default; enable `REGISTRATION_ENABLED=true` only deliberately,
preferably with `REGISTRATION_INVITE_CODE`.

## Documentation

Please see the docs folder for complete guides:
- docs/USER_GUIDE.md — UI overview, global settings, quick start
- docs/RAG.md — RAG pipeline and APIs
- docs/PERFORMANCE_MONITORING.md — Tokens/Latency/Model metrics
- docs/ENVIRONMENT_VARIABLES.md — environment configuration
- docs/DOCKER_DYNAMIC_CONFIG.md — deployment options

## Defaults & Endpoints (Quick Reference)

- Default models
  - Translate: `gpt-4.1-mini`
  - Chat: `gpt-5-chat-latest`
  - Summary: `gpt-5-chat-latest`
- Core Endpoints
  - `/healthz` — process liveness (`GET`/`HEAD`, no upstream calls)
  - `/readyz` — readiness, including a bounded PostgreSQL ping when configured
  - `/api/models/defaults` — backend default model set (Chat/Translate/Summary)
  - `/api/prompts/defaults` — default system prompts
  - `/api/metrics` + `/api/metrics/reset` — usage snapshot and reset
  - `/api/rag/ask` — RAG Q&A (supports per‑request overrides)
  - `/api/rag/summary` — current session summary
  - `/api/rag/title` — cached session title (generated once, then reused)
- Pro Endpoints (requires PostgreSQL)
  - `/api/auth/register` — user registration
  - `/api/auth/login` — user login (returns JWT)
  - `/api/auth/refresh` — refresh access token
  - `/api/user/profile` — get/update user profile
  - `/api/sessions` — list/create cloud sessions
  - `/api/sessions/{id}` — get/update/delete session
  - `/api/sessions/{id}/transcripts` — save transcripts
  - `/api/admin/users` — admin: list/manage users
  - `/api/admin/tenants` — admin: list/manage tenants
  - `/api/admin/settings` — admin: update system settings
  - `/api/system/settings` — public: get system settings (allow_user_api_key)
  - `/ws/speechmatics` — WebSocket proxy for Speechmatics (Pro only)

## License

This project is licensed under the **PolyForm Noncommercial License 1.0.0**.

### What this means:

✅ **Allowed:**
- Personal use, research, and experimentation
- Educational and academic use
- Use by non-profit organizations
- Creating derivative works (non-commercial only)
- Distributing copies with this license

❌ **Prohibited:**
- **Any commercial use whatsoever**
- Selling or monetizing this software
- Using in commercial products or services
- Providing commercial SaaS services with this software

### Commercial Licensing

If you need to use DreamTrans for commercial purposes, please contact the author for a commercial license.

**Unauthorized commercial use is copyright infringement and may result in legal action.**

See the full [LICENSE](./LICENSE) file for details.
