import { TranscriptStore } from '../../core/transcription'
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
  const store = new TranscriptStore()
  const segments: TranscriptSegment[] = []
  const translations: TranslationSegment[] = []
  const segmentIdentities = new Set<string>()
  const translationScopes = new Set<string>()

  store.batch(() => {
    for (const segment of local.segments) {
      const result = store.appendTranscript(segment)
      if (!result.inserted) continue
      segments.push(result.record)
      segmentIdentities.add(segmentIdentity(result.record))
    }

    for (const segment of incoming.segments) {
      if (store.getSegment(segment.id) || segmentIdentities.has(segmentIdentity(segment))) {
        continue
      }
      try {
        const result = store.appendTranscript(segment)
        if (!result.inserted) continue
        segments.push(result.record)
        segmentIdentities.add(segmentIdentity(result.record))
      } catch {
        // A remote id collision must not replace a different local record.
      }
    }

    const appendTranslation = (
      translation: TranslationSegment,
      localRecord: boolean,
    ) => {
      const scope = translationScope(translation)
      if (!localRecord && translationScopes.has(scope)) return
      try {
        const result = store.appendTranslation(translation)
        if (!result.inserted) return
        translations.push(result.record)
        translationScopes.add(translationScope(result.record))
      } catch {
        // Preserve an orphaned legacy translation rather than dropping it
        // merely because its former transcript id no longer exists.
        try {
          const result = store.appendTranslation({ ...translation, segmentId: null })
          if (!result.inserted) return
          translations.push(result.record)
          translationScopes.add(translationScope(result.record))
        } catch {
          // Invalid or colliding incoming data is ignored; valid local records
          // already added above remain authoritative.
        }
      }
    }

    for (const translation of local.translations) appendTranslation(translation, true)
    for (const translation of incoming.translations) appendTranslation(translation, false)
  })

  return {
    segments,
    translations,
    addedSegments: Math.max(0, segments.length - local.segments.length),
    addedTranslations: Math.max(0, translations.length - local.translations.length),
  }
}
