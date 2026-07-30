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
  total: number
}

declare global {
  interface Window { __dt_lex?: Record<string, LexState> }
}

function ensureStore(sessionId: string): LexState {
  if (!window.__dt_lex) window.__dt_lex = {}
  const cur = window.__dt_lex[sessionId]
  if (cur) return cur
  const st: LexState = { word: new Map(), bigram: new Map(), total: 0 }
  window.__dt_lex[sessionId] = st
  return st
}

export function lexReset(sessionId: string) {
  if (!window.__dt_lex) window.__dt_lex = {}
  window.__dt_lex[sessionId] = { word: new Map(), bigram: new Map(), total: 0 }
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
  const st: LexState = { word: new Map(), bigram: new Map(), total: 0 }
  let added = 0
  for (const text of texts) added += ingestText(st, text)
  window.__dt_lex[sessionId] = st
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
