// Lightweight, session-scoped, incremental lexicon aggregator (words + bigrams)
// No AI, all local.

export type LexSnapshot = {
  words: Array<[string, number]>
  bigrams: Array<[string, number]>
  total: number
}

type LexState = {
  word: Map<string, number>
  bigram: Map<string, number>
  lastWord?: string
  lastAccess: number
  total: number
}

declare global {
  interface Window { __dt_lex?: Record<string, LexState> }
}

const MAX_CACHED_LEXICON_SESSIONS = 6
let accessSequence = 0

function emptyState(): LexState {
  return {
    word: new Map(),
    bigram: new Map(),
    lastAccess: ++accessSequence,
    total: 0,
  }
}

function pruneStore(activeSessionId: string): void {
  const store = window.__dt_lex
  if (!store) return
  const sessionIds = Object.keys(store)
  if (sessionIds.length <= MAX_CACHED_LEXICON_SESSIONS) return
  sessionIds
    .filter((sessionId) => sessionId !== activeSessionId)
    .sort((left, right) => (
      (store[left]?.lastAccess ?? 0) - (store[right]?.lastAccess ?? 0)
    ))
    .slice(0, sessionIds.length - MAX_CACHED_LEXICON_SESSIONS)
    .forEach((sessionId) => {
      delete store[sessionId]
    })
}

function ensureStore(sessionId: string): LexState {
  if (!window.__dt_lex) window.__dt_lex = {}
  const cur = window.__dt_lex[sessionId]
  if (cur) {
    cur.lastAccess = ++accessSequence
    return cur
  }
  const st = emptyState()
  window.__dt_lex[sessionId] = st
  pruneStore(sessionId)
  return st
}

export function lexReset(sessionId: string) {
  if (!window.__dt_lex) window.__dt_lex = {}
  window.__dt_lex[sessionId] = emptyState()
  pruneStore(sessionId)
  window.dispatchEvent(new CustomEvent('dt-lex-updated', { detail: { session_id: sessionId } }))
}

function ingestText(st: LexState, text: string): number {
  const normalized = (text || '').toLowerCase()
  if (!normalized) return 0
  const words = Array.from(normalized.matchAll(/[a-z]+(?:'[a-z]+)?/g)).map(m => m[0])
  if (words.length === 0) return 0

  for (const word of words) {
    st.word.set(word, (st.word.get(word) || 0) + 1)
  }
  st.total += words.length

  let previous = st.lastWord
  for (const word of words) {
    if (previous) {
      const bigram = `${previous} ${word}`
      st.bigram.set(bigram, (st.bigram.get(bigram) || 0) + 1)
    }
    previous = word
  }
  st.lastWord = previous
  return words.length
}

export function lexIngest(sessionId: string, text: string) {
  const st = ensureStore(sessionId)
  const added = ingestText(st, text)
  if (added === 0) return
  window.dispatchEvent(new CustomEvent('dt-lex-updated', {
    detail: { session_id: sessionId, added },
  }))
}

export function lexReplace(sessionId: string, texts: Iterable<string>) {
  if (!window.__dt_lex) window.__dt_lex = {}
  const st = emptyState()
  let added = 0
  for (const text of texts) added += ingestText(st, text)
  window.__dt_lex[sessionId] = st
  pruneStore(sessionId)
  window.dispatchEvent(new CustomEvent('dt-lex-updated', {
    detail: { session_id: sessionId, added, replaced: true },
  }))
}

export function lexSnapshot(sessionId: string): LexSnapshot {
  const st = ensureStore(sessionId)
  return {
    words: Array.from(st.word.entries()),
    bigrams: Array.from(st.bigram.entries()),
    total: st.total,
  }
}

function ranksBefore(
  left: readonly [string, number],
  right: readonly [string, number],
): boolean {
  return left[1] > right[1]
    || (left[1] === right[1] && left[0].localeCompare(right[0]) < 0)
}

/**
 * Selects only the rows the panel can render. This keeps live vocabulary
 * refreshes O(V log N) instead of sorting every unique word once per tick.
 */
export function selectTopLexEntries(
  entries: readonly [string, number][],
  limit: number,
): Array<[string, number]> {
  const safeLimit = Math.max(0, Math.floor(limit))
  if (safeLimit === 0 || entries.length === 0) return []
  const heap: Array<[string, number]> = []
  const ranksAfter = (
    left: readonly [string, number],
    right: readonly [string, number],
  ) => ranksBefore(right, left)
  const bubbleUp = (startIndex: number) => {
    let index = startIndex
    while (index > 0) {
      const parent = (index - 1) >>> 1
      if (!ranksAfter(heap[index] as [string, number], heap[parent] as [string, number])) {
        break
      }
      ;[heap[index], heap[parent]] = [
        heap[parent] as [string, number],
        heap[index] as [string, number],
      ]
      index = parent
    }
  }
  const sink = () => {
    let index = 0
    while (true) {
      const left = index * 2 + 1
      if (left >= heap.length) return
      const right = left + 1
      let worse = left
      if (
        right < heap.length
        && ranksAfter(
          heap[right] as [string, number],
          heap[left] as [string, number],
        )
      ) {
        worse = right
      }
      if (!ranksAfter(heap[worse] as [string, number], heap[index] as [string, number])) {
        return
      }
      ;[heap[index], heap[worse]] = [
        heap[worse] as [string, number],
        heap[index] as [string, number],
      ]
      index = worse
    }
  }

  for (const entry of entries) {
    const candidate: [string, number] = [entry[0], entry[1]]
    if (heap.length < safeLimit) {
      heap.push(candidate)
      bubbleUp(heap.length - 1)
    } else if (ranksBefore(candidate, heap[0] as [string, number])) {
      heap[0] = candidate
      sink()
    }
  }
  return heap.sort((left, right) => (
    right[1] - left[1] || left[0].localeCompare(right[0])
  ))
}
