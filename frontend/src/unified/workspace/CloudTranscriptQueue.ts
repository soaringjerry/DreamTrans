import {
  ApiRequestError,
  getStoredUser,
  saveTranscriptsBatch,
  type TranscriptInput,
} from '../../pro/api/auth'

export interface CloudTranscriptQueueOptions {
  flushDelayMs?: number
  batchSize?: number
  maxPending?: number
  requestTimeoutMs?: number
  retryBaseDelayMs?: number
  retryMaxDelayMs?: number
  /** Delay before retrying after HTTP 402 (balance exhausted). */
  paymentRetryDelayMs?: number
  /** Cooldown for a payload rejected by the current backend contract. */
  payloadRetryDelayMs?: number
  /** Cooldown for a missing/forbidden cloud session. */
  sessionRetryDelayMs?: number
  onPendingChange?: (count: number) => void
  onError?: (error: Error) => void
  onBatchSaved?: (batch: SavedCloudTranscriptBatch) => void | Promise<void>
  /**
   * @deprecated Permanent HTTP rejections are now retained in memory and in
   * the durable outbox with bounded retry. This callback is intentionally not
   * invoked because acknowledging it would destroy the recovery record.
   */
  onEntriesRejected?: (batch: RejectedCloudTranscriptBatch) => void | Promise<void>
}

interface PendingEntry {
  input: TranscriptInput
  order: number
  durableVersion?: number
}

export interface RestoredCloudTranscriptEntry {
  ownerId: string
  sessionId: string
  input: TranscriptInput
  durableVersion: number
}

export interface SavedCloudTranscriptBatch {
  ownerId: string
  sessionId: string
  entries: Array<{
    clientSegmentId: string
    durableVersion?: number
  }>
}

export interface RejectedCloudTranscriptBatch extends SavedCloudTranscriptBatch {
  status: number
  message: string
}

interface SessionQueue {
  discarded: boolean
  entries: Map<string, PendingEntry>
  inFlightCount: number
  ownerId: string
  sessionId: string
  retryAttempt: number
  nextAttemptAt: number
  lastFailureKind: 'none' | 'transient' | 'payment' | 'permanent'
  /**
   * After a permanent rejection of a multi-record batch, records are sent one
   * at a time so a single bad record is quarantined instead of blocking every
   * healthy record behind endless batch retries.
   */
  isolating: boolean
}

export type CloudTranscriptSyncErrorKind =
  | 'network'
  | 'payment'
  | 'payload'
  | 'session'
  | 'owner'

export class CloudTranscriptSyncError extends Error {
  readonly kind: CloudTranscriptSyncErrorKind
  readonly status?: number
  readonly retryAt?: number

  constructor(
    message: string,
    kind: CloudTranscriptSyncErrorKind,
    options: { status?: number; retryAt?: number } = {},
  ) {
    super(message)
    this.name = 'CloudTranscriptSyncError'
    this.kind = kind
    this.status = options.status
    this.retryAt = options.retryAt
  }
}

type BatchFailureKind = 'transient' | 'payment' | 'payload' | 'session' | 'owner'

/**
 * HTTP statuses that will not succeed by retrying the same payload:
 * contract violations (400/422), missing or foreign sessions (403/404), and
 * oversized payloads (413). 402 waits for balance; everything else retries.
 */
function classifyBatchFailure(reason: unknown): BatchFailureKind {
  if (reason instanceof CloudTranscriptSyncError && reason.kind === 'owner') {
    return 'owner'
  }
  if (reason instanceof ApiRequestError) {
    if (reason.status === 402) return 'payment'
    if ([400, 413, 422].includes(reason.status)) return 'payload'
    if ([403, 404].includes(reason.status)) return 'session'
  }
  return 'transient'
}

interface PendingBatch {
  entries: Array<[string, PendingEntry]>
  sessionId: string
  sessionQueue: SessionQueue
}

type TimerHandle = ReturnType<typeof globalThis.setTimeout>

function sessionQueueKey(ownerId: string, sessionId: string): string {
  return `${ownerId}\u0000${sessionId}`
}

function boundedInteger(
  value: number | undefined,
  fallback: number,
  minimum: number,
  maximum: number,
): number {
  const candidate = value ?? fallback
  if (!Number.isFinite(candidate)) return fallback
  return Math.max(minimum, Math.min(maximum, Math.floor(candidate)))
}

function browserIsOffline(): boolean {
  return typeof navigator !== 'undefined' && navigator.onLine === false
}

function mergeInputs(
  current: TranscriptInput | undefined,
  incoming: TranscriptInput,
): TranscriptInput {
  return current ? { ...current, ...incoming } : incoming
}

/**
 * Small, deduplicating write-behind queue for the authenticated cloud API.
 * Final transcript updates are keyed by client_segment_id so a later
 * translation enriches the same server record instead of creating another.
 */
export class CloudTranscriptQueue {
  private readonly flushDelayMs: number
  private readonly batchSize: number
  private readonly maxPending: number
  private readonly requestTimeoutMs: number
  private readonly retryBaseDelayMs: number
  private readonly retryMaxDelayMs: number
  private readonly paymentRetryDelayMs: number
  private readonly payloadRetryDelayMs: number
  private readonly sessionRetryDelayMs: number
  private readonly onPendingChange?: (count: number) => void
  private readonly onError?: (error: Error) => void
  private readonly onBatchSaved?: (
    batch: SavedCloudTranscriptBatch,
  ) => void | Promise<void>
  private readonly pendingBySession = new Map<string, SessionQueue>()
  private sessionId: string | null = null
  private ownerId: string | null = null
  private timer: TimerHandle | null = null
  private flushPromise: Promise<void> | null = null
  private activeController: AbortController | null = null
  private activeQueueKey: string | null = null
  private pendingCount = 0
  private flushingCount = 0
  private nextOrder = 0
  private capacityErrorReported = false
  private destroyed = false
  private readonly handleOnline = () => {
    if (this.destroyed) return
    const now = Date.now()
    let changed = false
    for (const sessionQueue of this.pendingBySession.values()) {
      if (
        sessionQueue.ownerId === this.ownerId
        && sessionQueue.entries.size > 0
        && sessionQueue.lastFailureKind === 'transient'
      ) {
        sessionQueue.retryAttempt = 0
        sessionQueue.nextAttemptAt = now
        sessionQueue.lastFailureKind = 'none'
        changed = true
      }
    }
    // Fresh work queued while offline has no failure marker yet, and a
    // permanent/payment retry timer may also have fired during the outage.
    // Re-arm scheduling for all retained work; only transient retries are
    // accelerated to "now".
    if (changed || this.hasPendingForOwner()) this.scheduleNext()
  }

  constructor(options: CloudTranscriptQueueOptions = {}) {
    this.flushDelayMs = boundedInteger(options.flushDelayMs, 2_000, 100, 60_000)
    this.batchSize = boundedInteger(options.batchSize, 50, 1, 200)
    this.maxPending = boundedInteger(
      options.maxPending,
      2_000,
      this.batchSize,
      100_000,
    )
    this.requestTimeoutMs = boundedInteger(
      options.requestTimeoutMs,
      15_000,
      100,
      300_000,
    )
    this.retryBaseDelayMs = boundedInteger(
      options.retryBaseDelayMs,
      2_000,
      100,
      60_000,
    )
    this.retryMaxDelayMs = Math.max(
      this.retryBaseDelayMs,
      boundedInteger(options.retryMaxDelayMs, 30_000, 100, 300_000),
    )
    this.paymentRetryDelayMs = boundedInteger(
      options.paymentRetryDelayMs,
      60_000,
      1_000,
      600_000,
    )
    this.payloadRetryDelayMs = boundedInteger(
      options.payloadRetryDelayMs,
      30 * 60_000,
      60_000,
      24 * 60 * 60_000,
    )
    this.sessionRetryDelayMs = boundedInteger(
      options.sessionRetryDelayMs,
      5 * 60_000,
      30_000,
      24 * 60 * 60_000,
    )
    this.onPendingChange = options.onPendingChange
    this.onError = options.onError
    this.onBatchSaved = options.onBatchSaved
    if (typeof window !== 'undefined') {
      window.addEventListener('online', this.handleOnline)
    }
  }

  setSession(sessionId: string | null): void {
    if (this.destroyed || sessionId === this.sessionId) return
    this.sessionId = sessionId
    // Changing the input target must not discard unacknowledged data
    // from the previous cloud session. The scheduler continues draining every
    // retained session queue, while new items are routed to this session.
    this.scheduleNext()
  }

  setOwner(ownerId: string | null): void {
    if (this.destroyed || ownerId === this.ownerId) return
    this.activeController?.abort()
    this.ownerId = ownerId
    this.clearTimer()
    this.emitPending()
    this.scheduleNext()
  }

  /**
   * Permanently forget one deleted session for the lifetime of this queue.
   * An in-flight request is aborted and marked discarded so its failure path
   * cannot reinsert the batch into the retry map.
   */
  discardSession(ownerId: string, sessionId: string): void {
    if (this.destroyed || !ownerId || !sessionId) return
    const queueKey = sessionQueueKey(ownerId, sessionId)
    const sessionQueue = this.pendingBySession.get(queueKey)
    if (sessionQueue) {
      sessionQueue.discarded = true
      this.pendingCount = Math.max(
        0,
        this.pendingCount - sessionQueue.entries.size,
      )
      sessionQueue.entries.clear()
      this.pendingBySession.delete(queueKey)
    }
    if (this.activeQueueKey === queueKey) this.activeController?.abort()
    if (this.ownerId === ownerId && this.sessionId === sessionId) {
      this.sessionId = null
    }
    this.emitPending()
    this.scheduleNext()
  }

  queue(input: TranscriptInput, durableVersion?: number): void {
    if (!this.addPendingInput(input, durableVersion)) return
    this.emitPending()
    this.scheduleNext()
  }

  /**
   * Restores/reconciles a persisted local session without rescheduling one
   * timer and one React update per transcript.
   */
  queueMany(inputs: Iterable<TranscriptInput>): void {
    let changed = false
    for (const input of inputs) {
      changed = this.addPendingInput(input) || changed
    }
    if (!changed) return
    this.emitPending()
    this.scheduleNext()
  }

  restore(entries: Iterable<RestoredCloudTranscriptEntry>): void {
    let changed = false
    for (const entry of entries) {
      if (entry.ownerId !== this.ownerId) continue
      changed = this.addPendingInput(
        entry.input,
        entry.durableVersion,
        entry.sessionId,
        entry.ownerId,
      ) || changed
    }
    if (!changed) return
    this.emitPending()
    this.scheduleNext()
  }

  private addPendingInput(
    input: TranscriptInput,
    durableVersion?: number,
    sessionId = this.sessionId,
    ownerId = this.ownerId,
  ): boolean {
    if (!sessionId || !ownerId || this.destroyed) return false
    const sessionQueue = this.getOrCreateSessionQueue(sessionId, ownerId)
    if (!sessionQueue) return false
    const key = input.client_segment_id
    const current = sessionQueue.entries.get(key)
    if (current) {
      sessionQueue.entries.set(key, {
        input: mergeInputs(current.input, input),
        order: current.order,
        durableVersion: durableVersion === undefined
          ? current.durableVersion
          : Math.max(current.durableVersion ?? 0, durableVersion),
      })
    } else {
      if (!this.makeCapacity()) return false
      sessionQueue.entries.set(key, {
        input,
        order: this.nextOrder,
        ...(durableVersion === undefined ? {} : { durableVersion }),
      })
      this.nextOrder += 1
      this.pendingCount += 1
    }
    if (sessionQueue.retryAttempt === 0) {
      const delay = sessionQueue.entries.size >= this.batchSize
        ? 0
        : this.flushDelayMs
      sessionQueue.nextAttemptAt = Math.min(
        sessionQueue.nextAttemptAt || Number.POSITIVE_INFINITY,
        Date.now() + delay,
      )
    }
    return true
  }

  async flush(): Promise<void> {
    this.clearTimer()
    if (this.destroyed) return
    if (this.flushPromise) await this.flushPromise
    if (this.hasReadyPending(true)) {
      await this.startFlush(true)
    } else {
      this.scheduleNext()
    }
  }

  async drain(): Promise<void> {
    this.clearTimer()
    while (!this.destroyed) {
      if (this.flushPromise) {
        await this.flushPromise
        continue
      }
      // drain bypasses only the normal write-behind delay. A failed network,
      // payment, or contract request keeps its retry deadline, otherwise stop
      // could turn a retryable outage into a tight request loop.
      if (!this.hasReadyPending(true)) {
        this.scheduleNext()
        return
      }
      await this.startFlush(true)
    }
  }

  destroy(): void {
    if (this.destroyed) return
    this.destroyed = true
    this.clearTimer()
    this.activeController?.abort()
    this.activeController = null
    this.pendingBySession.clear()
    this.pendingCount = 0
    this.flushingCount = 0
    this.sessionId = null
    this.ownerId = null
    if (typeof window !== 'undefined') {
      window.removeEventListener('online', this.handleOnline)
    }
    this.emitPending()
  }

  private getOrCreateSessionQueue(
    sessionId: string,
    ownerId: string,
  ): SessionQueue | null {
    const queueKey = sessionQueueKey(ownerId, sessionId)
    let sessionQueue = this.pendingBySession.get(queueKey)
    if (!sessionQueue) {
      sessionQueue = {
        discarded: false,
        entries: new Map(),
        inFlightCount: 0,
        ownerId,
        sessionId,
        retryAttempt: 0,
        nextAttemptAt: Date.now() + this.flushDelayMs,
        lastFailureKind: 'none',
        isolating: false,
      }
      this.pendingBySession.set(queueKey, sessionQueue)
    }
    return sessionQueue
  }

  /**
   * Keep total queued + in-flight records bounded. The local session database
   * remains authoritative, so overload is surfaced while the oldest in-memory
   * cloud update is evicted instead of allowing a long outage to exhaust RAM.
   */
  private makeCapacity(): boolean {
    if (this.pendingCount + this.flushingCount < this.maxPending) return true

    let oldest:
      | { key: string; order: number; queueKey: string; sessionQueue: SessionQueue }
      | undefined
    for (const [queueKey, sessionQueue] of this.pendingBySession) {
      for (const [key, entry] of sessionQueue.entries) {
        if (!oldest || entry.order < oldest.order) {
          oldest = { key, order: entry.order, queueKey, sessionQueue }
        }
      }
    }
    if (!oldest) {
      this.reportCapacityError(
        `Cloud transcript queue reached its ${this.maxPending}-record limit; `
        + 'the newest update could not be queued while every slot was in flight',
      )
      return false
    }

    oldest.sessionQueue.entries.delete(oldest.key)
    if (
      oldest.sessionQueue.entries.size === 0
      && oldest.sessionQueue.inFlightCount === 0
      && (
        oldest.sessionQueue.ownerId !== this.ownerId
        || oldest.sessionQueue.sessionId !== this.sessionId
      )
    ) {
      this.pendingBySession.delete(oldest.queueKey)
    }
    this.pendingCount = Math.max(0, this.pendingCount - 1)
    this.reportCapacityError(
      `Cloud transcript queue reached its ${this.maxPending}-record limit; `
      + 'the oldest unsynced in-memory update was dropped',
    )
    return true
  }

  private reportCapacityError(message: string): void {
    if (this.capacityErrorReported) return
    this.capacityErrorReported = true
    this.onError?.(new Error(message))
  }

  private startFlush(force: boolean): Promise<void> {
    if (this.destroyed) return Promise.resolve()
    if (this.flushPromise) return this.flushPromise
    const operation = this.flushLoop(force)
    this.flushPromise = operation
    const finalize = () => {
      if (this.flushPromise === operation) this.flushPromise = null
      this.scheduleNext()
    }
    void operation.then(finalize, finalize)
    return operation
  }

  private async flushLoop(force: boolean): Promise<void> {
    while (!this.destroyed) {
      const batch = this.takeNextBatch(force)
      if (!batch) return
      try {
        await this.sendBatch(batch)
        if (!batch.sessionQueue.discarded) {
          await this.onBatchSaved?.({
            ownerId: batch.sessionQueue.ownerId,
            sessionId: batch.sessionId,
            entries: batch.entries.map(([clientSegmentId, entry]) => ({
              clientSegmentId,
              ...(entry.durableVersion === undefined
                ? {}
                : { durableVersion: entry.durableVersion }),
            })),
          })
        }
        this.finishBatch(batch, 'success')
      } catch (reason) {
        const discarded = batch.sessionQueue.discarded
        const error = reason instanceof Error ? reason : new Error(String(reason))
        const failureKind = discarded ? 'transient' : classifyBatchFailure(reason)

        if (discarded) {
          this.finishBatch(batch, 'failure')
          continue
        }

        if (failureKind === 'owner') {
          this.finishBatch(batch, 'failure')
          // The React owner update will make this queue ineligible. Keeping the
          // old owner's entries in memory and IndexedDB is safer than sending
          // them with the newly observed account token.
          return
        }

        if (failureKind === 'payload') {
          // A batch-level validation failure is narrowed to single records,
          // but every failed probe stops this flush cycle and backs off. The
          // record is rotated to the tail and never acknowledged from durable
          // storage, so a backend schema fix can recover it automatically.
          this.finishBatch(
            batch,
            batch.entries.length === 1 ? 'quarantine' : 'isolate',
          )
          const status = reason instanceof ApiRequestError ? reason.status : undefined
          const blockedError = new CloudTranscriptSyncError(
            `云端拒绝了转录数据${status ? `（HTTP ${status}）` : ''}；`
            + `待同步记录已保留并限速重试：${error.message}`,
            'payload',
            { status, retryAt: batch.sessionQueue.nextAttemptAt },
          )
          if (batch.sessionQueue.ownerId === this.ownerId) {
            this.onError?.(blockedError)
          }
          return
        }

        if (failureKind === 'session') {
          this.finishBatch(batch, 'session')
          const status = reason instanceof ApiRequestError ? reason.status : undefined
          const blockedError = new CloudTranscriptSyncError(
            `云端会话暂时无法写入${status ? `（HTTP ${status}）` : ''}；`
            + `待同步记录已保留：${error.message}`,
            'session',
            { status, retryAt: batch.sessionQueue.nextAttemptAt },
          )
          if (batch.sessionQueue.ownerId === this.ownerId) {
            this.onError?.(blockedError)
          }
          return
        }

        this.finishBatch(batch, failureKind === 'payment' ? 'payment' : 'failure')
        const retryError = new CloudTranscriptSyncError(
          failureKind === 'payment'
            ? `云端同步已暂停，等待余额恢复：${error.message}`
            : `网络或云端服务暂时不可用，待同步记录已保留：${error.message}`,
          failureKind === 'payment' ? 'payment' : 'network',
          {
            status: reason instanceof ApiRequestError ? reason.status : undefined,
            retryAt: batch.sessionQueue.nextAttemptAt,
          },
        )
        if (batch.sessionQueue.ownerId === this.ownerId) {
          this.onError?.(retryError)
        }
        throw retryError
      }
    }
  }

  private takeNextBatch(force: boolean): PendingBatch | null {
    if (browserIsOffline()) return null
    const now = Date.now()
    let selected:
      | { sessionId: string; sessionQueue: SessionQueue }
      | undefined

    const activeSessionQueue = this.sessionId
      && this.ownerId
      ? this.pendingBySession.get(sessionQueueKey(this.ownerId, this.sessionId))
      : undefined
    if (
      activeSessionQueue
      && activeSessionQueue.ownerId === this.ownerId
      && activeSessionQueue.entries.size > 0
      && (
        activeSessionQueue.nextAttemptAt <= now
        || (force && activeSessionQueue.retryAttempt === 0)
      )
    ) {
      selected = {
        sessionId: this.sessionId as string,
        sessionQueue: activeSessionQueue,
      }
    } else {
      for (const sessionQueue of this.pendingBySession.values()) {
        if (
          sessionQueue.ownerId === this.ownerId
          && sessionQueue.entries.size > 0
          && (
            sessionQueue.nextAttemptAt <= now
            || (force && sessionQueue.retryAttempt === 0)
          )
          && (
            !selected
            || sessionQueue.nextAttemptAt < selected.sessionQueue.nextAttemptAt
          )
        ) {
          selected = { sessionId: sessionQueue.sessionId, sessionQueue }
        }
      }
    }
    if (!selected) return null

    const batchLimit = selected.sessionQueue.isolating ? 1 : this.batchSize
    const entries: Array<[string, PendingEntry]> = []
    for (const entry of selected.sessionQueue.entries) {
      entries.push(entry)
      if (entries.length >= batchLimit) break
    }
    for (const [key] of entries) selected.sessionQueue.entries.delete(key)
    this.pendingCount = Math.max(0, this.pendingCount - entries.length)
    this.flushingCount += entries.length
    selected.sessionQueue.inFlightCount += entries.length
    this.emitPending()
    return {
      entries,
      sessionId: selected.sessionId,
      sessionQueue: selected.sessionQueue,
    }
  }

  private async sendBatch(batch: PendingBatch): Promise<void> {
    const authenticatedOwnerId = getStoredUser()?.id ?? null
    if (authenticatedOwnerId !== batch.sessionQueue.ownerId) {
      throw new CloudTranscriptSyncError(
        '认证账号已变化；旧账号的待同步记录已保留，未使用新账号凭据发送',
        'owner',
      )
    }
    if (browserIsOffline()) {
      throw new CloudTranscriptSyncError('浏览器当前处于离线状态', 'network')
    }
    const controller = new AbortController()
    const queueKey = sessionQueueKey(batch.sessionQueue.ownerId, batch.sessionId)
    this.activeController = controller
    this.activeQueueKey = queueKey
    let timeout: TimerHandle | null = null
    const timeoutError = new Error(
      `Cloud transcript batch timed out after ${this.requestTimeoutMs} ms`,
    )
    const timeoutPromise = new Promise<never>((_resolve, reject) => {
      timeout = globalThis.setTimeout(() => {
        reject(timeoutError)
        controller.abort()
      }, this.requestTimeoutMs)
    })

    try {
      await Promise.race([
        saveTranscriptsBatch(
          batch.sessionId,
          batch.entries.map(([, entry]) => entry.input),
          controller.signal,
        ),
        timeoutPromise,
      ])
    } finally {
      if (timeout !== null) globalThis.clearTimeout(timeout)
      if (this.activeController === controller) {
        this.activeController = null
        this.activeQueueKey = null
      }
    }
  }

  private finishBatch(
    batch: PendingBatch,
    outcome:
      | 'success'
      | 'failure'
      | 'payment'
      | 'isolate'
      | 'quarantine'
      | 'session',
  ): void {
    this.flushingCount = Math.max(0, this.flushingCount - batch.entries.length)
    batch.sessionQueue.inFlightCount = Math.max(
      0,
      batch.sessionQueue.inFlightCount - batch.entries.length,
    )
    if (batch.sessionQueue.discarded) {
      if (this.pendingCount + this.flushingCount < this.maxPending) {
        this.capacityErrorReported = false
      }
      this.emitPending()
      return
    }
    if (outcome === 'success') {
      batch.sessionQueue.retryAttempt = 0
      batch.sessionQueue.lastFailureKind = 'none'
      if (batch.sessionQueue.entries.size === 0) {
        batch.sessionQueue.isolating = false
      }
      if (
        batch.sessionQueue.entries.size === 0
        && batch.sessionQueue.inFlightCount === 0
        && this.pendingBySession.get(
          sessionQueueKey(batch.sessionQueue.ownerId, batch.sessionId),
        ) === batch.sessionQueue
      ) {
        this.pendingBySession.delete(
          sessionQueueKey(batch.sessionQueue.ownerId, batch.sessionId),
        )
      } else {
        batch.sessionQueue.nextAttemptAt = Date.now() + (
          batch.sessionQueue.isolating
            || batch.sessionQueue.entries.size >= this.batchSize
            ? 0
            : this.flushDelayMs
        )
      }
    } else if (!this.destroyed) {
      for (const [key, entry] of batch.entries) {
        const current = batch.sessionQueue.entries.get(key)
        if (outcome === 'quarantine' && current) {
          // Move the rejected key behind records that arrived while it was in
          // flight so one malformed update cannot starve healthy updates.
          batch.sessionQueue.entries.delete(key)
        }
        batch.sessionQueue.entries.set(key, current
          ? {
              input: mergeInputs(entry.input, current.input),
              order: Math.min(entry.order, current.order),
              durableVersion: current.durableVersion === undefined
                ? entry.durableVersion
                : Math.max(entry.durableVersion ?? 0, current.durableVersion),
            }
          : entry)
      }
      this.pendingBySession.set(
        sessionQueueKey(batch.sessionQueue.ownerId, batch.sessionId),
        batch.sessionQueue,
      )
      // Duplicate updates that arrived while the request was in flight collapse
      // back to one record. Recompute this queue's contribution to avoid count
      // drift without scanning transcript payloads.
      this.pendingCount = [...this.pendingBySession.values()].reduce(
        (count, sessionQueue) => count + sessionQueue.entries.size,
        0,
      )
      if (outcome === 'payment') {
        // Balance exhaustion is not the payload's fault: hold a steady,
        // longer interval instead of growing the exponential backoff.
        batch.sessionQueue.retryAttempt = 0
        batch.sessionQueue.nextAttemptAt = Date.now() + this.paymentRetryDelayMs
        batch.sessionQueue.lastFailureKind = 'payment'
      } else {
        if (outcome === 'isolate' || outcome === 'quarantine') {
          batch.sessionQueue.isolating = true
        }
        batch.sessionQueue.retryAttempt += 1
        batch.sessionQueue.lastFailureKind = (
          outcome === 'isolate'
          || outcome === 'quarantine'
          || outcome === 'session'
        )
          ? 'permanent'
          : 'transient'
        batch.sessionQueue.nextAttemptAt = Date.now() + Math.min(
          this.retryMaxDelayMs,
          this.retryBaseDelayMs * 2 ** Math.max(0, batch.sessionQueue.retryAttempt - 1),
        )
        if (outcome === 'isolate' || outcome === 'quarantine') {
          batch.sessionQueue.nextAttemptAt = Date.now() + this.payloadRetryDelayMs
        } else if (outcome === 'session') {
          batch.sessionQueue.nextAttemptAt = Date.now() + this.sessionRetryDelayMs
        }
      }
    }
    if (this.pendingCount + this.flushingCount < this.maxPending) {
      this.capacityErrorReported = false
    }
    this.emitPending()
  }

  private scheduleNext(): void {
    this.clearTimer()
    if (
      this.destroyed
      || browserIsOffline()
      || !this.hasPendingForOwner()
      || this.flushPromise
    ) {
      return
    }
    let nextAttemptAt = Number.POSITIVE_INFINITY
    for (const sessionQueue of this.pendingBySession.values()) {
      if (sessionQueue.ownerId === this.ownerId && sessionQueue.entries.size > 0) {
        nextAttemptAt = Math.min(nextAttemptAt, sessionQueue.nextAttemptAt)
      }
    }
    if (!Number.isFinite(nextAttemptAt)) return
    this.timer = globalThis.setTimeout(() => {
      this.timer = null
      void this.startFlush(false).catch(() => undefined)
    }, Math.max(0, nextAttemptAt - Date.now()))
  }

  private clearTimer(): void {
    if (this.timer === null) return
    globalThis.clearTimeout(this.timer)
    this.timer = null
  }

  private emitPending(): void {
    let visibleCount = 0
    if (this.ownerId) {
      for (const sessionQueue of this.pendingBySession.values()) {
        if (sessionQueue.ownerId === this.ownerId) {
          visibleCount += sessionQueue.entries.size + sessionQueue.inFlightCount
        }
      }
    }
    this.onPendingChange?.(visibleCount)
  }

  private hasPendingForOwner(): boolean {
    if (!this.ownerId) return false
    for (const sessionQueue of this.pendingBySession.values()) {
      if (
        sessionQueue.ownerId === this.ownerId
        && sessionQueue.entries.size > 0
      ) {
        return true
      }
    }
    return false
  }

  private hasReadyPending(force: boolean): boolean {
    if (!this.ownerId || browserIsOffline()) return false
    const now = Date.now()
    for (const sessionQueue of this.pendingBySession.values()) {
      if (
        sessionQueue.ownerId === this.ownerId
        && sessionQueue.entries.size > 0
        && (
          sessionQueue.nextAttemptAt <= now
          || (force && sessionQueue.retryAttempt === 0)
        )
      ) {
        return true
      }
    }
    return false
  }
}
