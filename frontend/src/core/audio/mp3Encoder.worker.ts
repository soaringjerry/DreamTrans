/// <reference lib="webworker" />

declare const lamejs: {
  Mp3Encoder: new (
    channels: number,
    rate: number,
    bitrate: number,
  ) => Mp3Encoder
}

interface Mp3Encoder {
  encodeBuffer(samples: Int16Array): Int8Array
  flush(): Int8Array
}

type WorkerRequest =
  | {
      type: 'init'
      sampleRate: number
      bitrateKbps: number
      chunkMilliseconds: number
      scriptUrl: string
    }
  | { type: 'encode'; buffer: ArrayBuffer }
  | { type: 'drain' }
  | { type: 'flush' }

type WorkerResponse =
  | { type: 'chunk'; buffer: ArrayBuffer }
  | { type: 'encoded'; byteLength: number }
  | { type: 'drained' }
  | { type: 'flushed' }
  | { type: 'error'; message: string }

const workerScope = self as DedicatedWorkerGlobalScope
const sampleBlockSize = 1_152
let encoder: Mp3Encoder | null = null
let sampleRate = 48_000
let chunkSamples = 96_000
let samplesSinceChunk = 0
let pendingParts: Uint8Array[] = []
let pendingBytes = 0

function appendEncoded(encoded: Int8Array): void {
  if (encoded.byteLength === 0) return
  const bytes = new Uint8Array(encoded.byteLength)
  bytes.set(new Uint8Array(encoded.buffer, encoded.byteOffset, encoded.byteLength))
  pendingParts.push(bytes)
  pendingBytes += bytes.byteLength
}

function emitChunk(): void {
  if (pendingBytes === 0) return
  const combined = new Uint8Array(pendingBytes)
  let offset = 0
  for (const part of pendingParts) {
    combined.set(part, offset)
    offset += part.byteLength
  }
  pendingParts = []
  pendingBytes = 0
  const response: WorkerResponse = { type: 'chunk', buffer: combined.buffer }
  workerScope.postMessage(response, [combined.buffer])
}

function floatPcmToInt16(buffer: ArrayBuffer): Int16Array {
  const input = new Float32Array(buffer)
  const output = new Int16Array(input.length)
  for (let index = 0; index < input.length; index += 1) {
    const sample = Math.max(-1, Math.min(1, input[index] ?? 0))
    output[index] = sample < 0
      ? Math.round(sample * 0x8000)
      : Math.round(sample * 0x7fff)
  }
  return output
}

function encode(buffer: ArrayBuffer): void {
  if (!encoder) throw new Error('MP3 encoder is not initialized')
  const samples = floatPcmToInt16(buffer)
  for (let offset = 0; offset < samples.length; offset += sampleBlockSize) {
    appendEncoded(encoder.encodeBuffer(samples.subarray(offset, offset + sampleBlockSize)))
  }
  samplesSinceChunk += samples.length
  if (samplesSinceChunk >= chunkSamples) {
    samplesSinceChunk %= chunkSamples
    emitChunk()
  }
}

workerScope.onmessage = (event: MessageEvent<WorkerRequest>) => {
  try {
    const request = event.data
    if (request.type === 'init') {
      if (typeof lamejs === 'undefined') {
        workerScope.importScripts(request.scriptUrl)
      }
      sampleRate = Math.max(8_000, Math.round(request.sampleRate))
      chunkSamples = Math.max(
        sampleBlockSize,
        Math.round(sampleRate * request.chunkMilliseconds / 1_000),
      )
      encoder = new lamejs.Mp3Encoder(1, sampleRate, request.bitrateKbps)
      samplesSinceChunk = 0
      pendingParts = []
      pendingBytes = 0
      return
    }
    if (request.type === 'encode') {
      const byteLength = request.buffer.byteLength
      encode(request.buffer)
      const response: WorkerResponse = { type: 'encoded', byteLength }
      workerScope.postMessage(response)
      return
    }
    if (request.type === 'drain') {
      emitChunk()
      const response: WorkerResponse = { type: 'drained' }
      workerScope.postMessage(response)
      return
    }
    if (!encoder) throw new Error('MP3 encoder is not initialized')
    appendEncoded(encoder.flush())
    emitChunk()
    encoder = null
    const response: WorkerResponse = { type: 'flushed' }
    workerScope.postMessage(response)
  } catch (reason) {
    const response: WorkerResponse = {
      type: 'error',
      message: reason instanceof Error ? reason.message : String(reason),
    }
    workerScope.postMessage(response)
  }
}
