import { useState, useRef, useEffect, useMemo, useCallback } from 'react';
import {
  RealtimeTranscriptionProvider,
  useRealtimeTranscription,
  useRealtimeEventListener,
  type RealtimeTranscriptionConfig,
} from '@speechmatics/real-time-client-react';
import {
  PCMAudioRecorderProvider,
  usePCMAudioRecorderContext,
  usePCMAudioListener,
} from '@speechmatics/browser-audio-input-react';
import { getJwt, resetMetrics } from './api';
import { useBackendWebSocket } from './hooks/useBackendWebSocket';
import { useSmartScroll } from './hooks/useSmartScroll';
import { useReconnectionHandler } from './hooks/useReconnectionHandler';
import { saveSession, loadSession, clearSession } from './db';
import { throttle } from 'lodash';
import { TranscriptItem } from './components/TranscriptItem';
import { TranslationItem } from './components/TranslationItem';
import './App.css';
import TopBar from './components/TopBar';
import ChatPanel from './components/ChatPanel';
import KnowledgePanel from './components/KnowledgePanel';
import FloatingDock from './components/FloatingDock';
import PerformancePanel from './components/PerformancePanel';
import GlobalOverlays from './components/GlobalOverlays';
import { emitMetric } from './utils/metrics';
import BilingualPanel from './components/BilingualPanel';
import { lexIngest, lexReset } from './utils/lexicon';
import { emitProState, onProCommand, type ProStateSnapshot } from './pro/bridge';
// Dictionary popover removed; will use cloud API externally in future

// High-resolution timestamp helper function
const getHighResTimestamp = () => {
  const now = new Date();
  return `${now.toISOString().slice(0, -1)}${String(now.getMilliseconds()).padStart(3, '0')}`;
};

interface ConfirmedSegment {
  text: string;
  startTime: number;
  endTime: number;
}

interface TranscriptLine {
  id: number;
  speaker: string;
  confirmedSegments: ConfirmedSegment[]; // Confirmed transcription segments
  partialText: string;                   // Partial/unconfirmed text
  lastSegmentEndTime: number;            // End time of last segment for gap detection
}

interface TranslationLine {
  id: string;                            // Unique ID composed from speaker and startTime
  speaker: string;
  startTime: number;
  content: string;
  original?: string;
  isPartial: boolean;
}


interface SpeechmaticsMessage {
  message: string;
  metadata?: {
    transcript?: string;
    start_time?: number;
    end_time?: number;
  };
  results?: Array<{
    alternatives?: Array<{
      speaker?: string;
      content?: string;
    }>;
    // Translation results
    start_time?: number;
    end_time?: number;
    content?: string;
    speaker?: string;
  }>;
  language?: string;
  reason?: string;
  type?: string;
  seq_no?: number;
}

interface BatchTranscriptionResult {
  status: string;
  error?: string;
  transcript?: {
    results: Array<{
      alternatives: Array<{
        content: string;
        speaker?: string;
      }>;
      start_time: number;
      end_time: number;
    }>;
  };
}

function TranscriptionApp() {
  const [isTranscribing, setIsTranscribing] = useState(false);
  const [isInitializing, setIsInitializing] = useState(false);
  const [isPaused, setIsPaused] = useState(false);
  const isPausedRef = useRef(false);
  useEffect(() => { isPausedRef.current = isPaused }, [isPaused])
  const [error, setError] = useState<string | null>(null);
  const [lines, setLines] = useState<TranscriptLine[]>([]);
  const [translations, setTranslations] = useState<TranslationLine[]>([]);
  const [translatedUntilBySpeaker, setTranslatedUntilBySpeaker] = useState<Record<string, number>>({});
  type TranslationMode = 'speechmatics' | 'ai_rolling' | 'ai_compressed';
  const [translationMode, setTranslationMode] = useState<TranslationMode>('ai_rolling');
  type ModelChoice = 'GPT5' | 'GPT5MINI' | 'GPT5NANO';
  const [modelChoice, setModelChoice] = useState<ModelChoice>('GPT5MINI');
  const [rollingContextChars] = useState<number>(1000);
  const [typewriterEnabled, setTypewriterEnabled] = useState(false); // Moved to Settings (default off)
  const [bilingualEnabled, setBilingualEnabled] = useState(false);
  const [elapsedTime, setElapsedTime] = useState(0); // Recording time in seconds
  const [isBatchProcessing, setIsBatchProcessing] = useState(false);
  const [loadedAudioBlob, setLoadedAudioBlob] = useState<Blob | null>(null);
  const nextIdRef = useRef(1);
  // Track last update time for partial lines; auto-clear stale partials
  const partialUpdatedAtRef = useRef<Map<number, number>>(new Map());
  // Speechmatics max_delay (seconds) for partial life; fallback 2s
  const smMaxDelaySecRef = useRef<number>(2);
  const timerIntervalRef = useRef<number | null>(null);
  const PARAGRAPH_BREAK_SILENCE_THRESHOLD = 2.0; // 2 second silence threshold for paragraph breaks
  
  // Recording states
  const [, setIsRecording] = useState(false);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const audioChunksRef = useRef<Blob[]>([]);
  const audioStreamRef = useRef<MediaStream | null>(null);
  // Wall-clock reference when starting a session, for transcript latency estimation
  const sessionStartEpochRef = useRef<number | null>(null);
  // Calibrated offset between ASR audio time (end_time) and wall clock
  const asrOffsetRef = useRef<number>(0);
  const asrOffsetReadyRef = useRef<boolean>(false);
  
  // Session management
  const [SESSION_ID, setSESSION_ID] = useState<string>(() => `session_${Date.now()}`);
  useEffect(() => { lexReset(SESSION_ID) }, [SESSION_ID])
  // Selection popover for AI lookup
  const [selOpen, setSelOpen] = useState(false)
  const [selX, setSelX] = useState(0)
  const [selY, setSelY] = useState(0)
  const [selText, setSelText] = useState('')
  const linesRef = useRef<TranscriptLine[]>([]);
  const translationsRef = useRef<TranslationLine[]>([]);
  // removed legacy dev-only restore flow
  // Store transcription config for reconnection
  const transcriptionConfigRef = useRef<RealtimeTranscriptionConfig | null>(null);
  
  // Scroll container refs for auto-scrolling
  const originalColumnRef = useRef<HTMLDivElement>(null);
  const translationColumnRef = useRef<HTMLDivElement>(null);
  
  // Throttle save operations to once every 10 seconds
  const throttledSave = useMemo(
    () => throttle(async () => {
      const audioBlob = audioChunksRef.current.length > 0 
        ? new Blob(audioChunksRef.current, { type: 'audio/webm' })
        : null;
      
      const saved = await saveSession(SESSION_ID, {
        audioBlob,
        lines: linesRef.current,
        translations: translationsRef.current,
      });
      
      if (saved) {
        console.log('Session saved to IndexedDB');
      }
    }, 10000, { leading: false, trailing: true }),
    [SESSION_ID]
  );

  // Pro UI bridge: publish trimmed state for the Vue shell
  const PRO_RENDER_WINDOW = 400;
  const publishToPro = useMemo(
    () =>
      throttle(() => {
        const fullLines = linesRef.current || [];
        const fullTranslations = translationsRef.current || [];
        const trimmedLines = fullLines.length > PRO_RENDER_WINDOW ? fullLines.slice(-PRO_RENDER_WINDOW) : fullLines;
        const trimmedTranslations = fullTranslations.length > PRO_RENDER_WINDOW ? fullTranslations.slice(-PRO_RENDER_WINDOW) : fullTranslations;
        const snapshot: ProStateSnapshot = {
          lines: trimmedLines.map((l) => ({
            id: l.id,
            speaker: l.speaker,
            confirmedSegments: l.confirmedSegments.map((s) => ({ text: s.text, startTime: s.startTime, endTime: s.endTime })),
            partialText: l.partialText,
          })),
          translations: trimmedTranslations.map((t) => ({
            id: t.id,
            speaker: t.speaker,
            startTime: t.startTime,
            content: t.content,
            original: t.original,
            isPartial: t.isPartial,
          })),
          isTranscribing,
          isInitializing,
          isPaused,
          elapsedTime,
          sessionId: SESSION_ID,
          hiddenCounts: {
            transcripts: fullLines.length > PRO_RENDER_WINDOW ? fullLines.length - PRO_RENDER_WINDOW : 0,
            translations: fullTranslations.length > PRO_RENDER_WINDOW ? fullTranslations.length - PRO_RENDER_WINDOW : 0,
          },
        };
        emitProState(snapshot);
      }, 750, { leading: true, trailing: true }),
    [SESSION_ID, elapsedTime, isInitializing, isPaused, isTranscribing],
  );

  // Load global settings on mount & when updated
  useEffect(() => {
    const loadSettings = () => {
      try {
        const raw = localStorage.getItem('dt_settings_v1')
        if (!raw) return
        const s = JSON.parse(raw) as { transMode?: string; transModel?: string; experimental_streaming?: boolean; experimental_smart?: boolean; experimental_typewriter?: boolean; experimental_bilingual?: boolean }
        if (s.transMode === 'speechmatics' || s.transMode === 'ai_rolling' || s.transMode === 'ai_compressed') {
          setTranslationMode(s.transMode as TranslationMode)
        }
        if (s.transModel) {
          const map: Record<string, ModelChoice> = {
            'gpt-5': 'GPT5', 'gpt-5-mini': 'GPT5MINI', 'gpt-5-nano': 'GPT5NANO'
          }
          const mc = map[s.transModel]
          if (mc) setModelChoice(mc)
        }
        setTypewriterEnabled(!!s.experimental_typewriter)
        setBilingualEnabled(s.experimental_bilingual !== undefined ? !!s.experimental_bilingual : true)
      } catch { /* ignore */ }
    }
    loadSettings()
    const onUpdated = () => loadSettings()
    window.addEventListener('dt-settings-updated', onUpdated)
    return () => window.removeEventListener('dt-settings-updated', onUpdated)
  }, [SESSION_ID])

  // Keep Pro UI in sync with the live state (trimmed to avoid huge payloads)
  useEffect(() => {
    publishToPro();
  }, [publishToPro, lines, translations, isTranscribing, isInitializing, isPaused, elapsedTime])

  const { startTranscription, stopTranscription, sendAudio, sessionId, socketState } = useRealtimeTranscription();
  const { startRecording, stopRecording } = usePCMAudioRecorderContext();
  // Backend WS: handle translation messages from our server
  const onBackendMessage = useCallback((msg: unknown) => {
    if (!msg || typeof msg !== 'object') return;
    const anyMsg = msg as { message?: string; results?: Array<{ speaker?: string; content?: string; original?: string; start_time?: number; end_time?: number; model?: string; latency_ms?: number }>; reason?: string };
    if (anyMsg.message === 'AddTranslation' && anyMsg.results && anyMsg.results.length > 0) {
      const t = anyMsg.results[0];
      const speaker = t.speaker || 'Speaker';
      const content = t.content || '';
      const original = t.original || '';
      const startTime = t.start_time || 0;
      const id = `${speaker}-${startTime}`;
      if (typeof t.end_time === 'number') {
        setTranslatedUntilBySpeaker(prev => ({ ...prev, [speaker]: Math.max(prev[speaker] || 0, t.end_time || 0) }))
      }
      setTranslations((prev) => {
        const list = [...prev];
        const existingIndex = list.findIndex(x => x.id === id);
        if (existingIndex !== -1) list[existingIndex] = { id, speaker, startTime, content, original, isPartial: false };
        else list.push({ id, speaker, startTime, content, original, isPartial: false });
        translationsRef.current = list;
        throttledSave();
        return list;
      });
      // emit metrics
      if (t.latency_ms != null) {
        emitMetric({ kind: 'translation', latency_ms: t.latency_ms, model: t.model, partial: false })
      }
    } else if (anyMsg.message === 'AddPartialTranslation' && anyMsg.results && anyMsg.results.length > 0) {
      // Ignore AI partial translations by design: only translate final
    } else if (anyMsg.message === 'Error') {
      setError(anyMsg.reason || 'Translation error');
    }
  }, [throttledSave]);

  const { connect, sendMessage, disconnect, status: backendWsStatus } = useBackendWebSocket(onBackendMessage);
  
  // console.log('Speechmatics connection state:', socketState, 'sessionId:', sessionId);
  
  // Listen for all messages from Speechmatics
  useRealtimeEventListener('receiveMessage', (event: unknown) => {
    const eventData = event as { data?: unknown };
    const message = (eventData.data || event) as SpeechmaticsMessage;
    
    if (message.message === 'RecognitionStarted') {
      // console.log('Recognition started!', message);
    } else if (message.message === 'AddTranscript') {
      // Handle final transcript
      if (message.metadata?.transcript && message.metadata.transcript.trim()) {
        const speaker = message.results?.[0]?.alternatives?.[0]?.speaker || 'Speaker';
        const transcript = message.metadata.transcript;
        const startTime = message.metadata.start_time || 0;
        const endTime = message.metadata.end_time || 0;
        
        console.log('Final:', transcript);
        
        setLines((prevLines) => {
          const newLines = [...prevLines];
          
          // Get the absolute last line regardless of speaker
          const lastLine = newLines.length > 0 ? newLines[newLines.length - 1] : null;
          
          // Determine if we should start a new paragraph
          let shouldStartNewParagraph = false;
          
          if (!lastLine || lastLine.speaker !== speaker) {
            // Rule 1: If no previous line or speaker changed, must start new paragraph
            shouldStartNewParagraph = true;
            if (lastLine) {
              console.log(`[${speaker}] Starting new paragraph due to speaker change from ${lastLine.speaker}`);
            }
          } else {
            // Rule 2: Same speaker - check time gap only if last line has confirmed segments
            if (lastLine.confirmedSegments.length > 0) {
              const timeGap = startTime - lastLine.lastSegmentEndTime;
              if (timeGap > PARAGRAPH_BREAK_SILENCE_THRESHOLD) {
                shouldStartNewParagraph = true;
                console.log(`[${speaker}] Starting new paragraph due to ${timeGap.toFixed(2)}s gap`);
              }
            } else {
              console.log(`[${speaker}] Continuing partial line - no confirmed segments yet`);
            }
          }
          
          // Create a new segment object with metadata
          const newSegment: ConfirmedSegment = {
            text: transcript,
            startTime: startTime,
            endTime: endTime
          };
          
          if (shouldStartNewParagraph) {
            // Create a new paragraph
            newLines.push({
              id: nextIdRef.current++,
              speaker,
              confirmedSegments: [newSegment],
              partialText: '',
              lastSegmentEndTime: endTime
            });
          } else {
            // Continue existing paragraph (we know lastLine exists and is same speaker)
            const lastLineIndex = newLines.length - 1;
            const updatedLine = { ...newLines[lastLineIndex] };
            
            // Check for duplicate based on start time (more reliable than text comparison)
            const lastSegment = updatedLine.confirmedSegments.at(-1);
            if (!lastSegment || lastSegment.startTime !== newSegment.startTime) {
              updatedLine.confirmedSegments.push(newSegment);
              console.log(`[${speaker}] Added segment: "${transcript}" at ${startTime.toFixed(2)}s`);
            } else {
              console.log(`[${speaker}] Duplicate event detected and skipped for segment: "${transcript}" at ${startTime.toFixed(2)}s`);
            }
            
            updatedLine.lastSegmentEndTime = endTime;
            updatedLine.partialText = ''; // Clear partial as this part is now confirmed
            try { partialUpdatedAtRef.current.delete(updatedLine.id) } catch { /* noop */ }
            newLines[lastLineIndex] = updatedLine;
          }
          
          // Update ref and trigger save
          linesRef.current = newLines;
          throttledSave();
          try { lexIngest(SESSION_ID, transcript) } catch { /* noop */ }
          
          return newLines;
        });
        
        // Send to backend
        // Send to backend (AI translation modes)
        if (translationMode === 'ai_rolling' || translationMode === 'ai_compressed') {
          sendMessage({ type: 'transcript', payload: { speaker, transcript, start_time: startTime, end_time: endTime } });
        }
        // Emit transcript latency metric using calibrated offset to reduce drift
        if (typeof endTime === 'number' && Number.isFinite(endTime)) {
          const now = Date.now()
          const delta = now - Math.round(endTime * 1000)
          // initialize or refine EMA offset
          if (!asrOffsetReadyRef.current) {
            asrOffsetRef.current = delta
            asrOffsetReadyRef.current = true
          } else {
            const alpha = 0.12 // smoothing factor
            asrOffsetRef.current = (1 - alpha) * asrOffsetRef.current + alpha * delta
          }
          const latencyMs = Math.max(0, Math.round(delta - asrOffsetRef.current))
          // filter obvious outliers (>20s)
          if (latencyMs < 20_000) {
            emitMetric({ kind: 'transcript', latency_ms: latencyMs })
          }
        }
      }
    } else if (message.message === 'AddPartialTranscript') {
      // Handle partial transcript
      if (message.metadata?.transcript && message.metadata.transcript.trim()) {
        const speaker = message.results?.[0]?.alternatives?.[0]?.speaker || 'Speaker';
        const partialText = message.metadata.transcript;
        const startTime = message.metadata.start_time || 0;
        
        setLines((prevLines) => {
          const newLines = [...prevLines];
          
          // Get the absolute last line regardless of speaker
          const lastLine = newLines.length > 0 ? newLines[newLines.length - 1] : null;
          
          // Determine if we should start a new paragraph
          let shouldStartNewParagraph = false;
          
          if (!lastLine || lastLine.speaker !== speaker) {
            // Rule 1: If no previous line or speaker changed, must start new paragraph
            shouldStartNewParagraph = true;
            if (lastLine) {
              console.log(`[${speaker}] Starting new paragraph in PARTIAL due to speaker change from ${lastLine.speaker}`);
            }
          } else {
            // Rule 2: Same speaker - check time gap only if last line has confirmed segments
            if (lastLine.confirmedSegments.length > 0) {
              const timeGap = startTime - lastLine.lastSegmentEndTime;
              if (timeGap > PARAGRAPH_BREAK_SILENCE_THRESHOLD) {
                shouldStartNewParagraph = true;
                console.log(`[${speaker}] Starting new paragraph in PARTIAL due to ${timeGap.toFixed(2)}s gap`);
              }
            }
          }
          
          if (shouldStartNewParagraph) {
            // Create a new paragraph
            const newId = nextIdRef.current++
            newLines.push({
              id: newId,
              speaker,
              confirmedSegments: [],
              partialText: partialText,
              lastSegmentEndTime: startTime // Use Partial's startTime to avoid false gap detection
            });
            try { partialUpdatedAtRef.current.set(newId, Date.now()) } catch { /* noop */ }
          } else {
            // Update the existing line's partial text (we know lastLine exists and is same speaker)
            const lastLineIndex = newLines.length - 1;
            const updatedLine = { ...newLines[lastLineIndex] };
            updatedLine.partialText = partialText;
            newLines[lastLineIndex] = updatedLine;
            try { partialUpdatedAtRef.current.set(updatedLine.id, Date.now()) } catch { /* noop */ }
          }
          
          // Update ref but don't trigger save for partial updates (too frequent)
          linesRef.current = newLines;
          
          return newLines;
        });
      }
    } else if (message.message === 'AddTranslation' && translationMode === 'speechmatics') {
      // Handle final translation
      if (message.results && message.results.length > 0) {
        const translationResult = message.results[0];
        const speaker = translationResult.speaker || 'Speaker';
        const content = translationResult.content || '';
        const startTime = translationResult.start_time || 0;
        if (typeof translationResult.end_time === 'number') {
          setTranslatedUntilBySpeaker(prev => ({ ...prev, [speaker]: Math.max(prev[speaker] || 0, translationResult.end_time || 0) }))
          // Emit translation latency for Speechmatics path using end_time vs wall clock
          if (sessionStartEpochRef.current != null) {
            const expectedWall = sessionStartEpochRef.current + Math.round((translationResult.end_time || 0) * 1000)
            const latencyMs = Math.max(0, Date.now() - expectedWall)
            emitMetric({ kind: 'translation', latency_ms: latencyMs, partial: false })
          }
        }

        console.log('Translation:', content);
        console.log('AddTranslation received:', {
          startTime,
          content,
          speaker
        });
        
        setTranslations((prevTranslations) => {
          const newTranslations = [...prevTranslations];
          
          // Create unique ID for this translation
          const id = `${speaker}-${startTime}`;
          
          // Check if we already have a partial translation for this ID
          const existingIndex = newTranslations.findIndex(t => t.id === id && t.isPartial);
          
          if (existingIndex !== -1) {
            // Replace the partial translation with the final one
            newTranslations[existingIndex] = {
              id,
              speaker,
              startTime,
              content,
              isPartial: false
            };
            console.log(`Replaced partial translation at index ${existingIndex} with final translation`);
          } else {
            // Add new final translation
            newTranslations.push({
              id,
              speaker,
              startTime,
              content,
              isPartial: false
            });
            console.log(`Added new final translation for ${speaker} at ${startTime}s`);
          }
          
          // Update ref and trigger save
          translationsRef.current = newTranslations;
          throttledSave();
          
          return newTranslations;
        });
      }
    } else if (message.message === 'AddPartialTranslation' && translationMode === 'speechmatics') {
      // Ignore Speechmatics partial translations by design: only translate final
    } else if (message.message === 'Error') {
      console.error('Speechmatics error:', message);
      console.error('Error details:', JSON.stringify(message, null, 2));
      
      // Special handling for translation-related errors
      if (message.reason && message.reason.includes('translation')) {
        setError(`Translation error: ${message.reason}. Translation might not be available on your account.`);
      } else {
        setError(message.reason || message.type || 'Unknown error');
      }
    } else if (message.message === 'Info') {
      // console.log('Speechmatics info:', message);
    } else if (message.message === 'AudioAdded') {
      // console.log('Audio confirmed by server, seq_no:', message.seq_no);
    }
  });
  
  // Send audio to Speechmatics - simplified direct approach
  usePCMAudioListener((audioData: ArrayBuffer) => {
    console.log(`[${getHighResTimestamp()}] AUDIO_CAPTURED: ${audioData.byteLength} bytes`);
    if (sessionId && socketState === 'open' && !isPausedRef.current) {
      // For pcm_f32le, each sample is 4 bytes
      if (audioData.byteLength % 4 !== 0) {
        console.error('Audio data length is not a multiple of 4 bytes!', audioData.byteLength);
        return;
      }
      console.log(`[${getHighResTimestamp()}] AUDIO_SENDING: ${audioData.byteLength} bytes`);
      sendAudio(audioData);
    }
  });

  // Apply smart auto-scroll to original text column
  useSmartScroll(originalColumnRef, lines);
  
  // Apply smart auto-scroll to translation column
  useSmartScroll(translationColumnRef, translations);

  // Periodically clear stale partials if older than Speechmatics max_delay to improve UX
  useEffect(() => {
    const id = window.setInterval(() => {
      const maxAgeMs = smMaxDelaySecRef.current * 1000;
      if (!Number.isFinite(maxAgeMs) || maxAgeMs <= 0) return;
      const now = Date.now();
      let changed = false;
      setLines(prev => {
        const arr = [...prev];
        for (let i = 0; i < arr.length; i++) {
          const ln = arr[i];
          if (ln.partialText && ln.partialText.trim() !== '') {
            const ts = partialUpdatedAtRef.current.get(ln.id) || 0;
            if (ts > 0 && (now - ts) > maxAgeMs) {
              arr[i] = { ...ln, partialText: '' };
              partialUpdatedAtRef.current.delete(ln.id);
              changed = true;
            }
          }
        }
        if (changed) {
          linesRef.current = arr;
        }
        return changed ? arr : prev;
      });
    }, 1000);
    return () => window.clearInterval(id);
  }, []);
  
  // Connect to backend WebSocket on mount
  useEffect(() => {
    connect();
    return () => {
      disconnect();
    };
  }, [connect, disconnect]);

  // Send translator init when mode or settings change (AI modes)
  // Map UI model choices to backend/OpenAI model ids
  const resolveModelId = useCallback((choice: ModelChoice): string => {
    switch (choice) {
      case 'GPT5': return 'gpt-5';
      case 'GPT5MINI': return 'gpt-5-mini';
      case 'GPT5NANO': return 'gpt-5-nano';
      default: return 'gpt-5-mini';
    }
  }, []);

  // Send translator init whenever WS is open (handles initial connect + reconnect)
  useEffect(() => {
    if (backendWsStatus === 'open' && (translationMode === 'ai_rolling' || translationMode === 'ai_compressed')) {
      // read experimental flags at send time
      let expStreaming = false, expSmart = false
      let promptTranslate = '', promptSummary = ''
      let modelTranslateOverride = ''
      let modelSummaryOverride = ''
      let expSummary = false
      let expEmb = true
      try {
        const raw = localStorage.getItem('dt_settings_v1')
        if (raw) {
          const s = JSON.parse(raw) as { experimental_streaming?: boolean; experimental_smart?: boolean; experimental_summary?: boolean; experimental_embeddings?: boolean; prompt_translate?: string; prompt_summary?: string; model_translate?: string; model_summary?: string }
          expStreaming = !!s.experimental_streaming
          expSmart = !!s.experimental_smart
          promptTranslate = s.prompt_translate || ''
          promptSummary = s.prompt_summary || ''
          modelTranslateOverride = (s.model_translate || '').trim()
          modelSummaryOverride = (s.model_summary || '').trim()
          expSummary = s.experimental_summary !== undefined ? !!s.experimental_summary : false
          expEmb = s.experimental_embeddings !== undefined ? !!s.experimental_embeddings : true
        }
      } catch { /* ignore */ }
      const initMsg = {
        type: 'init' as const,
        mode: translationMode,
        config: {
          rolling_window_chars: rollingContextChars,
          model: modelTranslateOverride || resolveModelId(modelChoice),
          translate_model: modelTranslateOverride || resolveModelId(modelChoice),
          summary_model: modelSummaryOverride || '',
          session_id: SESSION_ID,
          experimental_streaming: expStreaming,
          experimental_smart: expSmart,
          translate_prompt: promptTranslate,
          summary_prompt: promptSummary,
          disable_summarization: !expSummary,
          summarization_enabled: expSummary,
          disable_embeddings: !expEmb,
          embeddings_enabled: expEmb,
        },
      };
      sendMessage(initMsg);
    }
  }, [backendWsStatus, translationMode, rollingContextChars, modelChoice, resolveModelId, sendMessage, SESSION_ID]);

  // Re-send init when settings change (e.g., prompt updates) while WS is open
  useEffect(() => {
    const onUpdated = () => {
      if (backendWsStatus !== 'open') return
      if (!(translationMode === 'ai_rolling' || translationMode === 'ai_compressed')) return
      let expStreaming = false, expSmart = false
      let promptTranslate = '', promptSummary = ''
      let modelTranslateOverride = ''
      let modelSummaryOverride = ''
      try {
        const raw = localStorage.getItem('dt_settings_v1')
        if (raw) {
          const s = JSON.parse(raw) as { experimental_streaming?: boolean; experimental_smart?: boolean; experimental_summary?: boolean; experimental_embeddings?: boolean; prompt_translate?: string; prompt_summary?: string; model_translate?: string; model_summary?: string }
          expStreaming = !!s.experimental_streaming
          expSmart = !!s.experimental_smart
          promptTranslate = s.prompt_translate || ''
          promptSummary = s.prompt_summary || ''
          modelTranslateOverride = (s.model_translate || '').trim()
          modelSummaryOverride = (s.model_summary || '').trim()
        }
      } catch { /* ignore */ }
      const raw2 = localStorage.getItem('dt_settings_v1')
      const s2 = raw2 ? (JSON.parse(raw2) as { experimental_summary?: boolean; experimental_embeddings?: boolean }) : undefined
      const expSummary = s2?.experimental_summary ? true : false
      const expEmb = s2?.experimental_embeddings !== undefined ? !!s2.experimental_embeddings : true
      const initMsg = {
        type: 'init' as const,
        mode: translationMode,
        config: {
          rolling_window_chars: rollingContextChars,
          model: modelTranslateOverride || resolveModelId(modelChoice),
          translate_model: modelTranslateOverride || resolveModelId(modelChoice),
          summary_model: modelSummaryOverride || '',
          session_id: SESSION_ID,
          experimental_streaming: expStreaming,
          experimental_smart: expSmart,
          translate_prompt: promptTranslate,
          summary_prompt: promptSummary,
          disable_summarization: !expSummary,
          summarization_enabled: expSummary,
          disable_embeddings: !expEmb,
          embeddings_enabled: expEmb,
        },
      };
      sendMessage(initMsg)
    }
    window.addEventListener('dt-settings-updated', onUpdated)
    return () => window.removeEventListener('dt-settings-updated', onUpdated)
  }, [backendWsStatus, translationMode, rollingContextChars, modelChoice, resolveModelId, sendMessage, SESSION_ID])
  
  // Listen restore session event
  useEffect(() => {
    const onRestore = async (e: Event) => {
      const ce = e as CustomEvent
      const id = ce.detail?.session_id as string | undefined
      if (!id) return
      const savedSession = await loadSession(id)
      if (!savedSession) return
      setSESSION_ID(id)
      lexReset(id)
      setLines(savedSession.lines)
      linesRef.current = savedSession.lines
      setTranslations(savedSession.translations || [])
      translationsRef.current = savedSession.translations || []
      try {
        for (const line of savedSession.lines) {
          const t = line.confirmedSegments.map(s => s.text).join(' ')
          if (t) lexIngest(id, t)
        }
      } catch { /* noop */ }
      if (savedSession.audioBlob) {
        audioChunksRef.current = [savedSession.audioBlob]
        setLoadedAudioBlob(savedSession.audioBlob)
      } else {
        audioChunksRef.current = []
        setLoadedAudioBlob(null)
      }
      console.log(`Session restored: ${id}`)
    }
    window.addEventListener('dt-restore-session', onRestore as EventListener)
    return () => window.removeEventListener('dt-restore-session', onRestore as EventListener)
  }, [])

  // Define reconnection action
  const reconnectAction = useCallback(async () => {
    if (!transcriptionConfigRef.current) {
      throw new Error('No transcription config available');
    }

    // Get a new JWT token
    const newJwt = await getJwt();
    
    // Attempt to restart transcription with the same configuration
    await startTranscription(newJwt, transcriptionConfigRef.current);
    
    console.log('Successfully reconnected to Speechmatics');
  }, [startTranscription]);

  // Determine if we should attempt reconnection
  const shouldReconnect = isTranscribing && 
    (socketState === 'closing' || socketState === 'closed' || socketState === undefined);

  // Use the reconnection handler
  const { isReconnecting, attempt, error: reconnectError } = useReconnectionHandler({
    shouldReconnect,
    reconnectAction,
    maxRetries: 10,
    initialDelay: 1000,
    maxDelay: 30000
  });

  // Update error state based on reconnection status
  useEffect(() => {
    if (isReconnecting && attempt > 0) {
      setError(`Connection lost. Reconnection attempt ${attempt}/10...`);
    } else if (reconnectError) {
      setError(reconnectError);
    } else if (!isReconnecting && socketState === 'open' && error?.includes('Connection lost')) {
      // Clear error when successfully reconnected
      setError(null);
    }
  }, [isReconnecting, attempt, reconnectError, socketState, error]);

  // If user returns from background, proactively refresh transport state to avoid backlog
  useEffect(() => {
    const onVisibility = async () => {
      if (document.hidden) return
      if (!isTranscribing) return
      connect()
      if (socketState === 'closed' || socketState === 'closing' || socketState === undefined) {
        try { await reconnectAction() } catch (err) { console.error('visibility reconnect failed', err) }
      }
      if (backendWsStatus === 'open' && (translationMode === 'ai_rolling' || translationMode === 'ai_compressed')) {
        window.dispatchEvent(new CustomEvent('dt-settings-updated'))
      }
    }
    document.addEventListener('visibilitychange', onVisibility)
    return () => document.removeEventListener('visibilitychange', onVisibility)
  }, [backendWsStatus, connect, isTranscribing, reconnectAction, socketState, translationMode])

  const getLookupTemplate = () => {
    const raw = localStorage.getItem('dt_settings_v1')
    if (raw) {
      try { const s = JSON.parse(raw) as { prompt_lookup?: string }; if (s.prompt_lookup) return s.prompt_lookup } catch { /* noop */ }
    }
    return '请解释以下单词或短语的含义，并给出词性、常见搭配和 2 个例句（英文+中文）：\n{{text}}'
  }
  const sendLookup = useCallback((text: string) => {
    const tpl = getLookupTemplate()
    const q = tpl.replace(/\{\{\s*text\s*\}\}/g, text)
    window.dispatchEvent(new CustomEvent('dt-chat-send', { detail: { text: q } }))
  }, [])
  const handleWordClick = useCallback((word: string) => {
    if (!word || !word.trim()) return
    sendLookup(word.trim())
  }, [sendLookup])

  // Selection handling inside Original Text column
  useEffect(() => {
    const el = originalColumnRef.current
    if (!el) return
    const onMouseUp = () => {
      const sel = window.getSelection()
      if (!sel || sel.isCollapsed) { setSelOpen(false); return }
      const t = (sel.toString() || '').trim()
      if (!t) { setSelOpen(false); return }
      // Ensure selection is within Original Text column
      const anchor = sel.anchorNode as Node | null
      if (!anchor) { setSelOpen(false); return }
      if (!el.contains(anchor)) { setSelOpen(false); return }
      const range = sel.rangeCount > 0 ? sel.getRangeAt(0) : null
      if (!range) { setSelOpen(false); return }
      const rect = range.getBoundingClientRect()
      setSelX(rect.left + rect.width / 2)
      setSelY(rect.bottom)
      setSelText(t)
      setSelOpen(true)
    }
    el.addEventListener('mouseup', onMouseUp)
    return () => el.removeEventListener('mouseup', onMouseUp)
  }, [])



  const handleStart = useCallback(async () => {
    // Password verification
    const password = prompt("Please enter password");
    const correctPassword = "233333"; // Default password

    if (password !== correctPassword) {
      alert("Incorrect password");
      return; // Abort function execution
    }
    
    // If password is correct, continue with recording
    try {
      setError(null);
      setIsInitializing(true);
      sessionStartEpochRef.current = Date.now();
      
      // Start a new session id
      const newId = `session_${Date.now()}`
      setSESSION_ID(newId)
      lexReset(newId)
      // Reset API metrics counters so the Performance panel shows a fresh view for this session
      try { await resetMetrics() } catch { /* best-effort */ }
      // Clear previous session data (do not delete old from history)
      audioChunksRef.current = [];
      setLines([]);
      setTranslations([]);
      linesRef.current = [];
      translationsRef.current = [];
      
      // Get JWT from our backend
      const jwt = await getJwt();
      
      // Start transcription with required configuration
      // console.log('Starting transcription with JWT:', jwt);
      const operatingPoint = (import.meta.env.VITE_SPEECHMATICS_OPERATING_POINT as 'standard' | 'enhanced') || 'enhanced';
      const maxDelay = import.meta.env.VITE_SPEECHMATICS_MAX_DELAY ? 
        parseFloat(import.meta.env.VITE_SPEECHMATICS_MAX_DELAY) : undefined;
      
      const transcriptionConfig = {
        language: 'en',
        operating_point: operatingPoint as 'standard' | 'enhanced',
        enable_partials: true,
        diarization: 'speaker' as const,
        ...(maxDelay !== undefined && { max_delay: maxDelay }),
      };
      if (typeof maxDelay === 'number' && Number.isFinite(maxDelay) && maxDelay > 0) {
        smMaxDelaySecRef.current = maxDelay;
      }

      const config: RealtimeTranscriptionConfig = {
        audio_format: {
          type: 'raw' as const,
          encoding: 'pcm_f32le' as const,
          sample_rate: 48000,  // 48kHz sample rate
        },
        transcription_config: transcriptionConfig,
      };

      // Add translation config if enabled - at root level, not inside transcription_config
      if (translationMode === 'speechmatics') {
        Object.assign(config, {
          translation_config: {
            target_languages: ['cmn'],  // 'cmn' for Mandarin Chinese instead of 'zh'
            enable_partials: true
          }
        });
        console.log('Translation enabled: Engine A (Speechmatics)');
      }
      console.log('Transcription config:', JSON.stringify(config, null, 2));
      // console.log(`Using operating_point: ${operatingPoint}${maxDelay !== undefined ? `, max_delay: ${maxDelay}s` : ' (default max_delay)'}`);
      
      // Store config for potential reconnection
      transcriptionConfigRef.current = config;
      
      // First start the transcription session
      await startTranscription(jwt, config);
      // console.log('Transcription started successfully');
      
      // Then start recording audio
      // console.log('Starting audio recording...');
      await startRecording({});  // Using default audio settings
      // console.log('Audio recording started');
      
      // Start the timer
      if (timerIntervalRef.current) clearInterval(timerIntervalRef.current);
      setElapsedTime(0); // Reset time
      timerIntervalRef.current = window.setInterval(() => {
        setElapsedTime(prevTime => (isPausedRef.current ? prevTime : prevTime + 1));
      }, 1000);
      
      // Initialize MediaRecorder for saving audio
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        audioStreamRef.current = stream;
        
        const mediaRecorder = new MediaRecorder(stream, {
          mimeType: 'audio/webm;codecs=opus'
        });
        
        mediaRecorder.ondataavailable = (event) => {
          if (event.data.size > 0) {
            audioChunksRef.current.push(event.data);
            throttledSave();
          }
        };
        
        mediaRecorder.onstart = () => {
          console.log('MediaRecorder started');
          setIsRecording(true);
        };
        
        mediaRecorder.onstop = () => {
          console.log('MediaRecorder stopped');
          setIsRecording(false);
        };
        
        mediaRecorderRef.current = mediaRecorder;
        mediaRecorder.start(1000); // Collect data every second
      } catch (err) {
        console.error('Failed to initialize MediaRecorder:', err);
      }
      
      setIsTranscribing(true);
      setIsInitializing(false);
    } catch (err) {
      console.error('Failed to start transcription:', err);
      setError(err instanceof Error ? err.message : 'Failed to start transcription');
      setIsTranscribing(false);
      setIsInitializing(false);
    }
  }, [translationMode, startTranscription, startRecording, throttledSave]);

  const handleStop = useCallback(async () => {
    // Stop the timer
    if (timerIntervalRef.current) {
      clearInterval(timerIntervalRef.current);
      timerIntervalRef.current = null;
    }
    setElapsedTime(0); // Reset time
    
    try {
      await stopTranscription();
      await stopRecording();
      
      // Stop MediaRecorder
      if (mediaRecorderRef.current && mediaRecorderRef.current.state === 'recording') {
        mediaRecorderRef.current.stop();
      }
      
      // Stop all audio tracks
      if (audioStreamRef.current) {
        audioStreamRef.current.getTracks().forEach(track => track.stop());
        audioStreamRef.current = null;
      }
      
    setIsTranscribing(false);
    setIsPaused(false);
    } catch (err) {
      console.error('Failed to stop transcription:', err);
      setError(err instanceof Error ? err.message : 'Failed to stop transcription');
    }
  }, [stopTranscription, stopRecording]);

  // Continue current session: restart transcription without creating a new session id
  const handleContinue = useCallback(async () => {
    if (isTranscribing || isInitializing) return
    try {
      setError(null)
      setIsInitializing(true)
      sessionStartEpochRef.current = Date.now()

      // Get JWT and rebuild config (reuse same approach as handleStart)
      const jwt = await getJwt()
      const operatingPoint = (import.meta.env.VITE_SPEECHMATICS_OPERATING_POINT as 'standard' | 'enhanced') || 'enhanced'
      const maxDelay = import.meta.env.VITE_SPEECHMATICS_MAX_DELAY ? parseFloat(import.meta.env.VITE_SPEECHMATICS_MAX_DELAY) : undefined
      const transcriptionConfig = {
        language: 'en',
        operating_point: operatingPoint as 'standard' | 'enhanced',
        enable_partials: true,
        diarization: 'speaker' as const,
        ...(maxDelay !== undefined && { max_delay: maxDelay }),
      }
      if (typeof maxDelay === 'number' && Number.isFinite(maxDelay) && maxDelay > 0) {
        smMaxDelaySecRef.current = maxDelay
      }
      const config: RealtimeTranscriptionConfig = {
        audio_format: { type: 'raw' as const, encoding: 'pcm_f32le' as const, sample_rate: 48000 },
        transcription_config: transcriptionConfig,
      }
      if (translationMode === 'speechmatics') {
        Object.assign(config, { translation_config: { target_languages: ['cmn'], enable_partials: true } })
      }
      transcriptionConfigRef.current = config
      await startTranscription(jwt, config)

      // Resume local recording
      try {
        const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
        audioStreamRef.current = stream
        const mediaRecorder = new MediaRecorder(stream, { mimeType: 'audio/webm;codecs=opus' })
        mediaRecorder.ondataavailable = (event) => { if (event.data.size > 0) { audioChunksRef.current.push(event.data); throttledSave() } }
        mediaRecorder.onstart = () => { setIsRecording(true) }
        mediaRecorder.onstop = () => { setIsRecording(false) }
        mediaRecorderRef.current = mediaRecorder
        mediaRecorder.start(1000)
      } catch (err) { console.error('Failed to initialize MediaRecorder:', err) }

      // Restart local timer
      if (timerIntervalRef.current) clearInterval(timerIntervalRef.current)
      setElapsedTime(0)
      timerIntervalRef.current = window.setInterval(() => { setElapsedTime(prev => (isPausedRef.current ? prev : prev + 1)) }, 1000)

      setIsPaused(false)
      setIsTranscribing(true)
      setIsInitializing(false)

      // Re-send translator init with current settings (so WS catches latest session_id/options)
      window.dispatchEvent(new CustomEvent('dt-settings-updated'))
    } catch (err) {
      console.error('Failed to continue transcription:', err)
      setError(err instanceof Error ? err.message : 'Failed to continue transcription')
      setIsInitializing(false)
    }
  }, [isInitializing, isTranscribing, startTranscription, translationMode, throttledSave])

  const handlePauseToggle = useCallback(async () => {
    if (!isTranscribing) return
    try {
      if (!isPaused) {
        // Pause: stop sending audio and pause local recorder
        setIsPaused(true)
        try { mediaRecorderRef.current?.pause?.() } catch { /* noop */ }
      } else {
        // Resume: resume recorder and continue sending
        setIsPaused(false)
        try { mediaRecorderRef.current?.resume?.() } catch { /* noop */ }
      }
    } catch (e) {
      console.error('Failed to toggle pause:', e)
    }
  }, [isPaused, isTranscribing])

  // Listen for Pro UI commands (start/stop/continue/pause) so the Vue shell can drive the same pipeline
  useEffect(() => {
    const off = onProCommand(async (cmd) => {
      switch (cmd.type) {
        case 'start':
          if (!isTranscribing && !isInitializing) { await handleStart() }
          break
        case 'stop':
          if (isTranscribing || isInitializing) { await handleStop() }
          break
        case 'continue':
          if (!isTranscribing && !isInitializing) { await handleContinue() }
          break
        case 'pause-toggle':
          await handlePauseToggle()
          break
        case 'download-audio':
          handleDownloadAudio()
          break
        case 'download-transcript':
          handleDownloadText()
          break
        case 'download-translation':
          handleDownloadTranslation()
          break
        case 'open-settings':
          window.dispatchEvent(new CustomEvent('dt-open-settings'))
          break
        case 'open-history':
          window.dispatchEvent(new CustomEvent('dt-open-history'))
          break
        default:
          break
      }
    })
    return () => { off() }
  }, [handleContinue, handleDownloadAudio, handleDownloadText, handleDownloadTranslation, handlePauseToggle, handleStart, handleStop, isInitializing, isTranscribing])

  const handleDownloadAudio = useCallback(() => {
    if (audioChunksRef.current.length === 0) {
      alert('No audio recorded yet');
      return;
    }
    
    const audioBlob = new Blob(audioChunksRef.current, { type: 'audio/webm' });
    const url = URL.createObjectURL(audioBlob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `recording-${new Date().toISOString().replace(/:/g, '-')}.webm`;
    document.body.appendChild(a);
    a.click();
    window.URL.revokeObjectURL(url);
    document.body.removeChild(a);
  }, []);

  const handleDownloadText = useCallback(() => {
    if (lines.length === 0) {
      alert('No transcript available yet');
      return;
    }
    
    const fullText = lines.map(line => {
      const text = line.confirmedSegments.map(seg => seg.text).join('');
      return `${line.speaker}: ${text}`;
    }).join('\n\n');
    
    const textBlob = new Blob([fullText], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(textBlob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `transcript-${new Date().toISOString().replace(/:/g, '-')}.txt`;
    document.body.appendChild(a);
    a.click();
    window.URL.revokeObjectURL(url);
    document.body.removeChild(a);
  }, [lines]);

  const handleDownloadTranslation = useCallback(() => {
    if (translations.length === 0) {
      alert('No translations available yet');
      return;
    }
    
    // Filter out partial translations and format the final translations
    const fullText = translations
      .filter(t => !t.isPartial)
      .map(t => `${t.speaker}: ${t.content}`)
      .join('\n\n');
    
    const textBlob = new Blob([fullText], { type: 'text/plain;charset=utf-8' });
    const url = URL.createObjectURL(textBlob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `translation-${new Date().toISOString().replace(/:/g, '-')}.txt`;
    document.body.appendChild(a);
    a.click();
    window.URL.revokeObjectURL(url);
    document.body.removeChild(a);
  }, [translations]);
  
  // Format elapsed time in MM:SS format
  const formatTime = (seconds: number): string => {
    const minutes = Math.floor(seconds / 60).toString().padStart(2, '0');
    const secs = (seconds % 60).toString().padStart(2, '0');
    return `${minutes}:${secs}`;
  };

  // Limit on-screen DOM for very long sessions to reduce lag (full data kept in refs for export/history)
  const MAX_RENDERED_ITEMS = 500;
  const transcriptHiddenCount = lines.length > MAX_RENDERED_ITEMS ? lines.length - MAX_RENDERED_ITEMS : 0;
  const translationHiddenCount = translations.length > MAX_RENDERED_ITEMS ? translations.length - MAX_RENDERED_ITEMS : 0;
  const linesToRender = transcriptHiddenCount ? lines.slice(-MAX_RENDERED_ITEMS) : lines;
  const translationsToRender = translationHiddenCount ? translations.slice(-MAX_RENDERED_ITEMS) : translations;
  
  const handleClearSession = async () => {
    const confirmed = window.confirm('Are you sure you want to clear the current session? This will delete all transcription text and audio recordings.');
    if (confirmed) {
      await clearSession(SESSION_ID);
      setLines([]);
      setTranslations([]);
      linesRef.current = [];
      translationsRef.current = [];
      audioChunksRef.current = [];
      setLoadedAudioBlob(null);
      alert('Session cleared');
    }
  };

  const handleBatchTranscribe = async () => {
    if (!loadedAudioBlob) {
      alert('No cached audio to transcribe.');
      return;
    }

    setIsBatchProcessing(true);
    setError(null);

    try {
      let lastTimestamp = 0;
      if (linesRef.current.length > 0) {
        const lastLine = linesRef.current[linesRef.current.length - 1];
        if (lastLine.confirmedSegments.length > 0) {
          lastTimestamp = lastLine.confirmedSegments[lastLine.confirmedSegments.length - 1].endTime;
        }
      }
      console.log(`Found last timestamp (breakpoint): ${lastTimestamp}s`);

      const formData = new FormData();
      formData.append('audio', loadedAudioBlob, 'session_audio.webm');

      const response = await fetch('/api/transcribe/batch', {
        method: 'POST',
        body: formData,
      });

      if (!response.ok) {
        const errorText = await response.text();
        throw new Error(`Batch transcription failed: ${errorText}`);
      }

      const result: BatchTranscriptionResult = await response.json();

      if (result.error) {
        throw new Error(`Batch transcription error: ${result.error}`);
      }

      if (result.status === 'done' && result.transcript?.results) {
        const newSegments = result.transcript.results
          .filter(item => item.start_time > lastTimestamp)
          .map((item) => ({
            text: item.alternatives[0]?.content || '',
            startTime: item.start_time,
            endTime: item.end_time,
            speaker: item.alternatives[0]?.speaker || 'Speaker'
          }));

        if (newSegments.length === 0) {
          alert('No new content found in the cached audio to transcribe.');
          setLoadedAudioBlob(null); // Clear blob as it has been fully processed
          setIsBatchProcessing(false);
          return;
        }
        
        console.log(`Found ${newSegments.length} new segments to append.`);

        setLines(prevLines => {
          const newLines = [...prevLines];
          
          newSegments.forEach((segment) => {
            if (!segment.text.trim()) return;

            let lastSpeakerLineIndex = -1;
            for (let i = newLines.length - 1; i >= 0; i--) {
              if (newLines[i].speaker === segment.speaker) {
                lastSpeakerLineIndex = i;
                break;
              }
            }

            const newSegmentData = {
              text: segment.text,
              startTime: segment.startTime,
              endTime: segment.endTime,
            };

            const timeGap = lastSpeakerLineIndex !== -1 && newLines[lastSpeakerLineIndex].lastSegmentEndTime > 0
              ? segment.startTime - newLines[lastSpeakerLineIndex].lastSegmentEndTime
              : 0;

            if (lastSpeakerLineIndex === -1 || timeGap > PARAGRAPH_BREAK_SILENCE_THRESHOLD) {
              newLines.push({
                id: nextIdRef.current++,
                speaker: segment.speaker,
                confirmedSegments: [newSegmentData],
                partialText: '',
                lastSegmentEndTime: segment.endTime,
              });
            } else {
              const updatedLine = { ...newLines[lastSpeakerLineIndex] };
              updatedLine.confirmedSegments.push(newSegmentData);
              updatedLine.lastSegmentEndTime = segment.endTime;
              newLines[lastSpeakerLineIndex] = updatedLine;
            }
          });
          
          linesRef.current = newLines;
          throttledSave();
          return newLines;
        });

        alert('Successfully transcribed new content from cache!');
        setLoadedAudioBlob(null);
      }
    } catch (err) {
      console.error(err);
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setIsBatchProcessing(false);
    }
  };

  return (
    <div className="App">
      <h1>Real-time Speech Transcription</h1>
      
      {/* Unified Status Bar */}
      <div className="status-bar">
        {error ? (
          <>
            <div className="status-indicator" style={{ backgroundColor: isReconnecting ? 'var(--sakura)' : 'var(--ume)' }} />
            <span className="status-text">{isReconnecting ? 'Reconnecting...' : 'Error occurred'}</span>
          </>
        ) : isInitializing ? (
          <>
            <div className="status-indicator" style={{ backgroundColor: 'var(--sakura)' }} />
            <span className="status-text">Initializing microphone...</span>
          </>
        ) : isTranscribing ? (
          <>
            <div className="status-indicator" style={{ backgroundColor: 'var(--ume)' }} />
            <span className="status-text">Recording: {formatTime(elapsedTime)}</span>
          </>
        ) : (
          <>
            <div className="status-indicator" style={{ backgroundColor: 'var(--hai)' }} />
            <span className="status-text">Ready to start</span>
          </>
        )}
      </div>
      
      {/* Experimental toggles moved into Settings -> Experimental */}
      
      <div className="controls">
        {!isTranscribing ? (
          <>
            <button onClick={handleStart} disabled={isInitializing} className="btn btn-primary" title="新会话">
              {isInitializing ? 'Initializing...' : 'New Session'}
            </button>
            <button onClick={handleContinue} disabled={isInitializing} className="btn btn-secondary" title="继续当前会话">
              Continue
            </button>
          </>
        ) : (
          <button onClick={handleStart} disabled className="btn btn-primary">Transcribing</button>
        )}
        
        <button
          onClick={handlePauseToggle}
          disabled={!isTranscribing}
          className="btn btn-secondary"
          aria-pressed={isPaused}
        >
          {isPaused ? 'Resume' : 'Pause'}
        </button>
        
        <button 
          onClick={handleStop} 
          disabled={!isTranscribing}
          className="btn btn-danger"
        >
          Stop Transcription
        </button>
        
        {loadedAudioBlob && !isTranscribing && (
          <button 
            onClick={handleBatchTranscribe} 
            disabled={isBatchProcessing}
            className="control-button"
          >
            {isBatchProcessing ? 'Processing...' : 'Transcribe Cached Audio'}
          </button>
        )}
        
      </div>

      {/* Download buttons */}
      <div className="controls">
        <button 
          onClick={handleDownloadAudio} 
          disabled={audioChunksRef.current.length === 0}
          className="btn btn-secondary"
        >
          Download Audio
        </button>
        
        <button 
          onClick={handleDownloadText} 
          disabled={lines.length === 0}
          className="btn btn-secondary"
        >
          Download Text
        </button>
        
        <button 
          onClick={handleDownloadTranslation} 
          disabled={translations.length === 0}
          className="btn btn-secondary"
        >
          Download Translation
        </button>
        
        <button 
          onClick={handleClearSession} 
          disabled={lines.length === 0 && audioChunksRef.current.length === 0}
          className="btn btn-danger"
        >
          Clear Session
        </button>
      </div>

      {error && (
        <div className={`alert ${isReconnecting ? 'alert-warning' : 'alert-error'}` }>
          <span>{isReconnecting ? '!' : '!'}</span>
          <span>{error}</span>
        </div>
      )}

      <div className="transcript-container">
        <h2>{(translationMode === 'speechmatics' || translationMode === 'ai_rolling' || translationMode === 'ai_compressed') ? 'Transcription & Translation' : 'Transcription'}</h2>
        <div className="two-column-container">
          {/* Left Column - Original Text */}
          <div className="column-container">
            <h3>Original Text</h3>
            <div className="scrollable-column" ref={originalColumnRef}>
              {lines.length === 0 ? (
                <div style={{ color: 'var(--hai)', padding: '2rem', textAlign: 'center' }}>
                  <p style={{ fontSize: '1.125rem', marginBottom: '0.5rem' }}>
                    {isInitializing ? 'Initializing microphone and connection...' : 
                     isTranscribing ? 'Listening... Speak into your microphone.' : 
                     'Click Start to begin transcription'}
                  </p>
                  <p style={{ fontSize: '0.875rem', opacity: 0.7 }}>
                    {isTranscribing ? 'Your words will appear here in real-time' : 
                     'High-quality speech recognition powered by Speechmatics'}
                  </p>
                </div>
              ) : (
                <div className="content-list">
                  {transcriptHiddenCount > 0 && (
                    <div style={{ color: 'var(--hai)', fontSize: '0.85rem', padding: '0.25rem 0' }}>
                      Showing latest {MAX_RENDERED_ITEMS} items (older {transcriptHiddenCount} hidden for performance)
                    </div>
                  )}
                  {linesToRender.map((line) => {
                    const confirmedText = line.confirmedSegments.map(seg => seg.text).join('');
                    const segments = line.confirmedSegments.map(seg => ({ text: seg.text, startTime: seg.startTime, endTime: seg.endTime }))
                    return (
                      <TranscriptItem
                        key={line.id}
                        speaker={line.speaker}
                        confirmedText={confirmedText}
                        partialText={line.partialText}
                        typewriterEnabled={typewriterEnabled}
                        segments={segments}
                        translatedUntil={translatedUntilBySpeaker[line.speaker] || 0}
                        onWordClick={(w)=>handleWordClick(w)}
                      />
                    );
                  })}
                </div>
              )}
            </div>
          </div>

          {/* Right Column - Translations (only show if enabled) */}
          {(translationMode === 'speechmatics' || translationMode === 'ai_rolling' || translationMode === 'ai_compressed') && (
            <div className="column-container">
              <h3>{bilingualEnabled ? 'Bilingual (EN ↔ ZH)' : 'Chinese Translation (ZH)'}</h3>
              <div className="scrollable-column" ref={translationColumnRef}>
                {translations.length === 0 ? (
                  <div style={{ color: 'var(--text-tertiary)', padding: '2rem', textAlign: 'center' }}>
                    <p style={{ fontSize: '1.125rem', marginBottom: '0.5rem' }}>
                      {translationMode === 'speechmatics' ? 'Waiting for Speechmatics translations...' : 'Waiting for AI translations...'}
                    </p>
                    <p style={{ fontSize: '0.875rem', opacity: 0.7 }}>
                      {bilingualEnabled ? 'Final lines will appear as EN-ZH pairs' : 'Real-time AI translation to Chinese'}
                    </p>
                  </div>
                ) : bilingualEnabled ? (
                  <BilingualPanel lines={linesRef.current}
                                  translations={translations} />
                ) : (
                  <div className="content-list">
                    {translationHiddenCount > 0 && (
                      <div style={{ color: 'var(--hai)', fontSize: '0.85rem', padding: '0.25rem 0' }}>
                        Showing latest {MAX_RENDERED_ITEMS} items (older {translationHiddenCount} hidden for performance)
                      </div>
                    )}
                    {translationsToRender.map((translation) => (
                      <TranslationItem
                        key={translation.id}
                        speaker={translation.speaker}
                        startTime={translation.startTime}
                        content={translation.content}
                        isPartial={translation.isPartial}
                        typewriterEnabled={typewriterEnabled}
                      />
                    ))}
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
        {/* Chat moved to floating dock */}
      </div>

      {/* Floating dock with Chat, Summary and Performance bound to current SESSION_ID */}
      <FloatingDock
        chat={<ChatPanel sessionId={SESSION_ID} compact />}
        summary={<KnowledgePanel sessionId={SESSION_ID} compact />}
        metrics={<PerformancePanel sessionId={SESSION_ID} compact />}
      />
      {selOpen && (
        <div
          style={{
            position:'fixed', left: Math.min(Math.max(8, selX), window.innerWidth - 200), top: Math.min(Math.max(8, selY + 8), window.innerHeight - 80),
            background:'#fff', border:'1px solid var(--gin)', borderRadius:8, boxShadow:'0 8px 24px rgba(0,0,0,0.12)', padding:8, zIndex: 9999
          }}
        >
          <div style={{ display:'flex', alignItems:'center', gap:8 }}>
            <div style={{ maxWidth: 320, whiteSpace:'nowrap', overflow:'hidden', textOverflow:'ellipsis', color:'var(--hai)' }}>{selText}</div>
            <button className="btn btn-primary" onClick={() => { sendLookup(selText); setSelOpen(false) }}>释义</button>
            <button className="btn btn-secondary" onClick={() => setSelOpen(false)}>取消</button>
          </div>
        </div>
      )}
    </div>
  );
}

function App() {
  // Create AudioContext instance using useState to ensure it persists
  const [audioContext] = useState(() => {
    const AudioContextClass = window.AudioContext || (window as Window & { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!AudioContextClass) {
      throw new Error('AudioContext not supported');
    }
    return new AudioContextClass();
  });

  return (
    <RealtimeTranscriptionProvider appId="dreamtrans-app">
      <PCMAudioRecorderProvider 
        workletScriptURL="/pcm-audio-worklet.min.js"
        audioContext={audioContext}
      >
        <div className="app-shell">
          <header className="app-header">
            <div className="brand">
              <span className="brand-logo">DT</span>
              <div className="brand-text">
                <div className="brand-title">DreamTrans</div>
                <div className="brand-sub">Real‑Time Transcription & AI</div>
              </div>
              <div className="brand-actions">
                <a
                  className="brand-link"
                  href="https://github.com/soaringjerry/DreamTrans"
                  target="_blank"
                  rel="noreferrer noopener"
                  aria-label="Open GitHub repository"
                  title="GitHub"
                >
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
                    <path d="M12 .5a12 12 0 0 0-3.79 23.39c.6.11.82-.26.82-.58v-2.02c-3.34.73-4.04-1.61-4.04-1.61-.55-1.39-1.34-1.76-1.34-1.76-1.09-.74.08-.73.08-.73 1.2.09 1.83 1.24 1.83 1.24 1.07 1.83 2.8 1.3 3.49.99.11-.77.41-1.3.75-1.6-2.67-.3-5.47-1.34-5.47-5.97 0-1.32.47-2.39 1.24-3.24-.12-.3-.54-1.52.12-3.17 0 0 1.01-.32 3.3 1.23a11.5 11.5 0 0 1 6 0c2.28-1.55 3.29-1.23 3.29-1.23.67 1.65.25 2.87.12 3.17.77.85 1.23 1.92 1.23 3.24 0 4.64-2.81 5.66-5.49 5.96.42.36.8 1.09.8 2.2v3.26c0 .32.22.7.82.58A12 12 0 0 0 12 .5Z"/>
                  </svg>
                  <span className="brand-link-text">GitHub</span>
                </a>
                {/* Build info badge */}
                {(() => {
                  const commit = (import.meta.env.VITE_APP_COMMIT as string) || 'dev'
                  const short = commit.slice(0, 7)
                  const built = (import.meta.env.VITE_APP_BUILD_TIME as string) || ''
                  let builtShort = built
                  try {
                    if (built) {
                      const d = new Date(built)
                      if (!isNaN(d.getTime())) {
                        const y = d.getFullYear()
                        const m = String(d.getMonth() + 1).padStart(2, '0')
                        const dd = String(d.getDate()).padStart(2, '0')
                        const hh = String(d.getHours()).padStart(2, '0')
                        const mm = String(d.getMinutes()).padStart(2, '0')
                        builtShort = `${y}-${m}-${dd} ${hh}:${mm}`
                      }
                    }
                  } catch { /* ignore */ }
                  return (
                    <span className="build-badge" title={built ? `Built: ${built}` : 'Development build'}>
                      Commit {short}{builtShort ? ` · ${builtShort}` : ''}
                    </span>
                  )
                })()}
              </div>
            </div>
            <TopBar />
          </header>
          <TranscriptionApp />
          <GlobalOverlays />
        </div>
      </PCMAudioRecorderProvider>
    </RealtimeTranscriptionProvider>
  );
}

export default App;
