/**
 * Vocabulary-tab isolation and burst-coalescing verification.
 *
 * Run from frontend/:
 *   esbuild src/unified/workspace/vocabularyRefreshVerification.ts \
 *     --bundle --platform=node --format=cjs --jsx=automatic \
 *     --define:import.meta.env.VITE_BACKEND_URL='"/"' \
 *     --outfile=/tmp/dreamtrans-vocabulary-refresh-verify.cjs
 *   node /tmp/dreamtrans-vocabulary-refresh-verify.cjs
 */
import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { selectTopLexEntries } from '../../utils/lexicon'
import { InsightsPanel } from '../components/InsightsPanel'
import {
  subscribeVocabularyRefresh,
  type VocabularyRefreshEventSource,
  type VocabularyRefreshTimer,
} from './vocabularyRefresh'

function assert(condition: unknown, message: string): void {
  if (!condition) {
    throw new Error(`Vocabulary refresh verification failed: ${message}`)
  }
}

class FakeEventSource implements VocabularyRefreshEventSource {
  private readonly listeners = new Map<string, Set<EventListener>>()

  addEventListener(type: string, listener: EventListener): void {
    let listeners = this.listeners.get(type)
    if (!listeners) {
      listeners = new Set()
      this.listeners.set(type, listeners)
    }
    listeners.add(listener)
  }

  removeEventListener(type: string, listener: EventListener): void {
    this.listeners.get(type)?.delete(listener)
  }

  emit(type: string, sessionId?: string): void {
    const event = {
      type,
      ...(sessionId === undefined ? {} : { detail: { session_id: sessionId } }),
    } as unknown as Event
    for (const listener of [...(this.listeners.get(type) ?? [])]) listener(event)
  }

  listenerCount(): number {
    let count = 0
    for (const listeners of this.listeners.values()) count += listeners.size
    return count
  }
}

class FakeTimer implements VocabularyRefreshTimer {
  readonly delays: number[] = []
  private readonly callbacks = new Map<number, () => void>()
  private nextHandle = 1

  clear(handle: unknown): void {
    this.callbacks.delete(handle as number)
  }

  set(callback: () => void, delayMs: number): unknown {
    const handle = this.nextHandle
    this.nextHandle += 1
    this.callbacks.set(handle, callback)
    this.delays.push(delayMs)
    return handle
  }

  pendingCount(): number {
    return this.callbacks.size
  }

  runPending(): void {
    const pending = [...this.callbacks.values()]
    this.callbacks.clear()
    for (const callback of pending) callback()
  }
}

assert(
  typeof globalThis.window === 'undefined',
  'the Node verification must not provide a browser lexicon store',
)

let overviewSortCalls = 0
const nativeSort = Array.prototype.sort
const overviewMarkup = (() => {
  Array.prototype.sort = function (...args: Parameters<typeof nativeSort>) {
    overviewSortCalls += 1
    return nativeSort.apply(this, args)
  }
  try {
    return renderToStaticMarkup(createElement(InsightsPanel, {
      assistantEnabled: true,
      canViewApiMetrics: true,
      durationLabel: '01:23',
      finalSegments: 42,
      onExplainTerm: () => undefined,
      pendingWrites: 0,
      sessionId: 'long-session',
      speakers: 3,
      topWords: [{ word: 'dream', count: 7 }],
      translatedSegments: 40,
    }))
  } finally {
    Array.prototype.sort = nativeSort
  }
})()

assert(
  overviewMarkup.includes('dt-stat-grid'),
  'the overview renders normally without a browser window',
)
assert(
  !overviewMarkup.includes('dt-vocabulary__controls'),
  'the vocabulary analyzer is not rendered on the overview tab',
)
assert(
  overviewSortCalls === 0,
  'the overview performs zero vocabulary sorts',
)

const eventSource = new FakeEventSource()
const timer = new FakeTimer()
let refreshCount = 0
const unsubscribe = subscribeVocabularyRefresh({
  eventSource,
  onRefresh: () => {
    refreshCount += 1
  },
  sessionId: 'active-session',
  timer,
})

for (let index = 0; index < 200; index += 1) {
  eventSource.emit('dt-lex-updated', 'other-session')
}
assert(timer.pendingCount() === 0, 'other sessions schedule no snapshot refresh')

for (let index = 0; index < 500; index += 1) {
  eventSource.emit('dt-lex-updated', 'active-session')
}
eventSource.emit('dt-lex-user-updated')
assert(timer.pendingCount() === 1, 'a burst schedules only one trailing refresh')
assert(timer.delays[0] === 3_000, 'the active vocabulary refresh window is three seconds')
timer.runPending()
assert(refreshCount === 1, 'the first burst produces exactly one refresh')

for (let index = 0; index < 500; index += 1) {
  eventSource.emit('dt-lex-updated', 'active-session')
}
assert(timer.pendingCount() === 1, 'the next interval still has one pending refresh')
timer.runPending()
assert(refreshCount === 2, 'two refresh windows produce exactly two refreshes')

eventSource.emit('dt-lex-updated', 'active-session')
assert(timer.pendingCount() === 1, 'cleanup test starts with a pending refresh')
unsubscribe()
assert(timer.pendingCount() === 0, 'cleanup cancels the pending timer')
assert(eventSource.listenerCount() === 0, 'cleanup removes both event listeners')
timer.runPending()
eventSource.emit('dt-lex-updated', 'active-session')
assert(refreshCount === 2, 'cleanup prevents delayed and future refreshes')

const ranked = selectTopLexEntries([
  ['middle', 5],
  ['zebra', 9],
  ['alpha', 9],
  ['small', 1],
  ['second', 7],
], 3)
assert(
  JSON.stringify(ranked) === JSON.stringify([
    ['alpha', 9],
    ['zebra', 9],
    ['second', 7],
  ]),
  `bounded top-N selection preserves rank and tie order: ${JSON.stringify(ranked)}`,
)

console.log(JSON.stringify({
  activeBurstEvents: 1_000,
  activeRefreshes: refreshCount,
  cleanup: 'listeners-and-timer-removed',
  overviewSnapshotCalls: 0,
  overviewSortCalls,
}))
