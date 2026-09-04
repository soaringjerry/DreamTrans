/**
 * Script-aware text helpers shared by the transcript display model and the
 * AI translation batcher.
 *
 * Every length threshold in the pipeline was tuned on English, where a
 * character is roughly one fifth of a word. A Han character or a kana
 * syllable carries far more meaning, so counting raw characters makes
 * Chinese and Japanese cards run several times longer than English ones
 * before they break, and makes short CJK sentences wait for more text before
 * they are translated. `textWeight` measures text in Latin-equivalent
 * characters so one set of thresholds works for every supported script.
 */

/**
 * Scripts written without spaces between words: Han, kana, bopomofo, CJK
 * symbols/punctuation, fullwidth forms. Hangul is deliberately excluded
 * because Korean uses spaces between words.
 */
const SPACELESS_CJK_RANGES =
  '\\u2E80-\\u303F\\u3040-\\u30FF\\u3100-\\u312F\\u31C0-\\u9FFF\\uF900-\\uFAFF\\uFF00-\\uFFEF'
const SPACELESS_CJK_PATTERN = new RegExp(`[${SPACELESS_CJK_RANGES}]`, 'u')

/**
 * Scripts whose typography does not put a space between a word and a joined
 * fragment: the spaceless scripts above plus Hangul, whose provider fragments
 * already carry their own spacing.
 */
const CJK_PATTERN = new RegExp(`[${SPACELESS_CJK_RANGES}\\u3130-\\u318F\\uAC00-\\uD7AF]`, 'u')

const SENTENCE_END_PATTERN = /[.!?。！？…]["')\]»”’」』）】]*\s*$/u
const LEADING_NO_SPACE_PATTERN = /^[,.;:!?%)\]}»”’…、，。；：！？』」）】]/u

/** Whitespace wedged between two spaceless-script characters. */
const CJK_INNER_SPACE_PATTERN = new RegExp(
  `([${SPACELESS_CJK_RANGES}])\\s+(?=[${SPACELESS_CJK_RANGES}])`,
  'gu',
)
/** Whitespace before fullwidth punctuation ("你好 。" → "你好。"). */
const SPACE_BEFORE_CJK_PUNCTUATION_PATTERN = /\s+(?=[、，。；：！？』」）】…])/gu

/**
 * Approximate reading weight of one CJK character in Latin characters. A
 * Chinese sentence of 20 characters carries about as much as an English
 * sentence of 12 words (~65 characters).
 */
const CJK_CHAR_WEIGHT = 3

export function isCjkChar(char: string): boolean {
  return CJK_PATTERN.test(char)
}

export function endsSentence(text: string): boolean {
  return SENTENCE_END_PATTERN.test(text)
}

/**
 * Length of `text` in Latin-equivalent characters. Latin, Cyrillic, Hangul
 * and digits count 1 each; Han, kana and fullwidth forms count
 * `CJK_CHAR_WEIGHT`. Whitespace counts 1 like any other separator.
 */
export function textWeight(text: string): number {
  let weight = 0
  for (const char of text) {
    weight += SPACELESS_CJK_PATTERN.test(char) ? CJK_CHAR_WEIGHT : 1
  }
  return weight
}

/**
 * Punctuation-aware concatenation: "Hi." + "Hello." → "Hi. Hello.",
 * "你好。" + "今天" → "你好。今天" (no space between CJK), and no space
 * before trailing punctuation fragments.
 */
export function joinSegmentTexts(left: string, right: string): string {
  const head = left.trimEnd()
  const tail = right.trim()
  if (!head) return tail
  if (!tail) return head
  const lastChar = head[head.length - 1] ?? ''
  const firstChar = tail[0] ?? ''
  if (
    LEADING_NO_SPACE_PATTERN.test(tail)
    || CJK_PATTERN.test(lastChar)
    || CJK_PATTERN.test(firstChar)
  ) {
    return `${head}${tail}`
  }
  return `${head} ${tail}`
}

/**
 * Provider transcripts for Chinese and Japanese arrive word-segmented with
 * spaces ("我们 今天 讨论 一下 。"). Readers of those scripts expect none, and
 * the spaces also confuse sentence detection and length measurement, so they
 * are removed wherever both neighbours are spaceless-script characters.
 * Korean, Latin and mixed-script text keep their spaces.
 */
export function normalizeTranscriptText(text: string): string {
  const trimmed = text.trim()
  if (!trimmed || !SPACELESS_CJK_PATTERN.test(trimmed)) return trimmed
  return trimmed
    .replace(CJK_INNER_SPACE_PATTERN, '$1')
    .replace(SPACE_BEFORE_CJK_PUNCTUATION_PATTERN, '')
}
