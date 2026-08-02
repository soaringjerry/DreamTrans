class DreamTransPCMProcessor extends AudioWorkletProcessor {
  constructor(options) {
    super()
    const requestedFrames = options?.processorOptions?.batchFrames
    this.batchFrames = Number.isFinite(requestedFrames)
      ? Math.max(128, Math.floor(requestedFrames))
      : 1920
    this.buffer = new Float32Array(this.batchFrames)
    this.offset = 0
    this.active = true
    this.paused = false

    this.port.onmessage = (event) => {
      if (event.data === 'pause') {
        this.paused = true
        this.offset = 0
      } else if (event.data === 'resume') {
        this.paused = false
      } else if (event.data === 'flush') {
        this.flush()
        this.port.postMessage('flushed')
      } else if (event.data === 'stop') {
        this.flush()
        this.active = false
      }
    }
  }

  flush() {
    if (this.offset === 0) return
    const output = this.offset === this.buffer.length
      ? this.buffer
      : this.buffer.slice(0, this.offset)
    this.port.postMessage(output.buffer, [output.buffer])
    this.buffer = new Float32Array(this.batchFrames)
    this.offset = 0
  }

  /**
   * Downmix every channel on input 0 to mono. System/tab capture is often
   * stereo; reading only L drops content panned to R and can look like random
   * "the model is late" stalls when speech is off-center.
   */
  writeMono(channelData, sourceOffset, writeOffset, frames) {
    const channelCount = channelData.length
    if (channelCount === 1) {
      this.buffer.set(
        channelData[0].subarray(sourceOffset, sourceOffset + frames),
        writeOffset,
      )
      return
    }
    const scale = 1 / channelCount
    for (let i = 0; i < frames; i += 1) {
      let sum = 0
      const sampleIndex = sourceOffset + i
      for (let channel = 0; channel < channelCount; channel += 1) {
        sum += channelData[channel][sampleIndex] || 0
      }
      this.buffer[writeOffset + i] = sum * scale
    }
  }

  process(inputs) {
    if (!this.active) return false
    if (this.paused) return true

    const channelData = inputs[0]
    const first = channelData?.[0]
    if (!first || first.length === 0) return true

    let sourceOffset = 0
    while (sourceOffset < first.length) {
      const writable = Math.min(
        first.length - sourceOffset,
        this.buffer.length - this.offset,
      )
      this.writeMono(channelData, sourceOffset, this.offset, writable)
      this.offset += writable
      sourceOffset += writable
      if (this.offset === this.buffer.length) this.flush()
    }

    return true
  }
}

registerProcessor('dreamtrans-pcm-batched-processor', DreamTransPCMProcessor)
