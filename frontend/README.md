# DreamTrans Frontend

Real-time speech transcription and translation frontend using React + TypeScript + Vite.

## Setup

1. Install dependencies:
```bash
npm install
```

2. Copy `.env.example` to `.env` and configure as needed:
```bash
cp .env.example .env
```

3. Start the development server:
```bash
npm run dev
```

## Environment Variables

### Speechmatics Configuration

- `VITE_SPEECHMATICS_OPERATING_POINT` - Model quality setting
  - Values: `standard` or `enhanced` 
  - Default: `enhanced`
  - The enhanced model provides better accuracy at slightly higher latency

- `VITE_SPEECHMATICS_MAX_DELAY` - Delay before returning final transcripts
  - Range: `0.7` to `4` seconds
  - Default: Speechmatics default (4 seconds)
  - Lower values return results faster but may be less accurate
  - Example: `VITE_SPEECHMATICS_MAX_DELAY=2.0`

## Features

- Real-time speech-to-text transcription
- Speaker diarization
- Enhanced model for better accuracy
- Configurable latency settings
- Clean conversation-style display

## Development

This project uses Vite for fast development with HMR (Hot Module Replacement).

### Available Scripts

- `npm run dev` - Start development server
- `npm run build` - Build for production
- `npm run preview` - Preview production build
- `npm run lint` - Run ESLint
- `npm run type-check` - Type-check all TypeScript project references
- `npm run verify:core` - Verify browser audio, session, transcript, and security lifecycles
- `npm run verify:long-session` - Verify bounded long-session UI behavior
- `npm run verify:ai` - Verify AI API payloads and local artifact isolation
- `npm run test:e2e` - Run the mocked AI workspace flow in Playwright Chromium

### Browser workflow tests

The Playwright tests run on Linux/WSL and mock the HTTP API at the browser
boundary, so they do not require PostgreSQL or a real OpenAI account:

```bash
npx playwright install --with-deps chromium
npm run test:e2e
```
