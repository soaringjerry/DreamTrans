import { useEffect, useState } from 'react'

export default function KnowledgePanel({ sessionId, compact }: { sessionId: string; compact?: boolean }) {
  const [summary, setSummary] = useState<string>('')
  const [loading, setLoading] = useState(false)
  const [enabled, setEnabled] = useState(true)

  const fetchSummary = async () => {
    if (!enabled) return
    try {
      setLoading(true)
      const res = await fetch(`/api/rag/summary?session_id=${encodeURIComponent(sessionId)}`)
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
    // read summarization toggle from local settings
    try {
      const raw = localStorage.getItem('dt_settings_v1')
      if (raw) {
        const s = JSON.parse(raw) as { experimental_summary?: boolean }
        setEnabled(!!s.experimental_summary)
      } else { setEnabled(false) }
    } catch { setEnabled(false) }
    if (!enabled) return
    fetchSummary()
    const id = window.setInterval(fetchSummary, 5000)
    return () => window.clearInterval(id)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, enabled])

  return (
    <div className="column-container">
      {!compact && <h3>知识点摘要</h3>}
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
