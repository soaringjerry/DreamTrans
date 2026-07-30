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
import { ensureValidAccessToken, isAuthenticated } from '../api/auth'
import { websocketAuthProtocols } from '../../utils/websocketAuth'

const BACKEND_URL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080'
const isProduction = BACKEND_URL === '/'

// Reconnection configuration
const MAX_RECONNECT_ATTEMPTS = 5
const MAX_RECONNECT_DELAY_MS = 30000

// Audio buffering configuration
const AUDIO_BUFFER_MAX_BYTES = 48000 * 4 * 5 // Five seconds of 48 kHz Float32 mono PCM
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
  let endingSession = false
  let pendingEnd: {
    promise: Promise<void>
    resolve: () => void
    reject: (error: Error) => void
    timeoutId: number
  } | null = null


  // Audio buffer for reconnection
  const audioBuffer: ArrayBuffer[] = []
  let audioBufferBytes = 0
  let isFlushingBuffer = false
  let activeConnectionTimeOffset = 0
  let lastTimelineEnd = 0
  const recentFinalEventKeys = new Set<string>()
  const recentFinalEventOrder: string[] = []
  const MAX_RECENT_FINAL_EVENTS = 512

  // Event callbacks
  const onTranscript = ref<((segment: TranscriptSegment) => void) | null>(null)
  const onTranslation = ref<((segment: TranslationSegment) => void) | null>(null)
  const onError = ref<((error: string) => void) | null>(null)
  const onBalanceUpdate = ref<((payload: Record<string, unknown>) => void) | null>(null)
  const onReconnecting = ref<((attempt: number, maxAttempts: number) => void) | null>(null)
  const onReconnected = ref<(() => void) | null>(null)

  const isConnected = computed(() => state.value === 'connected')
  const isReconnecting = computed(() => state.value === 'reconnecting')

  function rawTime(value: unknown): number {
    return typeof value === 'number' && Number.isFinite(value) ? Math.max(0, value) : 0
  }

  function timelineRange(
    startValue: unknown,
    endValue: unknown,
    connectionOffset: number,
  ): { startTime: number; endTime: number } {
    const rawStart = rawTime(startValue)
    const rawEnd = Math.max(rawStart, rawTime(endValue))
    return {
      startTime: connectionOffset + rawStart,
      endTime: connectionOffset + rawEnd,
    }
  }

  function isDuplicateFinalEvent(
    kind: 'transcript' | 'translation',
    speaker: string,
    text: string,
    startTime: number,
    endTime: number,
  ): boolean {
    const key = [
      kind,
      speaker,
      startTime.toFixed(3),
      endTime.toFixed(3),
      text.trim(),
    ].join('\u0000')
    if (recentFinalEventKeys.has(key)) return true

    recentFinalEventKeys.add(key)
    recentFinalEventOrder.push(key)
    if (recentFinalEventOrder.length > MAX_RECENT_FINAL_EVENTS) {
      const expired = recentFinalEventOrder.shift()
      if (expired) recentFinalEventKeys.delete(expired)
    }
    return false
  }

  // Clear reconnect timer
  function clearTimers(): void {
    if (reconnectTimeoutId !== null) {
      clearTimeout(reconnectTimeoutId)
      reconnectTimeoutId = null
    }
  }

  function settlePendingEnd(error?: Error): void {
    const pending = pendingEnd
    if (!pending) return
    pendingEnd = null
    window.clearTimeout(pending.timeoutId)
    if (error) pending.reject(error)
    else pending.resolve()
  }

  function waitForActiveConnection(timeoutMs: number): Promise<void> {
    if (state.value === 'connected' && ws.value?.readyState === WebSocket.OPEN) {
      return Promise.resolve()
    }
    return new Promise((resolve, reject) => {
      const startedAt = Date.now()
      const intervalId = window.setInterval(() => {
        if (state.value === 'connected' && ws.value?.readyState === WebSocket.OPEN) {
          window.clearInterval(intervalId)
          resolve()
          return
        }
        if (state.value === 'error' || manuallyDisconnected.value || Date.now() - startedAt >= timeoutMs) {
          window.clearInterval(intervalId)
          reject(new Error('Timed out waiting for the transcription connection to recover'))
        }
      }, 50)
    })
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
    if (reconnectTimeoutId !== null) return

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
      void doConnect(lastConfig, true).catch((e) => {
        const message = e instanceof Error ? e.message : 'Connection failed'
        error.value = message
        onError.value?.(message)
        scheduleReconnect()
      })
    }, delay)
  }

  // Internal connect function
  async function doConnect(config: SpeechmaticsProxyConfig, isReconnect: boolean = false): Promise<void> {
    if (!isAuthenticated()) {
      throw new Error('Must be authenticated to use Speechmatics proxy')
    }

    // Access tokens are short-lived. Refresh proactively before every initial
    // connection and reconnect instead of reusing the token captured at start.
    const token = await ensureValidAccessToken(90)

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
    endingSession = false
    const socketTimeOffset = isReconnect
      ? Math.max(activeConnectionTimeOffset, lastTimelineEnd)
      : 0
    activeConnectionTimeOffset = socketTimeOffset

    try {
      const wsUrl = getWsUrl()

      // Put the JWT in the WebSocket protocol header instead of the URL so it
      // is not retained by ordinary reverse-proxy access logs.
      const url = new URL(wsUrl)
      const socket = new WebSocket(url.toString(), [...websocketAuthProtocols(token)])
      ws.value = socket
      let startupSettled = false
      let resolveStartup!: () => void
      let rejectStartup!: (error: Error) => void
      const startupPromise = new Promise<void>((resolve, reject) => {
        resolveStartup = resolve
        rejectStartup = reject
      })
      const startupTimeoutId = window.setTimeout(() => {
        if (startupSettled) return
        startupSettled = true
        rejectStartup(new Error('Timed out waiting for Speechmatics recognition to start'))
      }, 15000)
      const settleStartup = (startupError?: Error) => {
        if (startupSettled) return
        startupSettled = true
        window.clearTimeout(startupTimeoutId)
        if (startupError) rejectStartup(startupError)
        else resolveStartup()
      }

      socket.onopen = () => {
        if (ws.value !== socket) return
        console.log('[Speechmatics] WebSocket connected')
        error.value = null

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

        socket.send(JSON.stringify(startMsg))
      }

      socket.onmessage = (event) => {
        if (ws.value !== socket) return
        try {
          const msg = JSON.parse(event.data)
          handleMessage(msg, socketTimeOffset)
          if (msg.message === 'RecognitionStarted') settleStartup()
          if (msg.message === 'Error') {
            settleStartup(new Error((msg.reason as string) || 'Speechmatics rejected the session'))
          }
        } catch (e) {
          console.error('[Speechmatics] Failed to parse WebSocket message:', e)
        }
      }

      socket.onerror = (event) => {
        if (ws.value !== socket) return
        console.error('[Speechmatics] WebSocket error:', event)
        settleStartup(new Error('Speechmatics WebSocket connection failed'))
        // Don't set error state here - wait for onclose to handle reconnection
      }

      socket.onclose = (event) => {
        if (ws.value !== socket) return
        ws.value = null
        settleStartup(new Error(event.reason || 'Speechmatics WebSocket closed during startup'))
        console.log(`[Speechmatics] WebSocket closed: code=${event.code}, reason=${event.reason || 'None'}`)

        if (endingSession) {
          state.value = 'disconnected'
          settlePendingEnd(new Error(event.reason || 'Connection closed before the final transcript'))
          return
        }

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

      await startupPromise
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
    activeConnectionTimeOffset = 0
    lastTimelineEnd = 0
    recentFinalEventKeys.clear()
    recentFinalEventOrder.length = 0
    audioBuffer.length = 0
    audioBufferBytes = 0

    return doConnect(config, false)
  }

  function handleMessage(msg: Record<string, unknown>, connectionOffset: number) {
    const messageType = msg.message as string

    switch (messageType) {
      case 'RecognitionStarted':
        console.log('[Speechmatics] Recognition started via proxy')
        if (state.value === 'reconnecting' || reconnectAttempts.value > 0) {
          console.log('[Speechmatics] Successfully reconnected')
          onReconnected.value?.()
        }
        state.value = 'connected'
        error.value = null
        reconnectAttempts.value = 0
        isRecording.value = true
        // Flush any buffered audio from reconnection
        flushAudioBuffer()
        break

      case 'AddTranscript':
        if (msg.metadata && onTranscript.value) {
          const metadata = msg.metadata as { transcript?: string; start_time?: number; end_time?: number }
          const results = msg.results as Array<{ alternatives?: Array<{ speaker?: string }> }> | undefined
          const speaker = results?.[0]?.alternatives?.[0]?.speaker || 'Speaker'
          const text = metadata.transcript || ''
          const { startTime, endTime } = timelineRange(
            metadata.start_time,
            metadata.end_time,
            connectionOffset,
          )
          if (isDuplicateFinalEvent('transcript', speaker, text, startTime, endTime)) break
          lastTimelineEnd = Math.max(lastTimelineEnd, endTime)

          onTranscript.value({
            text,
            startTime,
            endTime,
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
          const { startTime, endTime } = timelineRange(
            metadata.start_time,
            metadata.end_time,
            connectionOffset,
          )

          onTranscript.value({
            text: metadata.transcript || '',
            startTime,
            endTime,
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
            const text = r.content || ''
            const speaker = r.speaker || 'Speaker'
            const { startTime, endTime } = timelineRange(
              r.start_time,
              r.end_time,
              connectionOffset,
            )
            if (isDuplicateFinalEvent('translation', speaker, text, startTime, endTime)) break
            lastTimelineEnd = Math.max(lastTimelineEnd, endTime)
            onTranslation.value({
              text,
              startTime,
              endTime,
              speaker,
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
        if (endingSession) settlePendingEnd(new Error(reason))
        break
      }

      case 'EndOfTranscript':
        console.log('End of transcript')
        isRecording.value = false
        settlePendingEnd()
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
          audioBufferBytes -= chunk.byteLength
          ws.value.send(chunk)
        } catch (e) {
          audioBuffer.unshift(chunk)
          audioBufferBytes += chunk.byteLength
          console.warn('[Speechmatics] Failed to send buffered chunk:', e)
          break
        }
      }
    }
    if (audioBuffer.length === 0) audioBufferBytes = 0

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
      // Bound by PCM bytes rather than worklet message count: AudioWorklets
      // commonly emit 128-frame chunks, far more often than every 50 ms.
      while (audioBuffer.length > 0 && audioBufferBytes + audioData.byteLength > AUDIO_BUFFER_MAX_BYTES) {
        const removed = audioBuffer.shift()
        if (removed) audioBufferBytes -= removed.byteLength
      }
      audioBuffer.push(audioData)
      audioBufferBytes += audioData.byteLength
    }
  }

  // End the session
  async function endSession(timeoutMs = 8000): Promise<void> {
    if (pendingEnd) return pendingEnd.promise
    const startedAt = Date.now()
    if (state.value === 'connecting' || state.value === 'reconnecting') {
      await waitForActiveConnection(timeoutMs)
    }
    if (state.value === 'error') {
      throw new Error(error.value || 'Transcription connection is unavailable')
    }
    if (!ws.value || ws.value.readyState !== WebSocket.OPEN || state.value !== 'connected') {
      return
    }

    endingSession = true
    let resolvePromise!: () => void
    let rejectPromise!: (error: Error) => void
    const promise = new Promise<void>((resolve, reject) => {
      resolvePromise = resolve
      rejectPromise = reject
    })
    const remainingMs = Math.max(1, timeoutMs - (Date.now() - startedAt))
    const timeoutId = window.setTimeout(() => {
      settlePendingEnd(new Error('Timed out waiting for the final transcript'))
    }, remainingMs)
    pendingEnd = {
      promise,
      resolve: resolvePromise,
      reject: rejectPromise,
      timeoutId,
    }

    try {
      ws.value.send(JSON.stringify({ message: 'EndOfStream' }))
    } catch (e) {
      settlePendingEnd(e instanceof Error ? e : new Error('Failed to end transcription'))
    }
    return promise
  }

  // Disconnect
  function disconnect(): void {
    manuallyDisconnected.value = true
    endingSession = false
    clearTimers()
    settlePendingEnd(new Error('Session disconnected before the final transcript'))

    // Clear audio buffer
    audioBuffer.length = 0
    audioBufferBytes = 0

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
    endingSession = false
    clearTimers()
    settlePendingEnd(new Error('Component unmounted before the final transcript'))
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
