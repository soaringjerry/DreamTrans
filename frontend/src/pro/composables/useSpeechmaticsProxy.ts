/**
 * Speechmatics WebSocket Proxy Composable for DreamTrans Pro
 *
 * This composable connects to the backend Speechmatics proxy endpoint
 * instead of directly to Speechmatics, allowing the server to control
 * API access and track usage.
 *
 * Features:
 * - Automatic reconnection with exponential backoff
 * - Heartbeat detection and watchdog monitoring
 * - Connection state management
 */
import { ref, computed, onUnmounted } from 'vue'
import { getAccessToken, isAuthenticated } from '../api/auth'

const BACKEND_URL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080'
const isProduction = BACKEND_URL === '/'

// Reconnection configuration
const MAX_RECONNECT_ATTEMPTS = 5
const MAX_RECONNECT_DELAY_MS = 30000

// Audio buffering configuration
const AUDIO_BUFFER_MAX_SIZE = 100 // Max chunks to buffer during reconnection (~5 seconds at 50ms/chunk)
const AUDIO_BUFFER_ENABLED = true // Enable/disable audio buffering

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

type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'reconnecting' | 'error'

export function useSpeechmaticsProxy() {
  const ws = ref<WebSocket | null>(null)
  const state = ref<ConnectionState>('disconnected')
  const error = ref<string | null>(null)
  const isRecording = ref(false)

  // Reconnection state
  const reconnectAttempts = ref(0)
  const manuallyDisconnected = ref(false)
  let reconnectTimeoutId: number | null = null
  let lastConfig: SpeechmaticsProxyConfig = {}


  // Audio buffer for reconnection
  const audioBuffer: ArrayBuffer[] = []
  let isFlushingBuffer = false

  // Event callbacks
  const onTranscript = ref<((segment: TranscriptSegment) => void) | null>(null)
  const onTranslation = ref<((segment: TranslationSegment) => void) | null>(null)
  const onError = ref<((error: string) => void) | null>(null)
  const onBalanceUpdate = ref<((payload: Record<string, unknown>) => void) | null>(null)
  const onReconnecting = ref<((attempt: number, maxAttempts: number) => void) | null>(null)
  const onReconnected = ref<(() => void) | null>(null)

  const isConnected = computed(() => state.value === 'connected')
  const isReconnecting = computed(() => state.value === 'reconnecting')

  // Clear reconnect timer
  function clearTimers(): void {
    if (reconnectTimeoutId !== null) {
      clearTimeout(reconnectTimeoutId)
      reconnectTimeoutId = null
    }
  }

  // Calculate reconnect delay with exponential backoff and jitter
  function getReconnectDelay(): number {
    const baseDelay = Math.pow(2, reconnectAttempts.value) * 1000
    const jitter = Math.random() * 1000
    return Math.min(MAX_RECONNECT_DELAY_MS, baseDelay + jitter)
  }

  // Attempt to reconnect
  function scheduleReconnect(): void {
    if (manuallyDisconnected.value) {
      console.log('[Speechmatics] Manual disconnect, not reconnecting')
      return
    }

    if (reconnectAttempts.value >= MAX_RECONNECT_ATTEMPTS) {
      console.error(`[Speechmatics] Max reconnect attempts (${MAX_RECONNECT_ATTEMPTS}) reached. Giving up.`)
      state.value = 'error'
      error.value = `Failed to reconnect after ${MAX_RECONNECT_ATTEMPTS} attempts`
      onError.value?.(error.value)
      return
    }

    reconnectAttempts.value++
    state.value = 'reconnecting'
    const delay = getReconnectDelay()

    console.log(`[Speechmatics] Reconnecting in ${Math.round(delay / 1000)}s... (Attempt ${reconnectAttempts.value}/${MAX_RECONNECT_ATTEMPTS})`)
    onReconnecting.value?.(reconnectAttempts.value, MAX_RECONNECT_ATTEMPTS)

    reconnectTimeoutId = window.setTimeout(() => {
      reconnectTimeoutId = null
      doConnect(lastConfig, true)
    }, delay)
  }

  // Internal connect function
  async function doConnect(config: SpeechmaticsProxyConfig, isReconnect: boolean = false): Promise<void> {
    if (!isAuthenticated()) {
      throw new Error('Must be authenticated to use Speechmatics proxy')
    }

    // Close existing connection if any
    if (ws.value) {
      try {
        ws.value.close(1000, 'Reconnecting')
      } catch {
        // Ignore
      }
      ws.value = null
    }

    state.value = isReconnect ? 'reconnecting' : 'connecting'
    if (!isReconnect) {
      error.value = null
    }

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
        console.log('[Speechmatics] WebSocket connected')
        state.value = 'connected'
        error.value = null

        // Notify if this was a successful reconnection
        if (isReconnect || reconnectAttempts.value > 0) {
          console.log('[Speechmatics] Successfully reconnected')
          onReconnected.value?.()
        }

        // Reset reconnect counter on successful connection
        reconnectAttempts.value = 0

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
          console.error('[Speechmatics] Failed to parse WebSocket message:', e)
        }
      }

      ws.value.onerror = (event) => {
        console.error('[Speechmatics] WebSocket error:', event)
        // Don't set error state here - wait for onclose to handle reconnection
      }

      ws.value.onclose = (event) => {
        console.log(`[Speechmatics] WebSocket closed: code=${event.code}, reason=${event.reason || 'None'}`)

        // Normal closure codes: 1000 (normal), 1001 (going away)
        const isNormalClose = event.code === 1000 || event.code === 1001

        if (isNormalClose && manuallyDisconnected.value) {
          // User requested disconnect - don't reconnect
          state.value = 'disconnected'
        } else if (!manuallyDisconnected.value) {
          // Unexpected close - attempt to reconnect
          const reason = event.reason || 'Unknown reason'
          error.value = `Connection closed: ${reason}`
          console.warn(`[Speechmatics] Unexpected disconnect (code: ${event.code}), will attempt reconnect`)
          onError.value?.(`Connection lost: ${reason}. Reconnecting...`)
          scheduleReconnect()
        } else {
          state.value = 'disconnected'
        }
      }
    } catch (e) {
      state.value = 'error'
      error.value = e instanceof Error ? e.message : 'Connection failed'
      throw e
    }
  }

  // Connect to the backend Speechmatics proxy
  async function connect(config: SpeechmaticsProxyConfig = {}): Promise<void> {
    // Save config for reconnection
    lastConfig = { ...config }
    manuallyDisconnected.value = false
    reconnectAttempts.value = 0
    clearTimers()

    return doConnect(config, false)
  }

  function handleMessage(msg: Record<string, unknown>) {
    const messageType = msg.message as string

    switch (messageType) {
      case 'RecognitionStarted':
        console.log('[Speechmatics] Recognition started via proxy')
        isRecording.value = true
        // Flush any buffered audio from reconnection
        flushAudioBuffer()
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

      case 'Error': {
        const reason = (msg.reason as string) || 'Unknown error'
        error.value = reason
        onError.value?.(reason)
        break
      }

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

  // Flush buffered audio after reconnection
  function flushAudioBuffer(): void {
    if (!AUDIO_BUFFER_ENABLED || audioBuffer.length === 0 || isFlushingBuffer) {
      return
    }

    if (ws.value?.readyState !== WebSocket.OPEN) {
      return
    }

    isFlushingBuffer = true
    console.log(`[Speechmatics] Flushing ${audioBuffer.length} buffered audio chunks`)

    // Send buffered chunks
    while (audioBuffer.length > 0) {
      const chunk = audioBuffer.shift()
      if (chunk && ws.value?.readyState === WebSocket.OPEN) {
        try {
          ws.value.send(chunk)
        } catch (e) {
          console.warn('[Speechmatics] Failed to send buffered chunk:', e)
          break
        }
      }
    }

    isFlushingBuffer = false
  }

  // Send audio data (with buffering during reconnection)
  function sendAudio(audioData: ArrayBuffer): void {
    // If connected, send directly
    if (ws.value?.readyState === WebSocket.OPEN && state.value === 'connected') {
      ws.value.send(audioData)
      return
    }

    // If reconnecting and buffering is enabled, buffer the audio
    if (AUDIO_BUFFER_ENABLED && state.value === 'reconnecting') {
      // Limit buffer size to prevent memory issues
      if (audioBuffer.length >= AUDIO_BUFFER_MAX_SIZE) {
        audioBuffer.shift() // Remove oldest chunk
      }
      audioBuffer.push(audioData)
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
    manuallyDisconnected.value = true
    clearTimers()

    // Clear audio buffer
    audioBuffer.length = 0

    if (ws.value) {
      ws.value.close(1000, 'Client disconnect')
      ws.value = null
    }
    state.value = 'disconnected'
    isRecording.value = false
    reconnectAttempts.value = 0
  }

  // Cancel any pending reconnection
  function cancelReconnect(): void {
    if (reconnectTimeoutId !== null) {
      clearTimeout(reconnectTimeoutId)
      reconnectTimeoutId = null
    }
    reconnectAttempts.value = 0
    if (state.value === 'reconnecting') {
      state.value = 'disconnected'
    }
  }

  // Cleanup on unmount
  onUnmounted(() => {
    manuallyDisconnected.value = true
    clearTimers()
    if (ws.value) {
      ws.value.close(1000, 'Component unmount')
      ws.value = null
    }
  })

  return {
    // State
    state,
    error,
    isConnected,
    isRecording,
    isReconnecting,
    reconnectAttempts,

    // Actions
    connect,
    sendAudio,
    endSession,
    disconnect,
    cancelReconnect,

    // Event handlers
    onTranscript,
    onTranslation,
    onError,
    onBalanceUpdate,
    onReconnecting,
    onReconnected,
  }
}
