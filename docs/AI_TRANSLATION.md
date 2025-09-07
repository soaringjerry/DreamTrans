# AI Translation (OpenAI-compatible) — Integration Guide

This document describes the custom AI translation pipeline added to DreamTrans, independent of Speechmatics translation.

## Overview

- Backend exposes a WebSocket at `/ws/translate` that accepts finalized transcripts and returns Chinese translations.
- Two context modes are supported:
  - ai_rolling: Fixed-size rolling context window (chars-based)
  - ai_compressed: Asynchronous context compression to keep a dense summary plus recent segments
- Frontend includes a selector to choose between: Speechmatics translation, AI Rolling, and AI Compressed.

## Environment Variables

Required:
```bash
OPENAI_API_KEY=your_openai_compatible_api_key
```

Optional:
```bash
# OpenAI-style API endpoint and model
OPENAI_API_BASE=https://api.openai.com/v1
OPENAI_MODEL=gpt-5
OPENAI_TEMPERATURE=0.2

# Server-side defaults (can be overridden by frontend init message)
ROLLING_CONTEXT_CHARS=1000
COMPRESS_BACKLOG_CHARS=1800
COMPRESS_KEEP_LAST_SEGMENTS=6

# Translation model default
# The translation WebSocket defaults to GPT‑5 Mini unless a model is specified by the client.
```

## WebSocket Protocol

- Init (client → server):
```json
{ "type": "init", "mode": "ai_rolling", "config": { "rolling_window_chars": 1200 } }
```
Modes: `ai_rolling` | `ai_compressed`

- Transcript (client → server):
```json
{
  "type": "transcript",
  "payload": { "speaker": "Speaker", "transcript": "...", "start_time": 1.2, "end_time": 2.8 }
}
```

- Translation (server → client):
```json
{
  "message": "AddTranslation",
  "results": [{ "speaker": "Speaker", "content": "中文翻译...", "start_time": 1.2, "end_time": 2.8 }]
}
```

## Frontend Usage

- Select Translation Mode (top toggle area):
  - Speechmatics 翻译: keeps Speechmatics translation_config
  - AI 滚动翻译: uses backend WS and rolling context window (configurable)
  - AI 压缩翻译: uses backend WS with background summarization

- For AI modes, the app sends an `init` message on mode/setting change, and sends each finalized transcript segment via `transcript` message. Server replies with translation packets that are displayed in the Translation column.

## Notes

- Streaming partials are not enabled yet; backend currently returns final translations.
- For production, restrict CORS and WS origins, and secure OPENAI_API_KEY.
