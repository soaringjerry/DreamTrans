# DreamTrans - A dApp for the Personal Central AI System (PCAS)

[![CI - Code Quality](https://github.com/soaringjerry/DreamTrans/actions/workflows/ci.yml/badge.svg)](https://github.com/soaringjerry/DreamTrans/actions/workflows/ci.yml)
[![Docker Image CI](https://github.com/soaringjerry/DreamTrans/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/soaringjerry/DreamTrans/actions/workflows/docker-publish.yml)

**DreamTrans** is a foundational dApp within the **DreamHub** ecosystem. Its primary role is to provide a powerful, real-time, multilingual transcription and translation service, acting as a core data-ingestion component for the **Personal Central AI System (PCAS)**.

This project serves two purposes:
1.  A fully functional, standalone web application for real-time transcription and translation.
2.  A reference implementation of a "headless" service dApp, demonstrating how to integrate with and provide capabilities to the PCAS event bus.

> **Core Architectural Philosophy**: This project is designed based on the "Personal Data Internet" model. Each dApp (like DreamTrans) is an Autonomous System (AS) that provides specific capabilities. PCAS acts as the core BGP backbone, routing events (data packets) between dApps based on a declarative policy. For more details, refer to [ADR-001: The "Personal Data Internet" Model](./docs/adr-001-personal-data-internet-model.md).

## Current Features (Standalone Web App)

- **Real-Time Transcription & Translation**: High-accuracy, low-latency, speaker-separated transcription and translation powered by Speechmatics.
- **Full Session Persistence**: Never lose your work. The entire session, including audio, original text, and translated text, is automatically saved to your browser's IndexedDB and can be restored after a refresh or crash.
- **Data Export**: Download your full session audio (`.webm`) and transcription (`.txt`) at any time.
- **Robust & Resilient**: Features automatic WebSocket reconnection to handle network interruptions gracefully.

## The PCAS Ecosystem Vision

DreamTrans is the first step towards a larger ecosystem of interconnected dApps.

- **DreamTrans (This App)**: The **Data Collector**. Its job is to capture the raw, real-time stream of human conversation and convert it into structured, multilingual text data. In the PCAS model, it acts as a "headless" service, providing the `dapp.dreamtrans.translate.stream.v1` capability to the entire ecosystem.
- **DreamNote (Future dApp)**: The **Knowledge Processor**. It will consume data from dApps like DreamTrans, and by leveraging PCAS and Large Language Models (LLMs), it will provide AI-powered summarization, note-taking, and knowledge graph integration.
- **PCAS (The Backbone)**: The central "BGP router" that understands the capabilities of all installed dApps (via their `dapp.yaml` manifests) and routes events between them based on user-defined policies. It transforms simple events into rich, context-aware actions.

## Getting Started & Deployment

This project is fully containerized and designed for easy deployment.

### Prerequisites
- Docker
- An API key from [Speechmatics](https://www.speechmatics.com/)

### Running with Docker (Recommended)

1.  **Create an environment file**:
    Copy the `backend/.env.example` file to a new file named `.env` in the project root and fill in your `SM_API_KEY`.

2.  **Build and Run**:
    ```bash
    # This will build and run the application using docker-compose.yml (if available)
    docker-compose up --build
    ```
    The application will be available at `http://localhost:8080`.

### Production Deployment

Our CI/CD pipeline automatically builds and pushes a multi-platform Docker image to GitHub Packages.

```bash
# Pull the latest image
docker pull ghcr.io/soaringjerry/dreamtrans:latest

# Run the container, passing your API key as an environment variable
docker run -d \
  --name dreamtrans \
  -p 8080:8080 \
  -e SM_API_KEY="your_speechmatics_api_key" \
  --restart unless-stopped \
  ghcr.io/soaringjerry/dreamtrans:latest
