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
# PostgreSQL (required for Pro features)
DATABASE_URL=postgres://user:pass@host:5432/dreamtrans

# JWT secrets
JWT_SECRET=your-jwt-secret
JWT_REFRESH_SECRET=your-refresh-secret

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
- ✅ Set up PostgreSQL automatically (Pro mode)
- ✅ Generate all configuration files
- ✅ Start DreamTrans

**Installation Options:**

```bash
# Basic installation (interactive)
curl -fsSL https://raw.githubusercontent.com/soaringjerry/DreamTrans/main/scripts/install.sh | bash

# Pro mode with custom port
curl -fsSL ... | bash -s -- --pro --port 3000

# Update existing installation
curl -fsSL ... | bash -s -- --update

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
   # Edit .env and add your API keys
   ```

3. **Start services:**
   ```bash
   docker compose up -d
   ```

4. **Access the application:**
   - Classic UI: http://localhost:8080
   - Pro UI: http://localhost:8080/pro

### Production Deployment (Simple Docker Run)

For basic deployment without PostgreSQL:

```bash
docker run -d \
  --name dreamtrans \
  -p 8080:8080 \
  -e SM_API_KEY="your_speechmatics_api_key" \
  -e OPENAI_API_KEY="your_openai_api_key" \
  -v dreamtrans_data:/app/data \
  --restart unless-stopped \
  ghcr.io/soaringjerry/dreamtrans:latest
```

> ⚠️ Note: Without PostgreSQL, Pro features (user auth, cloud sessions) will be disabled.

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
