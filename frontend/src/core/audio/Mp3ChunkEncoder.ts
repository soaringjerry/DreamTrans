import lameScriptUrl from 'lamejs/lame.min.js?url'

interface EncoderControl {
  reject: (reason: Error) => void
  resolve: () => void
  timeout: ReturnType<typeof globalThis.setTimeout>
  type: 'drain' | 'flush'
}

type EncoderWorkerResponse =
  | { type: 'chunk'; buffer: ArrayBuffer }
  | { type: 'encoded'; byteLength: number }
  | { type: 'drained' }
  | { type: 'flushed' }
  | { type: 'error'; message: string }

export interface Mp3ChunkEncoderOptions {
  bitrateKbps?: number
  chunkMilliseconds: number
  onChunk: (blob: Blob) => void
  onError?: (error: Error) => void
  sampleRate: number
  maxPendingPcmBytes?: number
  workerFactory?: () => Worker
}

function positiveInteger(value: number | undefined, fallback: number): number {
  const normalizedFallback =
    Number.isFinite(fallback) && fallback >= 1 ? Math.floor(fallback) : 1
  if (value === undefined || !Number.isFinite(value) || value < 1) {
    return normalizedFallback
  }
  return Math.floor(value)
}

/**
 * Encodes PCM away from the React/main thread. MP3 consists of independently
 * decodable frames, so chunks from a later Continue capture can be appended to
 * the same logical file without introducing a second WebM/MP4 container.
 */
export class Mp3ChunkEncoder {
  private readonly worker: Worker
  private readonly onChunk: (blob: Blob) => void
  private readonly onError?: (error: Error) => void
  private readonly maxPendingPcmBytes: number
  private controlChain: Promise<void> = Promise.resolve()
  private pendingControl: EncoderControl | null = null
  private pendingPcmBytes = 0
  private failure: Error | null = null
  private closing = false
  private destroyed = false

  constructor(options: Mp3ChunkEncoderOptions) {
    this.onChunk = options.onChunk
    this.onError = options.onError
    this.maxPendingPcmBytes = positiveInteger(
      options.maxPendingPcmBytes,
      options.sampleRate * 4 * 10,
    )
    this.worker = options.workerFactory?.() ?? new Worker(
      new URL('./mp3Encoder.worker.ts', import.meta.url),
      { type: 'classic' },
    )
    this.worker.onmessage = (event: MessageEvent<EncoderWorkerResponse>) => {
      this.handleMessage(event.data)
    }
    this.worker.onerror = (event) => {
      this.fail(new Error(event.message || 'MP3 encoder worker failed'))
    }
    this.worker.onmessageerror = () => {
      this.fail(new Error('MP3 encoder worker returned an unreadable message'))
    }
    this.worker.postMessage({
      type: 'init',
      sampleRate: options.sampleRate,
      bitrateKbps: options.bitrateKbps ?? 96,
      chunkMilliseconds: options.chunkMilliseconds,
      scriptUrl: lameScriptUrl,
    })
  }

  encode(buffer: ArrayBuffer): void {
    if (this.destroyed || this.closing || this.failure || buffer.byteLength === 0) return
    const byteLength = buffer.byteLength
    if (this.pendingPcmBytes + byteLength > this.maxPendingPcmBytes) {
      this.fail(new Error(
        `MP3 encoder fell behind by more than ${this.maxPendingPcmBytes} PCM bytes`,
      ))
      return
    }
    this.pendingPcmBytes += byteLength
    try {
      this.worker.postMessage({ type: 'encode', buffer }, [buffer])
    } catch (reason) {
      this.pendingPcmBytes = Math.max(0, this.pendingPcmBytes - byteLength)
      this.fail(reason instanceof Error ? reason : new Error(String(reason)))
    }
  }

  drain(): Promise<void> {
    return this.enqueueControl('drain')
  }

  flush(): Promise<void> {
    this.closing = true
    return this.enqueueControl('flush')
  }

  destroy(): void {
    if (this.destroyed) return
    const error = this.failure ?? new Error('MP3 encoder was destroyed')
    this.failure = error
    this.destroyed = true
    this.pendingPcmBytes = 0
    if (this.pendingControl) {
      globalThis.clearTimeout(this.pendingControl.timeout)
      this.pendingControl.reject(error)
      this.pendingControl = null
    }
    try {
      this.worker.terminate()
    } catch {
      // The persistent failure above remains authoritative.
    }
  }

  private enqueueControl(type: 'drain' | 'flush'): Promise<void> {
    if (this.destroyed) {
      return Promise.reject(this.failure ?? new Error('MP3 encoder was destroyed'))
    }
    if (this.failure) return Promise.reject(this.failure)
    const operation = this.controlChain.then(() => this.requestControl(type))
    this.controlChain = operation.catch(() => undefined)
    return operation
  }

  private requestControl(type: 'drain' | 'flush'): Promise<void> {
    if (this.destroyed) {
      return Promise.reject(this.failure ?? new Error('MP3 encoder was destroyed'))
    }
    if (this.failure) return Promise.reject(this.failure)
    return new Promise<void>((resolve, reject) => {
      const timeout = globalThis.setTimeout(() => {
        if (this.pendingControl?.type !== type) return
        this.pendingControl = null
        const error = new Error(`MP3 encoder ${type} timed out`)
        this.fail(error)
        reject(error)
      }, 10_000)
      this.pendingControl = { reject, resolve, timeout, type }
      this.worker.postMessage({ type })
    })
  }

  private handleMessage(message: EncoderWorkerResponse): void {
    if (this.destroyed) return
    if (message.type === 'encoded') {
      if (
        !Number.isSafeInteger(message.byteLength)
        || message.byteLength <= 0
        || message.byteLength > this.pendingPcmBytes
      ) {
        this.fail(new Error('MP3 encoder returned an invalid PCM acknowledgement'))
        return
      }
      this.pendingPcmBytes -= message.byteLength
      return
    }
    if (message.type === 'chunk') {
      if (message.buffer.byteLength > 0) {
        try {
          this.onChunk(new Blob([message.buffer], { type: 'audio/mpeg' }))
        } catch (reason) {
          this.fail(reason instanceof Error ? reason : new Error(String(reason)))
        }
      }
      return
    }
    if (message.type === 'error') {
      this.fail(new Error(message.message))
      return
    }
    const expected = message.type === 'drained' ? 'drain' : 'flush'
    const control = this.pendingControl
    if (!control || control.type !== expected) return
    this.pendingControl = null
    globalThis.clearTimeout(control.timeout)
    control.resolve()
    if (message.type === 'flushed') {
      this.destroyed = true
      try {
        this.worker.terminate()
      } catch {
        // Flush has already completed successfully.
      }
    }
  }

  private fail(error: Error): void {
    if (this.failure) return
    this.failure = error
    this.pendingPcmBytes = 0
    const control = this.pendingControl
    this.pendingControl = null
    if (control) {
      globalThis.clearTimeout(control.timeout)
      control.reject(error)
    }
    if (!this.destroyed) {
      this.destroyed = true
      try {
        this.worker.terminate()
      } catch {
        // The encoder failure must still reach the capture layer.
      }
    }
    try {
      this.onError?.(error)
    } catch {
      // A UI error callback must never mask the encoder's original failure.
    }
  }
}
