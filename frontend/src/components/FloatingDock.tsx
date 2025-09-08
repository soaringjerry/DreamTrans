import { useState, useMemo } from 'react'

type Props = {
  chat: React.ReactNode
  summary?: React.ReactNode
  metrics?: React.ReactNode
}

export default function FloatingDock({ chat, summary, metrics }: Props) {
  const [chatOpen, setChatOpen] = useState(false)
  const [summaryOpen, setSummaryOpen] = useState(false)
  const [metricsOpen, setMetricsOpen] = useState(false)

  const anyOpen = useMemo(() => chatOpen || summaryOpen || metricsOpen, [chatOpen, summaryOpen, metricsOpen])

  return (
    <>
      {/* Floating buttons */}
      <div className="floating-dock">
        {metrics && (
          <button
            className="bubble-btn"
            aria-label="Open Performance"
            title="Performance"
            onClick={() => setMetricsOpen(true)}
          >
            ⏱
          </button>
        )}
        {summary && (
          <button
            className="bubble-btn"
            aria-label="Open Knowledge Summary"
            title="Summary"
            onClick={() => setSummaryOpen(true)}
          >
            ✦
          </button>
        )}
        <button
          className="bubble-btn bubble-primary"
          aria-label="Open Chat"
          title="Chat"
          onClick={() => setChatOpen(true)}
        >
          💬
        </button>
      </div>

      {/* Drawer overlay (keeps Chat mounted for global events) */}
      <div className={`float-overlay ${anyOpen ? 'open' : ''}`} onClick={() => { setChatOpen(false); setSummaryOpen(false); setMetricsOpen(false); }}>
        <div className="float-modal" onClick={(e) => e.stopPropagation()}>
          <div className="float-header">
            <div className="float-tabs">
              <button className={`float-tab ${chatOpen ? 'active' : ''}`} onClick={() => { setChatOpen(true); setSummaryOpen(false); setMetricsOpen(false) }}>Chat</button>
              {summary && <button className={`float-tab ${summaryOpen ? 'active' : ''}`} onClick={() => { setChatOpen(false); setSummaryOpen(true); setMetricsOpen(false) }}>Summary</button>}
              {metrics && <button className={`float-tab ${metricsOpen ? 'active' : ''}`} onClick={() => { setChatOpen(false); setSummaryOpen(false); setMetricsOpen(true) }}>Performance</button>}
            </div>
            <button className="btn btn-secondary" onClick={() => { setChatOpen(false); setSummaryOpen(false); setMetricsOpen(false) }}>Close</button>
          </div>
          <div className="float-body">
            <div style={{ display: chatOpen ? 'block' : 'none', height: '100%' }}>
              {chat}
            </div>
            {summary && (
              <div style={{ display: summaryOpen ? 'block' : 'none', height: '100%' }}>
                {summary}
              </div>
            )}
            {metrics && (
              <div style={{ display: metricsOpen ? 'block' : 'none', height: '100%' }}>
                {metrics}
              </div>
            )}
          </div>
        </div>
      </div>
    </>
  )
}

