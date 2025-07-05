import { useState, useRef, useEffect } from 'react';
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
import './App.css';

interface TranscriptLine {
  id: number;
  speaker: string;
  confirmedSegments: string[]; // 累积最终转录的片段
  partialText: string;         // 当前完整的临时转录文本
  lastSegmentEndTime: number;  // 当前行中最后一个确认片段的结束时间（秒）
}

function TranscriptionApp() {
  const [isTranscribing, setIsTranscribing] = useState(false);
  const [isInitializing, setIsInitializing] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lines, setLines] = useState<TranscriptLine[]>([]);
  const nextIdRef = useRef(1);
  const PARAGRAPH_BREAK_SILENCE_THRESHOLD = 2.0; // 2 秒的静默时间，用于判断是否开启新段落
  
  // Recording states
  const [isRecording, setIsRecording] = useState(false);
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const audioChunksRef = useRef<Blob[]>([]);
  const audioStreamRef = useRef<MediaStream | null>(null);
  
  const { startTranscription, stopTranscription, sendAudio, sessionId, socketState } = useRealtimeTranscription();
  const { startRecording, stopRecording } = usePCMAudioRecorderContext();
  const { connect, sendMessage, disconnect, status: wsStatus } = useBackendWebSocket();
  
  // console.log('Speechmatics connection state:', socketState, 'sessionId:', sessionId);
  
  // Listen for all messages from Speechmatics
  useRealtimeEventListener('receiveMessage' as any, (event: any) => {
    const message = event.data || event;
    
    if (message.message === 'RecognitionStarted') {
      // console.log('Recognition started!', message);
    } else if (message.message === 'AddTranscript') {
      // Handle final transcript
      if (message.metadata.transcript && message.metadata.transcript.trim()) {
        const speaker = message.results?.[0]?.alternatives?.[0]?.speaker || 'Speaker';
        const transcript = message.metadata.transcript;
        const startTime = message.metadata.start_time;
        const endTime = message.metadata.end_time;
        
        console.log('Final:', message.metadata.transcript);
        
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
          
          if (shouldStartNewParagraph || lastSpeakerLineIndex === -1) {
            // Create a new paragraph
            newLines.push({
              id: nextIdRef.current++,
              speaker,
              confirmedSegments: [transcript],
              partialText: '',
              lastSegmentEndTime: endTime
            });
          } else {
            // Continue existing paragraph
            const updatedLine = { ...newLines[lastSpeakerLineIndex] };
            // Only push if the transcript is different from the last confirmed segment
            const lastConfirmed = updatedLine.confirmedSegments.at(-1);
            if (!lastConfirmed || lastConfirmed !== transcript) {
              updatedLine.confirmedSegments.push(transcript);
            } else {
              console.log(`[${speaker}] Duplicate transcript detected and skipped: "${transcript}"`);
            }
            updatedLine.lastSegmentEndTime = endTime;
            updatedLine.partialText = ''; // Clear partial as this part is now confirmed
            newLines[lastSpeakerLineIndex] = updatedLine;
          }
          
          return newLines;
        });
        
        // Send to backend
        sendMessage(message.metadata);
      }
    } else if (message.message === 'AddPartialTranscript') {
      // Handle partial transcript
      if (message.metadata.transcript && message.metadata.transcript.trim()) {
        const speaker = message.results?.[0]?.alternatives?.[0]?.speaker || 'Speaker';
        const partialText = message.metadata.transcript;
        const startTime = message.metadata.start_time;
        
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
          
          return newLines;
        });
      }
    } else if (message.message === 'Error') {
      console.error('Speechmatics error:', message);
      setError(message.reason || message.type || 'Unknown error');
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

  // Connect to backend WebSocket on mount
  useEffect(() => {
    connect();
    return () => {
      disconnect();
    };
  }, [connect, disconnect]);



  const handleStart = async () => {
    try {
      setError(null);
      setIsInitializing(true);
      
      // Clear previous recording data
      audioChunksRef.current = [];
      
      // Get JWT from our backend
      const jwt = await getJwt();
      
      // Start transcription with required configuration
      // console.log('Starting transcription with JWT:', jwt);
      // 从环境变量读取配置
      const operatingPoint = (import.meta.env.VITE_SPEECHMATICS_OPERATING_POINT as 'standard' | 'enhanced') || 'enhanced';
      const maxDelay = import.meta.env.VITE_SPEECHMATICS_MAX_DELAY ? 
        parseFloat(import.meta.env.VITE_SPEECHMATICS_MAX_DELAY) : undefined;
      
      const config = {
        audio_format: {
          type: 'raw' as const,
          encoding: 'pcm_f32le' as const,
          sample_rate: 48000,  // 使用 48kHz 获得更好的音质
        },
        transcription_config: {
          language: 'en',
          operating_point: operatingPoint,
          enable_partials: true,
          diarization: 'speaker' as const,
          ...(maxDelay !== undefined && { max_delay: maxDelay }),
        },
      };
      // console.log('Transcription config:', config);
      // console.log(`Using operating_point: ${operatingPoint}${maxDelay !== undefined ? `, max_delay: ${maxDelay}s` : ' (default max_delay)'}`);
      
      // First start the transcription session
      await startTranscription(jwt, config);
      // console.log('Transcription started successfully');
      
      // Then start recording audio
      // console.log('Starting audio recording...');
      await startRecording({});  // Using default audio settings
      // console.log('Audio recording started');
      
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
      const text = line.confirmedSegments.join('');
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

  return (
    <div className="App">
      <h1>Real-time Speech Transcription</h1>
      
      {/* 麦克风状态指示器 */}
      {(isInitializing || isTranscribing) && (
        <div style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          margin: '20px 0',
          fontSize: '18px',
        }}>
          <div style={{
            width: '20px',
            height: '20px',
            borderRadius: '50%',
            backgroundColor: isInitializing ? '#FFA500' : '#FF0000',
            marginRight: '10px',
            animation: isTranscribing ? 'pulse 1.5s infinite' : 'none',
          }} />
          <span style={{ color: isInitializing ? '#FFA500' : '#FF0000' }}>
            {isInitializing ? '正在初始化麦克风...' : '正在录音'}
          </span>
        </div>
      )}
      
      <div style={{
        padding: '10px',
        margin: '10px',
        backgroundColor: wsStatus === 'open' ? '#d4edda' : wsStatus === 'error' ? '#f8d7da' : '#fff3cd',
        color: wsStatus === 'open' ? '#155724' : wsStatus === 'error' ? '#721c24' : '#856404',
        borderRadius: '5px',
        fontSize: '14px',
      }}>
        Backend WebSocket: {wsStatus}
      </div>
      
      <div className="controls">
        <button 
          onClick={handleStart} 
          disabled={isTranscribing || isInitializing}
          style={{
            backgroundColor: isTranscribing || isInitializing ? '#ccc' : '#4CAF50',
            color: 'white',
            padding: '10px 20px',
            margin: '10px',
            border: 'none',
            borderRadius: '5px',
            cursor: isTranscribing || isInitializing ? 'not-allowed' : 'pointer',
            fontSize: '16px',
            minWidth: '200px',
          }}
        >
          {isInitializing ? 'Initializing...' : isTranscribing ? 'Transcribing' : 'Start Transcription'}
        </button>
        
        <button 
          onClick={handleStop} 
          disabled={!isTranscribing}
          style={{
            backgroundColor: !isTranscribing ? '#ccc' : '#f44336',
            color: 'white',
            padding: '10px 20px',
            margin: '10px',
            border: 'none',
            borderRadius: '5px',
            cursor: !isTranscribing ? 'not-allowed' : 'pointer',
            fontSize: '16px',
          }}
        >
          Stop Transcription
        </button>
      </div>

      {/* Download buttons */}
      <div className="controls" style={{ marginTop: '20px' }}>
        <button 
          onClick={handleDownloadAudio} 
          disabled={audioChunksRef.current.length === 0}
          style={{
            backgroundColor: audioChunksRef.current.length === 0 ? '#ccc' : '#2196F3',
            color: 'white',
            padding: '10px 20px',
            margin: '10px',
            border: 'none',
            borderRadius: '5px',
            cursor: audioChunksRef.current.length === 0 ? 'not-allowed' : 'pointer',
            fontSize: '16px',
          }}
        >
          下载音频
        </button>
        
        <button 
          onClick={handleDownloadText} 
          disabled={lines.length === 0}
          style={{
            backgroundColor: lines.length === 0 ? '#ccc' : '#FF9800',
            color: 'white',
            padding: '10px 20px',
            margin: '10px',
            border: 'none',
            borderRadius: '5px',
            cursor: lines.length === 0 ? 'not-allowed' : 'pointer',
            fontSize: '16px',
          }}
        >
          下载文本
        </button>
      </div>

      {error && (
        <div style={{
          backgroundColor: '#f8d7da',
          color: '#721c24',
          padding: '10px',
          margin: '20px',
          borderRadius: '5px',
        }}>
          Error: {error}
        </div>
      )}

      <div style={{
        marginTop: '20px',
        padding: '20px',
        border: '1px solid #ddd',
        borderRadius: '5px',
        minHeight: '200px',
        maxHeight: '400px',
        overflowY: 'auto',
        backgroundColor: '#f5f5f5',
      }}>
        <h2>Transcriptions:</h2>
        {lines.length === 0 ? (
          <p style={{ color: '#666' }}>
            {isInitializing ? 'Initializing microphone and connection...' : 
             isTranscribing ? 'Listening... Speak into your microphone.' : 
             'Click Start to begin transcription'}
          </p>
        ) : (
          <div>
            {lines.map((line) => {
              const confirmedText = line.confirmedSegments.join(''); // 直接拼接，避免多余空格
              // 计算 partialText 中超出 confirmedText 的部分，作为可见的临时文本
              const visiblePartial = line.partialText.startsWith(confirmedText)
                ? line.partialText.substring(confirmedText.length).trimStart()
                : line.partialText; // 如果 partial 不以 confirmed 开头（不常见），则显示整个 partial

              return (
                <div key={line.id} style={{ marginBottom: '15px' }}>
                  <span style={{
                    fontWeight: 'bold',
                    color: '#333',
                    marginRight: '10px',
                  }}>
                    {line.speaker}:
                  </span>
                  <span style={{
                    color: '#000', // 确认文本始终为黑色
                    lineHeight: '1.5'
                  }}>
                    {confirmedText}
                  </span>
                  {visiblePartial && ( // 仅当有可见的临时文本时才显示
                    <span style={{
                      color: '#666', // 临时文本为灰色
                      fontStyle: 'italic',
                      lineHeight: '1.5'
                    }}>
                      {confirmedText ? ' ' : ''}{visiblePartial} {/* 如果有确认文本，则在临时文本前加空格 */}
                      <span style={{
                        animation: 'blink 1s infinite',
                        marginLeft: '2px'
                      }}>|</span> {/* 闪烁光标 */}
                    </span>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

function App() {
  // Create AudioContext instance using useState to ensure it persists
  const [audioContext] = useState(() => {
    return new (window.AudioContext || (window as any).webkitAudioContext)();
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