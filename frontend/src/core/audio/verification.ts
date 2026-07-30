/**
 * Dependency-free lifecycle regression for BrowserAudioCapture.
 *
 * Run from frontend/:
 *   esbuild src/core/audio/verification.ts --bundle --platform=node \
 *     --format=esm --outfile=/tmp/dreamtrans-audio-verify.mjs
 *   node /tmp/dreamtrans-audio-verify.mjs
 */
import { BrowserAudioCapture } from './BrowserAudioCapture'

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
