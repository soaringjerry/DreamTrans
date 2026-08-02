import {
  forwardRef,
  useCallback,
  useEffect,
  useId,
  useImperativeHandle,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
  type UIEvent,
  type WheelEvent,
} from 'react'
import {
  annotateSentence,
  DEFAULT_TERM_DOMAINS,
  type LearningGloss,
  type LearningLevel,
  type TermDomain,
} from '../../learning'
import type {
  TranscriptChromeMode,
  TranscriptFeedHandle,
  TranscriptFeedItem,
  TranscriptFeedLabels,
  TranscriptFeedMode,
  TranscriptFeedModeSwitchProps,
  TranscriptFeedProps,
  TranscriptFeedTrack,
} from './types'
import { VirtualLayout } from './virtualLayout'
import './TranscriptFeed.css'

const DEFAULT_LABELS: TranscriptFeedLabels = {
  originalTrack: '原文',
  translationTrack: '译文',
  originalPending: '正在识别…',
  translationPending: '等待翻译…',
  originalUnavailable: '暂无原文',
  translationUnavailable: '暂无译文',
  streaming: '实时',
  failed: '处理失败',
  unknownSpeaker: '发言人',
  empty: '转录内容会显示在这里',
  scrollRegion: '实时转录内容',
  keyboardHelp: '使用方向键、Page Up 和 Page Down 浏览；按 End 回到实时。',
  returnToLive: '回到实时',
  newItems: (count) => `新增 ${count} 条`,
}

const DEFAULT_MODE_LABELS: Record<TranscriptChromeMode, string> = {
  original: '原文',
  bilingual: '双语',
  translation: '译文',
  learn: '学习',
}

// Aggregated cards hold full utterances, so they run taller than the old
// one-fragment-per-card rows. Real heights still come from ResizeObserver.
const DEFAULT_ESTIMATED_HEIGHT: Record<TranscriptFeedMode, number> = {
  original: 128,
  bilingual: 196,
  translation: 128,
}
const MAX_MEASUREMENT_CACHE_ENTRIES = 10_000

function clampInteger(value: number, minimum: number, maximum: number): number {
  if (!Number.isFinite(value)) return minimum
  return Math.min(maximum, Math.max(minimum, Math.floor(value)))
}

function joinClassNames(...names: Array<string | undefined | false>): string {
  return names.filter(Boolean).join(' ')
}

function defaultFormatTime(seconds: number): string {
  const safeSeconds = Math.max(0, Math.floor(seconds))
  const hours = Math.floor(safeSeconds / 3600)
  const minutes = Math.floor((safeSeconds % 3600) / 60)
  const remainder = safeSeconds % 60
  if (hours > 0) {
    return `${hours}:${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
  }
  return `${minutes}:${String(remainder).padStart(2, '0')}`
}

function speakerInitials(speaker: string): string {
  const trimmed = speaker.trim()
  if (!trimmed) return '?'
  const words = trimmed.split(/\s+/)
  if (words.length > 1) {
    return `${Array.from(words[0])[0] ?? ''}${Array.from(words[words.length - 1])[0] ?? ''}`.toUpperCase()
  }
  return Array.from(trimmed).slice(0, 2).join('').toUpperCase()
}

function speakerTone(speakerId: string): number {
  let hash = 0
  for (let index = 0; index < speakerId.length; index += 1) {
    hash = (hash * 31 + speakerId.charCodeAt(index)) | 0
  }
  return Math.abs(hash) % 8
}

function trackStatus(track: TranscriptFeedTrack | undefined): 'pending' | 'streaming' | 'final' | 'error' | 'empty' {
  if (!track) return 'empty'
  if (track.status) return track.status
  if (track.partialText) return 'streaming'
  if (track.text) return 'final'
  return 'empty'
}

function renderGlossedText(text: string, glosses: readonly LearningGloss[]) {
  if (glosses.length === 0) {
    return <span>{text}</span>
  }
  const nodes: ReactNode[] = []
  let cursor = 0
  glosses.forEach((gloss, index) => {
    if (gloss.start > cursor) {
      nodes.push(
        <span key={`t-${cursor}`}>{text.slice(cursor, gloss.start)}</span>,
      )
    }
    const surface = text.slice(gloss.start, gloss.end) || gloss.surface
    nodes.push(
      <span
        className="dt-transcript-feed__gloss-word"
        key={`g-${gloss.start}-${index}`}
        title={
          gloss.domain
            ? `术语 · ${gloss.domain}${gloss.zh ? ` · ${gloss.zh}` : ''}`
            : gloss.level
              ? `CEFR ${gloss.level}`
              : '生词'
        }
      >
        <span className="dt-transcript-feed__gloss-surface">{surface}</span>
        {gloss.zh ? (
          <span className="dt-transcript-feed__gloss-zh" lang="zh-CN">{gloss.zh}</span>
        ) : (
          <span className="dt-transcript-feed__gloss-zh dt-transcript-feed__gloss-zh--empty" lang="zh-CN">
            生词
          </span>
        )}
      </span>,
    )
    cursor = gloss.end
  })
  if (cursor < text.length) {
    nodes.push(<span key={`t-end`}>{text.slice(cursor)}</span>)
  }
  return nodes
}

interface TrackProps {
  kind: 'original' | 'translation'
  track: TranscriptFeedTrack | undefined
  showLabel: boolean
  labels: TranscriptFeedLabels
  learningMode?: boolean
  learningLevel?: LearningLevel
  learningDomains?: readonly TermDomain[]
  expandGlosses?: boolean
}

function TranscriptTrack({
  kind,
  track,
  showLabel,
  labels,
  learningMode = false,
  learningLevel = 'B1',
  learningDomains = DEFAULT_TERM_DOMAINS,
  expandGlosses = false,
}: TrackProps) {
  const status = trackStatus(track)
  const label = kind === 'original' ? labels.originalTrack : labels.translationTrack
  const pendingLabel = kind === 'original' ? labels.originalPending : labels.translationPending
  const emptyLabel = kind === 'original' ? labels.originalUnavailable : labels.translationUnavailable
  const hasText = Boolean(track?.text || track?.partialText)
  const statusLabel = status === 'streaming'
    ? labels.streaming
    : status === 'error'
      ? labels.failed
      : undefined
  const finalText = track?.text ?? ''
  const glosses = useMemo(() => {
    if (!learningMode || kind !== 'original' || !finalText.trim()) return []
    return annotateSentence(finalText, learningLevel, {
      maxGlosses: expandGlosses ? 24 : 3,
      forceAllContent: expandGlosses,
      domains: learningDomains,
    })
  }, [expandGlosses, finalText, kind, learningDomains, learningLevel, learningMode])

  return (
    <section
      className={joinClassNames(
        'dt-transcript-feed__track',
        `dt-transcript-feed__track--${kind}`,
        `dt-transcript-feed__track--${status}`,
        learningMode && kind === 'original' && 'dt-transcript-feed__track--learning',
      )}
      aria-label={label}
      aria-busy={status === 'pending' || status === 'streaming'}
    >
      {(showLabel || statusLabel) && (
        <div className="dt-transcript-feed__track-meta">
          {showLabel && <span className="dt-transcript-feed__track-label">{label}</span>}
          {statusLabel && (
            <span className="dt-transcript-feed__track-status">
              <span className="dt-transcript-feed__status-dot" aria-hidden="true" />
              {statusLabel}
            </span>
          )}
        </div>
      )}

      {hasText ? (
        <p
          className={joinClassNames(
            'dt-transcript-feed__text',
            learningMode && kind === 'original' && 'dt-transcript-feed__text--learning',
          )}
          lang={track?.language}
          dir="auto"
        >
          {finalText && (
            learningMode && kind === 'original'
              ? renderGlossedText(finalText, glosses)
              : <span>{finalText}</span>
          )}
          {track?.partialText && (
            <span className="dt-transcript-feed__partial">
              {finalText ? ' ' : ''}
              {track.partialText}
              <span className="dt-transcript-feed__cursor" aria-hidden="true" />
            </span>
          )}
        </p>
      ) : status === 'pending' || status === 'streaming' ? (
        <p className="dt-transcript-feed__placeholder dt-transcript-feed__placeholder--pending">
          <span className="dt-transcript-feed__pending-dots" aria-hidden="true">
            <i />
            <i />
            <i />
          </span>
          {pendingLabel}
        </p>
      ) : status === 'error' ? (
        <p className="dt-transcript-feed__placeholder dt-transcript-feed__placeholder--error">
          {track?.errorMessage || labels.failed}
        </p>
      ) : (
        <p className="dt-transcript-feed__placeholder">{emptyLabel}</p>
      )}
    </section>
  )
}

interface TranscriptRowProps {
  item: TranscriptFeedItem
  mode: TranscriptFeedMode
  index: number
  itemCount: number
  offset: number
  modeScope: string
  labels: TranscriptFeedLabels
  formatTime: (seconds: number) => string
  onMeasure: (id: string, index: number, modeScope: string, height: number) => void
  learningMode?: boolean
  learningLevel?: LearningLevel
  learningDomains?: readonly TermDomain[]
  expanded?: boolean
  onToggleExpand?: (id: string) => void
}

function TranscriptRow({
  item,
  mode,
  index,
  itemCount,
  offset,
  modeScope,
  labels,
  formatTime,
  onMeasure,
  learningMode = false,
  learningLevel = 'B1',
  learningDomains = DEFAULT_TERM_DOMAINS,
  expanded = false,
  onToggleExpand,
}: TranscriptRowProps) {
  const rowRef = useRef<HTMLLIElement>(null)
  const speaker = item.speaker.trim() || labels.unknownSpeaker
  const tone = speakerTone(item.speakerId || speaker)
  const showLearningControls = learningMode
    && Boolean(item.original?.text?.trim())
    && mode !== 'translation'

  useLayoutEffect(() => {
    const row = rowRef.current
    if (!row) return

    const measure = () => onMeasure(item.id, index, modeScope, row.getBoundingClientRect().height)
    measure()

    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(measure)
    observer.observe(row)
    return () => observer.disconnect()
  }, [expanded, index, item.id, learningMode, learningLevel, modeScope, onMeasure])

  return (
    <li
      ref={rowRef}
      className="dt-transcript-feed__row"
      style={{ transform: `translate3d(0, ${offset}px, 0)` }}
      aria-posinset={index + 1}
      aria-setsize={itemCount}
    >
      <article
        className="dt-transcript-feed__card"
        data-speaker-tone={tone}
        data-learning={learningMode ? 'true' : undefined}
        aria-label={`${speaker}${item.startTime === undefined ? '' : `，${formatTime(item.startTime)}`}`}
      >
        <header className="dt-transcript-feed__speaker">
          <span className="dt-transcript-feed__avatar" aria-hidden="true">
            {speakerInitials(speaker)}
          </span>
          <span className="dt-transcript-feed__speaker-name">{speaker}</span>
          {item.startTime !== undefined && (
            <span className="dt-transcript-feed__time">{formatTime(item.startTime)}</span>
          )}
        </header>

        <div className="dt-transcript-feed__tracks">
          {mode !== 'translation' && (
            <TranscriptTrack
              kind="original"
              track={item.original}
              showLabel={mode === 'bilingual' && !learningMode}
              labels={labels}
              learningMode={learningMode}
              learningLevel={learningLevel}
              learningDomains={learningDomains}
              expandGlosses={expanded}
            />
          )}
          {!learningMode && mode !== 'original' && (
            <TranscriptTrack
              kind="translation"
              track={item.translation}
              showLabel={mode === 'bilingual'}
              labels={labels}
            />
          )}
          {showLearningControls && (
            <div className="dt-transcript-feed__learning-actions">
              <button
                type="button"
                className="dt-transcript-feed__learning-button"
                aria-pressed={expanded}
                onClick={() => onToggleExpand?.(item.id)}
              >
                {expanded ? '收起词注' : '仍不懂 · 展开词注'}
              </button>
            </div>
          )}
        </div>
      </article>
    </li>
  )
}

export const TranscriptFeed = forwardRef<TranscriptFeedHandle, TranscriptFeedProps>(function TranscriptFeed(
  {
    items,
    mode,
    className,
    style,
    ariaLabel,
    labels: labelOverrides,
    emptyState,
    overscan = 5,
    estimatedItemHeight,
    bottomThreshold = 64,
    initialFollow = true,
    layoutRevision = 0,
    formatTime = defaultFormatTime,
    onFollowChange,
    learningMode = false,
    learningLevel = 'B1',
    learningDomains = DEFAULT_TERM_DOMAINS,
  },
  forwardedRef,
) {
  const labels = useMemo(
    () => ({ ...DEFAULT_LABELS, ...labelOverrides }),
    [labelOverrides],
  )
  const viewportRef = useRef<HTMLDivElement>(null)
  const followingRef = useRef(initialFollow)
  const lastScrollTopRef = useRef(0)
  const scrollFrameRef = useRef<number | null>(null)
  const pendingScrollTopRef = useRef(0)
  const measurementsRef = useRef(new Map<string, number>())
  const measurementRevisionRef = useRef(String(layoutRevision))
  const layoutRef = useRef<VirtualLayout | null>(null)
  const previousItemCountRef = useRef(items.length)
  const visibleStartRef = useRef(0)
  const [followingLive, setFollowingLive] = useState(initialFollow)
  const [newItemCount, setNewItemCount] = useState(0)
  const [scrollTop, setScrollTop] = useState(0)
  const [viewportSize, setViewportSize] = useState({ width: 0, height: 0 })
  const [expandedIds, setExpandedIds] = useState<ReadonlySet<string>>(() => new Set())
  const toggleExpand = useCallback((id: string) => {
    setExpandedIds((previous) => {
      const next = new Set(previous)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])
  const [, forceMeasurementRender] = useState(0)
  const helpId = useId()

  const safeOverscan = clampInteger(overscan, 0, 30)
  const safeBottomThreshold = Math.max(0, bottomThreshold)
  const safeEstimatedHeight = Math.max(
    48,
    estimatedItemHeight ?? DEFAULT_ESTIMATED_HEIGHT[mode],
  )
  // Width buckets avoid rebuilding the full height index for every pixel while
  // a desktop pane is being resized. Visible rows are still remeasured exactly.
  const widthKey = Math.max(0, Math.round(viewportSize.width / 16) * 16)
  const domainKey = learningMode ? learningDomains.slice().sort().join('+') || 'none' : 'off'
  const modeScope = `${mode}:${widthKey}:learn=${learningMode ? learningLevel : 'off'}:dom=${domainKey}`
  const revisionKey = String(layoutRevision)
  if (measurementRevisionRef.current !== revisionKey) {
    // Measurements are only valid for one loaded feed generation. Keeping
    // every visited session/width forever made history browsing leak memory.
    measurementRevisionRef.current = revisionKey
    measurementsRef.current.clear()
    layoutRef.current = null
  }

  let currentLayout = layoutRef.current
  const firstId = items[0]?.id
  const lastId = items[items.length - 1]?.id
  const layoutConfigurationMatches = Boolean(
    currentLayout
    && currentLayout.modeScope === modeScope
    && currentLayout.layoutRevision === revisionKey
    && currentLayout.estimatedSize === safeEstimatedHeight,
  )
  const isPureTailAppend = Boolean(
    currentLayout
    && layoutConfigurationMatches
    && items.length > currentLayout.length
    && (
      currentLayout.length === 0
      || (
        currentLayout.ids[0] === firstId
        && currentLayout.ids[currentLayout.length - 1] === items[currentLayout.length - 1]?.id
      )
    ),
  )
  const isPureTailTruncate = Boolean(
    currentLayout
    && layoutConfigurationMatches
    && items.length < currentLayout.length
    && (
      items.length === 0
      || (
        currentLayout.ids[0] === firstId
        && currentLayout.ids[items.length - 1] === lastId
      )
    ),
  )

  if (currentLayout && isPureTailAppend) {
    const appendedIds: string[] = []
    const appendedSizes: number[] = []
    for (let index = currentLayout.length; index < items.length; index += 1) {
      const id = items[index]?.id
      if (id === undefined) continue
      appendedIds.push(id)
      appendedSizes.push(
        measurementsRef.current.get(`${modeScope}:${id}`) ?? safeEstimatedHeight,
      )
    }
    currentLayout.append(appendedIds, appendedSizes)
  } else if (currentLayout && isPureTailTruncate) {
    currentLayout.truncate(items.length)
  } else {
    const layoutNeedsRebuild = !currentLayout
      || !layoutConfigurationMatches
      || currentLayout.length !== items.length
      || currentLayout.ids[0] !== firstId
      || currentLayout.ids[currentLayout.length - 1] !== lastId

    if (layoutNeedsRebuild) {
      const ids = items.map((item) => item.id)
      const sizes = ids.map(
        (id) => measurementsRef.current.get(`${modeScope}:${id}`) ?? safeEstimatedHeight,
      )
      layoutRef.current = new VirtualLayout(ids, sizes, modeScope, revisionKey, safeEstimatedHeight)
      currentLayout = layoutRef.current
    }
  }

  const layout = currentLayout
  const totalHeight = layout?.totalSize ?? 0
  const firstVisibleIndex = layout && layout.length > 0
    ? layout.indexAtOffset(scrollTop)
    : -1
  const lastVisibleIndex = layout && layout.length > 0
    ? layout.indexAtOffset(scrollTop + viewportSize.height)
    : -1
  visibleStartRef.current = Math.max(0, firstVisibleIndex)

  const renderStart = firstVisibleIndex < 0
    ? 0
    : Math.max(0, firstVisibleIndex - safeOverscan)
  const renderEnd = lastVisibleIndex < 0 || !layout
    ? -1
    : Math.min(layout.length - 1, lastVisibleIndex + safeOverscan)

  const setFollowState = useCallback((nextFollowing: boolean) => {
    if (nextFollowing) setNewItemCount(0)
    if (followingRef.current === nextFollowing) return
    followingRef.current = nextFollowing
    setFollowingLive(nextFollowing)
    onFollowChange?.(nextFollowing)
  }, [onFollowChange])

  const setViewportScrollTop = useCallback((nextScrollTop: number) => {
    pendingScrollTopRef.current = nextScrollTop
    if (scrollFrameRef.current !== null) return
    scrollFrameRef.current = window.requestAnimationFrame(() => {
      scrollFrameRef.current = null
      setScrollTop((previous) => (
        Math.abs(previous - pendingScrollTopRef.current) < 0.5
          ? previous
          : pendingScrollTopRef.current
      ))
    })
  }, [])

  const moveToLive = useCallback((behavior: ScrollBehavior = 'auto') => {
    const viewport = viewportRef.current
    setFollowState(true)
    if (!viewport) return

    const reduceMotion = typeof window !== 'undefined'
      && window.matchMedia('(prefers-reduced-motion: reduce)').matches
    const target = Math.max(0, viewport.scrollHeight - viewport.clientHeight)
    const safeBehavior = reduceMotion ? 'auto' : behavior
    viewport.scrollTo({ top: target, behavior: safeBehavior })
    if (safeBehavior === 'auto') {
      lastScrollTopRef.current = target
      setScrollTop(target)
    }
  }, [setFollowState])

  useImperativeHandle(forwardedRef, () => ({
    focus: () => viewportRef.current?.focus(),
    isFollowingLive: () => followingRef.current,
    scrollToLive: moveToLive,
  }), [moveToLive])

  const handleMeasure = useCallback((
    id: string,
    index: number,
    measuredModeScope: string,
    height: number,
  ) => {
    if (!Number.isFinite(height) || height <= 0) return
    const measurementKey = `${measuredModeScope}:${id}`
    // Refresh insertion order so the bounded map behaves as a small LRU.
    measurementsRef.current.delete(measurementKey)
    measurementsRef.current.set(measurementKey, height)
    while (measurementsRef.current.size > MAX_MEASUREMENT_CACHE_ENTRIES) {
      const oldestKey = measurementsRef.current.keys().next().value
      if (oldestKey === undefined) break
      measurementsRef.current.delete(oldestKey)
    }

    const activeLayout = layoutRef.current
    if (!activeLayout || activeLayout.modeScope !== measuredModeScope) return
    const activeIndex = activeLayout.getIndex(id)
    if (activeIndex === undefined || activeIndex !== index) return

    const delta = activeLayout.setSize(activeIndex, height)
    if (delta === 0) return

    const viewport = viewportRef.current
    if (viewport && !followingRef.current && activeIndex < visibleStartRef.current) {
      const anchoredScrollTop = Math.max(0, viewport.scrollTop + delta)
      viewport.scrollTop = anchoredScrollTop
      lastScrollTopRef.current = anchoredScrollTop
      setViewportScrollTop(anchoredScrollTop)
    }
    forceMeasurementRender((revision) => revision + 1)
  }, [setViewportScrollTop])

  const handleScroll = useCallback((event: UIEvent<HTMLDivElement>) => {
    const viewport = event.currentTarget
    const nextScrollTop = viewport.scrollTop
    const previousScrollTop = lastScrollTopRef.current
    const movingUp = nextScrollTop < previousScrollTop - 1
    const distanceFromBottom = viewport.scrollHeight - nextScrollTop - viewport.clientHeight

    if (movingUp) {
      setFollowState(false)
    } else if (distanceFromBottom <= safeBottomThreshold) {
      setFollowState(true)
    }

    lastScrollTopRef.current = nextScrollTop
    setViewportScrollTop(nextScrollTop)
  }, [safeBottomThreshold, setFollowState, setViewportScrollTop])

  const handleWheel = useCallback((event: WheelEvent<HTMLDivElement>) => {
    if (event.deltaY < 0) setFollowState(false)
  }, [setFollowState])

  const handleKeyDown = useCallback((event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'End') {
      event.preventDefault()
      moveToLive('smooth')
      return
    }
    if (
      event.key === 'ArrowUp'
      || event.key === 'PageUp'
      || event.key === 'Home'
      || (event.key === ' ' && event.shiftKey)
    ) {
      setFollowState(false)
    }
  }, [moveToLive, setFollowState])

  useLayoutEffect(() => {
    const viewport = viewportRef.current
    if (!viewport || typeof ResizeObserver === 'undefined') return

    const updateViewportSize = () => {
      const nextWidth = viewport.clientWidth
      const nextHeight = viewport.clientHeight
      setViewportSize((previous) => (
        previous.width === nextWidth && previous.height === nextHeight
          ? previous
          : { width: nextWidth, height: nextHeight }
      ))
    }

    updateViewportSize()
    const observer = new ResizeObserver(updateViewportSize)
    observer.observe(viewport)
    return () => observer.disconnect()
  }, [])

  useLayoutEffect(() => {
    if (!followingRef.current) return
    const viewport = viewportRef.current
    if (!viewport) return
    const target = Math.max(0, viewport.scrollHeight - viewport.clientHeight)
    viewport.scrollTop = target
    lastScrollTopRef.current = target
    setScrollTop(target)
  }, [modeScope, totalHeight])

  useEffect(() => {
    const previousCount = previousItemCountRef.current
    if (items.length > previousCount && !followingRef.current) {
      setNewItemCount((count) => count + items.length - previousCount)
    } else if (items.length < previousCount) {
      setNewItemCount(0)
    }
    previousItemCountRef.current = items.length
  }, [items.length])

  useEffect(() => {
    if (initialFollow) moveToLive('auto')
    else setFollowState(false)
  }, [initialFollow, moveToLive, setFollowState])

  useEffect(() => () => {
    if (scrollFrameRef.current !== null) {
      window.cancelAnimationFrame(scrollFrameRef.current)
    }
  }, [])

  return (
    <section
      className={joinClassNames('dt-transcript-feed', className)}
      style={style}
      aria-label={ariaLabel || labels.scrollRegion}
      data-following-live={followingLive}
    >
      <p id={helpId} className="dt-transcript-feed__sr-only">
        {labels.keyboardHelp}
      </p>

      <div
        ref={viewportRef}
        className="dt-transcript-feed__viewport"
        role="region"
        aria-label={labels.scrollRegion}
        aria-describedby={helpId}
        tabIndex={0}
        onScroll={handleScroll}
        onWheel={handleWheel}
        onKeyDown={handleKeyDown}
      >
        {items.length === 0 ? (
          <div className="dt-transcript-feed__empty" role="status">
            {emptyState ?? labels.empty}
          </div>
        ) : (
          <ol
            className="dt-transcript-feed__list"
            style={{ height: totalHeight }}
            aria-label={labels.scrollRegion}
          >
            {layout && renderEnd >= renderStart && Array.from(
              { length: renderEnd - renderStart + 1 },
              (_, itemOffset) => {
                const index = renderStart + itemOffset
                const item = items[index]
                if (!item) return null
                return (
                  <TranscriptRow
                    key={`${modeScope}:${item.id}`}
                    item={item}
                    mode={mode}
                    index={index}
                    itemCount={items.length}
                    offset={layout.getOffset(index)}
                    modeScope={modeScope}
                    labels={labels}
                    formatTime={formatTime}
                    onMeasure={handleMeasure}
                    learningMode={learningMode}
                    learningLevel={learningLevel}
                    learningDomains={learningDomains}
                    expanded={expandedIds.has(item.id)}
                    onToggleExpand={toggleExpand}
                  />
                )
              },
            )}
          </ol>
        )}
      </div>

      {!followingLive && (
        <button
          type="button"
          className="dt-transcript-feed__live-button"
          onClick={() => moveToLive('smooth')}
        >
          {newItemCount > 0 && (
            <>
              <span>{labels.newItems(newItemCount)}</span>
              <span className="dt-transcript-feed__live-divider" aria-hidden="true">·</span>
            </>
          )}
          <span>{labels.returnToLive}</span>
          <span className="dt-transcript-feed__live-arrow" aria-hidden="true">↓</span>
        </button>
      )}

      <span className="dt-transcript-feed__sr-only" role="status" aria-live="polite">
        {!followingLive && newItemCount > 0
          ? `${labels.newItems(newItemCount)}，${labels.returnToLive}`
          : ''}
      </span>
    </section>
  )
})

export function TranscriptFeedModeSwitch({
  value,
  onChange,
  className,
  translationDisabled = false,
  ariaLabel = '转录显示模式',
  labels: labelOverrides,
}: TranscriptFeedModeSwitchProps) {
  const buttonRefs = useRef<Array<HTMLButtonElement | null>>([])
  const modes: readonly TranscriptChromeMode[] = [
    'original',
    'bilingual',
    'translation',
    'learn',
  ]
  const labels = { ...DEFAULT_MODE_LABELS, ...labelOverrides }

  const isOptionDisabled = (mode: TranscriptChromeMode) => (
    translationDisabled && (mode === 'bilingual' || mode === 'translation')
  )

  const selectAdjacentMode = (currentIndex: number, direction: -1 | 1) => {
    for (let step = 1; step <= modes.length; step += 1) {
      const nextIndex = (currentIndex + direction * step + modes.length) % modes.length
      const nextMode = modes[nextIndex]
      if (isOptionDisabled(nextMode)) continue
      onChange(nextMode)
      buttonRefs.current[nextIndex]?.focus()
      return
    }
  }

  return (
    <div
      className={joinClassNames('dt-transcript-feed-mode-switch', className)}
      role="radiogroup"
      aria-label={ariaLabel}
    >
      {modes.map((itemMode, index) => {
        const optionDisabled = isOptionDisabled(itemMode)
        return (
          <button
            key={itemMode}
            ref={(element) => {
              buttonRefs.current[index] = element
            }}
            type="button"
            role="radio"
            aria-checked={value === itemMode}
            tabIndex={value === itemMode ? 0 : -1}
            disabled={optionDisabled}
            title={
              itemMode === 'learn'
                ? '学习模式：原文 + 难词/术语旁注（本地，不请求翻译模型）'
                : undefined
            }
            className={joinClassNames(
              'dt-transcript-feed-mode-switch__option',
              itemMode === 'learn' && 'dt-transcript-feed-mode-switch__option--learn',
            )}
            onClick={() => onChange(itemMode)}
            onKeyDown={(event) => {
              if (event.key === 'ArrowLeft' || event.key === 'ArrowUp') {
                event.preventDefault()
                selectAdjacentMode(index, -1)
              } else if (event.key === 'ArrowRight' || event.key === 'ArrowDown') {
                event.preventDefault()
                selectAdjacentMode(index, 1)
              }
            }}
          >
            {labels[itemMode]}
          </button>
        )
      })}
    </div>
  )
}
