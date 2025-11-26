/**
 * Speechmatics WebSocket Proxy Composable for DreamTrans Pro
 *
 * This composable connects to the backend Speechmatics proxy endpoint
 * instead of directly to Speechmatics, allowing the server to control
 * API access and track usage.
 */
import { ref, computed, onUnmounted } from 'vue'
import { getAccessToken, isAuthenticated } from '../api/auth'

const BACKEND_URL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080'
const isProduction = BACKEND_URL === '/'

// Convert HTTP URL to WebSocket URL
function getWsUrl(): string {
  if (isProduction) {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${protocol}//${window.location.host}/ws/speechmatics`
  }
  const wsUrl = BACKEND_URL.replace('http://', 'ws://').replace('https://', 'wss://')
  return `${wsUrl}/ws/speechmatics`
}

export interface SpeechmaticsProxyConfig {
  language?: string
  enable_partials?: boolean
  diarization?: 'none' | 'speaker'
  max_delay?: number
  operating_point?: 'standard' | 'enhanced'
  translation_config?: {
    target_languages: string[]
    enable_partials?: boolean
  }
}

export interface TranscriptSegment {
  text: string
  startTime: number
  endTime: number
  speaker: string
  isPartial: boolean
}

export interface TranslationSegment {
  text: string
  startTime: number
  endTime: number
  speaker: string
  isPartial: boolean
}

type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'error'

export function useSpeechmaticsProxy() {
  const ws = ref<WebSocket | null>(null)
  const state = ref<ConnectionState>('disconnected')
  const error = ref<string | null>(null)
  const isRecording = ref(false)

  // Event callbacks
  const onTranscript = ref<((segment: TranscriptSegment) => void) | null>(null)
  const onTranslation = ref<((segment: TranslationSegment) => void) | null>(null)
  const onError = ref<((error: string) => void) | null>(null)
  const onBalanceUpdate = ref<((payload: Record<string, unknown>) => void) | null>(null)

  const isConnected = computed(() => state.value === 'connected')

  // Connect to the backend Speechmatics proxy
  async function connect(config: SpeechmaticsProxyConfig = {}): Promise<void> {
    if (!isAuthenticated()) {
      throw new Error('Must be authenticated to use Speechmatics proxy')
    }

    if (ws.value && state.value === 'connected') {
      return // Already connected
    }

    state.value = 'connecting'
    error.value = null

    try {
      const wsUrl = getWsUrl()
      const token = getAccessToken()

      // Create WebSocket with auth token in header (via query param as fallback)
      const url = new URL(wsUrl)
      if (token) {
        url.searchParams.set('token', token)
      }

      ws.value = new WebSocket(url.toString())

      ws.value.onopen = () => {
        state.value = 'connected'

        // Send StartRecognition message
        const startMsg = {
          message: 'StartRecognition',
          audio_format: {
            type: 'raw',
            encoding: 'pcm_f32le',
            sample_rate: 48000,
          },
          transcription_config: {
            language: config.language || 'en',
            enable_partials: config.enable_partials ?? true,
            diarization: config.diarization || 'speaker',
            operating_point: config.operating_point || 'enhanced',
            ...(config.max_delay !== undefined && { max_delay: config.max_delay }),
          },
          ...(config.translation_config && { translation_config: config.translation_config }),
        }

        ws.value?.send(JSON.stringify(startMsg))
      }

      ws.value.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data)
          handleMessage(msg)
        } catch (e) {
          console.error('Failed to parse WebSocket message:', e)
        }
      }

      ws.value.onerror = (event) => {
        console.error('WebSocket error:', event)
        state.value = 'error'
        error.value = 'WebSocket connection error'
        onError.value?.('WebSocket connection error')
      }

      ws.value.onclose = (event) => {
        state.value = 'disconnected'
        if (event.code !== 1000 && event.code !== 1001) {
          error.value = `Connection closed: ${event.reason || 'Unknown reason'}`
          onError.value?.(error.value)
        }
      }
    } catch (e) {
      state.value = 'error'
      error.value = e instanceof Error ? e.message : 'Connection failed'
      throw e
    }
  }

  function handleMessage(msg: Record<string, unknown>) {
    const messageType = msg.message as string

    switch (messageType) {
      case 'RecognitionStarted':
        console.log('Recognition started via proxy')
        isRecording.value = true
        break

      case 'AddTranscript':
        if (msg.metadata && onTranscript.value) {
          const metadata = msg.metadata as { transcript?: string; start_time?: number; end_time?: number }
          const results = msg.results as Array<{ alternatives?: Array<{ speaker?: string }> }> | undefined
          const speaker = results?.[0]?.alternatives?.[0]?.speaker || 'Speaker'

          onTranscript.value({
            text: metadata.transcript || '',
            startTime: metadata.start_time || 0,
            endTime: metadata.end_time || 0,
            speaker,
            isPartial: false,
          })
        }
        break

      case 'AddPartialTranscript':
        if (msg.metadata && onTranscript.value) {
          const metadata = msg.metadata as { transcript?: string; start_time?: number; end_time?: number }
          const results = msg.results as Array<{ alternatives?: Array<{ speaker?: string }> }> | undefined
          const speaker = results?.[0]?.alternatives?.[0]?.speaker || 'Speaker'

          onTranscript.value({
            text: metadata.transcript || '',
            startTime: metadata.start_time || 0,
            endTime: metadata.end_time || 0,
            speaker,
            isPartial: true,
          })
        }
        break

      case 'AddTranslation':
        if (msg.results && onTranslation.value) {
          const results = msg.results as Array<{ content?: string; start_time?: number; end_time?: number; speaker?: string }>
          if (results.length > 0) {
            const r = results[0]
            onTranslation.value({
              text: r.content || '',
              startTime: r.start_time || 0,
              endTime: r.end_time || 0,
              speaker: r.speaker || 'Speaker',
              isPartial: false,
            })
          }
        }
        break

      case 'AddPartialTranslation':
        // Ignore partial translations for now
        break

      case 'Error':
        const reason = (msg.reason as string) || 'Unknown error'
        error.value = reason
        onError.value?.(reason)
        break

      case 'EndOfTranscript':
        console.log('End of transcript')
        isRecording.value = false
        break

      case 'BalanceUpdated':
        if (onBalanceUpdate.value) {
          onBalanceUpdate.value(msg as Record<string, unknown>)
        }
        break

      default:
        // Ignore other message types
        break
    }
  }

  // Send audio data
  function sendAudio(audioData: ArrayBuffer): void {
    if (ws.value && state.value === 'connected') {
      ws.value.send(audioData)
    }
  }

  // End the session
  function endSession(): void {
    if (ws.value && state.value === 'connected') {
      ws.value.send(JSON.stringify({ message: 'EndOfStream' }))
    }
  }

  // Disconnect
  function disconnect(): void {
    if (ws.value) {
      ws.value.close(1000, 'Client disconnect')
      ws.value = null
    }
    state.value = 'disconnected'
    isRecording.value = false
  }

  // Cleanup on unmount
  onUnmounted(() => {
    disconnect()
  })

  return {
    // State
    state,
    error,
    isConnected,
    isRecording,

    // Actions
    connect,
    sendAudio,
    endSession,
    disconnect,

    // Event handlers
    onTranscript,
    onTranslation,
    onError,
    onBalanceUpdate,
  }
}
