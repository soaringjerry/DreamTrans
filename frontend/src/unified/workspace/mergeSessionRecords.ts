import {
  createStableTranscriptId,
  createStableTranslationId,
} from '../../core/transcription'
import type {
  TranscriptSegment,
  TranslationSegment,
} from '../../core/transcription/types'

export interface StoredSessionRecords {
  segments: TranscriptSegment[]
  translations: TranslationSegment[]
}

export interface SessionRecordMergeResult extends StoredSessionRecords {
  addedSegments: number
  addedTranslations: number
}

function segmentIdentity(segment: TranscriptSegment): string {
  return [
    segment.speaker,
    segment.startTime.toFixed(3),
    segment.endTime.toFixed(3),
    segment.text,
  ].join('\u0000')
}

function translationScope(translation: TranslationSegment): string {
  return [
    translation.segmentId ?? '',
    translation.language.toLocaleLowerCase(),
    translation.segmentId ? '' : translation.speaker,
    translation.segmentId ? '' : translation.startTime.toFixed(3),
    translation.segmentId ? '' : translation.endTime.toFixed(3),
  ].join('\u0000')
}

function finiteTime(value: number, field: string): number {
  if (!Number.isFinite(value)) {
    throw new TypeError(`${field} must be a finite number`)
  }
  return Math.max(0, value)
}

function requiredText(value: string, field: string): string {
  const text = value.trim()
  if (!text) throw new TypeError(`${field} must not be empty`)
  return text
}

function denseTranscript(
  input: TranscriptSegment,
  sequence: number,
): TranscriptSegment {
  const text = requiredText(input.text, 'Transcript text')
  const startTime = finiteTime(input.startTime, 'startTime')
  const endTime = Math.max(startTime, finiteTime(input.endTime, 'endTime'))
  const speaker = input.speaker.trim() || 'Speaker'
  const source = input.source.trim() || 'speechmatics'
  const id = input.id.trim() || createStableTranscriptId({
    speaker,
    text,
    startTime,
    endTime,
  })
  if (
    Object.isFrozen(input)
    && input.id === id
    && input.sequence === sequence
    && input.speaker === speaker
    && input.text === text
    && input.startTime === startTime
    && input.endTime === endTime
    && input.source === source
  ) {
    return input
  }
  return Object.freeze({
    id,
    sequence,
    speaker,
    text,
    status: 'final',
    startTime,
    endTime,
    receivedAt: input.receivedAt,
    source,
  })
}

function denseTranslation(
  input: TranslationSegment,
  sequence: number,
  segmentId: string | null,
): TranslationSegment {
  const text = requiredText(input.text, 'Translation text')
  const language = requiredText(input.language, 'Translation language').toLowerCase()
  const startTime = finiteTime(input.startTime, 'startTime')
  const endTime = Math.max(startTime, finiteTime(input.endTime, 'endTime'))
  const speaker = input.speaker.trim() || 'Speaker'
  const source = input.source.trim() || 'speechmatics'
  const id = input.id.trim() || createStableTranslationId({
    segmentId,
    speaker,
    language,
    text,
    startTime,
    endTime,
  })
  if (
    Object.isFrozen(input)
    && input.id === id
    && input.sequence === sequence
    && input.segmentId === segmentId
    && input.speaker === speaker
    && input.language === language
    && input.text === text
    && input.startTime === startTime
    && input.endTime === endTime
    && input.source === source
  ) {
    return input
  }
  return Object.freeze({
    id,
    sequence,
    segmentId,
    speaker,
    language,
    text,
    status: 'final',
    startTime,
    endTime,
    receivedAt: input.receivedAt,
    source,
  })
}

/**
 * Merges an incoming cloud snapshot into the local append-only snapshot.
 *
 * Local records win on identity conflicts. Cloud-only records are appended,
 * and the result is always dense. This is deliberately not a "replace from
 * cloud" operation: a partial cloud sync must never delete locally persisted
 * transcript or translation records.
 */
export function mergeSessionRecords(
  local: StoredSessionRecords,
  incoming: StoredSessionRecords,
): SessionRecordMergeResult {
  const segments: TranscriptSegment[] = []
  const translations: TranslationSegment[] = []
  const segmentIds = new Set<string>()
  const segmentIdentities = new Set<string>()
  const translationIds = new Set<string>()
  const translationScopes = new Set<string>()

  for (const segment of local.segments) {
    const inputID = segment.id.trim()
    if (inputID && segmentIds.has(inputID)) continue
    const record = denseTranscript(segment, segments.length)
    if (segmentIds.has(record.id)) continue
    segments.push(record)
    segmentIds.add(record.id)
    segmentIdentities.add(segmentIdentity(record))
  }

  for (const segment of incoming.segments) {
    try {
      const inputID = segment.id.trim()
      if (
        (inputID && segmentIds.has(inputID))
        || segmentIdentities.has(segmentIdentity(segment))
      ) {
        continue
      }
      const record = denseTranscript(segment, segments.length)
      if (
        segmentIds.has(record.id)
        || segmentIdentities.has(segmentIdentity(record))
      ) {
        continue
      }
      segments.push(record)
      segmentIds.add(record.id)
      segmentIdentities.add(segmentIdentity(record))
    } catch {
      // Invalid remote data and remote id collisions never replace a valid
      // local record.
    }
  }

  const appendTranslation = (
    translation: TranslationSegment,
    localRecord: boolean,
  ) => {
    const requestedScope = translationScope(translation)
    if (!localRecord && translationScopes.has(requestedScope)) return
    try {
      const inputID = translation.id.trim()
      if (inputID && translationIds.has(inputID)) return
      // A legacy translation can point at a transcript that no longer exists.
      // Preserve its text as an orphan without building TranscriptStore's
      // speaker/time indexes solely to discover that the link is invalid.
      const segmentId = translation.segmentId !== null
        && segmentIds.has(translation.segmentId)
        ? translation.segmentId
        : null
      const record = denseTranslation(translation, translations.length, segmentId)
      if (translationIds.has(record.id)) return
      translations.push(record)
      translationIds.add(record.id)
      translationScopes.add(translationScope(record))
    } catch {
      // Invalid or colliding incoming data is ignored; valid local records
      // already added above remain authoritative.
    }
  }

  for (const translation of local.translations) appendTranslation(translation, true)
  for (const translation of incoming.translations) appendTranslation(translation, false)

  return {
    segments,
    translations,
    addedSegments: Math.max(0, segments.length - local.segments.length),
    addedTranslations: Math.max(0, translations.length - local.translations.length),
  }
}
