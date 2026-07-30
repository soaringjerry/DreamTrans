import type { CSSProperties, ReactNode } from 'react'

/**
 * The feed always keeps one logical item per utterance. Switching modes only
 * changes which tracks are rendered; it never filters partial or pending rows.
 */
export type TranscriptFeedMode = 'original' | 'bilingual' | 'translation'

export type TranscriptTrackStatus = 'pending' | 'streaming' | 'final' | 'error'

export interface TranscriptFeedTrack {
  /** Committed text. */
  text?: string
  /** Uncommitted tail. Keep this separate from text so it can update cheaply. */
  partialText?: string
  status?: TranscriptTrackStatus
  language?: string
  errorMessage?: string
}

export interface TranscriptFeedItem {
  /** Stable and unique for the lifetime of the feed. */
  id: string
  speaker: string
  speakerId?: string
  startTime?: number
  endTime?: number
  /**
   * Atomic transcript segments aggregated into this display card. The
   * underlying store keeps one record per provider final; the feed merges
   * short same-speaker fragments into readable utterances.
   */
  segmentIds?: readonly string[]
  original?: TranscriptFeedTrack
  translation?: TranscriptFeedTrack
}

export interface TranscriptFeedLabels {
  originalTrack: string
  translationTrack: string
  originalPending: string
  translationPending: string
  originalUnavailable: string
  translationUnavailable: string
  streaming: string
  failed: string
  unknownSpeaker: string
  empty: string
  scrollRegion: string
  keyboardHelp: string
  returnToLive: string
  newItems: (count: number) => string
}

export interface TranscriptFeedProps {
  items: readonly TranscriptFeedItem[]
  mode: TranscriptFeedMode
  className?: string
  style?: CSSProperties
  ariaLabel?: string
  labels?: Partial<TranscriptFeedLabels>
  emptyState?: ReactNode
  /**
   * Extra rows mounted before and after the visible range. The DOM still only
   * contains the visible range plus this overscan.
   */
  overscan?: number
  /** Initial row-height estimate, replaced by ResizeObserver measurements. */
  estimatedItemHeight?: number
  /** Distance from the bottom that counts as being live, in CSS pixels. */
  bottomThreshold?: number
  initialFollow?: boolean
  /**
   * Increment this for a non-append reorder/replacement. It is required when
   * the array length and its first/last IDs stay unchanged. Append/remove needs
   * no revision.
   */
  layoutRevision?: string | number
  formatTime?: (seconds: number) => string
  onFollowChange?: (followingLive: boolean) => void
}

export interface TranscriptFeedHandle {
  focus: () => void
  isFollowingLive: () => boolean
  scrollToLive: (behavior?: ScrollBehavior) => void
}

export interface TranscriptFeedModeSwitchProps {
  value: TranscriptFeedMode
  onChange: (mode: TranscriptFeedMode) => void
  className?: string
  disabled?: boolean
  ariaLabel?: string
  labels?: Partial<Record<TranscriptFeedMode, string>>
}
