import domainTermsPack from './data/domain_terms.json'
import type { TermDomain, TermDomainMeta } from './types'

interface DomainTermsFile {
  domains: Record<string, { label: string; terms: Record<string, string> }>
}

const pack = domainTermsPack as DomainTermsFile

export const TERM_DOMAINS: readonly TermDomain[] = [
  'ai',
  'internet',
  'psychology',
  'geography',
  'biology',
] as const

export const DEFAULT_TERM_DOMAINS: readonly TermDomain[] = [...TERM_DOMAINS]

export function isTermDomain(value: unknown): value is TermDomain {
  return typeof value === 'string' && (TERM_DOMAINS as readonly string[]).includes(value)
}

export function listTermDomains(): TermDomainMeta[] {
  return TERM_DOMAINS.map((id) => ({
    id,
    label: pack.domains[id]?.label ?? id,
    termCount: Object.keys(pack.domains[id]?.terms ?? {}).length,
  }))
}

export function normalizeTermDomains(value: unknown): TermDomain[] {
  if (!Array.isArray(value)) return [...DEFAULT_TERM_DOMAINS]
  const selected = value.filter(isTermDomain)
  // Empty array is valid: user turned every domain off.
  const unique = Array.from(new Set(selected))
  return unique
}

/** Flatten enabled domains into head term → { zh, domain }, longer phrases win later via matcher. */
export function buildTermIndex(
  domains: readonly TermDomain[],
): Map<string, { zh: string; domain: TermDomain }> {
  const index = new Map<string, { zh: string; domain: TermDomain }>()
  for (const domain of domains) {
    const terms = pack.domains[domain]?.terms
    if (!terms) continue
    for (const [raw, zh] of Object.entries(terms)) {
      const key = normalizeTermKey(raw)
      if (!key || !zh.trim()) continue
      // First domain wins if the same term appears twice; pack is mostly disjoint.
      if (!index.has(key)) {
        index.set(key, { zh: zh.trim(), domain })
      }
    }
  }
  return index
}

export function normalizeTermKey(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[’]/g, "'")
    .replace(/\s+/g, ' ')
}

/**
 * Longest-match domain terms over a tokenized sentence.
 * Returns non-overlapping spans ordered by start index.
 */
export function findDomainTermMatches(
  text: string,
  termIndex: Map<string, { zh: string; domain: TermDomain }>,
): Array<{
  start: number
  end: number
  surface: string
  key: string
  zh: string
  domain: TermDomain
}> {
  if (!text.trim() || termIndex.size === 0) return []

  type Token = { start: number; end: number; lower: string }
  const tokenRe = /[A-Za-z]+(?:['’][A-Za-z]+)?|\d+(?:\.\d+)?/g
  const tokens: Token[] = []
  let match: RegExpExecArray | null
  while ((match = tokenRe.exec(text)) !== null) {
    tokens.push({
      start: match.index,
      end: match.index + match[0].length,
      lower: match[0].replace(/’/g, "'").toLowerCase(),
    })
  }
  if (tokens.length === 0) return []

  // Max phrase length in the pack is small; cap scan window.
  let maxWords = 1
  for (const key of termIndex.keys()) {
    maxWords = Math.max(maxWords, key.split(' ').length)
  }
  maxWords = Math.min(maxWords, 6)

  type Hit = {
    start: number
    end: number
    surface: string
    key: string
    zh: string
    domain: TermDomain
    tokenStart: number
    tokenEnd: number
  }
  const hits: Hit[] = []

  for (let i = 0; i < tokens.length; i += 1) {
    for (let len = Math.min(maxWords, tokens.length - i); len >= 1; len -= 1) {
      const slice = tokens.slice(i, i + len)
      const key = slice.map((token) => token.lower).join(' ')
      const entry = termIndex.get(key)
      if (!entry) continue
      const start = slice[0].start
      const end = slice[slice.length - 1].end
      hits.push({
        start,
        end,
        surface: text.slice(start, end),
        key,
        zh: entry.zh,
        domain: entry.domain,
        tokenStart: i,
        tokenEnd: i + len,
      })
      break // longest match at this start
    }
  }

  // Greedy non-overlapping: earlier longer already preferred per start; resolve overlaps by earliest then longer.
  hits.sort((a, b) => {
    if (a.start !== b.start) return a.start - b.start
    return (b.end - b.start) - (a.end - a.start)
  })
  const selected: typeof hits = []
  let cursor = 0
  for (const hit of hits) {
    if (hit.start < cursor) continue
    selected.push(hit)
    cursor = hit.end
  }
  return selected.map(({ start, end, surface, key, zh, domain }) => ({
    start, end, surface, key, zh, domain,
  }))
}
