# DreamTrans - Real-Time AI Transcription and Translation

**DreamTrans** is a real-time transcription and translation DAPP within the DreamHub ecosystem. It captures, transcribes, and translates spoken language directly in your browser, with future integration to PCAS (Personal Central AI System) for advanced AI-powered note-taking and summarization.

**Note**: For optimal performance and accuracy, this DAPP currently operates in cloud mode only, leveraging the Speechmatics Real-Time API.

This project is currently in the **active development phase**.

## Core Features (Current)

- **Real-Time Transcription**: Leverages the [Speechmatics Real-Time API](https://www.speechmatics.com/) to provide highly accurate, low-latency transcription of microphone audio.
- **Intelligent Paragraph Handling**: Automatically detects significant pauses in speech to intelligently segment conversations into distinct paragraphs, mirroring natural speech patterns.
- **Seamless User Experience**: A clean, intuitive interface built with React and TypeScript provides a clear distinction between finalized text and in-progress "partial" results.
- **Secure Backend-for-Frontend (BFF)**: A Go (Golang) backend manages secure authentication with the Speechmatics API by issuing short-lived JWTs, ensuring API keys are never exposed on the client-side.
- **Placeholder for Translation**: The architecture includes a dedicated WebSocket endpoint (`/ws/translate`) ready for the implementation of real-time translation features.

## Future Vision & DreamHub Integration

DreamTrans is more than just a transcription tool; it's the first step towards an intelligent, context-aware AI assistant. The future roadmap includes:

1.  **Real-Time Translation**: Implementing the translation layer to provide multilingual support in real-time.
2.  **AI Notes & Summarization (DreamHub PCAS Integration)**: The ultimate goal is to connect DreamTrans to the **DreamHub PCAS (Personal Central AI System) core**. This will enable groundbreaking features:
    - **Knowledge-based Memory**: Transcribed and translated content will be fed into a personal knowledge base.
    - **AI-Generated Notes**: The system will automatically generate structured, intelligent notes from the conversation.
    - **Contextual Summaries**: Leveraging the PCAS core, DreamTrans will provide summaries that are aware of the user's existing knowledge and context.

## Technology Stack

- **Frontend**:
  - [React](https://react.dev/)
  - [TypeScript](https://www.typescriptlang.org/)
  - [Vite](https://vitejs.dev/)
  - [@speechmatics/real-time-client-react](https://github.com/speechmatics/speechmatics-js/tree/main/packages/real-time-client-react)
- **Backend**:
  - [Go (Golang)](https://go.dev/)
- **API**:
  - [Speechmatics Real-Time API](https://docs.speechmatics.com/rt-api)

## Getting Started

> **Note**: This project is under active development. Setup instructions will be finalized as the project approaches a stable release.

### Prerequisites

- Node.js and npm (for frontend)
- Go (for backend)
- A Speechmatics API Key

### Backend Setup

```bash
# Navigate to the backend directory
cd backend

# Create a .env file from the example
cp .env.example .env

# Add your Speechmatics API key to the .env file
# SM_API_KEY="YOUR_API_KEY_HERE"

# Install dependencies and run the server
go mod tidy
go run main.go
```

### Frontend Setup

```bash
# Navigate to the frontend directory
cd frontend

# Create a .env file from the example
cp .env.example .env

# (Optional) Configure Speechmatics parameters in .env if needed

# Install dependencies and start the development server
npm install
npm run dev
```

Once both servers are running, open your browser to the address provided by Vite (usually `http://localhost:5173`).
