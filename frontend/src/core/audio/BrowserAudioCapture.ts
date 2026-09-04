import { Mp3ChunkEncoder } from './Mp3ChunkEncoder'
import { messages } from '../../i18n'

export type AudioCaptureStatus = 'idle' | 'starting' | 'recording' | 'paused' | 'stopping'

/**
 * microphone — default browser mic via getUserMedia
 * system — tab/window/system audio via getDisplayMedia (share audio)
 * mixed — microphone + system audio mixed in the AudioContext graph
 */
export type AudioCaptureSource = 'microphone' | 'system' | 'mixed'

export type AudioCaptureErrorCode =
  | 'audio-context-interrupted'
  | 'audio-encoder-failed'
  | 'audio-storage-backpressure'
  | 'audio-storage-write-failed'
  | 'microphone-ended'
  | 'microphone-muted'

export class AudioCaptureError extends Error {
  readonly code: AudioCaptureErrorCode
  readonly recoverable: boolean
  readonly reason?: unknown

  constructor(
    code: AudioCaptureErrorCode,
    message: string,
    options: { recoverable: boolean; reason?: unknown },
  ) {
    super(message)
    this.name = 'AudioCaptureError'
    this.code = code
    this.recoverable = options.recoverable
    this.reason = options.reason
  }
}

export interface AudioChunk {
  sequence: number
  recordedAt: number
  blob: Blob
}

export interface BrowserAudioCaptureOptions {
  onPCM: (audio: ArrayBuffer) => void
  onChunk?: (chunk: AudioChunk) => void | Promise<void>
  onError?: (error: AudioCaptureError) => void
  onStatusChange?: (status: AudioCaptureStatus) => void
  /**
   * Which live input(s) to capture. Defaults to microphone-only.
   * System/mixed modes require the user to share a tab/window with audio.
   */
  audioSource?: AudioCaptureSource
  workletURL?: string
  sampleRate?: number
  batchMilliseconds?: number
  mediaChunkMilliseconds?: number
  maxPendingChunks?: number
  maxPendingPcmBytes?: number
  chunkWriteTimeoutMilliseconds?: number
  stopTimeoutMilliseconds?: number
  interruptionGraceMilliseconds?: number
}

export function normalizeAudioCaptureSource(
  value: unknown,
): AudioCaptureSource {
  if (value === 'system' || value === 'mixed' || value === 'microphone') {
    return value
  }
  return 'microphone'
}

/**
 * Probe the rate the browser will actually run an AudioContext at. Speechmatics
 * and the capture graph must agree; a silent mismatch drifts the transcript
 * timeline and feels like growing / random lag.
 */
export async function probePreferredAudioSampleRate(
  requested = 48_000,
): Promise<number> {
  const fallback = boundedInteger(requested, 48_000, 8_000)
  if (typeof AudioContext === 'undefined') return fallback
  let context: AudioContext | null = null
  try {
    context = new AudioContext({
      latencyHint: 'interactive',
      sampleRate: fallback,
    })
    const actual = context.sampleRate
    return Number.isFinite(actual) && actual >= 8_000
      ? Math.floor(actual)
      : fallback
  } catch {
    return fallback
  } finally {
    if (context && context.state !== 'closed') {
      await context.close().catch(() => undefined)
    }
  }
}

function boundedInteger(
  value: number | undefined,
  fallback: number,
  minimum: number,
): number {
  if (value === undefined || !Number.isFinite(value)) return fallback
  return Math.max(minimum, Math.floor(value))
}

function stopTrackQuietly(track: MediaStreamTrack): void {
  try {
    track.stop()
  } catch {
    // Best-effort release.
  }
}

/**
 * Prefer the lowest latency the engine will grant. Display-media tracks often
 * ignore this; mic tracks on Chromium usually honor it.
 */
async function preferLowLatencyTrack(track: MediaStreamTrack): Promise<void> {
  if (typeof track.applyConstraints !== 'function') return
  try {
    await track.applyConstraints({
      // Not in the TypeScript lib for every engine, but Chromium accepts it.
      latency: 0.01,
    } as MediaTrackConstraints)
  } catch {
    // Constraint unsupported — keep the track as-is.
  }
}

export class BrowserAudioCapture {
  private readonly options: Required<Pick<
    BrowserAudioCaptureOptions,
    | 'workletURL'
    | 'sampleRate'
    | 'batchMilliseconds'
    | 'mediaChunkMilliseconds'
    | 'maxPendingChunks'
    | 'maxPendingPcmBytes'
    | 'chunkWriteTimeoutMilliseconds'
    | 'stopTimeoutMilliseconds'
    | 'interruptionGraceMilliseconds'
  >> & BrowserAudioCaptureOptions

  private statusValue: AudioCaptureStatus = 'idle'
  private streams: MediaStream[] = []
  private context: AudioContext | null = null
  private sources: MediaStreamAudioSourceNode[] = []
  private mixer: GainNode | null = null
  private worklet: AudioWorkletNode | null = null
  private silentGain: GainNode | null = null
  private encoder: Mp3ChunkEncoder | null = null
  private wakeLock: WakeLockSentinel | null = null
  private chunkSequence = 0
  private activeSampleRate: number
  private visibilityHandler: (() => void) | null = null
  private captureLifecycleCleanup: (() => void) | null = null
  private contextRecoveryTimer: ReturnType<typeof globalThis.setTimeout> | null = null
  private contextResumePromise: Promise<void> | null = null
  private trackMuteTimer: ReturnType<typeof globalThis.setTimeout> | null = null
  private readonly chunkQueue: AudioChunk[] = []
  private chunkWritePromise: Promise<void> | null = null
  private chunkWriteFailure: AudioCaptureError | null = null
  private activeChunkWriteCancel: ((error: AudioCaptureError) => void) | null = null
  private chunkPipelineGeneration = 0
  private readonly reportedIssueCodes = new Set<AudioCaptureErrorCode>()
  private pcmFlushResolve: (() => void) | null = null
  private lifecycleGeneration = 0
  private startPromise: Promise<void> | null = null
  private stopPromise: Promise<void> | null = null
  private pendingStartCleanup: (() => Promise<void>) | null = null

  constructor(options: BrowserAudioCaptureOptions) {
    const sampleRate = boundedInteger(options.sampleRate, 48_000, 8_000)
    this.activeSampleRate = sampleRate
    this.options = {
      ...options,
      audioSource: normalizeAudioCaptureSource(options.audioSource),
      workletURL: options.workletURL ?? '/pcm-batched-audio-worklet.js',
      sampleRate,
      batchMilliseconds: boundedInteger(options.batchMilliseconds, 40, 10),
      mediaChunkMilliseconds: boundedInteger(
        options.mediaChunkMilliseconds,
        2_000,
        100,
      ),
      maxPendingChunks: boundedInteger(options.maxPendingChunks, 16, 1),
      maxPendingPcmBytes: boundedInteger(
        options.maxPendingPcmBytes,
        sampleRate * 4 * 10,
        1,
      ),
      chunkWriteTimeoutMilliseconds: boundedInteger(
        options.chunkWriteTimeoutMilliseconds,
        8_000,
        250,
      ),
      stopTimeoutMilliseconds: boundedInteger(
        options.stopTimeoutMilliseconds,
        15_000,
        1_000,
      ),
      interruptionGraceMilliseconds: boundedInteger(
        options.interruptionGraceMilliseconds,
        5_000,
        250,
      ),
    }
  }

  get audioSource(): AudioCaptureSource {
    return this.options.audioSource ?? 'microphone'
  }

  get status(): AudioCaptureStatus {
    return this.statusValue
  }

  get mimeType(): string {
    return 'audio/mpeg'
  }

  /** Actual AudioContext sample rate after start (may differ from the request). */
  get sampleRate(): number {
    return this.activeSampleRate
  }

  private setStatus(status: AudioCaptureStatus): void {
    this.statusValue = status
    try {
      this.options.onStatusChange?.(status)
    } catch {
      // UI callbacks must not make capture cleanup indeterminate.
    }
  }

  private reportIssue(error: AudioCaptureError): void {
    if (this.reportedIssueCodes.has(error.code)) return
    this.reportedIssueCodes.add(error.code)
    try {
      this.options.onError?.(error)
    } catch {
      // Capture must keep its own lifecycle deterministic even if UI code fails.
    }
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
        void this.resumeAudioContext()
      }
    }
    document.addEventListener('visibilitychange', this.visibilityHandler)
  }

  private liveAudioTracks(): MediaStreamTrack[] {
    const tracks: MediaStreamTrack[] = []
    for (const stream of this.streams) {
      try {
        for (const track of stream.getAudioTracks?.() ?? stream.getTracks()) {
          if (track.readyState !== 'ended') tracks.push(track)
        }
      } catch {
        // Stream may already be released during teardown.
      }
    }
    return tracks
  }

  private installCaptureLifecycleHandlers(
    streams: MediaStream[],
    context: AudioContext,
  ): void {
    const tracks: MediaStreamTrack[] = []
    for (const stream of streams) {
      tracks.push(...(stream.getAudioTracks?.() ?? stream.getTracks()))
    }
    const cleanups: Array<() => void> = []
    const sourceLabel = this.audioSource === 'microphone'
      ? 'Microphone'
      : this.audioSource === 'system'
        ? 'System audio'
        : 'Audio input'

    for (const track of tracks) {
      const handleEnded = () => {
        if (this.statusValue !== 'recording' && this.statusValue !== 'paused') return
        // Mixed capture can lose one stream (e.g. user stops sharing) while the
        // other remains live; only fail hard when nothing is left to send.
        if (this.liveAudioTracks().length > 0) return
        this.reportIssue(new AudioCaptureError(
          'microphone-ended',
          `${sourceLabel} ended or was disconnected`,
          { recoverable: false },
        ))
      }
      const handleMute = () => {
        if (this.trackMuteTimer !== null) globalThis.clearTimeout(this.trackMuteTimer)
        this.trackMuteTimer = globalThis.setTimeout(() => {
          this.trackMuteTimer = null
          if (
            track.muted
            && (this.statusValue === 'recording' || this.statusValue === 'paused')
          ) {
            // Only warn when every remaining live track is muted.
            const live = this.liveAudioTracks()
            if (live.length > 0 && live.every((item) => item.muted)) {
              this.reportIssue(new AudioCaptureError(
                'microphone-muted',
                `${sourceLabel} has been interrupted`,
                { recoverable: true },
              ))
            }
          }
        }, this.options.interruptionGraceMilliseconds)
      }
      const handleUnmute = () => {
        if (this.trackMuteTimer !== null) {
          globalThis.clearTimeout(this.trackMuteTimer)
          this.trackMuteTimer = null
        }
        this.reportedIssueCodes.delete('microphone-muted')
      }
      track.addEventListener('ended', handleEnded)
      track.addEventListener('mute', handleMute)
      track.addEventListener('unmute', handleUnmute)
      cleanups.push(() => {
        track.removeEventListener('ended', handleEnded)
        track.removeEventListener('mute', handleMute)
        track.removeEventListener('unmute', handleUnmute)
      })
    }

    const handleContextState = () => {
      const state = String(context.state)
      if (state === 'running') {
        if (this.contextRecoveryTimer !== null) {
          globalThis.clearTimeout(this.contextRecoveryTimer)
          this.contextRecoveryTimer = null
        }
        this.reportedIssueCodes.delete('audio-context-interrupted')
        return
      }
      if (
        state === 'closed'
        || (this.statusValue !== 'recording' && this.statusValue !== 'paused')
      ) {
        return
      }
      if (!document.hidden) void this.resumeAudioContext()
      this.scheduleContextRecoveryCheck(context)
    }
    context.addEventListener('statechange', handleContextState)
    cleanups.push(() => context.removeEventListener('statechange', handleContextState))

    this.captureLifecycleCleanup = () => {
      for (const cleanup of cleanups) cleanup()
      if (this.trackMuteTimer !== null) {
        globalThis.clearTimeout(this.trackMuteTimer)
        this.trackMuteTimer = null
      }
      if (this.contextRecoveryTimer !== null) {
        globalThis.clearTimeout(this.contextRecoveryTimer)
        this.contextRecoveryTimer = null
      }
    }
  }

  private scheduleContextRecoveryCheck(context: AudioContext): void {
    if (this.contextRecoveryTimer !== null) return
    this.contextRecoveryTimer = globalThis.setTimeout(() => {
      this.contextRecoveryTimer = null
      if (
        context !== this.context
        || String(context.state) === 'running'
        || (this.statusValue !== 'recording' && this.statusValue !== 'paused')
      ) {
        return
      }
      this.reportIssue(new AudioCaptureError(
        'audio-context-interrupted',
        'Browser audio processing is suspended',
        { recoverable: true },
      ))
    }, this.options.interruptionGraceMilliseconds)
  }

  private resumeAudioContext(): Promise<void> {
    const context = this.context
    if (
      !context
      || String(context.state) === 'running'
      || String(context.state) === 'closed'
      || document.hidden
    ) {
      return Promise.resolve()
    }
    if (this.contextResumePromise) return this.contextResumePromise
    const operation = context.resume()
      .then(() => {
        if (context !== this.context) return
        if (String(context.state) === 'running') {
          this.reportedIssueCodes.delete('audio-context-interrupted')
          if (this.contextRecoveryTimer !== null) {
            globalThis.clearTimeout(this.contextRecoveryTimer)
            this.contextRecoveryTimer = null
          }
        } else {
          this.scheduleContextRecoveryCheck(context)
        }
      })
      .catch((reason: unknown) => {
        if (context !== this.context) return
        this.scheduleContextRecoveryCheck(context)
        if (
          !document.hidden
          && (this.statusValue === 'recording' || this.statusValue === 'paused')
        ) {
          this.reportIssue(new AudioCaptureError(
            'audio-context-interrupted',
            'Browser audio processing could not resume',
            { recoverable: true, reason },
          ))
        }
      })
    const tracked = operation.finally(() => {
      if (this.contextResumePromise === tracked) this.contextResumePromise = null
    })
    this.contextResumePromise = tracked
    return tracked
  }

  start(): Promise<void> {
    if (this.statusValue !== 'idle') {
      return Promise.reject(
        new Error(`Audio capture cannot start while ${this.statusValue}`),
      )
    }
    const source = this.audioSource
    if (
      (source === 'microphone' || source === 'mixed')
      && !navigator.mediaDevices?.getUserMedia
    ) {
      return Promise.reject(
        new Error('This browser does not support microphone capture'),
      )
    }
    if (
      (source === 'system' || source === 'mixed')
      && !navigator.mediaDevices?.getDisplayMedia
    ) {
      return Promise.reject(
        new Error('This browser does not support system audio capture'),
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
    this.chunkQueue.length = 0
    this.chunkWritePromise = null
    this.chunkWriteFailure = null
    this.activeChunkWriteCancel = null
    this.chunkPipelineGeneration += 1
    this.reportedIssueCodes.clear()
    const operation = this.performStart(generation)
    const tracked = operation.finally(() => {
      if (this.startPromise === tracked) this.startPromise = null
    })
    this.startPromise = tracked
    return tracked
  }

  private async acquireInputStreams(generation: number): Promise<MediaStream[]> {
    const source = this.audioSource
    const streams: MediaStream[] = []
    const release = () => {
      for (const stream of streams) {
        try {
          stream.getTracks().forEach((track) => track.stop())
        } catch {
          // Best-effort release after a partial acquire failure.
        }
      }
      streams.length = 0
    }

    try {
      if (source === 'microphone' || source === 'mixed') {
        const micStream = await navigator.mediaDevices.getUserMedia({
          audio: {
            echoCancellation: true,
            noiseSuppression: true,
            autoGainControl: true,
            channelCount: 1,
            sampleRate: this.options.sampleRate,
            // Chromium: ask the capture stack for a short buffer.
            latency: 0.01,
          } as MediaTrackConstraints,
        })
        // Register before any await so cancel/cleanup always releases the track.
        streams.push(micStream)
        this.assertCurrentStart(generation)
        const micTracks = micStream.getAudioTracks?.() ?? micStream.getTracks()
        await Promise.all(micTracks.map(preferLowLatencyTrack))
        this.assertCurrentStart(generation)
      }

      if (source === 'system' || source === 'mixed') {
        // Browsers require a video track for getDisplayMedia. Request a tiny
        // video surface so the capture pipeline is not clocked like a full HD
        // screen share (a common source of variable system-audio latency),
        // then drop video immediately and keep only audio.
        const displayConstraints = {
          video: {
            width: { ideal: 320, max: 640 },
            height: { ideal: 180, max: 360 },
            frameRate: { ideal: 1, max: 5 },
          },
          audio: {
            echoCancellation: false,
            noiseSuppression: false,
            autoGainControl: false,
            // Keep stereo; the worklet downmixes to mono so off-center speech
            // is not dropped.
            channelCount: 2,
            latency: 0.01,
          },
          // Chromium: include OS-level loopback when the user picks a screen.
          systemAudio: 'include',
          preferCurrentTab: false,
        } as DisplayMediaStreamOptions
        const displayStream = await navigator.mediaDevices.getDisplayMedia(
          displayConstraints,
        )
        streams.push(displayStream)
        this.assertCurrentStart(generation)

        for (const track of displayStream.getVideoTracks?.() ?? []) {
          stopTrackQuietly(track)
          try {
            displayStream.removeTrack(track)
          } catch {
            // Some engines remove ended tracks automatically.
          }
        }

        const systemTracks = displayStream.getAudioTracks?.()
          ?? displayStream.getTracks()
        if (systemTracks.length === 0) {
          throw new Error(
            messages().common.errors.audioShare,
          )
        }
        await Promise.all(systemTracks.map(preferLowLatencyTrack))
        this.assertCurrentStart(generation)
      }

      if (streams.length === 0) {
        throw new Error('No audio input stream was acquired')
      }
      return streams
    } catch (error) {
      release()
      throw error
    }
  }

  private async performStart(generation: number): Promise<void> {
    const resources: {
      streams: MediaStream[]
      context: AudioContext | null
      sources: MediaStreamAudioSourceNode[]
      mixer: GainNode | null
      worklet: AudioWorkletNode | null
      silentGain: GainNode | null
      encoder: Mp3ChunkEncoder | null
    } = {
      streams: [],
      context: null,
      sources: [],
      mixer: null,
      worklet: null,
      silentGain: null,
      encoder: null,
    }
    const cleanup = async () => {
      const encoder = resources.encoder
      const sources = resources.sources
      const mixer = resources.mixer
      const worklet = resources.worklet
      const silentGain = resources.silentGain
      const streams = resources.streams
      const context = resources.context
      resources.encoder = null
      resources.sources = []
      resources.mixer = null
      resources.worklet = null
      resources.silentGain = null
      resources.streams = []
      resources.context = null
      encoder?.destroy()
      for (const source of sources) {
        try {
          source.disconnect()
        } catch {
          // Best-effort teardown for browser resources.
        }
      }
      try {
        mixer?.disconnect()
      } catch {
        // Best-effort teardown for browser resources.
      }
      try {
        worklet?.disconnect()
      } catch {
        // Best-effort teardown for browser resources.
      }
      try {
        silentGain?.disconnect()
      } catch {
        // Best-effort teardown for browser resources.
      }
      for (const stream of streams) {
        try {
          stream.getTracks().forEach((track) => track.stop())
        } catch {
          // Best-effort teardown for browser resources.
        }
      }
      if (context && context.state !== 'closed') {
        await context.close().catch(() => undefined)
      }
    }
    this.pendingStartCleanup = cleanup

    try {
      resources.streams = await this.acquireInputStreams(generation)
      this.assertCurrentStart(generation)

      // interactive: smaller render quantum / lower baseLatency than "balanced".
      resources.context = new AudioContext({
        latencyHint: 'interactive',
        sampleRate: this.options.sampleRate,
      })
      const actualSampleRate = Number.isFinite(resources.context.sampleRate)
        && resources.context.sampleRate >= 8_000
        ? Math.floor(resources.context.sampleRate)
        : this.options.sampleRate
      this.activeSampleRate = actualSampleRate
      if (resources.context.state !== 'running') await resources.context.resume()
      this.assertCurrentStart(generation)
      await resources.context.audioWorklet.addModule(this.options.workletURL)
      this.assertCurrentStart(generation)

      const batchFrames = Math.max(
        128,
        Math.round(actualSampleRate * this.options.batchMilliseconds / 1_000),
      )
      // Accept stereo from system capture; the worklet downmixes to mono PCM.
      resources.worklet = new AudioWorkletNode(
        resources.context,
        'dreamtrans-pcm-batched-processor',
        {
          numberOfInputs: 1,
          numberOfOutputs: 1,
          channelCount: 2,
          channelCountMode: 'explicit',
          channelInterpretation: 'speakers',
          processorOptions: { batchFrames },
        },
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
          // Live transcription first: local MP3 is best-effort and must not
          // add main-thread work ahead of the WebSocket send path.
          const pcm = event.data
          try {
            this.options.onPCM(pcm)
          } finally {
            // sendAudio copies bytes synchronously; slice after so a thrown
            // network callback cannot skip local recording either.
            resources.encoder?.encode(pcm.slice(0))
          }
        }
      }
      // Sum mic + system into one node before the worklet so channel counts and
      // start-of-stream timing do not depend on multi-producer fan-in quirks.
      resources.mixer = resources.context.createGain()
      resources.mixer.gain.value = 1
      resources.silentGain = resources.context.createGain()
      resources.silentGain.gain.value = 0
      for (const stream of resources.streams) {
        const sourceNode = resources.context.createMediaStreamSource(stream)
        sourceNode.connect(resources.mixer)
        resources.sources.push(sourceNode)
      }
      resources.mixer.connect(resources.worklet)
      resources.worklet.connect(resources.silentGain)
      resources.silentGain.connect(resources.context.destination)

      if (this.options.onChunk) {
        resources.encoder = new Mp3ChunkEncoder({
          sampleRate: actualSampleRate,
          chunkMilliseconds: this.options.mediaChunkMilliseconds,
          maxPendingPcmBytes: this.options.maxPendingPcmBytes,
          onChunk: (blob) => {
            if (blob.size === 0 || !this.options.onChunk) return
            this.enqueueChunk({
              sequence: this.chunkSequence++,
              recordedAt: Date.now(),
              blob,
            }, resources.encoder)
          },
          onError: (reason) => {
            const error = new AudioCaptureError(
              'audio-encoder-failed',
              `Local audio encoder failed: ${reason.message}`,
              { recoverable: false, reason },
            )
            this.failChunkWrites(error, resources.encoder)
          },
        })
      }

      this.assertCurrentStart(generation)
      this.streams = resources.streams
      this.context = resources.context
      this.sources = resources.sources
      this.mixer = resources.mixer
      this.worklet = resources.worklet
      this.silentGain = resources.silentGain
      this.encoder = resources.encoder
      if (this.pendingStartCleanup === cleanup) this.pendingStartCleanup = null
      this.installCaptureLifecycleHandlers(resources.streams, resources.context)
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

  private enqueueChunk(chunk: AudioChunk, encoder: Mp3ChunkEncoder | null): void {
    if (this.chunkWriteFailure) return
    const pendingCount = this.chunkQueue.length + (this.chunkWritePromise ? 1 : 0)
    if (pendingCount >= this.options.maxPendingChunks) {
      const error = new AudioCaptureError(
        'audio-storage-backpressure',
        `Local audio storage fell more than ${this.options.maxPendingChunks} chunks behind`,
        { recoverable: false },
      )
      this.failChunkWrites(error, encoder)
      return
    }
    this.chunkQueue.push(chunk)
    this.startChunkWriter()
  }

  private startChunkWriter(): void {
    if (this.chunkWritePromise || this.chunkWriteFailure || this.chunkQueue.length === 0) return
    const generation = this.chunkPipelineGeneration
    const operation = this.processChunkQueue(generation)
    const tracked = operation.finally(() => {
      if (this.chunkWritePromise === tracked) this.chunkWritePromise = null
      if (
        generation === this.chunkPipelineGeneration
        && !this.chunkWriteFailure
        && this.chunkQueue.length > 0
      ) {
        this.startChunkWriter()
      }
    })
    // Attach a rejection handler immediately; flush/stop still observe the
    // persistent chunkWriteFailure rather than producing an unhandled promise.
    void tracked.catch(() => undefined)
    this.chunkWritePromise = tracked
  }

  private async processChunkQueue(generation: number): Promise<void> {
    while (
      generation === this.chunkPipelineGeneration
      && this.chunkQueue.length > 0
      && !this.chunkWriteFailure
    ) {
      const chunk = this.chunkQueue.shift()
      if (!chunk) continue
      try {
        await this.writeChunkWithTimeout(chunk)
      } catch (reason) {
        if (generation !== this.chunkPipelineGeneration) return
        const error = reason instanceof AudioCaptureError
          ? reason
          : new AudioCaptureError(
              'audio-storage-write-failed',
              `Local audio chunk could not be saved: ${
                reason instanceof Error ? reason.message : String(reason)
              }`,
              { recoverable: false, reason },
            )
        this.failChunkWrites(error, this.encoder)
        throw error
      }
    }
  }

  private writeChunkWithTimeout(chunk: AudioChunk): Promise<void> {
    const write = this.options.onChunk
    if (!write) return Promise.resolve()

    const operation = Promise.resolve().then(() => write(chunk))
    // The underlying IndexedDB promise cannot be cancelled. Keep its eventual
    // rejection observed after our bounded wrapper has already failed.
    void operation.catch(() => undefined)

    return new Promise<void>((resolve, reject) => {
      let settled = false
      const beginSettlement = () => {
        if (settled) return false
        settled = true
        globalThis.clearTimeout(timeout)
        if (this.activeChunkWriteCancel === cancel) {
          this.activeChunkWriteCancel = null
        }
        return true
      }
      const succeed = () => {
        if (beginSettlement()) resolve()
      }
      const fail = (reason: unknown) => {
        if (beginSettlement()) reject(reason)
      }
      const cancel = (error: AudioCaptureError) => fail(error)
      const timeout = globalThis.setTimeout(() => {
        cancel(new AudioCaptureError(
          'audio-storage-write-failed',
          `Local audio chunk save timed out after ${
            this.options.chunkWriteTimeoutMilliseconds
          } ms`,
          { recoverable: false },
        ))
      }, this.options.chunkWriteTimeoutMilliseconds)
      this.activeChunkWriteCancel = cancel
      operation.then(
        succeed,
        fail,
      )
    })
  }

  private failChunkWrites(
    error: AudioCaptureError,
    encoder: Mp3ChunkEncoder | null,
  ): void {
    if (this.chunkWriteFailure) return
    this.chunkWriteFailure = error
    this.chunkPipelineGeneration += 1
    this.chunkQueue.length = 0
    const cancelActiveWrite = this.activeChunkWriteCancel
    this.activeChunkWriteCancel = null
    encoder?.destroy()
    cancelActiveWrite?.(error)
    this.reportIssue(error)
  }

  private async awaitChunkWrites(): Promise<void> {
    while (this.chunkWritePromise) {
      await this.chunkWritePromise.catch(() => undefined)
    }
    if (this.chunkWriteFailure) throw this.chunkWriteFailure
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
    if (this.chunkWriteFailure) throw this.chunkWriteFailure
    await this.encoder?.drain()
    await this.awaitChunkWrites()
  }

  stop(): Promise<void> {
    if (this.statusValue === 'idle') return Promise.resolve()
    if (this.stopPromise) return this.stopPromise
    const generation = ++this.lifecycleGeneration
    this.setStatus('stopping')
    const operation = this.withStopDeadline(
      this.performStop(generation),
      generation,
    )
    const tracked = operation.finally(() => {
      if (this.stopPromise === tracked) this.stopPromise = null
    })
    this.stopPromise = tracked
    return tracked
  }

  private withStopDeadline(
    operation: Promise<void>,
    generation: number,
  ): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      let settled = false
      const beginSettlement = () => {
        if (settled) return false
        settled = true
        globalThis.clearTimeout(timeout)
        return true
      }
      const succeed = () => {
        if (beginSettlement()) resolve()
      }
      const fail = (reason: unknown) => {
        if (beginSettlement()) reject(reason)
      }
      const timeout = globalThis.setTimeout(() => {
        const error = new AudioCaptureError(
          'audio-encoder-failed',
          `Local audio capture could not stop within ${
            this.options.stopTimeoutMilliseconds
          } ms`,
          { recoverable: false },
        )
        if (generation === this.lifecycleGeneration) {
          this.lifecycleGeneration += 1
          this.failChunkWrites(error, this.encoder)
          void this.disposeResources(true).catch(() => undefined)
          this.setStatus('idle')
        }
        fail(error)
      }, this.options.stopTimeoutMilliseconds)
      operation.then(
        succeed,
        fail,
      )
    })
  }

  private async performStop(generation: number): Promise<void> {
    const pendingStartCleanup = this.pendingStartCleanup
    if (pendingStartCleanup) {
      this.pendingStartCleanup = null
      await pendingStartCleanup()
    }
    if (generation !== this.lifecycleGeneration) return
    await this.flushPCM()
    if (generation !== this.lifecycleGeneration) return
    let encoderFailure: unknown
    try {
      if (this.chunkWriteFailure) throw this.chunkWriteFailure
      await this.encoder?.flush()
      if (generation !== this.lifecycleGeneration) return
      await this.awaitChunkWrites()
    } catch (reason) {
      encoderFailure = this.chunkWriteFailure ?? reason
    } finally {
      if (generation === this.lifecycleGeneration) {
        await this.disposeResources()
        if (generation === this.lifecycleGeneration) this.setStatus('idle')
      }
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

  private async disposeResources(force = false): Promise<void> {
    const visibilityHandler = this.visibilityHandler
    const captureLifecycleCleanup = this.captureLifecycleCleanup
    const worklet = this.worklet
    const sources = this.sources
    const mixer = this.mixer
    const silentGain = this.silentGain
    const streams = this.streams
    const context = this.context
    const wakeLock = this.wakeLock
    const encoder = this.encoder

    this.visibilityHandler = null
    this.captureLifecycleCleanup = null
    this.contextResumePromise = null
    this.encoder = null
    this.worklet = null
    this.silentGain = null
    this.mixer = null
    this.sources = []
    this.context = null
    this.streams = []
    this.wakeLock = null
    this.chunkQueue.length = 0
    const resolvePCMFlush = this.pcmFlushResolve
    this.pcmFlushResolve = null

    if (visibilityHandler) {
      try {
        document.removeEventListener('visibilitychange', visibilityHandler)
      } catch {
        // Best-effort teardown for browser resources.
      }
    }
    try {
      captureLifecycleCleanup?.()
    } catch {
      // Best-effort teardown for browser resources.
    }
    try {
      worklet?.port.postMessage('stop')
    } catch {
      // Best-effort teardown for browser resources.
    }
    resolvePCMFlush?.()
    try {
      if (worklet) worklet.port.onmessage = null
    } catch {
      // Best-effort teardown for browser resources.
    }
    for (const source of sources) {
      try {
        source.disconnect()
      } catch {
        // Best-effort teardown for browser resources.
      }
    }
    try {
      mixer?.disconnect()
    } catch {
      // Best-effort teardown for browser resources.
    }
    try {
      worklet?.disconnect()
    } catch {
      // Best-effort teardown for browser resources.
    }
    try {
      silentGain?.disconnect()
    } catch {
      // Best-effort teardown for browser resources.
    }
    const tracks: MediaStreamTrack[] = []
    for (const stream of streams) {
      try {
        tracks.push(...(stream.getTracks() ?? []))
      } catch {
        // Best-effort teardown for browser resources.
      }
    }
    for (const track of tracks) {
      try {
        track.stop()
      } catch {
        // Best-effort teardown for browser resources.
      }
    }
    try {
      encoder?.destroy()
    } catch {
      // Best-effort teardown for browser resources.
    }

    const releases: Promise<unknown>[] = []
    let contextIsOpen = false
    try {
      contextIsOpen = Boolean(context && context.state !== 'closed')
    } catch {
      // Best-effort teardown for browser resources.
    }
    if (context && contextIsOpen) {
      try {
        releases.push(context.close())
      } catch {
        // Best-effort teardown for browser resources.
      }
    }
    if (wakeLock) {
      try {
        releases.push(wakeLock.release())
      } catch {
        // Best-effort teardown for browser resources.
      }
    }
    if (force) {
      for (const release of releases) void release.catch(() => undefined)
      return
    }
    await Promise.all(releases.map((release) => release.catch(() => undefined)))
  }
}
