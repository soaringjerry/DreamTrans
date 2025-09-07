import { useState } from 'react'
import { askRag } from '../api'

interface ChatMessage { role: 'user' | 'assistant'; content: string }

export default function ChatPanel({ sessionId }: { sessionId: string }) {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)

  const onSend = async () => {
    const q = input.trim()
    if (!q || loading) return
    setMessages((m) => [...m, { role: 'user', content: q }])
    setInput('')
    setLoading(true)
    try {
      const res = await askRag(sessionId, q, 5)
      setMessages((m) => [...m, { role: 'assistant', content: res.answer }])
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e)
      setMessages((m) => [...m, { role: 'assistant', content: `请求失败：${msg}` }])
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="column-container">
      <h3>学习助手（RAG）</h3>
      <div className="scrollable-column" style={{ minHeight: 240 }}>
        <div className="content-list">
          {messages.map((m, i) => (
            <div key={i} style={{
              padding: '0.5rem 0',
              color: m.role === 'user' ? 'var(--sumi)' : 'var(--hai)'
            }}>
              <strong>{m.role === 'user' ? '你' : '助手'}：</strong> {m.content}
            </div>
          ))}
          {messages.length === 0 && (
            <div style={{ color: 'var(--hai)', padding: '2rem', textAlign: 'center' }}>
              提问课程相关问题，助手会结合上下文（摘要+向量检索）回答。
            </div>
          )}
        </div>
      </div>
      <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
        <input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="输入问题…"
          style={{ flex: 1, padding: '8px 12px', borderRadius: 8, border: '1px solid var(--gin)' }}
          onKeyDown={(e) => { if (e.key === 'Enter') onSend() }}
        />
        <button onClick={onSend} disabled={loading || !input.trim()} className="btn btn-primary">
          {loading ? '思考中…' : '发送'}
        </button>
      </div>
    </div>
  )
}
