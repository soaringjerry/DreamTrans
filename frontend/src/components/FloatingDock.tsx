import { useState, useEffect, useRef, useCallback } from 'react'

type Props = {
  chat: React.ReactNode
  summary?: React.ReactNode
  metrics?: React.ReactNode
}

type Pos = { x: number; y: number }

function useDraggable(initial: Pos, storageKey: string) {
  const [pos, setPos] = useState<Pos>(() => {
    try {
      const raw = localStorage.getItem(storageKey)
      if (raw) return JSON.parse(raw) as Pos
    } catch { /* noop */ }
    return initial
  })
  useEffect(() => {
    try { localStorage.setItem(storageKey, JSON.stringify(pos)) } catch { /* noop */ }
  }, [pos, storageKey])
  const draggingRef = useRef<{ startX: number; startY: number; baseX: number; baseY: number } | null>(null)
  const onDown = useCallback((e: React.MouseEvent) => {
    e.preventDefault()
    const sx = e.clientX; const sy = e.clientY
    draggingRef.current = { startX: sx, startY: sy, baseX: pos.x, baseY: pos.y }
    const onMove = (ev: MouseEvent) => {
      const cur = draggingRef.current; if (!cur) return
      const nx = cur.baseX + (ev.clientX - cur.startX)
      const ny = cur.baseY + (ev.clientY - cur.startY)
      setPos({ x: nx, y: ny })
    }
    const onUp = () => {
      draggingRef.current = null
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
  }, [pos.x, pos.y])
  return { pos, setPos, onDown }
}

export default function FloatingDock({ chat, summary, metrics }: Props) {
  // Open states: multiple windows can be open simultaneously
  const [chatOpen, setChatOpen] = useState(false)
  const [summaryOpen, setSummaryOpen] = useState(false)
  const [metricsOpen, setMetricsOpen] = useState(false)

  // Positions (default near bottom-right with small offsets)
  const vw = typeof window !== 'undefined' ? window.innerWidth : 1200
  const vh = typeof window !== 'undefined' ? window.innerHeight : 800
  const chatDrag = useDraggable({ x: Math.max(16, vw - 420), y: Math.max(16, vh - 460) }, 'dt_win_chat')
  const sumDrag = useDraggable({ x: Math.max(16, vw - 760), y: Math.max(16, vh - 460) }, 'dt_win_summary')
  const metDrag = useDraggable({ x: Math.max(16, vw - 420), y: Math.max(16, vh - 820) }, 'dt_win_metrics')

  // z-index management to bring active window to front
  const [zChat, setZChat] = useState(81)
  const [zSum, setZSum] = useState(80)
  const [zMet, setZMet] = useState(79)
  const zCounter = useRef(90)
  const bringFront = (which: 'chat' | 'summary' | 'metrics') => {
    const z = ++zCounter.current
    if (which === 'chat') setZChat(z)
    else if (which === 'summary') setZSum(z)
    else setZMet(z)
  }

  // Open Chat window automatically when an external component triggers a chat send
  useEffect(() => {
    const h = () => { setChatOpen(true); bringFront('chat') }
    window.addEventListener('dt-chat-send', h)
    return () => window.removeEventListener('dt-chat-send', h)
  }, [])

  // No bridging: Settings/History will be handled by a global overlay component

  return (
    <>
      {/* Floating buttons - toggle windows, icons centered */}
      <div className="floating-dock">
        {metrics && (
          <button
            className="bubble-btn"
            aria-label="Open Performance"
            title="Performance"
            onClick={() => { setMetricsOpen(v => !v); bringFront('metrics') }}
          >
            <span aria-hidden>⏱</span>
          </button>
        )}
        {summary && (
          <button
            className="bubble-btn"
            aria-label="Open Knowledge Summary"
            title="Summary"
            onClick={() => { setSummaryOpen(v => !v); bringFront('summary') }}
          >
            <span aria-hidden>✦</span>
          </button>
        )}
        <button
          className="bubble-btn bubble-primary"
          aria-label="Open Chat"
          title="Chat"
          onClick={() => { setChatOpen(v => !v); bringFront('chat') }}
        >
          <span aria-hidden>💬</span>
        </button>
      </div>

      {/* Non-modal floating windows (picture-in-picture style) */}
      {chatOpen && (
        <div
          className="float-window"
          style={{ left: chatDrag.pos.x, top: chatDrag.pos.y, zIndex: zChat }}
          onMouseDown={() => bringFront('chat')}
        >
          <div className="float-header" onMouseDown={chatDrag.onDown} style={{ cursor: 'grab' }}>
            <div className="float-title">Chat</div>
            <button className="btn btn-secondary" onClick={() => setChatOpen(false)}>Close</button>
          </div>
          <div className="float-body">
            {chat}
          </div>
        </div>
      )}

      {summary && summaryOpen && (
        <div
          className="float-window"
          style={{ left: sumDrag.pos.x, top: sumDrag.pos.y, zIndex: zSum }}
          onMouseDown={() => bringFront('summary')}
        >
          <div className="float-header" onMouseDown={sumDrag.onDown} style={{ cursor: 'grab' }}>
            <div className="float-title">Summary</div>
            <button className="btn btn-secondary" onClick={() => setSummaryOpen(false)}>Close</button>
          </div>
          <div className="float-body">
            {summary}
          </div>
        </div>
      )}

      {metrics && metricsOpen && (
        <div
          className="float-window metrics-large"
          style={{ left: metDrag.pos.x, top: metDrag.pos.y, zIndex: zMet }}
          onMouseDown={() => bringFront('metrics')}
        >
          <div className="float-header" onMouseDown={metDrag.onDown} style={{ cursor: 'grab' }}>
            <div className="float-title">Performance</div>
            <button className="btn btn-secondary" onClick={() => setMetricsOpen(false)}>Close</button>
          </div>
          <div className="float-body">
            {metrics}
          </div>
        </div>
      )}
    </>
  )
}
