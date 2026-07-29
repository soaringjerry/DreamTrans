import { useEffect, useState, useCallback } from 'react'
import { getOptionalAuthHeaders } from '../api'

function readSummaryEnabled() {
  try {
    const raw = localStorage.getItem('dt_settings_v1')
    if (raw) {
      const settings = JSON.parse(raw) as { experimental_summary?: boolean }
      return !!settings.experimental_summary
    }
  } catch { /* noop */ }
  return false
}

export default function KnowledgePanel({ sessionId, compact }: { sessionId: string; compact?: boolean }) {
  const [summary, setSummary] = useState<string>('')
  const [loading, setLoading] = useState(false)
  const [enabled, setEnabled] = useState(readSummaryEnabled)
  const [savedBlink, setSavedBlink] = useState(false)

  const writeEnabledToSettings = useCallback((value: boolean) => {
    try {
      const raw = localStorage.getItem('dt_settings_v1')
      const base = raw ? JSON.parse(raw) as Record<string, unknown> : {}
      const next = { ...base, experimental_summary: !!value }
      localStorage.setItem('dt_settings_v1', JSON.stringify(next))
      window.dispatchEvent(new CustomEvent('dt-settings-updated'))
      setSavedBlink(true)
      window.setTimeout(() => setSavedBlink(false), 1000)
    } catch { /* noop */ }
  }, [])

  const fetchSummary = async () => {
    if (!enabled) return
    try {
      setLoading(true)
      const authHeaders = await getOptionalAuthHeaders()
      const res = await fetch(`/api/rag/summary?session_id=${encodeURIComponent(sessionId)}`, {
        headers: authHeaders,
      })
      if (!res.ok) throw new Error(await res.text())
      const data = await res.json() as { summary?: string }
      setSummary(data.summary || '')
    } catch {
      // ignore
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (!enabled) return
    fetchSummary()
    const id = window.setInterval(fetchSummary, 5000)
    return () => window.clearInterval(id)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, enabled])

  // react to global settings changes
  useEffect(() => {
    const h = () => {
      const on = readSummaryEnabled()
      setEnabled(on)
      if (!on) setSummary("")
    }
    window.addEventListener('dt-settings-updated', h as EventListener)
    return () => window.removeEventListener('dt-settings-updated', h as EventListener)
  }, [])

  return (
    <div className="column-container">
      {!compact && (
        <div style={{ display:'flex', justifyContent:'space-between', alignItems:'center', gap:8 }}>
          <h3 style={{ margin:0 }}>知识点摘要</h3>
          <label style={{ fontSize:12, color:'var(--hai)', display:'inline-flex', alignItems:'center', gap:6 }}>
            <input type="checkbox" checked={enabled} onChange={(e)=>{ const v=e.target.checked; setEnabled(v); if (!v) setSummary(''); writeEnabledToSettings(v); }} /> 启用摘要
            {savedBlink && <span style={{ color:'var(--ume)', fontWeight:700 }}>已保存 ✓</span>}
          </label>
        </div>
      )}
      {compact && (
        <div style={{ display:'flex', justifyContent:'flex-end', alignItems:'center', gap:8, marginBottom:6 }}>
          <label style={{ fontSize:12, color:'var(--hai)', display:'inline-flex', alignItems:'center', gap:6 }}>
            <input type="checkbox" checked={enabled} onChange={(e)=>{ const v=e.target.checked; setEnabled(v); if (!v) setSummary(''); writeEnabledToSettings(v); }} /> 摘要
            {savedBlink && <span style={{ color:'var(--ume)', fontWeight:700 }}>✓</span>}
          </label>
        </div>
      )}
      <div className="scrollable-column">
        {!enabled ? (
          <div className="chat-empty">已关闭摘要（设置 → Experimental）</div>
        ) : loading && summary === '' ? (
          <div className="chat-empty">加载中…</div>
        ) : summary ? (
          <pre style={{ whiteSpace: 'pre-wrap', fontFamily: 'inherit' }}>{summary}</pre>
        ) : (
          <div className="chat-empty">暂无摘要。开始讲话几句后，这里会显示要点。</div>
        )}
      </div>
    </div>
  )
}
