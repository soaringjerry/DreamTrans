import { useEffect, useMemo, useState } from 'react'
import { clamp, formatDuration } from '../utils/format'
import { getMetrics, type MetricEvent } from '../utils/metrics'

type ChatMessage = { role: 'user' | 'assistant'; content: string; meta?: { tokens?: string; latency?: string; model?: string } }

function parseTokens(s?: string): { prompt: number; completion: number; total: number } | null {
  if (!s) return null
  // Expected format: "p/c (t)" e.g. "123/456 (579)"
  const m = s.match(/(\d+)\/(\d+)\s*\((\d+)\)/)
  if (!m) return null
  return { prompt: Number(m[1]), completion: Number(m[2]), total: Number(m[3]) }
}

function parseLatency(s?: string): number | null {
  if (!s) return null
  const m = s.match(/(\d+(?:\.\d+)?)\s*ms/i)
  return m ? Number(m[1]) : null
}

export default function PerformancePanel({ sessionId }: { sessionId: string }) {
  const HISTORY_KEY = useMemo(() => `dt_chat_history_${sessionId}`, [sessionId])
  const [messages, setMessages] = useState<ChatMessage[]>([])

  useEffect(() => {
    try {
      const raw = localStorage.getItem(HISTORY_KEY)
      if (raw) {
        const arr = JSON.parse(raw) as ChatMessage[]
        if (Array.isArray(arr)) setMessages(arr)
      }
    } catch { /* ignore */ }
  }, [HISTORY_KEY])

  const stats = useMemo(() => {
    const replies = messages.filter(m => m.role === 'assistant' && m.meta)
    let totalTokens = 0
    let tokenReplies = 0
    let totalLatency = 0
    let count = 0
    for (const r of replies) {
      const tk = parseTokens(r.meta?.tokens)
      const lt = parseLatency(r.meta?.latency)
      if (tk) { totalTokens += tk.total; tokenReplies++ }
      if (lt != null) totalLatency += lt
      count++
    }
    const avgLatency = count > 0 ? Math.round((totalLatency / count) * 10) / 10 : 0
    return { turns: messages.length, replies: replies.length, totalTokens, tokenReplies, avgLatency }
  }, [messages])

  const lastFew = useMemo(() => {
    return messages
      .filter(m => m.role === 'assistant' && m.meta)
      .slice(-5)
      .reverse()
  }, [messages])

  const [events, setEvents] = useState<MetricEvent[]>([])
  useEffect(() => {
    // Initialize from global buffer to catch events fired before mount
    setEvents(getMetrics())
    const onMetric = (e: Event) => {
      const ce = e as CustomEvent
      const d = ce.detail as { kind?: 'chat'|'translation'|'transcript'; latency_ms?: number; model?: string; partial?: boolean } | undefined
      if (!d) return
      const item: MetricEvent = { kind: (d.kind ?? 'translation'), latency_ms: d.latency_ms, model: d.model, partial: !!d.partial, at: Date.now() }
      setEvents(prev => {
        const next = [item, ...prev]
        return next.slice(0, 50)
      })
    }
    window.addEventListener('dt-metrics', onMetric as EventListener)
    return () => window.removeEventListener('dt-metrics', onMetric as EventListener)
  }, [])

  return (
    <div className="column-container" style={{ height: '100%' }}>
      <h3>Performance</h3>
      <div className="scrollable-column" style={{ height: '100%' }}>
        <div className="chat-empty" style={{ marginBottom: 8 }}>
          Turns: {stats.turns} · Replies: {stats.replies} · Total tokens: {stats.tokenReplies > 0 ? stats.totalTokens : 'n/a'} · Avg chat latency: {formatDuration(stats.avgLatency)}
        </div>
        {lastFew.length === 0 ? (
          <div className="chat-empty">Metrics will appear after RAG replies. Streaming metrics planned.</div>
        ) : (
          <div className="content-list">
            {lastFew.map((m, i) => (
              <div key={`perf-${i}`} className={`chat-msg assistant`}>
                <div className="chat-avatar">AI</div>
                <div className="chat-bubble">
                  <div className="chat-text" style={{ fontWeight: 600 }}>Recent Reply</div>
                  <div style={{ marginTop: 6, fontSize: '12px', color: 'var(--hai)' }}>
                    {m.meta?.model ? `model ${m.meta.model}` : ''}
                    {m.meta?.tokens ? ` · tokens ${m.meta.tokens}` : ''}
                    {m.meta?.latency ? ` · latency ${m.meta.latency}` : ''}
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}

        <div className="chat-empty" style={{ margin: '12px 0 4px 0' }}>Live Metrics</div>
        {events.length === 0 ? (
          <div className="chat-empty">Waiting for transcript/translation/chat metrics…</div>
        ) : (
          <div className="content-list">
            {events.slice(0, 10).map((ev, i) => {
              const ms = ev.latency_ms ?? 0
              const w = clamp((ms / 30000) * 100, 2, 100) // scale to 30s window
              return (
                <div key={`ev-${i}`} className="chat-msg assistant">
                  <div className="chat-avatar" title={ev.partial ? 'partial' : 'final'}>{ev.kind === 'transcript' ? 'T' : ev.kind === 'translation' ? '译' : '聊'}</div>
                  <div className="chat-bubble" style={{ width: '100%' }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                      <div style={{ fontWeight: 600 }}>{ev.kind}{ev.partial ? ' (partial)' : ''}</div>
                      <div style={{ fontSize: '12px', color: 'var(--hai)' }}>{formatDuration(ms)}</div>
                    </div>
                    <div style={{ height: 6, background: '#f1f5f9', borderRadius: 999, marginTop: 6 }}>
                      <div style={{ width: `${w}%`, height: 6, background: 'linear-gradient(90deg,#60a5fa,#f59e0b)', borderRadius: 999 }} />
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
