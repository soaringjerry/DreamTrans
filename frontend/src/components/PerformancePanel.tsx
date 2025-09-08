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

  // Sanitize events: drop obviously bad latencies (e.g., > 5 minutes)
  const cleanEvents = useMemo(() => events.filter(e => (e.latency_ms ?? 0) >= 0 && (e.latency_ms ?? 0) < 5 * 60_000), [events])
  const byKind = useMemo(() => ({
    transcript: cleanEvents.filter(e => e.kind === 'transcript'),
    translation: cleanEvents.filter(e => e.kind === 'translation' && !e.partial),
    chat: cleanEvents.filter(e => e.kind === 'chat')
  }), [cleanEvents])

  const avg = (arr: number[]) => arr.length ? Math.round((arr.reduce((a,b)=>a+b,0)/arr.length)*10)/10 : 0
  const avgTranscript = useMemo(() => avg(byKind.transcript.map(e => e.latency_ms ?? 0)), [byKind])
  const avgTranslation = useMemo(() => avg(byKind.translation.map(e => e.latency_ms ?? 0)), [byKind])
  const avgChat = stats.avgLatency

  // Build mini bars per kind (last 24)
  const bars = (list: MetricEvent[]) => {
    const last = list.slice(0, 24)
    const max = Math.max(1, ...last.map(e => e.latency_ms ?? 0, 1))
    return last.map((e, i) => ({ key: i, h: Math.max(3, Math.round(((e.latency_ms ?? 0)/max)*48)) }))
  }

  return (
    <div className="column-container" style={{ height: '100%' }}>
      <h3>性能监控</h3>
      <div className="scrollable-column" style={{ height: '100%' }}>
        {/* Summary cards */}
        <div className="perf-cards">
          <div className="perf-card">
            <h4>Transcript Avg</h4>
            <div className="big">{formatDuration(avgTranscript)}</div>
            <div className="perf-bars">
              {bars(byKind.transcript).map(b => <div key={b.key} className="perf-bar" style={{ height: b.h }} />)}
            </div>
          </div>
          <div className="perf-card">
            <h4>Translation Avg</h4>
            <div className="big">{formatDuration(avgTranslation)}</div>
            <div className="perf-bars">
              {bars(byKind.translation).map(b => <div key={b.key} className="perf-bar" style={{ height: b.h, background: 'linear-gradient(180deg,#34d399,#3b82f6)' }} />)}
            </div>
          </div>
          <div className="perf-card">
            <h4>Chat Avg · Tokens</h4>
            <div className="big">{formatDuration(avgChat)}{stats.tokenReplies>0 ? ` · ${stats.totalTokens}` : ''}</div>
            <div className="perf-bars">
              {bars(byKind.chat).map(b => <div key={b.key} className="perf-bar" style={{ height: b.h, background: 'linear-gradient(180deg,#f59e0b,#ef4444)' }} />)}
            </div>
          </div>
        </div>

        {/* Recent chat replies short list */}
        {lastFew.length > 0 && (
          <div className="content-list" style={{ marginBottom: 8 }}>
            {lastFew.map((m, i) => (
              <div key={`perf-${i}`} className={`chat-msg assistant`}>
                <div className="chat-avatar">AI</div>
                <div className="chat-bubble">
                  <div style={{ fontWeight: 600 }}>Recent Reply</div>
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

        {/* Live metrics stream, compact */}
        <div className="chat-empty" style={{ margin: '6px 0 4px 0' }}>Live Metrics</div>
        {cleanEvents.length === 0 ? (
          <div className="chat-empty">等待实时指标…</div>
        ) : (
          <div className="content-list">
            {cleanEvents.slice(0, 16).map((ev, i) => {
              const ms = ev.latency_ms ?? 0
              const w = clamp((ms / 10000) * 100, 3, 100) // scale to 10s window
              const label = ev.kind === 'transcript' ? 'T' : ev.kind === 'translation' ? '译' : '聊'
              return (
                <div key={`ev-${i}`} className="chat-msg assistant">
                  <div className="chat-avatar" title={ev.partial ? 'partial' : 'final'}>{label}</div>
                  <div className="chat-bubble" style={{ width: '100%' }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                      <div style={{ fontWeight: 600 }}>{formatDuration(ms)}</div>
                      <div style={{ fontSize: 12, color: 'var(--hai)' }}>{ev.model ?? ''}</div>
                    </div>
                    <div style={{ height: 8, background: '#f1f5f9', borderRadius: 999, marginTop: 6 }}>
                      <div style={{ width: `${w}%`, height: 8, background: 'linear-gradient(90deg,#60a5fa,#f59e0b)', borderRadius: 999 }} />
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
