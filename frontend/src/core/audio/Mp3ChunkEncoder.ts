import lameScriptUrl from 'lamejs/lame.min.js?url'

interface EncoderControl {
  reject: (reason: Error) => void
  resolve: () => void
  timeout: ReturnType<typeof globalThis.setTimeout>
  type: 'drain' | 'flush'
}

type EncoderWorkerResponse =
  | { type: 'chunk'; buffer: ArrayBuffer }
  | { type: 'drained' }
  | { type: 'flushed' }
  | { type: 'error'; message: string }

export interface Mp3ChunkEncoderOptions {
  bitrateKbps?: number
  chunkMilliseconds: number
  onChunk: (blob: Blob) => void
  sampleRate: number
}

/**
 * Encodes PCM away from the React/main thread. MP3 consists of independently
 * decodable frames, so chunks from a later Continue capture can be appended to
 * the same logical file without introducing a second WebM/MP4 container.
 */
export class Mp3ChunkEncoder {
  private readonly worker: Worker
  private readonly onChunk: (blob: Blob) => void
  private controlChain: Promise<void> = Promise.resolve()
  private pendingControl: EncoderControl | null = null
  private failure: Error | null = null
  private closing = false
  private destroyed = false

  constructor(options: Mp3ChunkEncoderOptions) {
    this.onChunk = options.onChunk
    this.worker = new Worker(
      new URL('./mp3Encoder.worker.ts', import.meta.url),
      { type: 'classic' },
    )
    this.worker.onmessage = (event: MessageEvent<EncoderWorkerResponse>) => {
      this.handleMessage(event.data)
    }
    this.worker.onerror = (event) => {
      this.fail(new Error(event.message || 'MP3 encoder worker failed'))
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
    this.worker.postMessage({ type: 'encode', buffer }, [buffer])
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
    this.destroyed = true
    const error = new Error('MP3 encoder was destroyed')
    if (this.pendingControl) {
      globalThis.clearTimeout(this.pendingControl.timeout)
      this.pendingControl.reject(error)
      this.pendingControl = null
    }
    this.worker.terminate()
  }

  private enqueueControl(type: 'drain' | 'flush'): Promise<void> {
    if (this.destroyed) return Promise.reject(new Error('MP3 encoder was destroyed'))
    if (this.failure) return Promise.reject(this.failure)
    const operation = this.controlChain.then(() => this.requestControl(type))
    this.controlChain = operation.catch(() => undefined)
    return operation
  }

  private requestControl(type: 'drain' | 'flush'): Promise<void> {
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
    if (message.type === 'chunk') {
      if (message.buffer.byteLength > 0) {
        this.onChunk(new Blob([message.buffer], { type: 'audio/mpeg' }))
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
      this.worker.terminate()
    }
  }

  private fail(error: Error): void {
    if (this.failure) return
    this.failure = error
    const control = this.pendingControl
    this.pendingControl = null
    if (control) {
      globalThis.clearTimeout(control.timeout)
      control.reject(error)
    }
    if (!this.destroyed) {
      this.destroyed = true
      this.worker.terminate()
    }
  }
}
