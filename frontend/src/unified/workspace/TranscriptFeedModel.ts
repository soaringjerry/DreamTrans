import type {
  TranscriptPartial,
  TranscriptSegment,
  TranslationPartial,
  TranslationSegment,
} from '../../core/transcription/types'
import type { TranscriptFeedItem } from '../feed'

export interface TranscriptFeedModelSnapshot {
  readonly version: number
  readonly generation: number
  /**
   * Intentionally stable, append-only backing storage. Rows are mutated only
   * immediately before a new snapshot is published, avoiding a full-array
   * clone for every streaming partial in a multi-hour session.
   */
  readonly items: readonly TranscriptFeedItem[]
}

export interface TranscriptFeedModelOptions {
  sourceLanguage: string
  targetLanguage: string
  translationEnabled: boolean
}

const DEFAULT_SPEAKER = 'Speaker'
const PARTIAL_MATCH_TOLERANCE_SECONDS = 2

function rangesTouch(
  left: { startTime?: number; endTime?: number; speaker: string },
  right: { startTime: number; endTime: number; speaker: string },
  allowSpeakerRevision = false,
): boolean {
  if (
    left.startTime === undefined
    || left.endTime === undefined
    || (!allowSpeakerRevision && left.speaker !== right.speaker)
  ) {
    return false
  }
  return (
    right.startTime <= left.endTime + PARTIAL_MATCH_TOLERANCE_SECONDS
    && right.endTime + PARTIAL_MATCH_TOLERANCE_SECONDS >= left.startTime
  )
}

function speakerLabel(speaker: string | undefined): string {
  return speaker?.trim() || DEFAULT_SPEAKER
}

/**
 * Incremental view model between the normalized transcript store and React.
 *
 * The final row array is never rebuilt during recording. A streaming row is
 * updated in place and promoted to a final row without changing its UI id,
 * which keeps virtual-list layout work independent of transcript length.
 */
export class TranscriptFeedModel {
  private readonly listeners = new Set<() => void>()
  private readonly segmentIndex = new Map<string, number>()
  private readonly itemsValue: TranscriptFeedItem[] = []
  private options: TranscriptFeedModelOptions
  private activePartialIndex: number | null = null
  private version = 0
  private generation = 0
  private snapshotValue: TranscriptFeedModelSnapshot

  constructor(options: TranscriptFeedModelOptions) {
    this.options = options
    this.snapshotValue = this.createSnapshot()
  }

  readonly subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  readonly getSnapshot = (): TranscriptFeedModelSnapshot => this.snapshotValue

  configure(options: Partial<TranscriptFeedModelOptions>): void {
    this.options = { ...this.options, ...options }
  }

  reset(options?: Partial<TranscriptFeedModelOptions>): void {
    this.itemsValue.length = 0
    this.segmentIndex.clear()
    this.activePartialIndex = null
    this.generation += 1
    if (options) this.options = { ...this.options, ...options }
    this.publish()
  }

  appendSegment(segment: TranscriptSegment): boolean {
    if (this.segmentIndex.has(segment.id)) return false
    const speaker = speakerLabel(segment.speaker)
    const partialIndex = this.activePartialIndex
    const partialItem = partialIndex === null ? undefined : this.itemsValue[partialIndex]
    const canPromote = partialItem !== undefined && rangesTouch(
      partialItem,
      {
        speaker,
        startTime: segment.startTime,
        endTime: segment.endTime,
      },
      true,
    )

    const item: TranscriptFeedItem = {
      id: canPromote ? partialItem.id : segment.id,
      speaker,
      speakerId: speaker,
      startTime: segment.startTime,
      endTime: segment.endTime,
      original: {
        text: segment.text,
        status: 'final',
        language: this.options.sourceLanguage,
      },
      ...(this.options.translationEnabled
        ? {
            translation: {
              status: 'pending' as const,
              language: this.options.targetLanguage,
            },
          }
        : {}),
    }

    let index: number
    if (canPromote && partialIndex !== null) {
      index = partialIndex
      this.itemsValue[index] = item
    } else {
      index = this.itemsValue.length
      this.itemsValue.push(item)
    }
    this.segmentIndex.set(segment.id, index)
    this.activePartialIndex = null
    this.publish()
    return true
  }

  setPartial(partial: TranscriptPartial): void {
    const speaker = speakerLabel(partial.speaker)
    const existingIndex = this.activePartialIndex
    const existing = existingIndex === null ? undefined : this.itemsValue[existingIndex]
    const item: TranscriptFeedItem = {
      id: existing?.id ?? `live:${partial.id}`,
      speaker,
      speakerId: speaker,
      startTime: partial.startTime,
      endTime: partial.endTime,
      original: {
        partialText: partial.text,
        status: 'streaming',
        language: this.options.sourceLanguage,
      },
      ...(this.options.translationEnabled
        ? {
            translation: {
              status: 'pending' as const,
              language: this.options.targetLanguage,
            },
          }
        : {}),
    }

    if (existingIndex === null) {
      this.activePartialIndex = this.itemsValue.length
      this.itemsValue.push(item)
    } else {
      this.itemsValue[existingIndex] = item
    }
    this.publish()
  }

  clearPartial(): void {
    const index = this.activePartialIndex
    if (index === null) return
    if (index === this.itemsValue.length - 1) this.itemsValue.pop()
    else this.itemsValue.splice(index, 1)
    this.activePartialIndex = null
    this.rebuildSegmentIndex(index)
    this.publish()
  }

  appendTranslation(translation: TranslationSegment): boolean {
    if (!translation.segmentId) return false
    const index = this.segmentIndex.get(translation.segmentId)
    if (index === undefined) return false
    const current = this.itemsValue[index]
    if (!current) return false
    this.itemsValue[index] = {
      ...current,
      translation: {
        text: translation.text,
        status: 'final',
        language: translation.language,
      },
    }
    this.publish()
    return true
  }

  setTranslationPartial(translation: TranslationPartial): boolean {
    if (!translation.segmentId) return false
    const index = this.segmentIndex.get(translation.segmentId)
    if (index === undefined) return false
    const current = this.itemsValue[index]
    if (!current) return false
    this.itemsValue[index] = {
      ...current,
      translation: {
        partialText: translation.text,
        status: 'streaming',
        language: translation.language,
      },
    }
    this.publish()
    return true
  }

  hydrate(
    segments: readonly TranscriptSegment[],
    translations: readonly TranslationSegment[],
  ): void {
    this.reset()
    for (const segment of segments) this.appendSegmentWithoutPublish(segment)
    for (const translation of translations) this.appendTranslationWithoutPublish(translation)
    this.publish()
  }

  private appendSegmentWithoutPublish(segment: TranscriptSegment): void {
    if (this.segmentIndex.has(segment.id)) return
    const speaker = speakerLabel(segment.speaker)
    const index = this.itemsValue.length
    this.itemsValue.push({
      id: segment.id,
      speaker,
      speakerId: speaker,
      startTime: segment.startTime,
      endTime: segment.endTime,
      original: {
        text: segment.text,
        status: 'final',
        language: this.options.sourceLanguage,
      },
      ...(this.options.translationEnabled
        ? {
            translation: {
              status: 'pending' as const,
              language: this.options.targetLanguage,
            },
          }
        : {}),
    })
    this.segmentIndex.set(segment.id, index)
  }

  private appendTranslationWithoutPublish(translation: TranslationSegment): void {
    if (!translation.segmentId) return
    const index = this.segmentIndex.get(translation.segmentId)
    if (index === undefined) return
    const current = this.itemsValue[index]
    if (!current) return
    this.itemsValue[index] = {
      ...current,
      translation: {
        text: translation.text,
        status: 'final',
        language: translation.language,
      },
    }
  }

  private rebuildSegmentIndex(removedIndex: number): void {
    for (const [segmentId, index] of this.segmentIndex) {
      if (index > removedIndex) {
        this.segmentIndex.set(segmentId, index - 1)
      }
    }
  }

  private publish(): void {
    this.version += 1
    this.snapshotValue = this.createSnapshot()
    for (const listener of [...this.listeners]) listener()
  }

  private createSnapshot(): TranscriptFeedModelSnapshot {
    return Object.freeze({
      version: this.version,
      generation: this.generation,
      items: this.itemsValue,
    })
  }
}
