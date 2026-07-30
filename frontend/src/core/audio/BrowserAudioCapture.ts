import { Mp3ChunkEncoder } from './Mp3ChunkEncoder'

export type AudioCaptureStatus = 'idle' | 'starting' | 'recording' | 'paused' | 'stopping'

export interface AudioChunk {
  sequence: number
  recordedAt: number
  blob: Blob
}

export interface BrowserAudioCaptureOptions {
  onPCM: (audio: ArrayBuffer) => void
  onChunk?: (chunk: AudioChunk) => void | Promise<void>
  onStatusChange?: (status: AudioCaptureStatus) => void
  workletURL?: string
  sampleRate?: number
  batchMilliseconds?: number
  mediaChunkMilliseconds?: number
}

export class BrowserAudioCapture {
  private readonly options: Required<Pick<
    BrowserAudioCaptureOptions,
    'workletURL' | 'sampleRate' | 'batchMilliseconds' | 'mediaChunkMilliseconds'
  >> & BrowserAudioCaptureOptions

  private statusValue: AudioCaptureStatus = 'idle'
  private stream: MediaStream | null = null
  private context: AudioContext | null = null
  private source: MediaStreamAudioSourceNode | null = null
  private worklet: AudioWorkletNode | null = null
  private silentGain: GainNode | null = null
  private encoder: Mp3ChunkEncoder | null = null
  private wakeLock: WakeLockSentinel | null = null
  private chunkSequence = 0
  private visibilityHandler: (() => void) | null = null
  private chunkWriteChain: Promise<void> = Promise.resolve()
  private pcmFlushResolve: (() => void) | null = null
  private lifecycleGeneration = 0
  private startPromise: Promise<void> | null = null
  private stopPromise: Promise<void> | null = null
  private pendingStartCleanup: (() => Promise<void>) | null = null

  constructor(options: BrowserAudioCaptureOptions) {
    this.options = {
      ...options,
      workletURL: options.workletURL ?? '/pcm-batched-audio-worklet.js',
      sampleRate: options.sampleRate ?? 48_000,
      batchMilliseconds: options.batchMilliseconds ?? 40,
      mediaChunkMilliseconds: options.mediaChunkMilliseconds ?? 2_000,
    }
  }

  get status(): AudioCaptureStatus {
    return this.statusValue
  }

  get mimeType(): string {
    return 'audio/mpeg'
  }

  private setStatus(status: AudioCaptureStatus): void {
    this.statusValue = status
    this.options.onStatusChange?.(status)
  }

  private async acquireWakeLock(): Promise<void> {
    if (this.wakeLock || !('wakeLock' in navigator) || document.hidden) return
    try {
      this.wakeLock = await navigator.wakeLock.request('screen')
      this.wakeLock.addEventListener('release', () => {
        this.wakeLock = null
      }, { once: true })
    } catch {
      this.wakeLock = null
    }
  }

  private installVisibilityHandler(): void {
    this.visibilityHandler = () => {
      if (!document.hidden && (this.statusValue === 'recording' || this.statusValue === 'paused')) {
        void this.acquireWakeLock()
      }
    }
    document.addEventListener('visibilitychange', this.visibilityHandler)
  }

  start(): Promise<void> {
    if (this.statusValue !== 'idle') {
      return Promise.reject(
        new Error(`Audio capture cannot start while ${this.statusValue}`),
      )
    }
    if (!navigator.mediaDevices?.getUserMedia) {
      return Promise.reject(
        new Error('This browser does not support microphone capture'),
      )
    }
    if (typeof AudioWorkletNode === 'undefined') {
      return Promise.reject(
        new Error('This browser does not support AudioWorklet'),
      )
    }

    const generation = ++this.lifecycleGeneration
    this.setStatus('starting')
    this.chunkSequence = 0
    const operation = this.performStart(generation)
    const tracked = operation.finally(() => {
      if (this.startPromise === tracked) this.startPromise = null
    })
    this.startPromise = tracked
    return tracked
  }

  private async performStart(generation: number): Promise<void> {
    const resources: {
      stream: MediaStream | null
      context: AudioContext | null
      source: MediaStreamAudioSourceNode | null
      worklet: AudioWorkletNode | null
      silentGain: GainNode | null
      encoder: Mp3ChunkEncoder | null
    } = {
      stream: null,
      context: null,
      source: null,
      worklet: null,
      silentGain: null,
      encoder: null,
    }
    const cleanup = async () => {
      const encoder = resources.encoder
      const source = resources.source
      const worklet = resources.worklet
      const silentGain = resources.silentGain
      const stream = resources.stream
      const context = resources.context
      resources.encoder = null
      resources.source = null
      resources.worklet = null
      resources.silentGain = null
      resources.stream = null
      resources.context = null
      encoder?.destroy()
      source?.disconnect()
      worklet?.disconnect()
      silentGain?.disconnect()
      stream?.getTracks().forEach((track) => track.stop())
      if (context && context.state !== 'closed') {
        await context.close().catch(() => undefined)
      }
    }
    this.pendingStartCleanup = cleanup

    try {
      resources.stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: true,
          noiseSuppression: true,
          autoGainControl: true,
          channelCount: 1,
          sampleRate: this.options.sampleRate,
        },
      })
      this.assertCurrentStart(generation)

      resources.context = new AudioContext({ sampleRate: this.options.sampleRate })
      if (resources.context.state !== 'running') await resources.context.resume()
      this.assertCurrentStart(generation)
      await resources.context.audioWorklet.addModule(this.options.workletURL)
      this.assertCurrentStart(generation)

      resources.source = resources.context.createMediaStreamSource(resources.stream)
      const batchFrames = Math.max(
        128,
        Math.round(this.options.sampleRate * this.options.batchMilliseconds / 1_000),
      )
      resources.worklet = new AudioWorkletNode(
        resources.context,
        'dreamtrans-pcm-batched-processor',
        { processorOptions: { batchFrames } },
      )
      resources.worklet.port.onmessage = (event: MessageEvent<ArrayBuffer | string>) => {
        if (event.data === 'flushed') {
          this.pcmFlushResolve?.()
          this.pcmFlushResolve = null
          return
        }
        if (!(event.data instanceof ArrayBuffer)) return
        if (this.statusValue !== 'recording' && this.statusValue !== 'stopping') return
        if (event.data.byteLength > 0) {
          this.options.onPCM(event.data)
          resources.encoder?.encode(event.data.slice(0))
        }
      }
      resources.silentGain = resources.context.createGain()
      resources.silentGain.gain.value = 0
      resources.source.connect(resources.worklet)
      resources.worklet.connect(resources.silentGain)
      resources.silentGain.connect(resources.context.destination)

      if (this.options.onChunk) {
        resources.encoder = new Mp3ChunkEncoder({
          sampleRate: this.options.sampleRate,
          chunkMilliseconds: this.options.mediaChunkMilliseconds,
          onChunk: (blob) => {
            if (blob.size === 0 || !this.options.onChunk) return
            const chunk: AudioChunk = {
              sequence: this.chunkSequence++,
              recordedAt: Date.now(),
              blob,
            }
            this.chunkWriteChain = this.chunkWriteChain
              .catch(() => undefined)
              .then(async () => {
                await this.options.onChunk?.(chunk)
              })
          },
        })
      }

      this.assertCurrentStart(generation)
      this.stream = resources.stream
      this.context = resources.context
      this.source = resources.source
      this.worklet = resources.worklet
      this.silentGain = resources.silentGain
      this.encoder = resources.encoder
      if (this.pendingStartCleanup === cleanup) this.pendingStartCleanup = null
      this.installVisibilityHandler()
      this.setStatus('recording')
      void this.acquireWakeLock()
    } catch (error) {
      await cleanup()
      if (generation === this.lifecycleGeneration && this.statusValue !== 'stopping') {
        this.setStatus('idle')
      }
      throw error
    } finally {
      if (this.pendingStartCleanup === cleanup) this.pendingStartCleanup = null
    }
  }

  private assertCurrentStart(generation: number): void {
    if (generation !== this.lifecycleGeneration || this.statusValue === 'stopping') {
      throw new DOMException('Audio capture start was cancelled', 'AbortError')
    }
  }

  setPaused(paused: boolean): void {
    if (paused && this.statusValue === 'recording') {
      this.worklet?.port.postMessage('pause')
      this.setStatus('paused')
    } else if (!paused && this.statusValue === 'paused') {
      this.worklet?.port.postMessage('resume')
      this.setStatus('recording')
    }
  }

  async flushCompressedChunk(): Promise<void> {
    await this.encoder?.drain()
    await this.chunkWriteChain.catch(() => undefined)
  }

  async stop(): Promise<void> {
    if (this.statusValue === 'idle') return
    if (this.stopPromise) return this.stopPromise
    this.lifecycleGeneration += 1
    this.setStatus('stopping')
    const operation = this.performStop()
    const tracked = operation.finally(() => {
      if (this.stopPromise === tracked) this.stopPromise = null
    })
    this.stopPromise = tracked
    return tracked
  }

  private async performStop(): Promise<void> {
    const pendingStartCleanup = this.pendingStartCleanup
    if (pendingStartCleanup) {
      this.pendingStartCleanup = null
      await pendingStartCleanup()
    }
    await this.flushPCM()
    let encoderFailure: unknown
    try {
      await this.encoder?.flush()
      await this.chunkWriteChain
    } catch (reason) {
      encoderFailure = reason
    } finally {
      await this.disposeResources()
      this.setStatus('idle')
    }
    if (encoderFailure) throw encoderFailure
  }

  private async flushPCM(): Promise<void> {
    if (
      !this.worklet
      || (this.statusValue !== 'recording' && this.statusValue !== 'stopping')
    ) {
      return
    }
    await new Promise<void>((resolve) => {
      let settled = false
      const finish = () => {
        if (settled) return
        settled = true
        globalThis.clearTimeout(timeout)
        if (this.pcmFlushResolve === finish) this.pcmFlushResolve = null
        resolve()
      }
      const timeout = globalThis.setTimeout(finish, 250)
      this.pcmFlushResolve = finish
      this.worklet?.port.postMessage('flush')
    })
  }

  private async disposeResources(): Promise<void> {
    if (this.visibilityHandler) {
      document.removeEventListener('visibilitychange', this.visibilityHandler)
      this.visibilityHandler = null
    }
    this.worklet?.port.postMessage('stop')
    this.pcmFlushResolve?.()
    this.pcmFlushResolve = null
    if (this.worklet) this.worklet.port.onmessage = null
    this.source?.disconnect()
    this.worklet?.disconnect()
    this.silentGain?.disconnect()
    this.stream?.getTracks().forEach((track) => track.stop())
    if (this.context && this.context.state !== 'closed') {
      await this.context.close().catch(() => undefined)
    }
    if (this.wakeLock) {
      await this.wakeLock.release().catch(() => undefined)
    }
    this.encoder?.destroy()
    this.encoder = null
    this.worklet = null
    this.silentGain = null
    this.source = null
    this.context = null
    this.stream = null
    this.wakeLock = null
  }
}
