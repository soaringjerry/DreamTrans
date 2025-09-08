import { useEffect, useState } from 'react'

export default function KnowledgePanel({ sessionId }: { sessionId: string }) {
  const [summary, setSummary] = useState<string>('')
  const [loading, setLoading] = useState(false)

  const fetchSummary = async () => {
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
    fetchSummary()
    const id = window.setInterval(fetchSummary, 5000)
    return () => window.clearInterval(id)
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId])

  return (
    <div className="column-container">
      <h3>知识点摘要</h3>
      <div className="scrollable-column">
        {loading && summary === '' ? (
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

