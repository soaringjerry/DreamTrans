import learningPack from './data/learning_pack.json'
import { LEARNING_STOPWORDS } from './stopwords'
import {
  buildTermIndex,
  DEFAULT_TERM_DOMAINS,
  findDomainTermMatches,
} from './terms'
import type {
  AnnotateOptions,
  CefrLevel,
  LearningGloss,
  LearningLevel,
  LearningPack,
  TermDomain,
} from './types'

const pack = learningPack as LearningPack

const LEVEL_RANK: Record<CefrLevel, number> = {
  A1: 1,
  A2: 2,
  B1: 3,
  B2: 4,
  C1: 5,
  C2: 6,
}

/**
 * Words at or below this rank are treated as already known.
 * Selecting "A2" means the learner is *working at* A2, so only A1 is assumed
 * known — A2+ words still get glosses (more help). Same one-band lag for B1/B2.
 */
const USER_KNOWN_MAX_RANK: Record<LearningLevel, number> = {
  A2: LEVEL_RANK.A1,
  B1: LEVEL_RANK.A2,
  B2: LEVEL_RANK.B1,
}

/** Domain terms always outrank CEFR hard words when filling maxGlosses slots. */
const DOMAIN_TERM_RANK = 100

const TOKEN_RE = /[A-Za-z]+(?:['’][A-Za-z]+)?/g

function normalizeApostrophe(value: string): string {
  return value.replace(/’/g, "'")
}

/** Lightweight lemma candidates for table lookup (no NLP runtime). */
export function lemmaCandidates(raw: string): string[] {
  const word = normalizeApostrophe(raw).toLowerCase()
  if (!word) return []
  const out: string[] = [word]
  const push = (form: string) => {
    if (form && form !== word && form.length >= 2) out.push(form)
  }

  if (word.endsWith("n't") && word.length > 3) {
    push(word.slice(0, -3))
  }
  if (word.endsWith("'s") && word.length > 2) {
    push(word.slice(0, -2))
  }
  if (word.endsWith('ies') && word.length > 4) {
    push(`${word.slice(0, -3)}y`)
  }
  if (word.endsWith('ves') && word.length > 4) {
    push(`${word.slice(0, -3)}f`)
    push(`${word.slice(0, -3)}fe`)
  }
  if (/(?:sses|shes|ches|xes|zes)$/.test(word) && word.length > 4) {
    push(word.slice(0, -2))
  } else if (word.endsWith('es') && word.length > 3) {
    push(word.slice(0, -2))
    push(word.slice(0, -1))
  } else if (word.endsWith('s') && !word.endsWith('ss') && word.length > 3) {
    push(word.slice(0, -1))
  }
  if (word.endsWith('ing') && word.length > 5) {
    const stem = word.slice(0, -3)
    push(stem)
    push(`${stem}e`)
    if (stem.length >= 2 && stem.at(-1) === stem.at(-2)) {
      push(stem.slice(0, -1))
    }
  }
  if (word.endsWith('ed') && word.length > 4) {
    const stem = word.slice(0, -2)
    push(stem)
    push(`${stem}e`)
    if (stem.length >= 2 && stem.at(-1) === stem.at(-2)) {
      push(stem.slice(0, -1))
    }
  }
  if (word.endsWith('ly') && word.length > 4) {
    push(word.slice(0, -2))
  }

  return out
}

function resolveLemma(surface: string): {
  lemma: string
  level: CefrLevel | ''
} {
  for (const candidate of lemmaCandidates(surface)) {
    const level = pack.levels[candidate]
    if (level) return { lemma: candidate, level }
  }
  const fallback = normalizeApostrophe(surface).toLowerCase()
  return { lemma: fallback, level: '' }
}

function isContentWord(surface: string): boolean {
  const lower = normalizeApostrophe(surface).toLowerCase()
  if (lower.length < 3) return false
  if (LEARNING_STOPWORDS.has(lower)) return false
  if (/^\d+$/.test(lower)) return false
  return true
}

function isHardForLevel(
  level: CefrLevel | '',
  userLevel: LearningLevel,
  forceAllContent: boolean,
): boolean {
  if (forceAllContent) return true
  if (!level) {
    return true
  }
  return LEVEL_RANK[level] > USER_KNOWN_MAX_RANK[userLevel]
}

function glossFor(lemma: string, surface: string): string {
  return pack.gloss[lemma]
    || pack.gloss[normalizeApostrophe(surface).toLowerCase()]
    || ''
}

function rangesOverlap(
  start: number,
  end: number,
  spans: Array<{ start: number; end: number }>,
): boolean {
  return spans.some((span) => start < span.end && end > span.start)
}

/**
 * Pure-algorithm learning glosses for a finalized utterance.
 * Domain terminology (longest match) is preferred over CEFR hard words.
 */
export function annotateSentence(
  text: string,
  userLevel: LearningLevel,
  options: AnnotateOptions = {},
): LearningGloss[] {
  const source = text ?? ''
  if (!source.trim()) return []

  const maxGlosses = options.forceAllContent
    ? Number.POSITIVE_INFINITY
    : Math.max(1, options.maxGlosses ?? 3)
  const forceAllContent = Boolean(options.forceAllContent)
  const domains: readonly TermDomain[] = options.domains ?? DEFAULT_TERM_DOMAINS
  const termIndex = buildTermIndex(domains)

  type Candidate = LearningGloss & { rank: number }
  const candidates: Candidate[] = []

  // 1) Specialty terms — always hard, high priority, multi-word first.
  const termHits = findDomainTermMatches(source, termIndex)
  for (const hit of termHits) {
    candidates.push({
      start: hit.start,
      end: hit.end,
      surface: hit.surface,
      lemma: hit.key,
      zh: hit.zh,
      level: '',
      domain: hit.domain,
      rank: DOMAIN_TERM_RANK + hit.key.split(' ').length,
    })
  }

  // 2) CEFR / OOV single tokens outside term spans.
  TOKEN_RE.lastIndex = 0
  let match: RegExpExecArray | null
  while ((match = TOKEN_RE.exec(source)) !== null) {
    const surface = match[0]
    const start = match.index
    const end = start + surface.length
    if (rangesOverlap(start, end, termHits)) continue
    if (!isContentWord(surface)) continue
    const { lemma, level } = resolveLemma(surface)
    if (!isHardForLevel(level, userLevel, forceAllContent)) continue
    // Single-word term table can still supply a better gloss for OOV CEFR hits.
    const termHit = termIndex.get(lemma) || termIndex.get(normalizeApostrophe(surface).toLowerCase())
    const zh = termHit?.zh || glossFor(lemma, surface)
    const rank = termHit
      ? DOMAIN_TERM_RANK
      : level
        ? LEVEL_RANK[level]
        : 7
    candidates.push({
      start,
      end,
      surface,
      lemma,
      zh,
      level,
      domain: termHit?.domain,
      rank,
    })
  }

  candidates.sort((left, right) => {
    if (right.rank !== left.rank) return right.rank - left.rank
    return left.start - right.start
  })

  const selected: LearningGloss[] = []
  const usedKeys = new Set<string>()
  const takenSpans: Array<{ start: number; end: number }> = []
  for (const candidate of candidates) {
    if (selected.length >= maxGlosses) break
    if (usedKeys.has(candidate.lemma)) continue
    if (rangesOverlap(candidate.start, candidate.end, takenSpans)) continue
    usedKeys.add(candidate.lemma)
    takenSpans.push({ start: candidate.start, end: candidate.end })
    selected.push({
      start: candidate.start,
      end: candidate.end,
      surface: candidate.surface,
      lemma: candidate.lemma,
      zh: candidate.zh,
      level: candidate.level,
      domain: candidate.domain,
    })
  }

  selected.sort((left, right) => left.start - right.start)
  return selected
}

export function learningPackSource(): string {
  return pack.source || 'CEFR-J Vocabulary Profile'
}

export function isLearningLevel(value: unknown): value is LearningLevel {
  return value === 'A2' || value === 'B1' || value === 'B2'
}

export function isAssistMode(value: unknown): value is import('./types').AssistMode {
  return value === 'interpret' || value === 'learn'
}
