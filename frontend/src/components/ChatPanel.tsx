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
    <div className="column-container chat-panel">
      <div className="chat-header">
        <div className="chat-title">学习助手（RAG）</div>
        <div className="chat-subtitle">结合上下文的实时学习助理</div>
      </div>
      <div className="chat-messages">
        {messages.length === 0 && (
          <div className="chat-empty">提问课程相关问题，助手会结合上下文（摘要+向量检索）回答。</div>
        )}
        {messages.map((m, i) => (
          <div key={i} className={`chat-msg ${m.role}`}>
            <div className="chat-avatar">{m.role === 'user' ? '你' : '助'}</div>
            <div className="chat-bubble"><span className="chat-text">{m.content}</span></div>
          </div>
        ))}
      </div>
      <div className="chat-input">
        <input
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="输入问题…（Enter 发送）"
          onKeyDown={(e) => { if (e.key === 'Enter') onSend() }}
        />
        <button onClick={onSend} disabled={loading || !input.trim()} className="btn btn-primary">
          {loading ? '思考中…' : '发送'}
        </button>
      </div>
    </div>
  )
}
