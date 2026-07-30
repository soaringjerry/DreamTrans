/**
 * Dependency-free lifecycle regression for BrowserAudioCapture.
 *
 * Run from frontend/:
 *   esbuild src/core/audio/verification.ts --bundle --platform=node \
 *     --format=esm --outfile=/tmp/dreamtrans-audio-verify.mjs
 *   node /tmp/dreamtrans-audio-verify.mjs
 */
import { BrowserAudioCapture } from './BrowserAudioCapture'
import { Mp3ChunkEncoder } from './Mp3ChunkEncoder'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(`Audio capture verification failed: ${message}`)
}

let releaseMicrophone!: (stream: MediaStream) => void
const microphone = new Promise<MediaStream>((resolve) => {
  releaseMicrophone = resolve
})
let trackStopped = false
let audioContextCreated = false

const fakeStream = {
  getTracks: () => [{
    stop: () => {
      trackStopped = true
    },
  }],
} as unknown as MediaStream

Object.defineProperty(globalThis, 'navigator', {
  configurable: true,
  value: {
    mediaDevices: {
      getUserMedia: () => microphone,
    },
  },
})
Object.defineProperty(globalThis, 'AudioWorkletNode', {
  configurable: true,
  value: class {},
})
Object.defineProperty(globalThis, 'AudioContext', {
  configurable: true,
  value: class {
    constructor() {
      audioContextCreated = true
    }
  },
})

const capture = new BrowserAudioCapture({
  onPCM: () => {
    throw new Error('cancelled capture must not emit PCM')
  },
})
const startWasCancelled = capture.start().then(
  () => false,
  () => true,
)
const stopPromise = capture.stop()
const stopResolvedWithoutPermission = await Promise.race([
  stopPromise.then(() => true),
  new Promise<boolean>((resolve) => {
    globalThis.setTimeout(() => resolve(false), 100)
  }),
])
assert(stopResolvedWithoutPermission, 'stop must not wait for a pending permission prompt')
assert(capture.status === 'idle', 'bounded stop must leave capture idle')
releaseMicrophone(fakeStream)

assert(await startWasCancelled, 'a stopped pending start must reject')
assert(trackStopped, 'a late microphone stream must be released')
assert(!audioContextCreated, 'cancelled start must not create an audio context')

class FakeEventTarget {
  private readonly listeners = new Map<string, Set<() => void>>()

  addEventListener(type: string, listener: EventListenerOrEventListenerObject): void {
    const callback = typeof listener === 'function'
      ? listener as () => void
      : () => listener.handleEvent(new Event(type))
    let listeners = this.listeners.get(type)
    if (!listeners) {
      listeners = new Set()
      this.listeners.set(type, listeners)
    }
    listeners.add(callback)
  }

  removeEventListener(type: string, listener: EventListenerOrEventListenerObject): void {
    if (typeof listener !== 'function') return
    this.listeners.get(type)?.delete(listener as () => void)
  }

  emit(type: string): void {
    for (const listener of [...(this.listeners.get(type) ?? [])]) listener()
  }
}

class FakeTrack extends FakeEventTarget {
  muted = false
  stopped = false

  stop(): void {
    this.stopped = true
  }
}

class FakeWorkletPort {
  onmessage: ((event: MessageEvent<ArrayBuffer | string>) => void) | null = null

  postMessage(message: string): void {
    if (message === 'flush') {
      queueMicrotask(() => this.onmessage?.({ data: 'flushed' } as MessageEvent<string>))
    }
  }

  emitPCM(buffer: ArrayBuffer): void {
    this.onmessage?.({ data: buffer } as MessageEvent<ArrayBuffer>)
  }
}

class FakeAudioWorkletNode {
  static readonly instances: FakeAudioWorkletNode[] = []
  readonly port = new FakeWorkletPort()

  constructor() {
    FakeAudioWorkletNode.instances.push(this)
  }

  connect(): void {}
  disconnect(): void {}
}

class FakeAudioContext extends FakeEventTarget {
  readonly audioWorklet = { addModule: async () => undefined }
  readonly destination = {}
  state = 'running'
  resumeCalls = 0

  createGain() {
    return {
      connect: () => undefined,
      disconnect: () => undefined,
      gain: { value: 1 },
    }
  }

  createMediaStreamSource() {
    return {
      connect: () => undefined,
      disconnect: () => undefined,
    }
  }

  async close(): Promise<void> {
    this.state = 'closed'
  }

  async resume(): Promise<void> {
    this.resumeCalls += 1
    this.state = 'running'
    this.emit('statechange')
  }
}

type EncoderResponse =
  | { type: 'chunk'; buffer: ArrayBuffer }
  | { type: 'encoded'; byteLength: number }
  | { type: 'drained' }
  | { type: 'flushed' }
  | { type: 'error'; message: string }

class FakeEncoderWorker {
  onerror: ((event: { message?: string }) => void) | null = null
  onmessage: ((event: MessageEvent<EncoderResponse>) => void) | null = null
  onmessageerror: (() => void) | null = null
  terminated = false
  acknowledgeEncodes = true
  readonly encodedPcmBytes: number[] = []

  postMessage(message: { type?: string; buffer?: ArrayBuffer }): void {
    if (message.type === 'encode' && message.buffer) {
      const byteLength = message.buffer.byteLength
      this.encodedPcmBytes.push(byteLength)
      if (this.acknowledgeEncodes) {
        queueMicrotask(() => this.emit({ type: 'encoded', byteLength }))
      }
    } else if (message.type === 'drain') {
      queueMicrotask(() => this.emit({ type: 'drained' }))
    } else if (message.type === 'flush') {
      queueMicrotask(() => this.emit({ type: 'flushed' }))
    }
  }

  terminate(): void {
    this.terminated = true
  }

  emitChunk(): void {
    this.emit({ type: 'chunk', buffer: new Uint8Array([1, 2, 3]).buffer })
  }

  fail(message: string): void {
    this.onerror?.({ message })
  }

  private emit(response: EncoderResponse): void {
    if (this.terminated) return
    this.onmessage?.({ data: response } as MessageEvent<EncoderResponse>)
  }
}

const encoderWorker = new FakeEncoderWorker()
const encoderErrors: Error[] = []
const encoder = new Mp3ChunkEncoder({
  chunkMilliseconds: 2_000,
  onChunk: () => undefined,
  onError: (error) => encoderErrors.push(error),
  sampleRate: 48_000,
  workerFactory: () => encoderWorker as unknown as Worker,
})
encoderWorker.fail('worker exploded')
assert(encoderErrors.length === 1, 'worker failure must be reported immediately')
assert(encoderWorker.terminated, 'failed encoder worker must be terminated')
assert(
  await encoder.drain().then(() => false, () => true),
  'controls must reject with a previously reported worker failure',
)

const stalledEncoderWorker = new FakeEncoderWorker()
stalledEncoderWorker.acknowledgeEncodes = false
const stalledEncoderErrors: Error[] = []
const stalledEncoder = new Mp3ChunkEncoder({
  chunkMilliseconds: 2_000,
  maxPendingPcmBytes: 8,
  onChunk: () => undefined,
  onError: (error) => stalledEncoderErrors.push(error),
  sampleRate: 48_000,
  workerFactory: () => stalledEncoderWorker as unknown as Worker,
})
stalledEncoder.encode(new ArrayBuffer(8))
stalledEncoder.encode(new ArrayBuffer(1))
assert(
  stalledEncoderErrors.length === 1,
  'an unresponsive worker must fail immediately at the pending PCM byte bound',
)
assert(
  stalledEncoderWorker.terminated,
  'pending PCM overflow must terminate the unresponsive worker',
)
assert(
  await stalledEncoder.flush().then(() => false, () => true),
  'destroyed encoder controls preserve and return the original failure',
)

const acknowledgedEncoderWorker = new FakeEncoderWorker()
const acknowledgedEncoderErrors: Error[] = []
const acknowledgedEncoder = new Mp3ChunkEncoder({
  chunkMilliseconds: 2_000,
  maxPendingPcmBytes: 8,
  onChunk: () => undefined,
  onError: (error) => acknowledgedEncoderErrors.push(error),
  sampleRate: 48_000,
  workerFactory: () => acknowledgedEncoderWorker as unknown as Worker,
})
acknowledgedEncoder.encode(new ArrayBuffer(8))
await Promise.resolve()
acknowledgedEncoder.encode(new ArrayBuffer(8))
assert(
  acknowledgedEncoderErrors.length === 0,
  'per-buffer acknowledgements release pending PCM capacity',
)
await acknowledgedEncoder.flush()

const manuallyDestroyedWorker = new FakeEncoderWorker()
const manuallyDestroyedEncoder = new Mp3ChunkEncoder({
  chunkMilliseconds: 2_000,
  onChunk: () => undefined,
  sampleRate: 48_000,
  workerFactory: () => manuallyDestroyedWorker as unknown as Worker,
})
manuallyDestroyedEncoder.destroy()
const destroyedReason = await manuallyDestroyedEncoder.drain().then(
  () => '',
  (reason: unknown) => reason instanceof Error ? reason.message : String(reason),
)
assert(
  destroyedReason === 'MP3 encoder was destroyed',
  'manual destroy must persist its failure for later control requests',
)

const documentEvents = new FakeEventTarget()
Object.defineProperty(globalThis, 'document', {
  configurable: true,
  value: {
    hidden: false,
    addEventListener: documentEvents.addEventListener.bind(documentEvents),
    removeEventListener: documentEvents.removeEventListener.bind(documentEvents),
  },
})
const activeTrack = new FakeTrack()
const activeStream = {
  getAudioTracks: () => [activeTrack],
  getTracks: () => [activeTrack],
} as unknown as MediaStream
Object.defineProperty(globalThis, 'navigator', {
  configurable: true,
  value: {
    mediaDevices: {
      getUserMedia: async () => activeStream,
    },
  },
})
const contexts: FakeAudioContext[] = []
Object.defineProperty(globalThis, 'AudioContext', {
  configurable: true,
  value: class extends FakeAudioContext {
    constructor() {
      super()
      contexts.push(this)
    }
  },
})
Object.defineProperty(globalThis, 'AudioWorkletNode', {
  configurable: true,
  value: FakeAudioWorkletNode,
})
const workers: FakeEncoderWorker[] = []
Object.defineProperty(globalThis, 'Worker', {
  configurable: true,
  value: class extends FakeEncoderWorker {
    constructor() {
      super()
      workers.push(this)
    }
  },
})

let releaseFirstChunk!: () => void
const firstChunkBlocked = new Promise<void>((resolve) => {
  releaseFirstChunk = resolve
})
let chunkWrites = 0
const captureIssues: string[] = []
const boundedCapture = new BrowserAudioCapture({
  maxPendingChunks: 2,
  onChunk: async () => {
    chunkWrites += 1
    if (chunkWrites === 1) await firstChunkBlocked
  },
  onError: (error) => captureIssues.push(error.code),
  onPCM: () => undefined,
})
await boundedCapture.start()
const activeWorker = workers[0]
assert(activeWorker !== undefined, 'local recording starts an encoder worker')
activeWorker.emitChunk()
await Promise.resolve()
activeWorker.emitChunk()
activeWorker.emitChunk()
assert(
  captureIssues.includes('audio-storage-backpressure'),
  'a stalled chunk writer must fail at the configured bound',
)
assert(activeWorker.terminated, 'backpressure stops further local encoding')
releaseFirstChunk()
assert(
  await boundedCapture.stop().then(() => false, () => true),
  'stop reports the persistent local-audio failure',
)

const lifecycleIssues: string[] = []
const lifecycleCapture = new BrowserAudioCapture({
  onError: (error) => lifecycleIssues.push(error.code),
  onPCM: () => undefined,
})
await lifecycleCapture.start()
const lifecycleContext = contexts[1]
assert(lifecycleContext !== undefined, 'second capture creates an audio context')
lifecycleContext.state = 'suspended'
lifecycleContext.emit('statechange')
await Promise.resolve()
assert(
  lifecycleContext.resumeCalls === 1 && lifecycleContext.state === 'running',
  'a visible suspended audio context is resumed automatically',
)
activeTrack.emit('ended')
assert(
  lifecycleIssues.includes('microphone-ended'),
  'microphone disconnection must be reported immediately',
)
await lifecycleCapture.stop()

const networkFailureCapture = new BrowserAudioCapture({
  onChunk: () => undefined,
  onPCM: () => {
    throw new Error('network send failed')
  },
})
await networkFailureCapture.start()
const networkFailureWorker = workers.at(-1)
const networkFailureWorklet = FakeAudioWorkletNode.instances.at(-1)
let networkFailurePropagated = false
try {
  networkFailureWorklet?.port.emitPCM(new ArrayBuffer(16))
} catch {
  networkFailurePropagated = true
}
assert(networkFailurePropagated, 'the fake network callback must exercise its failure path')
assert(
  networkFailureWorker?.encodedPcmBytes[0] === 16,
  'a network callback failure must not prevent the same PCM block being recorded locally',
)
await networkFailureCapture.stop()

let releaseTimedOutChunk!: () => void
const timedOutChunk = new Promise<void>((resolve) => {
  releaseTimedOutChunk = resolve
})
const timeoutIssues: string[] = []
const timeoutCapture = new BrowserAudioCapture({
  chunkWriteTimeoutMilliseconds: 250,
  onChunk: () => timedOutChunk,
  onError: (error) => timeoutIssues.push(error.code),
  onPCM: () => undefined,
})
await timeoutCapture.start()
workers.at(-1)?.emitChunk()
await new Promise((resolve) => globalThis.setTimeout(resolve, 300))
assert(
  timeoutIssues.includes('audio-storage-write-failed'),
  'one stalled local chunk write must time out and report permanent incompleteness',
)
assert(
  await timeoutCapture.stop().then(() => false, () => true),
  'stop must return the persistent chunk timeout instead of waiting forever',
)
releaseTimedOutChunk()

class HangingCloseAudioContext extends FakeAudioContext {
  close(): Promise<void> {
    return new Promise(() => undefined)
  }
}
Object.defineProperty(globalThis, 'AudioContext', {
  configurable: true,
  value: HangingCloseAudioContext,
})
const stopTimeoutIssues: string[] = []
const stopTimeoutCapture = new BrowserAudioCapture({
  onChunk: () => undefined,
  onError: (error) => stopTimeoutIssues.push(error.code),
  onPCM: () => undefined,
  stopTimeoutMilliseconds: 1_000,
})
await stopTimeoutCapture.start()
const boundedStopStartedAt = Date.now()
assert(
  await stopTimeoutCapture.stop().then(() => false, () => true),
  'a browser resource that never closes must reject at the total stop deadline',
)
assert(
  Date.now() - boundedStopStartedAt < 1_500,
  'the total stop deadline must remain bounded',
)
assert(stopTimeoutCapture.status === 'idle', 'a timed-out stop must still become idle')
assert(
  stopTimeoutIssues.includes('audio-encoder-failed'),
  'a total stop timeout must mark local audio as unhealthy',
)
