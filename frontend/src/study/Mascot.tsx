import { useEffect, useState } from 'react'

/** Every face the instructor's screen can show. */
export type MascotMood =
  | 'idle'
  | 'thinking'
  | 'happy'
  | 'proud'
  | 'oops'
  | 'surprised'
  | 'wink'
  | 'glitch'
  | 'meh'

interface MascotProps {
  mood?: MascotMood
  size?: number
  className?: string
}

const BLINKING_MOODS: ReadonlySet<MascotMood> = new Set(['idle', 'happy', 'meh'])

/**
 * TUTOR-01: a small TV-headed unit. The screen is the whole face; the body
 * never changes, so a mood swap reads as the display re-rendering.
 */
export function Mascot({ mood = 'idle', size = 56, className }: MascotProps) {
  const [blink, setBlink] = useState(false)

  useEffect(() => {
    if (!BLINKING_MOODS.has(mood)) return
    let cancelled = false
    let timer = 0
    const schedule = () => {
      timer = window.setTimeout(() => {
        if (cancelled) return
        setBlink(true)
        timer = window.setTimeout(() => {
          if (cancelled) return
          setBlink(false)
          schedule()
        }, 130)
      }, 2200 + Math.random() * 2800)
    }
    schedule()
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [mood])

  return (
    <svg
      aria-hidden="true"
      className={`st-mascot is-${mood}${className ? ` ${className}` : ''}`}
      height={size}
      viewBox="0 0 64 64"
      width={size}
    >
      {/* antenna */}
      <line className="st-mascot__antenna" x1="45" x2="53" y1="13" y2="4" />
      <circle className="st-mascot__beacon" cx="53.5" cy="3.5" r="2.4" />
      {/* monitor body */}
      <path
        className="st-mascot__body"
        d="M9 13h46a3 3 0 0 1 3 3v33a3 3 0 0 1-3 3H9a3 3 0 0 1-3-3V16a3 3 0 0 1 3-3z"
      />
      <rect className="st-mascot__screen" height="30" rx="2.5" width="42" x="11" y="18" />
      {/* bezel details */}
      <rect className="st-mascot__vent" height="1.6" width="4" x="55" y="21" />
      <rect className="st-mascot__vent" height="1.6" width="4" x="55" y="24.5" />
      <circle className="st-mascot__knob" cx="15.5" cy="51" r="1.2" />
      <circle className="st-mascot__knob" cx="19.5" cy="51" r="1.2" />
      {/* feet */}
      <rect className="st-mascot__foot" height="4" rx="1" width="8" x="17" y="53" />
      <rect className="st-mascot__foot" height="4" rx="1" width="8" x="39" y="53" />
      {/* face: keyed so a mood change re-mounts and replays the flicker */}
      <g className="st-mascot__face" key={mood}>
        <Face blink={blink} mood={mood} />
      </g>
    </svg>
  )
}

function Face({ mood, blink }: { mood: MascotMood; blink: boolean }) {
  if (blink) {
    return (
      <>
        <line x1="21" x2="27" y1="32" y2="32" />
        <line x1="37" x2="43" y1="32" y2="32" />
        <Mouth mood={mood} />
      </>
    )
  }
  switch (mood) {
    case 'happy':
    case 'proud':
      return (
        <>
          {mood === 'proud' ? <Star cx={24} cy={31} /> : <path d="M20 33q4-6 8 0" />}
          {mood === 'proud' ? <Star cx={40} cy={31} /> : <path d="M36 33q4-6 8 0" />}
          <path d="M25 39q7 7 14 0" />
        </>
      )
    case 'thinking':
      return (
        <>
          <rect height="5" rx="1.5" width="4" x="23" y="27" />
          <rect height="5" rx="1.5" width="4" x="39" y="27" />
          <line x1="28" x2="36" y1="41" y2="41" />
          <circle className="st-mascot__dot" cx="43" cy="23" r="1.3" />
          <circle className="st-mascot__dot" cx="47" cy="23" r="1.3" />
          <circle className="st-mascot__dot" cx="51" cy="23" r="1.3" />
        </>
      )
    case 'oops':
      return (
        <>
          <path d="M20 29l6 3-6 3" />
          <path d="M44 29l-6 3 6 3" />
          <path d="M27 41q2.5-2.5 5 0t5 0" />
        </>
      )
    case 'surprised':
      return (
        <>
          <circle cx="24" cy="31" r="3.4" />
          <circle cx="40" cy="31" r="3.4" />
          <circle cx="32" cy="41" r="2.6" />
        </>
      )
    case 'wink':
      return (
        <>
          <path d="M20 32q4-5 8 0" />
          <rect height="7" rx="2" width="4" x="38" y="28" />
          <path d="M26 39q6 5 12 0" />
        </>
      )
    case 'glitch':
      return (
        <>
          <path d="M21 28l6 6M27 28l-6 6" />
          <path d="M37 28l6 6M43 28l-6 6" />
          <line x1="26" x2="38" y1="41" y2="41" />
        </>
      )
    case 'meh':
      return (
        <>
          <rect height="3" rx="1" width="7" x="20.5" y="31" />
          <rect height="3" rx="1" width="7" x="36.5" y="31" />
          <path d="M27 41.5q5-2 10 0" />
        </>
      )
    default:
      return (
        <>
          <rect height="7" rx="2" width="4" x="22" y="28" />
          <rect height="7" rx="2" width="4" x="38" y="28" />
          <Mouth mood={mood} />
        </>
      )
  }
}

function Mouth({ mood }: { mood: MascotMood }) {
  if (mood === 'meh' || mood === 'thinking' || mood === 'glitch') {
    return <line x1="28" x2="36" y1="41" y2="41" />
  }
  return <path d="M27 40q5 4 10 0" />
}

function Star({ cx, cy }: { cx: number; cy: number }) {
  const points: string[] = []
  for (let index = 0; index < 10; index += 1) {
    const radius = index % 2 === 0 ? 4.6 : 2
    const angle = -Math.PI / 2 + index * Math.PI / 5
    points.push(`${(cx + radius * Math.cos(angle)).toFixed(2)},${(cy + radius * Math.sin(angle)).toFixed(2)}`)
  }
  return <polygon className="st-mascot__star" points={points.join(' ')} />
}
