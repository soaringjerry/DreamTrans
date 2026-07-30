import { ingestRag } from '../../api'

export interface RagIngestItem {
  id: string
  sessionId: string
  speaker: string
  text: string
  startTime: number
  endTime: number
}

export interface RagIngestQueueOptions {
  maxPending?: number
  onError?: (error: Error) => void
}

/**
 * Best-effort, bounded and serial RAG ingestion. Network slowness can never
 * grow an unbounded promise chain on the recording hot path.
 */
export class RagIngestQueue {
  private readonly maxPending: number
  private readonly onError?: (error: Error) => void
  private readonly pending = new Map<string, RagIngestItem>()
  private running = false
  private destroyed = false

  constructor(options: RagIngestQueueOptions = {}) {
    this.maxPending = Math.max(10, options.maxPending ?? 200)
    this.onError = options.onError
  }

  queue(item: RagIngestItem): void {
    if (this.destroyed) return
    this.pending.set(item.id, item)
    while (this.pending.size > this.maxPending) {
      const oldest = this.pending.keys().next().value as string | undefined
      if (!oldest) break
      this.pending.delete(oldest)
    }
    void this.pump()
  }

  clear(): void {
    this.pending.clear()
  }

  destroy(): void {
    this.destroyed = true
    this.pending.clear()
  }

  private async pump(): Promise<void> {
    if (this.running || this.destroyed) return
    this.running = true
    try {
      while (!this.destroyed && this.pending.size > 0) {
        const next = this.pending.entries().next().value as
          | [string, RagIngestItem]
          | undefined
        if (!next) break
        const [key, item] = next
        this.pending.delete(key)
        try {
          await ingestRag(
            item.sessionId,
            item.speaker,
            item.text,
            item.startTime,
            item.endTime,
          )
        } catch (reason) {
          this.onError?.(
            reason instanceof Error ? reason : new Error(String(reason)),
          )
        }
      }
    } finally {
      this.running = false
      if (!this.destroyed && this.pending.size > 0) void this.pump()
    }
  }
}
