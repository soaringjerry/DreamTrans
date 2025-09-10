export type UserLex = {
  known: Record<string, number> // word -> timestamp (ms) when marked known
  learning: Record<string, { addedAt: number }>
}

const KEY = 'dt_lex_user_v1'

export function loadUserLex(): UserLex {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return { known: {}, learning: {} }
    const j = JSON.parse(raw) as UserLex
    return { known: j.known || {}, learning: j.learning || {} }
  } catch {
    return { known: {}, learning: {} }
  }
}

export function saveUserLex(lex: UserLex) {
  try { localStorage.setItem(KEY, JSON.stringify(lex)) } catch { /* ignore */ }
}

export function isKnown(word: string): boolean {
  const w = word.toLowerCase()
  const u = loadUserLex()
  return !!u.known[w]
}

export function markKnown(word: string, yes: boolean) {
  const w = word.toLowerCase()
  const u = loadUserLex()
  if (yes) u.known[w] = Date.now()
  else delete u.known[w]
  saveUserLex(u)
  window.dispatchEvent(new CustomEvent('dt-lex-user-updated', { detail: { word: w, known: yes } }))
}

export function isLearning(word: string): boolean {
  const w = word.toLowerCase()
  const u = loadUserLex()
  return !!u.learning[w]
}

export function markLearning(word: string, yes: boolean) {
  const w = word.toLowerCase()
  const u = loadUserLex()
  if (yes) u.learning[w] = { addedAt: Date.now() }
  else delete u.learning[w]
  saveUserLex(u)
  window.dispatchEvent(new CustomEvent('dt-lex-user-updated', { detail: { word: w, learning: yes } }))
}

