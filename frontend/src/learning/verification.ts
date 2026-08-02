import { annotateSentence, lemmaCandidates } from './annotate'
import { DEFAULT_TERM_DOMAINS } from './terms'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message)
}

const easy = annotateSentence(
  'I go to school every day with my friends.',
  'B1',
)
assert(easy.length === 0, 'basic A1-ish sentence should need no glosses at B1')

const sample = annotateSentence(
  "I'm really interesting about human behavior and artificial intelligence today.",
  'A2',
  { maxGlosses: 5 },
)
assert(sample.length > 0, 'A2 learner should still see A2+ content words glossed')
assert(
  sample.some((item) => /behavior|artificial|intelligence|human/i.test(item.surface)),
  'A2 band content words in the sample sentence should be gloss candidates',
)

const hard = annotateSentence(
  'We should abandon the obsolete protocol immediately.',
  'A2',
  { maxGlosses: 3 },
)
assert(hard.length > 0, 'harder words should gloss for A2')
assert(
  hard.every((item) => item.end > item.start),
  'gloss ranges must be valid',
)
assert(
  hard.some((item) => item.lemma === 'abandon' || item.surface.toLowerCase() === 'abandon'),
  'abandon should be detected',
)
assert(
  hard.some((item) => item.zh.includes('放弃') || item.zh.length > 0),
  'abandon should carry a short Chinese gloss',
)

const capped = annotateSentence(
  'The comprehensive sophisticated methodology requires extraordinary perseverance.',
  'A2',
  { maxGlosses: 2 },
)
assert(capped.length <= 2, 'maxGlosses must cap output')

const forced = annotateSentence('Please finalize the checklist today.', 'B2', {
  forceAllContent: true,
  maxGlosses: 3,
})
assert(forced.length >= 1, 'forceAllContent should gloss content words')

const withTerms = annotateSentence(
  'Artificial intelligence and amino acid pathways appear in modern research.',
  'B2',
  { maxGlosses: 5, domains: DEFAULT_TERM_DOMAINS },
)
assert(
  withTerms.some((item) => /artificial intelligence/i.test(item.surface)),
  'multi-word AI term should match',
)
assert(
  withTerms.some((item) => item.domain === 'ai' && item.zh.includes('人工智能')),
  'AI term should carry domain gloss',
)
assert(
  withTerms.some((item) => /amino acid/i.test(item.surface) && item.domain === 'biology'),
  'biology multi-word term from ECDICT should match',
)

const noDomains = annotateSentence(
  'Artificial intelligence changes the industry.',
  'B2',
  { maxGlosses: 5, domains: [] },
)
assert(
  !noDomains.some((item) => item.domain === 'ai'),
  'disabled domains must not inject specialty terms',
)

assert(
  lemmaCandidates('running').includes('run')
  || lemmaCandidates('running').includes('running'),
  'lemma candidates should reduce -ing forms',
)

console.log('learning verification ok', {
  hard: hard.map((item) => `${item.surface}->${item.zh}`),
  capped: capped.map((item) => item.surface),
  forced: forced.map((item) => item.surface),
  terms: withTerms.map((item) => `${item.surface}[${item.domain ?? '-'}]->${item.zh}`),
})
