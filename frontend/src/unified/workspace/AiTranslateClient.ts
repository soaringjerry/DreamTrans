import { joinSegmentTexts } from './TranscriptFeedModel'

/**
 * Context-aware AI translation over the backend /ws/translate endpoint.
 *
 * The client batches provider micro-finals into sentence-sized chunks aligned
 * with the display cards, sends each chunk followed by an explicit flush so
 * the server translates exactly one paragraph per chunk, and matches ordered
 * AddTranslation results back to the originating chunk. The server keeps a
 * rolling context window across chunks, so translations see the conversation
 * history the way the old Classic UI did.
 */

export interface AiTranslateSegmentInput {
  id: string
  speaker: string
  text: string
  startTime: number
  endTime: number
}

export interface AiTranslateChunk {
  segmentIds: readonly string[]
  speaker: string
  text: string
  startTime: number
  endTime: number
}

export interface AiTranslationResult {
  text: string
  model?: string
  latencyMs?: number
}

export interface AiTranslateSessionConfig {
  /** Cloud session UUID for billing/RAG attribution; omit for local sessions. */
  sessionId?: string
  /** Custom translate system prompt; empty uses the server default. */
  translatePrompt?: string
}

interface AiTranslateSocket {
  readyState: number
  send(data: string): void
  close(code?: number, reason?: string): void
  onopen: ((event: unknown) => void) | null
  onmessage: ((event: { data: unknown }) => void) | null
  onclose: ((event: { code: number; reason: string }) => void) | null
  onerror: ((event: unknown) => void) | null
}

export interface AiTranslateClientOptions {
  url: string | (() => string)
  tokenProvider: () => Promise<string>
  protocolFactory: (token: string) => readonly string[]
  socketFactory?: (url: string, protocols: readonly string[]) => AiTranslateSocket
  onTranslation: (chunk: AiTranslateChunk, result: AiTranslationResult) => void
  onError?: (message: string) => void
  /** Flush an unfinished sentence after this idle period. */
  idleFlushMs?: number
  /** Sentence-punctuation chunks shorter than this wait for more text. */
  minChunkChars?: number
  maxPendingChunks?: number
  reconnectDelaysMs?: readonly number[]
}

const SOCKET_OPEN = 1
const SENTENCE_END = /[.!?。！？…]["')\]»”’]*\s*$/u
const DEFAULT_IDLE_FLUSH_MS = 2_500
const DEFAULT_MIN_CHUNK_CHARS = 12
const DEFAULT_MAX_PENDING = 64
const DEFAULT_RECONNECT_DELAYS = [1_000, 2_000, 4_000, 8_000, 15_000] as const
const CLOSE_LINGER_MS = 20_000

interface BufferedChunk {
  cardId: string
  speaker: string
  segmentIds: string[]
  text: string
  startTime: number
  endTime: number
}

function defaultSocketFactory(
  url: string,
  protocols: readonly string[],
): AiTranslateSocket {
  return new WebSocket(url, [...protocols]) as unknown as AiTranslateSocket
}

export class AiTranslateClient {
  private readonly url: string | (() => string)
  private readonly tokenProvider: () => Promise<string>
  private readonly protocolFactory: (token: string) => readonly string[]
  private readonly socketFactory: (
    url: string,
    protocols: readonly string[],
  ) => AiTranslateSocket
  private readonly onTranslation: (
    chunk: AiTranslateChunk,
    result: AiTranslationResult,
  ) => void
  private readonly onError?: (message: string) => void
  private readonly idleFlushMs: number
  private readonly minChunkChars: number
  private readonly maxPendingChunks: number
  private readonly reconnectDelaysMs: readonly number[]

  private socket: AiTranslateSocket | null = null
  private socketSerial = 0
  private socketReady = false
  private active = false
  private destroyed = false
  private sessionConfig: AiTranslateSessionConfig = {}
  private buffer: BufferedChunk | null = null
  private readonly pending: AiTranslateChunk[] = []
  private idleTimer: ReturnType<typeof globalThis.setTimeout> | null = null
  private closeTimer: ReturnType<typeof globalThis.setTimeout> | null = null
  private reconnectTimer: ReturnType<typeof globalThis.setTimeout> | null = null
  private reconnectAttempt = 0

  constructor(options: AiTranslateClientOptions) {
    this.url = options.url
    this.tokenProvider = options.tokenProvider
    this.protocolFactory = options.protocolFactory
    this.socketFactory = options.socketFactory ?? defaultSocketFactory
    this.onTranslation = options.onTranslation
    this.onError = options.onError
    this.idleFlushMs = options.idleFlushMs ?? DEFAULT_IDLE_FLUSH_MS
    this.minChunkChars = options.minChunkChars ?? DEFAULT_MIN_CHUNK_CHARS
    this.maxPendingChunks = options.maxPendingChunks ?? DEFAULT_MAX_PENDING
    this.reconnectDelaysMs = options.reconnectDelaysMs ?? DEFAULT_RECONNECT_DELAYS
  }

  startSession(config: AiTranslateSessionConfig): void {
    if (this.destroyed) return
    this.sessionConfig = { ...config }
    this.active = true
    this.reconnectAttempt = 0
    this.clearTimer('close')
    // A previous session's unmatched chunks must not consume new results.
    this.buffer = null
    this.pending.length = 0
    if (this.socketReady) this.sendInit()
    else this.connect()
  }

  /**
   * Feed one finalized transcript fragment. Fragments buffer per display card
   * and flush as complete sentences, on card change, or after an idle pause.
   */
  addSegment(segment: AiTranslateSegmentInput, cardId: string): void {
    if (!this.active || this.destroyed) return
    const text = segment.text.trim()
    if (!text) return

    if (this.buffer && this.buffer.cardId !== cardId) this.flushBuffer()

    if (this.buffer) {
      this.buffer.segmentIds.push(segment.id)
      this.buffer.text = joinSegmentTexts(this.buffer.text, text)
      this.buffer.endTime = segment.endTime
    } else {
      this.buffer = {
        cardId,
        speaker: segment.speaker,
        segmentIds: [segment.id],
        text,
        startTime: segment.startTime,
        endTime: segment.endTime,
      }
    }

    if (
      SENTENCE_END.test(this.buffer.text)
      && this.buffer.text.length >= this.minChunkChars
    ) {
      this.flushBuffer()
    } else {
      this.restartIdleTimer()
    }
  }

  /** Flush the open sentence buffer immediately (pause/stop). */
  flush(): void {
    this.flushBuffer()
  }

  /**
   * Ends the translation session. The socket lingers briefly so in-flight
   * translations still arrive for the final sentences.
   */
  stopSession(): void {
    if (this.destroyed) return
    this.flushBuffer()
    this.active = false
    this.clearTimer('idle')
    if (this.pending.length === 0) this.scheduleClose(0)
    else this.scheduleClose(CLOSE_LINGER_MS)
  }

  destroy(): void {
    if (this.destroyed) return
    this.destroyed = true
    this.active = false
    this.clearTimer('idle')
    this.clearTimer('close')
    this.clearTimer('reconnect')
    this.closeSocket()
    this.buffer = null
    this.pending.length = 0
  }

  getDiagnostics(): { pendingChunks: number; bufferedChars: number } {
    return {
      pendingChunks: this.pending.length,
      bufferedChars: this.buffer?.text.length ?? 0,
    }
  }

  private connect(): void {
    if (this.destroyed || this.socket) return
    const serial = ++this.socketSerial
    void (async () => {
      let socket: AiTranslateSocket
      try {
        const token = (await this.tokenProvider()).trim()
        if (this.destroyed || serial !== this.socketSerial) return
        const url = typeof this.url === 'function' ? this.url() : this.url
        socket = this.socketFactory(url, this.protocolFactory(token))
      } catch (reason) {
        this.reportError(
          `AI 翻译连接初始化失败：${reason instanceof Error ? reason.message : String(reason)}`,
        )
        this.scheduleReconnect()
        return
      }

      this.socket = socket
      socket.onopen = () => {
        if (this.socket !== socket) return
        this.socketReady = true
        this.reconnectAttempt = 0
        this.sendInit()
        // Chunks sent before a disconnect were never acknowledged: resend so
        // the rolling context and their translations are not silently lost.
        for (const chunk of this.pending) this.sendChunk(chunk)
      }
      socket.onmessage = (event) => {
        if (this.socket !== socket) return
        this.handleMessage(event.data)
      }
      socket.onclose = () => {
        if (this.socket !== socket) return
        this.socket = null
        this.socketReady = false
        if (this.active && !this.destroyed) this.scheduleReconnect()
      }
      socket.onerror = () => {
        // onclose follows and owns the retry policy.
      }
    })()
  }

  private sendInit(): void {
    const config: Record<string, unknown> = {
      // Chunks arrive pre-batched and explicitly flushed; the server-side
      // chunker must pass them through as-is.
      min_chunk_chars: 1,
      disable_summarization: true,
      disable_embeddings: true,
    }
    if (this.sessionConfig.sessionId) config.session_id = this.sessionConfig.sessionId
    const prompt = this.sessionConfig.translatePrompt?.trim()
    if (prompt) config.translate_prompt = prompt
    this.sendJSON({ type: 'init', mode: 'ai_rolling', config })
  }

  private flushBuffer(): void {
    this.clearTimer('idle')
    const buffered = this.buffer
    if (!buffered) return
    this.buffer = null
    const chunk: AiTranslateChunk = {
      segmentIds: buffered.segmentIds,
      speaker: buffered.speaker,
      text: buffered.text,
      startTime: buffered.startTime,
      endTime: buffered.endTime,
    }
    if (this.pending.length >= this.maxPendingChunks) {
      this.pending.shift()
      this.reportError('AI 翻译积压过多，最早的未完成段落已被丢弃')
    }
    this.pending.push(chunk)
    if (this.socketReady) this.sendChunk(chunk)
    else this.connect()
  }

  private sendChunk(chunk: AiTranslateChunk): void {
    this.sendJSON({
      type: 'transcript',
      payload: {
        speaker: chunk.speaker,
        transcript: chunk.text,
        start_time: chunk.startTime,
        end_time: chunk.endTime,
      },
    })
    // Force a server-side paragraph boundary so results map 1:1 to chunks.
    this.sendJSON({ type: 'flush' })
  }

  private handleMessage(data: unknown): void {
    if (typeof data !== 'string') return
    let payload: {
      message?: string
      reason?: string
      results?: Array<{
        content?: string
        start_time?: number
        end_time?: number
        model?: string
        latency_ms?: number
      }>
    }
    try {
      payload = JSON.parse(data) as typeof payload
    } catch {
      return
    }

    switch (payload.message) {
      case 'AddTranslation': {
        for (const result of payload.results ?? []) {
          const text = (result.content ?? '').trim()
          if (!text) continue
          const chunk = this.takeMatchingChunk(result.start_time, result.end_time)
          if (!chunk) continue
          this.onTranslation(chunk, {
            text,
            ...(result.model ? { model: result.model } : {}),
            ...(result.latency_ms === undefined ? {} : { latencyMs: result.latency_ms }),
          })
        }
        if (!this.active && this.pending.length === 0) this.scheduleClose(0)
        break
      }
      case 'Error': {
        // Ordered delivery: an error consumes the oldest outstanding chunk.
        this.pending.shift()
        this.reportError(`AI 翻译失败：${payload.reason ?? '未知错误'}`)
        break
      }
      default:
        break
    }
  }

  /**
   * Results arrive in send order; the oldest pending chunk is the expected
   * match. Time bounds guard against drift after an error was dropped by an
   * intermediary.
   */
  private takeMatchingChunk(
    startTime?: number,
    endTime?: number,
  ): AiTranslateChunk | null {
    if (this.pending.length === 0) return null
    if (typeof startTime === 'number' && typeof endTime === 'number') {
      for (let index = 0; index < this.pending.length; index += 1) {
        const candidate = this.pending[index]
        if (!candidate) continue
        if (
          Math.abs(candidate.startTime - startTime) < 0.75
          && Math.abs(candidate.endTime - endTime) < 0.75
        ) {
          this.pending.splice(0, index + 1)
          return candidate
        }
      }
    }
    return this.pending.shift() ?? null
  }

  private restartIdleTimer(): void {
    this.clearTimer('idle')
    this.idleTimer = globalThis.setTimeout(() => {
      this.idleTimer = null
      this.flushBuffer()
    }, this.idleFlushMs)
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer !== null || this.destroyed || !this.active) return
    const delay = this.reconnectDelaysMs[
      Math.min(this.reconnectAttempt, this.reconnectDelaysMs.length - 1)
    ] ?? 15_000
    if (this.reconnectAttempt >= this.reconnectDelaysMs.length + 3) {
      this.reportError('AI 翻译连接多次失败，已停止重试；后续内容仍会保留原文')
      this.active = false
      return
    }
    this.reconnectAttempt += 1
    this.reconnectTimer = globalThis.setTimeout(() => {
      this.reconnectTimer = null
      if (this.active && !this.destroyed) this.connect()
    }, delay)
  }

  private scheduleClose(delayMs: number): void {
    this.clearTimer('close')
    if (delayMs <= 0) {
      this.closeSocket()
      return
    }
    this.closeTimer = globalThis.setTimeout(() => {
      this.closeTimer = null
      if (!this.active) this.closeSocket()
    }, delayMs)
  }

  private closeSocket(): void {
    const socket = this.socket
    this.socket = null
    this.socketReady = false
    if (!socket) return
    socket.onopen = null
    socket.onmessage = null
    socket.onclose = null
    socket.onerror = null
    try {
      socket.close(1000, 'Session complete')
    } catch {
      // Socket may already be closed.
    }
  }

  private sendJSON(value: unknown): void {
    const socket = this.socket
    if (!socket || socket.readyState !== SOCKET_OPEN) return
    try {
      socket.send(JSON.stringify(value))
    } catch (reason) {
      this.reportError(
        `AI 翻译消息发送失败：${reason instanceof Error ? reason.message : String(reason)}`,
      )
    }
  }

  private reportError(message: string): void {
    if (!this.destroyed) this.onError?.(message)
  }

  private clearTimer(kind: 'idle' | 'close' | 'reconnect'): void {
    const key = kind === 'idle'
      ? 'idleTimer' as const
      : kind === 'close'
        ? 'closeTimer' as const
        : 'reconnectTimer' as const
    const timer = this[key]
    if (timer !== null) {
      globalThis.clearTimeout(timer)
      this[key] = null
    }
  }
}
