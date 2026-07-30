import type {
  AppendResult,
  RecordRange,
  SegmentId,
  TimeRange,
  TranscriptPartial,
  TranscriptPartialInput,
  TranscriptSegment,
  TranscriptSegmentInput,
  TranscriptStats,
  TranscriptStoreSnapshot,
  TranslationId,
  TranslationPartial,
  TranslationPartialInput,
  TranslationSegment,
  TranslationSegmentInput,
} from './types'

export interface TranscriptStoreOptions {
  readonly clock?: () => number
  readonly defaultSpeaker?: string
  readonly defaultSource?: string
  readonly partialMergeGapSeconds?: number
  readonly translationMatchToleranceSeconds?: number
}

const EMPTY_STATS: TranscriptStats = Object.freeze({
  segmentCount: 0,
  translationCount: 0,
  linkedTranslationCount: 0,
  speakerCount: 0,
  transcriptWordCount: 0,
  transcriptCharacterCount: 0,
  translationWordCount: 0,
  translationCharacterCount: 0,
  durationSeconds: 0,
})

const START_BUCKET_SECONDS = 0.5
const DEFAULT_RANGE_LIMIT = 100
const MAX_RANGE_LIMIT = 10_000

function finiteTime(value: number, field: string): number {
  if (!Number.isFinite(value)) {
    throw new TypeError(`${field} must be a finite number`)
  }
  return Math.max(0, value)
}

function normalizeRange(startValue: number, endValue: number): {
  startTime: number
  endTime: number
} {
  const startTime = finiteTime(startValue, 'startTime')
  const endTime = Math.max(startTime, finiteTime(endValue, 'endTime'))
  return { startTime, endTime }
}

function requiredText(value: string, field: string): string {
  const text = value.trim()
  if (!text) throw new TypeError(`${field} must not be empty`)
  return text
}

function normalizedLabel(value: string | undefined, fallback: string): string {
  return value?.trim() || fallback
}

function countWords(text: string): number {
  const matches = text.match(/[\p{L}\p{N}]+(?:['’_-][\p{L}\p{N}]+)*/gu)
  return matches?.length ?? 0
}

function hashString(value: string): string {
  let first = 0x811c9dc5
  let second = 0x9e3779b9

  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    first = Math.imul(first ^ code, 0x01000193)
    second = Math.imul(second ^ code, 0x85ebca6b)
    second ^= second >>> 13
  }

  return `${(first >>> 0).toString(36)}${(second >>> 0).toString(36)}`
}

function canonicalTime(value: number): string {
  return Math.round(value * 1000).toString(36)
}

export function createStableTranscriptId(
  input: Pick<TranscriptSegmentInput, 'speaker' | 'text' | 'startTime' | 'endTime'>,
): SegmentId {
  const { startTime, endTime } = normalizeRange(input.startTime, input.endTime)
  const speaker = input.speaker?.trim() || 'Speaker'
  const text = input.text.trim()
  const identity = `${speaker}\u0000${startTime.toFixed(3)}\u0000${endTime.toFixed(3)}\u0000${text}`
  return `seg_${canonicalTime(startTime)}_${canonicalTime(endTime)}_${hashString(identity)}`
}

export function createStableTranslationId(
  input: Pick<
    TranslationSegmentInput,
    'segmentId' | 'speaker' | 'language' | 'text' | 'startTime' | 'endTime'
  >,
): TranslationId {
  const { startTime, endTime } = normalizeRange(input.startTime, input.endTime)
  const speaker = input.speaker?.trim() || 'Speaker'
  const language = input.language.trim().toLowerCase()
  const text = input.text.trim()
  const identity = [
    input.segmentId ?? '',
    speaker,
    language,
    startTime.toFixed(3),
    endTime.toFixed(3),
    text,
  ].join('\u0000')
  return `tr_${canonicalTime(startTime)}_${language || 'und'}_${hashString(identity)}`
}

function sameTranscript(left: TranscriptSegment, right: TranscriptSegmentInput): boolean {
  return (
    left.speaker === (right.speaker?.trim() || left.speaker) &&
    left.text === right.text.trim() &&
    Math.abs(left.startTime - right.startTime) < 0.0005 &&
    Math.abs(left.endTime - right.endTime) < 0.0005
  )
}

function sameTranslation(left: TranslationSegment, right: TranslationSegmentInput): boolean {
  return (
    left.segmentId === (right.segmentId ?? left.segmentId) &&
    left.speaker === (right.speaker?.trim() || left.speaker) &&
    left.language === right.language.trim().toLowerCase() &&
    left.text === right.text.trim() &&
    Math.abs(left.startTime - right.startTime) < 0.0005 &&
    Math.abs(left.endTime - right.endTime) < 0.0005
  )
}

function rangeStart(range: RecordRange | undefined, count: number): {
  offset: number
  end: number
} {
  const requestedOffset = Math.trunc(range?.offset ?? 0)
  const requestedLimit = Math.trunc(range?.limit ?? DEFAULT_RANGE_LIMIT)
  const offset = Math.min(count, Math.max(0, requestedOffset))
  const limit = Math.min(MAX_RANGE_LIMIT, Math.max(0, requestedLimit))
  return { offset, end: Math.min(count, offset + limit) }
}

/**
 * Append-only, normalized storage for a single transcription session.
 *
 * The maps and order arrays remain private. Consumers read only the visible
 * range they need, which keeps partial updates and React snapshots O(1) with
 * respect to session length.
 */
export class TranscriptStore {
  private readonly clock: () => number
  private readonly defaultSpeaker: string
  private readonly defaultSource: string
  private readonly partialMergeGapSeconds: number
  private readonly translationMatchToleranceSeconds: number

  private readonly listeners = new Set<() => void>()
  private segmentsById = new Map<SegmentId, TranscriptSegment>()
  private segmentOrder: SegmentId[] = []
  private translationsById = new Map<TranslationId, TranslationSegment>()
  private translationOrder: TranslationId[] = []
  private translationIdsBySegment = new Map<SegmentId, Map<string, TranslationId[]>>()
  private segmentIdsBySpeakerBucket = new Map<string, Map<number, SegmentId[]>>()
  private speakers = new Set<string>()
  private activePartialValue: TranscriptPartial | null = null
  private activeTranslationPartialValues = new Map<string, TranslationPartial>()
  private statsValue: TranscriptStats = EMPTY_STATS
  private snapshotValue: TranscriptStoreSnapshot
  private version = 0
  private generation = 0
  private batchDepth = 0
  private batchChanged = false

  constructor(options: TranscriptStoreOptions = {}) {
    this.clock = options.clock ?? Date.now
    this.defaultSpeaker = options.defaultSpeaker?.trim() || 'Speaker'
    this.defaultSource = options.defaultSource?.trim() || 'speechmatics'
    this.partialMergeGapSeconds = Math.max(0, options.partialMergeGapSeconds ?? 1.5)
    this.translationMatchToleranceSeconds = Math.max(
      0,
      options.translationMatchToleranceSeconds ?? 0.75,
    )
    this.snapshotValue = this.buildSnapshot()
  }

  /**
   * Stable method references make these directly usable with
   * useSyncExternalStore(store.subscribe, store.getSnapshot).
   */
  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  readonly getSnapshot = (): TranscriptStoreSnapshot => this.snapshotValue

  get size(): number {
    return this.segmentOrder.length
  }

  get translationSize(): number {
    return this.translationOrder.length
  }

  appendTranscript(input: TranscriptSegmentInput): AppendResult<TranscriptSegment> {
    const text = requiredText(input.text, 'Transcript text')
    const { startTime, endTime } = normalizeRange(input.startTime, input.endTime)
    const speaker = normalizedLabel(input.speaker, this.defaultSpeaker)
    const normalizedInput: TranscriptSegmentInput = {
      ...input,
      text,
      speaker,
      startTime,
      endTime,
    }
    const id = input.id?.trim() || createStableTranscriptId(normalizedInput)
    const existing = this.segmentsById.get(id)

    if (existing) {
      if (!sameTranscript(existing, normalizedInput)) {
        throw new Error(`Transcript segment id collision: ${id}`)
      }
      return Object.freeze({ record: existing, inserted: false })
    }

    const segment: TranscriptSegment = Object.freeze({
      id,
      sequence: this.segmentOrder.length,
      speaker,
      text,
      status: 'final',
      startTime,
      endTime,
      receivedAt: input.receivedAt ?? this.clock(),
      source: normalizedLabel(input.source, this.defaultSource),
    })

    this.segmentsById.set(id, segment)
    this.segmentOrder.push(id)
    this.addSegmentToTimeIndex(segment)
    this.speakers.add(speaker)

    const clearsPartial =
      this.activePartialValue !== null &&
      this.rangesBelongTogether(this.activePartialValue, segment, true)
    if (clearsPartial) this.activePartialValue = null

    this.statsValue = Object.freeze({
      ...this.statsValue,
      segmentCount: this.statsValue.segmentCount + 1,
      speakerCount: this.speakers.size,
      transcriptWordCount: this.statsValue.transcriptWordCount + countWords(text),
      transcriptCharacterCount: this.statsValue.transcriptCharacterCount + text.length,
      durationSeconds: Math.max(this.statsValue.durationSeconds, endTime),
    })
    this.markChanged()
    return Object.freeze({ record: segment, inserted: true })
  }

  setPartial(input: TranscriptPartialInput): TranscriptPartial | null {
    const text = input.text.trim()
    if (!text) {
      this.clearPartial()
      return null
    }

    const { startTime: incomingStart, endTime: incomingEnd } = normalizeRange(
      input.startTime,
      input.endTime,
    )
    const speaker = normalizedLabel(input.speaker, this.defaultSpeaker)
    const source = normalizedLabel(input.source, this.defaultSource)
    const receivedAt = input.receivedAt ?? this.clock()
    const current = this.activePartialValue
    const merge =
      current !== null &&
      this.rangesBelongTogether(
        current,
        { startTime: incomingStart, endTime: incomingEnd, speaker },
        false,
      )
    const startTime = merge ? Math.min(current.startTime, incomingStart) : incomingStart
    const endTime = merge ? Math.max(current.endTime, incomingEnd) : incomingEnd
    const id =
      input.id?.trim() ||
      (merge
        ? current.id
        : `partial_${canonicalTime(startTime)}_${hashString(`${speaker}\u0000${startTime.toFixed(3)}`)}`)

    if (
      current &&
      current.id === id &&
      current.text === text &&
      current.speaker === speaker &&
      current.startTime === startTime &&
      current.endTime === endTime
    ) {
      return current
    }

    const partial: TranscriptPartial = Object.freeze({
      id,
      speaker,
      text,
      status: 'partial',
      startTime,
      endTime,
      revision: merge ? current.revision + 1 : 0,
      createdAt: merge ? current.createdAt : receivedAt,
      updatedAt: receivedAt,
      source,
    })
    this.activePartialValue = partial
    this.markChanged()
    return partial
  }

  clearPartial(expectedId?: string): boolean {
    if (!this.activePartialValue) return false
    if (expectedId !== undefined && this.activePartialValue.id !== expectedId) return false
    this.activePartialValue = null
    this.markChanged()
    return true
  }

  appendTranslation(input: TranslationSegmentInput): AppendResult<TranslationSegment> {
    const text = requiredText(input.text, 'Translation text')
    const language = requiredText(input.language, 'Translation language').toLowerCase()
    const { startTime, endTime } = normalizeRange(input.startTime, input.endTime)
    const speaker = normalizedLabel(input.speaker, this.defaultSpeaker)
    const segmentId =
      input.segmentId === null
        ? null
        : input.segmentId ?? this.findSegmentId(speaker, startTime, endTime)
    const normalizedInput: TranslationSegmentInput = {
      ...input,
      text,
      language,
      speaker,
      segmentId,
      startTime,
      endTime,
    }
    const id = input.id?.trim() || createStableTranslationId(normalizedInput)
    const existing = this.translationsById.get(id)

    if (existing) {
      if (!sameTranslation(existing, normalizedInput)) {
        throw new Error(`Translation id collision: ${id}`)
      }
      return Object.freeze({ record: existing, inserted: false })
    }

    if (segmentId !== null && !this.segmentsById.has(segmentId)) {
      throw new Error(`Cannot link translation to unknown transcript segment: ${segmentId}`)
    }

    const translation: TranslationSegment = Object.freeze({
      id,
      sequence: this.translationOrder.length,
      segmentId,
      speaker,
      language,
      text,
      status: 'final',
      startTime,
      endTime,
      receivedAt: input.receivedAt ?? this.clock(),
      source: normalizedLabel(input.source, this.defaultSource),
    })
    this.translationsById.set(id, translation)
    this.translationOrder.push(id)

    if (segmentId !== null) {
      let byLanguage = this.translationIdsBySegment.get(segmentId)
      if (!byLanguage) {
        byLanguage = new Map()
        this.translationIdsBySegment.set(segmentId, byLanguage)
      }
      const languageIds = byLanguage.get(language)
      if (languageIds) languageIds.push(id)
      else byLanguage.set(language, [id])
    }

    const partialKey = this.translationPartialKey(language, speaker)
    const activeTranslationPartial = this.activeTranslationPartialValues.get(partialKey)
    if (
      activeTranslationPartial &&
      this.rangesBelongTogether(activeTranslationPartial, translation, true)
    ) {
      this.activeTranslationPartialValues.delete(partialKey)
    }

    this.statsValue = Object.freeze({
      ...this.statsValue,
      translationCount: this.statsValue.translationCount + 1,
      linkedTranslationCount:
        this.statsValue.linkedTranslationCount + (segmentId === null ? 0 : 1),
      translationWordCount: this.statsValue.translationWordCount + countWords(text),
      translationCharacterCount: this.statsValue.translationCharacterCount + text.length,
      durationSeconds: Math.max(this.statsValue.durationSeconds, endTime),
    })
    this.markChanged()
    return Object.freeze({ record: translation, inserted: true })
  }

  /**
   * Link a final translation that arrived before its transcript. Speechmatics
   * may deliver the two final events out of order; keeping the same id and
   * sequence lets persistence update the existing record instead of appending
   * a duplicate.
   */
  relinkTranslation(
    translationId: TranslationId,
    segmentId: SegmentId,
  ): TranslationSegment | null {
    const existing = this.translationsById.get(translationId)
    if (!existing) return null
    if (existing.segmentId === segmentId) return existing
    if (existing.segmentId !== null) return existing
    if (!this.segmentsById.has(segmentId)) {
      throw new Error(`Cannot link translation to unknown transcript segment: ${segmentId}`)
    }

    const linked = Object.freeze({ ...existing, segmentId })
    this.translationsById.set(translationId, linked)
    let byLanguage = this.translationIdsBySegment.get(segmentId)
    if (!byLanguage) {
      byLanguage = new Map()
      this.translationIdsBySegment.set(segmentId, byLanguage)
    }
    const languageIds = byLanguage.get(linked.language)
    if (languageIds) languageIds.push(translationId)
    else byLanguage.set(linked.language, [translationId])
    this.statsValue = Object.freeze({
      ...this.statsValue,
      linkedTranslationCount: this.statsValue.linkedTranslationCount + 1,
    })
    this.markChanged()
    return linked
  }

  setTranslationPartial(input: TranslationPartialInput): TranslationPartial | null {
    const text = input.text.trim()
    const language = requiredText(input.language, 'Translation language').toLowerCase()
    const speaker = normalizedLabel(input.speaker, this.defaultSpeaker)
    const key = this.translationPartialKey(language, speaker)

    if (!text) {
      this.clearTranslationPartial(language, speaker)
      return null
    }

    const { startTime: incomingStart, endTime: incomingEnd } = normalizeRange(
      input.startTime,
      input.endTime,
    )
    const current = this.activeTranslationPartialValues.get(key)
    const merge =
      current !== undefined &&
      this.rangesBelongTogether(
        current,
        { startTime: incomingStart, endTime: incomingEnd, speaker },
        false,
      )
    const startTime = merge ? Math.min(current.startTime, incomingStart) : incomingStart
    const endTime = merge ? Math.max(current.endTime, incomingEnd) : incomingEnd
    const receivedAt = input.receivedAt ?? this.clock()
    const segmentId =
      input.segmentId === null
        ? null
        : input.segmentId ?? this.findSegmentId(speaker, startTime, endTime)
    const id =
      input.id?.trim() ||
      (merge
        ? current.id
        : `translation_partial_${language}_${canonicalTime(startTime)}_${hashString(speaker)}`)

    if (
      current &&
      current.id === id &&
      current.text === text &&
      current.startTime === startTime &&
      current.endTime === endTime
    ) {
      return current
    }

    const partial: TranslationPartial = Object.freeze({
      id,
      segmentId,
      speaker,
      language,
      text,
      status: 'partial',
      startTime,
      endTime,
      revision: merge ? current.revision + 1 : 0,
      createdAt: merge ? current.createdAt : receivedAt,
      updatedAt: receivedAt,
      source: normalizedLabel(input.source, this.defaultSource),
    })
    this.activeTranslationPartialValues.set(key, partial)
    this.markChanged()
    return partial
  }

  clearTranslationPartial(language: string, speaker?: string): boolean {
    const key = this.translationPartialKey(
      language.trim().toLowerCase(),
      normalizedLabel(speaker, this.defaultSpeaker),
    )
    const removed = this.activeTranslationPartialValues.delete(key)
    if (removed) this.markChanged()
    return removed
  }

  clearTranslationPartials(): boolean {
    if (this.activeTranslationPartialValues.size === 0) return false
    this.activeTranslationPartialValues.clear()
    this.markChanged()
    return true
  }

  getSegment(id: SegmentId): TranscriptSegment | undefined {
    return this.segmentsById.get(id)
  }

  getSegmentAt(index: number): TranscriptSegment | undefined {
    const normalizedIndex = Math.trunc(index)
    if (normalizedIndex < 0) return undefined
    const id = this.segmentOrder[normalizedIndex]
    return id === undefined ? undefined : this.segmentsById.get(id)
  }

  getSegments(range?: RecordRange): readonly TranscriptSegment[] {
    const { offset, end } = rangeStart(range, this.segmentOrder.length)
    const records: TranscriptSegment[] = []
    for (let index = offset; index < end; index += 1) {
      const id = this.segmentOrder[index]
      if (id === undefined) continue
      const segment = this.segmentsById.get(id)
      if (segment) records.push(segment)
    }
    return Object.freeze(records)
  }

  getLatestSegments(limit = DEFAULT_RANGE_LIMIT): readonly TranscriptSegment[] {
    const normalizedLimit = Math.min(MAX_RANGE_LIMIT, Math.max(0, Math.trunc(limit)))
    return this.getSegments({
      offset: Math.max(0, this.segmentOrder.length - normalizedLimit),
      limit: normalizedLimit,
    })
  }

  getTranslation(id: TranslationId): TranslationSegment | undefined {
    return this.translationsById.get(id)
  }

  getTranslationAt(index: number): TranslationSegment | undefined {
    const normalizedIndex = Math.trunc(index)
    if (normalizedIndex < 0) return undefined
    const id = this.translationOrder[normalizedIndex]
    return id === undefined ? undefined : this.translationsById.get(id)
  }

  getTranslations(range?: RecordRange): readonly TranslationSegment[] {
    const { offset, end } = rangeStart(range, this.translationOrder.length)
    const records: TranslationSegment[] = []
    for (let index = offset; index < end; index += 1) {
      const id = this.translationOrder[index]
      if (id === undefined) continue
      const translation = this.translationsById.get(id)
      if (translation) records.push(translation)
    }
    return Object.freeze(records)
  }

  getTranslationsForSegment(
    segmentId: SegmentId,
    language?: string,
  ): readonly TranslationSegment[] {
    const byLanguage = this.translationIdsBySegment.get(segmentId)
    if (!byLanguage) return Object.freeze([])

    const ids =
      language === undefined
        ? Array.from(byLanguage.values()).flat()
        : [...(byLanguage.get(language.trim().toLowerCase()) ?? [])]
    ids.sort((left, right) => {
      const leftSequence = this.translationsById.get(left)?.sequence ?? 0
      const rightSequence = this.translationsById.get(right)?.sequence ?? 0
      return leftSequence - rightSequence
    })
    const records = ids.flatMap((id) => {
      const translation = this.translationsById.get(id)
      return translation ? [translation] : []
    })
    return Object.freeze(records)
  }

  getLatestTranslationForSegment(
    segmentId: SegmentId,
    language: string,
  ): TranslationSegment | undefined {
    const ids = this.translationIdsBySegment
      .get(segmentId)
      ?.get(language.trim().toLowerCase())
    const id = ids?.at(-1)
    return id === undefined ? undefined : this.translationsById.get(id)
  }

  findSegmentId(speaker: string, startTime: number, endTime: number): SegmentId | null {
    const normalizedSpeaker = normalizedLabel(speaker, this.defaultSpeaker)
    const range = normalizeRange(startTime, endTime)
    const buckets = this.segmentIdsBySpeakerBucket.get(normalizedSpeaker)
    if (!buckets) return null

    const tolerance = this.translationMatchToleranceSeconds
    const firstBucket = Math.floor((range.startTime - tolerance) / START_BUCKET_SECONDS)
    const lastBucket = Math.floor((range.startTime + tolerance) / START_BUCKET_SECONDS)
    let bestId: SegmentId | null = null
    let bestScore = Number.POSITIVE_INFINITY

    for (let bucket = firstBucket; bucket <= lastBucket; bucket += 1) {
      const ids = buckets.get(bucket)
      if (!ids) continue
      for (const id of ids) {
        const candidate = this.segmentsById.get(id)
        if (!candidate) continue
        const startDelta = Math.abs(candidate.startTime - range.startTime)
        const endDelta = Math.abs(candidate.endTime - range.endTime)
        const overlaps =
          candidate.startTime <= range.endTime + tolerance &&
          candidate.endTime + tolerance >= range.startTime
        if (!overlaps || startDelta > tolerance) continue
        const score = startDelta * 2 + endDelta
        if (score < bestScore) {
          bestScore = score
          bestId = id
        }
      }
    }

    return bestId
  }

  /**
   * Coalesces notifications while hydrating/importing many final records.
   */
  batch<T>(operation: () => T): T {
    this.batchDepth += 1
    try {
      return operation()
    } finally {
      this.batchDepth -= 1
      if (this.batchDepth === 0 && this.batchChanged) {
        this.batchChanged = false
        this.commitSnapshot()
      }
    }
  }

  reset(): void {
    this.segmentsById = new Map()
    this.segmentOrder = []
    this.translationsById = new Map()
    this.translationOrder = []
    this.translationIdsBySegment = new Map()
    this.segmentIdsBySpeakerBucket = new Map()
    this.speakers = new Set()
    this.activePartialValue = null
    this.activeTranslationPartialValues = new Map()
    this.statsValue = EMPTY_STATS
    this.generation += 1
    this.markChanged()
  }

  private addSegmentToTimeIndex(segment: TranscriptSegment): void {
    let speakerBuckets = this.segmentIdsBySpeakerBucket.get(segment.speaker)
    if (!speakerBuckets) {
      speakerBuckets = new Map()
      this.segmentIdsBySpeakerBucket.set(segment.speaker, speakerBuckets)
    }
    const bucket = Math.floor(segment.startTime / START_BUCKET_SECONDS)
    const ids = speakerBuckets.get(bucket)
    if (ids) ids.push(segment.id)
    else speakerBuckets.set(bucket, [segment.id])
  }

  private rangesBelongTogether(
    current: TimeRange & { readonly speaker: string },
    incoming: TimeRange & { readonly speaker: string },
    allowSpeakerRevision: boolean,
  ): boolean {
    if (!allowSpeakerRevision && current.speaker !== incoming.speaker) return false
    const touches =
      incoming.startTime <= current.endTime + this.partialMergeGapSeconds &&
      incoming.endTime + this.partialMergeGapSeconds >= current.startTime
    return touches
  }

  private translationPartialKey(language: string, speaker: string): string {
    return `${language}\u0000${speaker}`
  }

  private markChanged(): void {
    if (this.batchDepth > 0) {
      this.batchChanged = true
      return
    }
    this.commitSnapshot()
  }

  private commitSnapshot(): void {
    this.version += 1
    this.snapshotValue = this.buildSnapshot()
    for (const listener of [...this.listeners]) listener()
  }

  private buildSnapshot(): TranscriptStoreSnapshot {
    return Object.freeze({
      version: this.version,
      generation: this.generation,
      segmentCount: this.segmentOrder.length,
      translationCount: this.translationOrder.length,
      activePartial: this.activePartialValue,
      activeTranslationPartials: Object.freeze([
        ...this.activeTranslationPartialValues.values(),
      ]),
      latestSegmentId: this.segmentOrder.at(-1) ?? null,
      latestTranslationId: this.translationOrder.at(-1) ?? null,
      stats: this.statsValue,
    })
  }
}
