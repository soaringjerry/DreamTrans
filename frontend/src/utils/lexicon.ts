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

export function lexIngest(sessionId: string, text: string) {
  const st = ensureStore(sessionId)
  const t = (text || '').toLowerCase()
  if (!t) return
  const words = Array.from(t.matchAll(/[a-z]+(?:'[a-z]+)?/g)).map(m => m[0])
  if (words.length === 0) return
  // update words
  for (const w of words) {
    st.word.set(w, (st.word.get(w) || 0) + 1)
  }
  st.total += words.length
  // update bigrams (connect with lastWord across calls)
  let prev = st.lastWord
  for (let i=0;i<words.length;i++) {
    const cur = words[i]
    if (prev) {
      const bg = `${prev} ${cur}`
      st.bigram.set(bg, (st.bigram.get(bg) || 0) + 1)
    }
    prev = cur
  }
  st.lastWord = prev
  window.dispatchEvent(new CustomEvent('dt-lex-updated', { detail: { session_id: sessionId, added: words.length } }))
}

export function lexSnapshot(sessionId: string): LexSnapshot {
  const st = ensureStore(sessionId)
  return {
    words: Array.from(st.word.entries()),
    bigrams: Array.from(st.bigram.entries()),
    total: st.total,
  }
}

