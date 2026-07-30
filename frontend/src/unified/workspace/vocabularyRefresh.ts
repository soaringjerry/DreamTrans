export const VOCABULARY_REFRESH_INTERVAL_MS = 1_000

export interface VocabularyRefreshEventSource {
  addEventListener(type: string, listener: EventListener): void
  removeEventListener(type: string, listener: EventListener): void
}

export interface VocabularyRefreshTimer {
  clear(handle: unknown): void
  set(callback: () => void, delayMs: number): unknown
}

interface SubscribeVocabularyRefreshOptions {
  eventSource: VocabularyRefreshEventSource
  intervalMs?: number
  onRefresh: () => void
  sessionId: string
  timer: VocabularyRefreshTimer
}

function updatedSessionId(event: Event): string | undefined {
  if (event.type !== 'dt-lex-updated') return undefined
  const detail = (event as CustomEvent<{ session_id?: unknown }>).detail
  return typeof detail?.session_id === 'string' ? detail.session_id : undefined
}

/**
 * Coalesces live lexicon updates into one trailing refresh per interval.
 *
 * The caller owns activation: mounting the vocabulary view subscribes, while
 * leaving that tab calls the returned cleanup and removes both listeners and
 * any pending timer. Recording never awaits this work.
 */
export function subscribeVocabularyRefresh({
  eventSource,
  intervalMs = VOCABULARY_REFRESH_INTERVAL_MS,
  onRefresh,
  sessionId,
  timer,
}: SubscribeVocabularyRefreshOptions): () => void {
  let pendingTimer: unknown
  let refreshScheduled = false
  let active = true
  const safeIntervalMs = Math.max(250, Math.floor(intervalMs))

  const scheduleRefresh: EventListener = (event) => {
    if (!active) return
    if (
      event.type === 'dt-lex-updated'
      && updatedSessionId(event) !== sessionId
    ) {
      return
    }
    if (refreshScheduled) return
    refreshScheduled = true
    pendingTimer = timer.set(() => {
      refreshScheduled = false
      pendingTimer = undefined
      if (active) onRefresh()
    }, safeIntervalMs)
  }

  eventSource.addEventListener('dt-lex-updated', scheduleRefresh)
  eventSource.addEventListener('dt-lex-user-updated', scheduleRefresh)

  return () => {
    if (!active) return
    active = false
    eventSource.removeEventListener('dt-lex-updated', scheduleRefresh)
    eventSource.removeEventListener('dt-lex-user-updated', scheduleRefresh)
    if (refreshScheduled) timer.clear(pendingTimer)
    refreshScheduled = false
    pendingTimer = undefined
  }
}
