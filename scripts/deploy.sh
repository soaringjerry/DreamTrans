#!/usr/bin/env bash
set -euo pipefail

# Simple one-command deployment for standalone DreamTrans with RAG
# Usage:
#  ./scripts/deploy.sh \
#    --sm-key YOUR_SPEECHMATICS_KEY \
#    --openai-key YOUR_OPENAI_KEY \
#    [--image ghcr.io/soaringjerry/dreamtrans:latest] \
#    [--openai-base https://api.openai.com/v1] \
#    [--openai-model gpt-4o-mini] \
#    [--embed-model text-embedding-3-small]

IMAGE="ghcr.io/soaringjerry/dreamtrans:latest"
SM_KEY=""
OPENAI_KEY=""
OPENAI_BASE=""
OPENAI_MODEL=""
EMBED_MODEL=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --image) IMAGE="$2"; shift 2 ;;
    --sm-key) SM_KEY="$2"; shift 2 ;;
    --openai-key) OPENAI_KEY="$2"; shift 2 ;;
    --openai-base) OPENAI_BASE="$2"; shift 2 ;;
    --openai-model) OPENAI_MODEL="$2"; shift 2 ;;
    --embed-model) EMBED_MODEL="$2"; shift 2 ;;
    *) echo "Unknown arg: $1"; exit 1 ;;
  esac
done

if [[ -z "$SM_KEY" || -z "$OPENAI_KEY" ]]; then
  echo "Missing required --sm-key and/or --openai-key"
  exit 1
fi

echo "Pulling image: $IMAGE"
docker pull "$IMAGE"

echo "Stopping existing container (if any)"
docker rm -f dreamtrans 2>/dev/null || true

echo "Starting DreamTrans (standalone) on :8080 with persistent volume 'dreamtrans_data'"
docker run -d \
  --name dreamtrans \
  -p 8080:8080 \
  -e SM_API_KEY="$SM_KEY" \
  -e OPENAI_API_KEY="$OPENAI_KEY" \
  ${OPENAI_BASE:+-e OPENAI_API_BASE="$OPENAI_BASE"} \
  ${OPENAI_MODEL:+-e OPENAI_MODEL="$OPENAI_MODEL"} \
  ${EMBED_MODEL:+-e OPENAI_EMBEDDING_MODEL="$EMBED_MODEL"} \
  -v dreamtrans_data:/app/data \
  --restart unless-stopped \
  "$IMAGE"

echo "DreamTrans is up: http://localhost:8080"
echo "- API:   http://localhost:8080/api"
echo "- WS:    ws://localhost:8080/ws/translate"
echo "- RAG:   POST /api/rag/ask"

