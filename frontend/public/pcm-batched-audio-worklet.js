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

  process(inputs) {
    if (!this.active) return false
    if (this.paused) return true

    const channel = inputs[0]?.[0]
    if (!channel || channel.length === 0) return true

    let sourceOffset = 0
    while (sourceOffset < channel.length) {
      const writable = Math.min(
        channel.length - sourceOffset,
        this.buffer.length - this.offset,
      )
      this.buffer.set(
        channel.subarray(sourceOffset, sourceOffset + writable),
        this.offset,
      )
      this.offset += writable
      sourceOffset += writable
      if (this.offset === this.buffer.length) this.flush()
    }

    return true
  }
}

registerProcessor('dreamtrans-pcm-batched-processor', DreamTransPCMProcessor)
