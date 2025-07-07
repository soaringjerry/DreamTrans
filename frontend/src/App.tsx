import { useState, useRef, useEffect, useMemo } from 'react';
import {
  RealtimeTranscriptionProvider,
  useRealtimeTranscription,
  useRealtimeEventListener,
} from '@speechmatics/real-time-client-react';
import {
  PCMAudioRecorderProvider,
  usePCMAudioRecorderContext,
  usePCMAudioListener,
} from '@speechmatics/browser-audio-input-react';
import { getJwt } from './api';
import { useBackendWebSocket } from './hooks/useBackendWebSocket';
import { useSmartScroll } from './hooks/useSmartScroll';
import { saveSession, loadSession, clearSession } from './db';
import { throttle } from 'lodash';
import { TranscriptItem } from './components/TranscriptItem';
import { TranslationItem } from './components/TranslationItem';
import './App.css';

interface ConfirmedSegment {
  text: string;
  startTime: number;
  endTime: number;
}

interface TranscriptLine {
  id: number;
  speaker: string;
  confirmedSegments: ConfirmedSegment[]; // 累积最终转录的片段（包含时间戳）
  partialText: string;                   // 当前完整的临时转录文本
  lastSegmentEndTime: number;            // 当前行中最后一个确认片段的结束时间（秒）
}

interface TranslationLine {
  id: string;                            // Unique ID composed from speaker and startTime
  speaker: string;
  startTime: number;
  content: string;
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

function TranscriptionApp() {
  const [isTranscribing, setIsTranscribing] = useState(false);
  const [isInitializing, setIsInitializing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lines, setLines] = useState<TranscriptLine[]>([]);
  const [translations, setTranslations] = useState<TranslationLine[]>([]);
  const [translationEnabled, setTranslationEnabled] = useState(false);
  const [typewriterEnabled, setTypewriterEnabled] = useState(true); // New state for typewriter mode
  const [elapsedTime, setElapsedTime] = useState(0); // Recording time in seconds
  const [isReconnecting, setIsReconnecting] = useState(false); // New state for reconnection
  const nextIdRef = useRef(1);
  const timerIntervalRef = useRef<number | null>(null);
  const PARAGRAPH_BREAK_SILENCE_THRESHOLD = 2.0; // 2 秒的静默时间，用于判断是否开启新段落
  
  // Recording states
  const [, setIsRecording] = useState(false);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const audioChunksRef = useRef<Blob[]>([]);
  const audioStreamRef = useRef<MediaStream | null>(null);
  
  // Session management
  const SESSION_ID = 'current_session';
  const linesRef = useRef<TranscriptLine[]>([]);
  const translationsRef = useRef<TranslationLine[]>([]);
  const effectRan = useRef(false);
  const transcriptionConfigRef = useRef<any>(null); // Store transcription config for reconnection
  
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
    []
  );
  
  const { startTranscription, stopTranscription, sendAudio, sessionId, socketState } = useRealtimeTranscription();
  const { startRecording, stopRecording } = usePCMAudioRecorderContext();
  const { connect, sendMessage, disconnect } = useBackendWebSocket();
  
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
          
          // Find the last line for this speaker
          let lastSpeakerLineIndex = -1;
          for (let i = newLines.length - 1; i >= 0; i--) {
            if (newLines[i].speaker === speaker) {
              lastSpeakerLineIndex = i;
              break;
            }
          }
          
          // Determine if we should start a new paragraph
          let shouldStartNewParagraph = false;
          if (lastSpeakerLineIndex !== -1) {
            const lastLine = newLines[lastSpeakerLineIndex];
            // Only check time gap if the last line already has confirmed segments
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
          
          if (shouldStartNewParagraph || lastSpeakerLineIndex === -1) {
            // Create a new paragraph
            newLines.push({
              id: nextIdRef.current++,
              speaker,
              confirmedSegments: [newSegment],
              partialText: '',
              lastSegmentEndTime: endTime
            });
          } else {
            // Continue existing paragraph
            const updatedLine = { ...newLines[lastSpeakerLineIndex] };
            
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
            newLines[lastSpeakerLineIndex] = updatedLine;
          }
          
          // Update ref and trigger save
          linesRef.current = newLines;
          throttledSave();
          
          return newLines;
        });
        
        // Send to backend
        sendMessage(message.metadata);
      }
    } else if (message.message === 'AddPartialTranscript') {
      // Handle partial transcript
      if (message.metadata?.transcript && message.metadata.transcript.trim()) {
        const speaker = message.results?.[0]?.alternatives?.[0]?.speaker || 'Speaker';
        const partialText = message.metadata.transcript;
        const startTime = message.metadata.start_time || 0;
        
        setLines((prevLines) => {
          const newLines = [...prevLines];
          
          // Find the last line for this speaker
          let lastSpeakerLineIndex = -1;
          for (let i = newLines.length - 1; i >= 0; i--) {
            if (newLines[i].speaker === speaker) {
              lastSpeakerLineIndex = i;
              break;
            }
          }
          
          // Check if we should start a new paragraph based on time gap
          let shouldStartNewParagraph = false;
          if (lastSpeakerLineIndex !== -1) {
            const lastLine = newLines[lastSpeakerLineIndex];
            // Only check time gap if the last line has confirmed segments
            if (lastLine.confirmedSegments.length > 0) {
              const timeGap = startTime - lastLine.lastSegmentEndTime;
              if (timeGap > PARAGRAPH_BREAK_SILENCE_THRESHOLD) {
                shouldStartNewParagraph = true;
                console.log(`[${speaker}] Starting new paragraph in PARTIAL due to ${timeGap.toFixed(2)}s gap`);
              }
            }
          }
          
          if (shouldStartNewParagraph || lastSpeakerLineIndex === -1) {
            // Create a new paragraph
            newLines.push({
              id: nextIdRef.current++,
              speaker,
              confirmedSegments: [],
              partialText: partialText,
              lastSegmentEndTime: startTime // Use Partial's startTime to avoid false gap detection
            });
          } else {
            // Update the existing line's partial text
            const updatedLine = { ...newLines[lastSpeakerLineIndex] };
            updatedLine.partialText = partialText;
            newLines[lastSpeakerLineIndex] = updatedLine;
          }
          
          // Update ref but don't trigger save for partial updates (too frequent)
          linesRef.current = newLines;
          
          return newLines;
        });
      }
    } else if (message.message === 'AddTranslation') {
      // Handle final translation
      if (message.results && message.results.length > 0) {
        const translationResult = message.results[0];
        const speaker = translationResult.speaker || 'Speaker';
        const content = translationResult.content || '';
        const startTime = translationResult.start_time || 0;
        
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
    } else if (message.message === 'AddPartialTranslation') {
      // Handle partial translation
      if (message.results && message.results.length > 0) {
        const partialResult = message.results[0];
        const content = partialResult.content || '';
        const speaker = partialResult.speaker || 'Speaker';
        const startTime = partialResult.start_time || 0;
        
        setTranslations((prevTranslations) => {
          const newTranslations = [...prevTranslations];
          
          // Create unique ID for this partial translation
          const id = `${speaker}-${startTime}`;
          
          // Find if we already have a partial translation that we're updating
          const existingPartialIndex = newTranslations.findIndex(t => t.isPartial);
          
          if (existingPartialIndex !== -1) {
            // Update the existing partial translation
            newTranslations[existingPartialIndex] = {
              id,
              speaker,
              startTime,
              content,
              isPartial: true
            };
          } else {
            // Add new partial translation
            newTranslations.push({
              id,
              speaker,
              startTime,
              content,
              isPartial: true
            });
          }
          
          // Update ref and trigger save
          translationsRef.current = newTranslations;
          throttledSave();
          
          return newTranslations;
        });
      }
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
  
  // Send audio to Speechmatics when captured
  usePCMAudioListener((audioData) => {
    if (sessionId && socketState === 'open') {
      // For pcm_f32le, each sample is 4 bytes
      if (audioData.byteLength % 4 !== 0) {
        console.error('Audio data length is not a multiple of 4 bytes!', audioData.byteLength);
        return;
      }
      // console.log('Sending audio to Speechmatics:', audioData.byteLength, 'bytes, sessionId:', sessionId);
      sendAudio(audioData);
    }
  });

  // Apply smart auto-scroll to original text column
  useSmartScroll(originalColumnRef, lines);
  
  // Apply smart auto-scroll to translation column
  useSmartScroll(translationColumnRef, translations);
  
  // Connect to backend WebSocket on mount
  useEffect(() => {
    connect();
    return () => {
      disconnect();
    };
  }, [connect, disconnect]);
  
  // Load saved session on mount
  useEffect(() => {
    // In StrictMode, effects run twice. We only want to run this logic once.
    if (effectRan.current === true || import.meta.env.MODE !== 'development') {
      const restoreSession = async () => {
        const savedSession = await loadSession(SESSION_ID);
        if (savedSession && (savedSession.lines.length > 0 || savedSession.audioBlob)) {
          console.log('Restoring session from IndexedDB');
          
          // Show a notification to user in English
          const userConfirmed = window.confirm(
            `An unfinished transcription session was found.\n` +
            `Recording time: ${new Date(savedSession.timestamp).toLocaleString()}\n` +
            `Do you want to restore it?`
          );
          
          if (userConfirmed) {
            // Restore transcript lines
            setLines(savedSession.lines);
            linesRef.current = savedSession.lines;
            
            // Restore translations if available
            if (savedSession.translations) {
              setTranslations(savedSession.translations);
              translationsRef.current = savedSession.translations;
            }
            
            // Restore audio data if available
            if (savedSession.audioBlob) {
              audioChunksRef.current = [savedSession.audioBlob];
            }
          } else {
            // User chose not to restore, clear the session
            await clearSession(SESSION_ID);
            console.log('User declined to restore. Cleared saved session.');
          }
        }
      };
      
      restoreSession();
    }
    
    // Cleanup function to set the ref, ensuring the effect runs on the next render in dev
    return () => {
      effectRan.current = true;
    };
  }, []);

  // Monitor socket state and handle reconnection
  useEffect(() => {
    const handleReconnection = async () => {
      // Only attempt reconnection if we were actively transcribing
      if (!isTranscribing || !transcriptionConfigRef.current) {
        return;
      }

      if (socketState === 'closing' || socketState === undefined) {
        console.log('WebSocket disconnected, attempting to reconnect...');
        setIsReconnecting(true);
        setError('Connection lost. Attempting to reconnect...');

        try {
          // Get a new JWT token
          const newJwt = await getJwt();
          
          // Attempt to restart transcription with the same configuration
          // Using the existing sessionId to resume the session if possible
          await startTranscription(newJwt, transcriptionConfigRef.current);
          
          console.log('Successfully reconnected to Speechmatics');
          setError(null);
        } catch (err) {
          console.error('Failed to reconnect:', err);
          setError('Failed to reconnect. Please stop and restart transcription.');
        }
      } else if (socketState === 'open' && isReconnecting) {
        // Connection restored
        console.log('Connection restored');
        setIsReconnecting(false);
        setError(null);
      }
    };

    handleReconnection();
  }, [socketState, isTranscribing, isReconnecting, startTranscription]);



  const handleStart = async () => {
    // Password verification
    const password = prompt("Please enter password：");
    const correctPassword = "233333"; // Default password

    if (password !== correctPassword) {
      alert("密码错误！");
      return; // Abort function execution
    }
    
    // If password is correct, continue with recording
    try {
      setError(null);
      setIsInitializing(true);
      
      // Clear previous session data
      await clearSession(SESSION_ID);
      audioChunksRef.current = [];
      setLines([]);
      setTranslations([]);
      linesRef.current = [];
      translationsRef.current = [];
      
      // Get JWT from our backend
      const jwt = await getJwt();
      
      // Start transcription with required configuration
      // console.log('Starting transcription with JWT:', jwt);
      // 从环境变量读取配置
      const operatingPoint = (import.meta.env.VITE_SPEECHMATICS_OPERATING_POINT as 'standard' | 'enhanced') || 'enhanced';
      const maxDelay = import.meta.env.VITE_SPEECHMATICS_MAX_DELAY ? 
        parseFloat(import.meta.env.VITE_SPEECHMATICS_MAX_DELAY) : undefined;
      
      const transcriptionConfig: any = {
        language: 'en',
        operating_point: operatingPoint,
        enable_partials: true,
        diarization: 'speaker' as const,
        ...(maxDelay !== undefined && { max_delay: maxDelay }),
      };

      const config: any = {
        audio_format: {
          type: 'raw' as const,
          encoding: 'pcm_f32le' as const,
          sample_rate: 48000,  // 使用 48kHz 获得更好的音质
        },
        transcription_config: transcriptionConfig,
      };

      // Add translation config if enabled - at root level, not inside transcription_config
      if (translationEnabled) {
        config.translation_config = {
          target_languages: ['cmn'],  // 'cmn' for Mandarin Chinese instead of 'zh'
          enable_partials: true
        };
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
        setElapsedTime(prevTime => prevTime + 1);
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
      
      // 现在才真正开始转录
      setIsTranscribing(true);
      setIsInitializing(false);
    } catch (err) {
      console.error('Failed to start transcription:', err);
      setError(err instanceof Error ? err.message : 'Failed to start transcription');
      setIsTranscribing(false);
      setIsInitializing(false);
    }
  };

  const handleStop = async () => {
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
    } catch (err) {
      console.error('Failed to stop transcription:', err);
      setError(err instanceof Error ? err.message : 'Failed to stop transcription');
    }
  };

  const handleDownloadAudio = () => {
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
  };

  const handleDownloadText = () => {
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
  };

  const handleDownloadTranslation = () => {
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
  };
  
  // Format elapsed time in MM:SS format
  const formatTime = (seconds: number): string => {
    const minutes = Math.floor(seconds / 60).toString().padStart(2, '0');
    const secs = (seconds % 60).toString().padStart(2, '0');
    return `${minutes}:${secs}`;
  };
  
  const handleClearSession = async () => {
    const confirmed = window.confirm('Are you sure you want to clear the current session? This will delete all transcription text and audio recordings.');
    if (confirmed) {
      await clearSession(SESSION_ID);
      setLines([]);
      setTranslations([]);
      linesRef.current = [];
      translationsRef.current = [];
      audioChunksRef.current = [];
      alert('Session cleared');
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
      
      {/* Toggle Switches */}
      <div className="toggle-group">
        <div className="toggle-container">
          <label className="toggle-label">
            <input
              type="checkbox"
              checked={translationEnabled}
              onChange={(e) => setTranslationEnabled(e.target.checked)}
              disabled={isTranscribing}
            />
            <div className="toggle-switch">
              <div className="toggle-slider" />
            </div>
            <span>Enable Chinese Translation (中文翻译)</span>
          </label>
        </div>
        
        <div className="toggle-container">
          <label className="toggle-label">
            <input
              type="checkbox"
              checked={typewriterEnabled}
              onChange={(e) => setTypewriterEnabled(e.target.checked)}
            />
            <div className="toggle-switch">
              <div className="toggle-slider" />
            </div>
            <span>Typewriter Mode (打字机效果)</span>
          </label>
        </div>
      </div>
      
      <div className="controls">
        <button 
          onClick={handleStart} 
          disabled={isTranscribing || isInitializing}
          className="btn btn-primary"
        >
          {isInitializing ? 'Initializing...' : isTranscribing ? 'Transcribing' : 'Start Transcription'}
        </button>
        
        <button 
          onClick={handleStop} 
          disabled={!isTranscribing}
          className="btn btn-danger"
        >
          Stop Transcription
        </button>
        
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
        <div className={`alert ${isReconnecting ? 'alert-warning' : 'alert-error'}`}>
          <span>{isReconnecting ? '⚠️' : '❌'}</span>
          <span>{error}</span>
        </div>
      )}

      <div className="transcript-container">
        <h2>{translationEnabled ? 'Transcription & Translation' : 'Transcription'}</h2>
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
                  {lines.map((line) => {
                    const confirmedText = line.confirmedSegments.map(seg => seg.text).join('');
                    
                    return (
                      <TranscriptItem
                        key={line.id}
                        speaker={line.speaker}
                        confirmedText={confirmedText}
                        partialText={line.partialText}
                        typewriterEnabled={typewriterEnabled}
                      />
                    );
                  })}
                </div>
              )}
            </div>
          </div>

          {/* Right Column - Translations (only show if enabled) */}
          {translationEnabled && (
            <div className="column-container">
              <h3>Chinese Translation (中文翻译)</h3>
              <div className="scrollable-column" ref={translationColumnRef}>
                {translations.length === 0 ? (
                  <div style={{ color: 'var(--text-tertiary)', padding: '2rem', textAlign: 'center' }}>
                    <p style={{ fontSize: '1.125rem', marginBottom: '0.5rem' }}>
                      Waiting for translations...
                    </p>
                    <p style={{ fontSize: '0.875rem', opacity: 0.7 }}>
                      Real-time AI translation to Chinese
                    </p>
                  </div>
                ) : (
                  <div className="content-list">
                    {translations.map((translation) => (
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
      </div>
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
        <TranscriptionApp />
      </PCMAudioRecorderProvider>
    </RealtimeTranscriptionProvider>
  );
}

export default App;