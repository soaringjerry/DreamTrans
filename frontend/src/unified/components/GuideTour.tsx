import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { useMessages } from '../../i18n'
import { useDialogFocusTrap } from '../hooks/useDialogFocusTrap'

export interface TourStep {
  id: string
  /** Candidate targets in preference order; the first visible one is used. */
  selectors: readonly string[]
  title: string
  body: string
}

interface GuideTourProps {
  steps: readonly TourStep[]
  onFinish: () => void
}

interface SpotRect {
  top: number
  left: number
  width: number
  height: number
}

const SPOT_PADDING = 6
const CARD_GAP = 12
const VIEWPORT_MARGIN = 12

function findTarget(step: TourStep): HTMLElement | null {
  for (const selector of step.selectors) {
    const candidates = document.querySelectorAll<HTMLElement>(selector)
    for (const element of candidates) {
      if (element.getClientRects().length === 0) continue
      const rect = element.getBoundingClientRect()
      if (rect.width > 0 && rect.height > 0) return element
    }
  }
  return null
}

/**
 * Spotlight tour: dims the workspace, cuts a window around one control at a
 * time and explains it. Steps whose target is not rendered (for example the
 * desktop sidebar on a phone) are skipped automatically.
 */
export function GuideTour({ steps, onFinish }: GuideTourProps) {
  const m = useMessages()
  const [visibleSteps, setVisibleSteps] = useState<readonly TourStep[]>([])
  const [index, setIndex] = useState(0)
  const [spot, setSpot] = useState<SpotRect | null>(null)
  const [cardStyle, setCardStyle] = useState<{ top: number; left: number } | null>(null)
  const cardRef = useRef<HTMLElement>(null)
  const dialogRef = useRef<HTMLDivElement>(null)
  const titleId = useId()
  const bodyId = useId()

  useDialogFocusTrap(dialogRef, onFinish)

  // Resolve which steps have a target in the current layout.
  useEffect(() => {
    const available = steps.filter((step) => findTarget(step) !== null)
    if (available.length === 0) {
      onFinish()
      return
    }
    setVisibleSteps(available)
    setIndex(0)
  }, [steps, onFinish])

  const step = visibleSteps[index] ?? null

  const measure = useCallback(() => {
    if (!step) return
    const target = findTarget(step)
    if (!target) {
      setSpot(null)
      setCardStyle(null)
      return
    }
    const rect = target.getBoundingClientRect()
    const nextSpot: SpotRect = {
      top: rect.top - SPOT_PADDING,
      left: rect.left - SPOT_PADDING,
      width: rect.width + SPOT_PADDING * 2,
      height: rect.height + SPOT_PADDING * 2,
    }
    setSpot(nextSpot)

    const card = cardRef.current
    if (!card) return
    const cardRect = card.getBoundingClientRect()
    const viewportWidth = window.innerWidth
    const viewportHeight = window.innerHeight
    const spaceBelow = viewportHeight - (nextSpot.top + nextSpot.height)
    const placeBelow = spaceBelow >= cardRect.height + CARD_GAP + VIEWPORT_MARGIN
      || nextSpot.top < cardRect.height + CARD_GAP + VIEWPORT_MARGIN
    const top = placeBelow
      ? Math.min(
          nextSpot.top + nextSpot.height + CARD_GAP,
          viewportHeight - cardRect.height - VIEWPORT_MARGIN,
        )
      : Math.max(nextSpot.top - CARD_GAP - cardRect.height, VIEWPORT_MARGIN)
    const centered = nextSpot.left + nextSpot.width / 2 - cardRect.width / 2
    const left = Math.min(
      Math.max(centered, VIEWPORT_MARGIN),
      Math.max(viewportWidth - cardRect.width - VIEWPORT_MARGIN, VIEWPORT_MARGIN),
    )
    setCardStyle({ top: Math.max(top, VIEWPORT_MARGIN), left })
  }, [step])

  useLayoutEffect(() => {
    if (!step) return
    findTarget(step)?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    measure()
    // Fonts and transitions can shift the card after first paint.
    const frame = window.requestAnimationFrame(measure)
    return () => window.cancelAnimationFrame(frame)
  }, [step, measure])

  useEffect(() => {
    if (!step) return
    const handle = () => measure()
    window.addEventListener('resize', handle)
    window.addEventListener('scroll', handle, true)
    return () => {
      window.removeEventListener('resize', handle)
      window.removeEventListener('scroll', handle, true)
    }
  }, [step, measure])

  useEffect(() => {
    if (!step) return
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'ArrowRight') {
        event.preventDefault()
        setIndex((value) => Math.min(value + 1, visibleSteps.length - 1))
      } else if (event.key === 'ArrowLeft') {
        event.preventDefault()
        setIndex((value) => Math.max(value - 1, 0))
      }
    }
    window.addEventListener('keydown', handleKeyDown)
    return () => window.removeEventListener('keydown', handleKeyDown)
  }, [step, visibleSteps.length])

  if (!step) return null
  const last = index === visibleSteps.length - 1

  return (
    <div className="dt-tour" ref={dialogRef} role="presentation" tabIndex={-1}>
      {spot && (
        <div
          aria-hidden="true"
          className="dt-tour__spot"
          style={{
            top: spot.top,
            left: spot.left,
            width: spot.width,
            height: spot.height,
          }}
        />
      )}
      <section
        aria-describedby={bodyId}
        aria-labelledby={titleId}
        aria-modal="true"
        className={`dt-tour__card${cardStyle ? ' is-placed' : ''}`}
        data-step={step.id}
        ref={cardRef}
        role="dialog"
        style={cardStyle ?? undefined}
      >
        <p className="dt-eyebrow">{m.tour.stepOf(index + 1, visibleSteps.length)}</p>
        <h3 id={titleId}>{step.title}</h3>
        <p id={bodyId}>{step.body}</p>
        <div className="dt-tour__actions">
          <button className="dt-button dt-button--text" onClick={onFinish} type="button">
            {m.common.skip}
          </button>
          <span>
            {index > 0 && (
              <button
                className="dt-button dt-button--secondary dt-button--small"
                onClick={() => setIndex((value) => Math.max(value - 1, 0))}
                type="button"
              >
                {m.common.prev}
              </button>
            )}
            <button
              className="dt-button dt-button--primary dt-button--small"
              data-autofocus
              onClick={() => {
                if (last) {
                  onFinish()
                  return
                }
                setIndex((value) => Math.min(value + 1, visibleSteps.length - 1))
              }}
              type="button"
            >
              {last ? m.common.done : m.common.next}
            </button>
          </span>
        </div>
      </section>
    </div>
  )
}
