export type LearningLevel = 'A2' | 'B1' | 'B2'

/** Reading assistance: full AI interpretation vs original-first learning. */
export type AssistMode = 'interpret' | 'learn'

export type CefrLevel = 'A1' | 'A2' | 'B1' | 'B2' | 'C1' | 'C2'

export type TermDomain =
  | 'ai'
  | 'internet'
  | 'psychology'
  | 'geography'
  | 'biology'

export interface TermDomainMeta {
  id: TermDomain
  label: string
  termCount: number
}

export interface LearningGloss {
  /** Inclusive start index into the original string (UTF-16 code units). */
  start: number
  /** Exclusive end index. */
  end: number
  surface: string
  lemma: string
  /** Short Chinese gloss when available. */
  zh: string
  /** CEFR label when known from the pack; empty if term/OOV. */
  level: CefrLevel | ''
  /** Set when the gloss comes from a domain terminology pack. */
  domain?: TermDomain
}

export interface AnnotateOptions {
  /** Max glosses per sentence (default 3). Domain terms still compete for slots. */
  maxGlosses?: number
  /** Gloss every content word, not only hard ones. */
  forceAllContent?: boolean
  /** Enabled specialty term domains. Empty disables term packs. */
  domains?: readonly TermDomain[]
}

export interface LearningPack {
  levels: Record<string, CefrLevel>
  gloss: Record<string, string>
  source?: string
}
