import { useEffect, useMemo, useRef, useState } from 'react'
import { askRag } from '../api'
import type { RagConfig, RagAskResponse } from '../api'
import { loadSession } from '../db'

interface ChatMessage { role: 'user' | 'assistant'; content: string; meta?: { tokens?: string; latency?: string; model?: string } }

export default function ChatPanel({ sessionId }: { sessionId: string }) {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  // Chat history persistence (per session)
  const HISTORY_KEY = useMemo(() => `dt_chat_history_${sessionId}`, [sessionId])
  const [historyOpen, setHistoryOpen] = useState(false)
  const hasLoadedHistoryRef = useRef(false)
  const [fallbackItems, setFallbackItems] = useState<string[]>([])
  const [apiKey, setApiKey] = useState<string>('')
  const [apiBase, setApiBase] = useState<string>('https://api.openai.com/v1')
  const [model, setModel] = useState<string>('gpt-5')
  const [prompt, setPrompt] = useState<string>('请用简洁的中文、分点列出要点。')
  // Translation settings (moved from outer UI)
  const [transMode, setTransMode] = useState<'speechmatics' | 'ai_rolling' | 'ai_compressed'>('ai_rolling')
  const [transModel, setTransModel] = useState<string>('gpt-5-mini')
  const [expStreaming, setExpStreaming] = useState<boolean>(false)
  const [expSmart, setExpSmart] = useState<boolean>(false)

  // Load settings from localStorage
  const SETTINGS_KEY = 'dt_settings_v1'
  useEffect(() => {
    try {
      const raw = localStorage.getItem(SETTINGS_KEY)
      if (raw) {
        const s = JSON.parse(raw) as { apiKey?: string; apiBase?: string; model?: string; prompt?: string; transMode?: string; transModel?: string; experimental_streaming?: boolean; experimental_smart?: boolean }
        if (s.apiKey) setApiKey(s.apiKey)
        if (s.apiBase) setApiBase(s.apiBase)
        if (s.model) setModel(s.model)
        if (s.prompt) setPrompt(s.prompt)
        if (s.transMode === 'speechmatics' || s.transMode === 'ai_rolling' || s.transMode === 'ai_compressed') setTransMode(s.transMode)
        if (s.transModel) setTransModel(s.transModel)
        setExpStreaming(!!s.experimental_streaming)
        setExpSmart(!!s.experimental_smart)
      }
    } catch { /* ignore */ }
  }, [])

  // Global open triggers
  useEffect(() => {
    const onOpenSettings = () => setSettingsOpen(true)
    const onOpenHistory = () => setHistoryOpen(true)
    window.addEventListener('dt-open-settings', onOpenSettings as EventListener)
    window.addEventListener('dt-open-history', onOpenHistory as EventListener)
    return () => {
      window.removeEventListener('dt-open-settings', onOpenSettings as EventListener)
      window.removeEventListener('dt-open-history', onOpenHistory as EventListener)
    }
  }, [])

  const saveSettings = () => {
    const s = { apiKey, apiBase, model, prompt, transMode, transModel, experimental_streaming: expStreaming, experimental_smart: expSmart }
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(s))
    setSettingsOpen(false)
    window.dispatchEvent(new CustomEvent('dt-settings-updated'))
  }

  // Load history on mount
  useEffect(() => {
    try {
      const raw = localStorage.getItem(HISTORY_KEY)
      if (raw) {
        const arr = JSON.parse(raw) as ChatMessage[]
        if (Array.isArray(arr)) setMessages(arr)
      }
      hasLoadedHistoryRef.current = true
    } catch { /* ignore */ }
  }, [HISTORY_KEY])

  // Save history on change (debounced minimal)
  useEffect(() => {
    try {
      // Avoid overwriting with [] before initial load completes (StrictMode/timing)
      if (hasLoadedHistoryRef.current) {
        localStorage.setItem(HISTORY_KEY, JSON.stringify(messages))
      }
    } catch { /* ignore */ }
  }, [HISTORY_KEY, messages])

  const clearHistory = () => {
    localStorage.removeItem(HISTORY_KEY)
    setMessages([])
  }

  const onSend = async () => {
    const q = input.trim()
    if (!q || loading) return
    setMessages((m) => [...m, { role: 'user', content: q }])
    setInput('')
    setLoading(true)
    try {
      // typing indicator
      setMessages((m) => [...m, { role: 'assistant', content: '…' }])
      const cfg: RagConfig = {
        api_key: apiKey || undefined,
        api_base: apiBase || undefined,
        model: model || undefined,
        prompt: prompt || undefined,
      }
      const res: RagAskResponse = await askRag(sessionId, q, 5, cfg)
      setMessages((m) => {
        const mm = [...m]
        // replace last typing indicator
        if (mm.length && mm[mm.length - 1].role === 'assistant' && mm[mm.length - 1].content === '…') {
          const tokens = res.usage ? `${res.usage.prompt_tokens}/${res.usage.completion_tokens} (${res.usage.total_tokens})` : undefined
          const latency = res.latency_ms !== undefined ? `${res.latency_ms} ms` : undefined
          const model = res.usage?.model
          mm[mm.length - 1] = { role: 'assistant', content: res.answer, meta: { tokens, latency, model } }
        } else {
          const tokens = res.usage ? `${res.usage.prompt_tokens}/${res.usage.completion_tokens} (${res.usage.total_tokens})` : undefined
          const latency = res.latency_ms !== undefined ? `${res.latency_ms} ms` : undefined
          const model = res.usage?.model
          mm.push({ role: 'assistant', content: res.answer, meta: { tokens, latency, model } })
        }
        return mm
      })
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : String(e)
      setMessages((m) => {
        const mm = [...m]
        if (mm.length && mm[mm.length - 1].role === 'assistant' && mm[mm.length - 1].content === '…') {
          mm[mm.length - 1] = { role: 'assistant', content: `请求失败：${msg}` }
        } else {
          mm.push({ role: 'assistant', content: `请求失败：${msg}` })
        }
        return mm
      })
    } finally {
      setLoading(false)
    }
  }

  // Build transcript fallback when opening History and chat is empty
  useEffect(() => {
    const maybeBuildFallback = async () => {
      if (!historyOpen) return
      if (messages.length > 0) { setFallbackItems([]); return }
      try {
        const sess = await loadSession(sessionId)
        if (!sess) return
        if (sess.translations && sess.translations.length > 0) {
          const items = sess.translations
            .filter(t => !t.isPartial && t.content && t.content.trim())
            .slice(-30)
            .map(t => t.content.trim())
          setFallbackItems(items)
        } else if (sess.lines && sess.lines.length > 0) {
          const items: string[] = []
          for (const line of sess.lines.slice(-15)) {
            const txt = line.confirmedSegments.map(s => s.text).join('').trim()
            if (txt) items.push(txt)
          }
          setFallbackItems(items)
        }
      } catch { /* ignore */ }
    }
    maybeBuildFallback()
  }, [historyOpen, sessionId, messages.length])

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
            <div className="chat-bubble">
              {m.role === 'assistant' && m.content === '…' ? (
                <span className="chat-typing">
                  <span className="dot"/><span className="dot"/><span className="dot"/>
                </span>
              ) : (
                <span className="chat-text">{m.content}</span>
              )}
              {m.role === 'assistant' && m.meta && (m.meta.tokens || m.meta.latency || m.meta.model) && (
                <div style={{ marginTop: 6, fontSize: '12px', color: 'var(--hai)' }}>
                  {m.meta.model ? `model ${m.meta.model}` : ''}
                  {m.meta.tokens ? ` · tokens ${m.meta.tokens}` : ''}
                  {m.meta.latency ? ` · latency ${m.meta.latency}` : ''}
                </div>
              )}
            </div>
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

      {settingsOpen && (
        <div className="settings-overlay" onClick={() => setSettingsOpen(false)}>
          <div className="settings-modal" onClick={(e) => e.stopPropagation()}>
            <div className="settings-header">
              <div className="settings-title">设置</div>
              <button className="btn btn-secondary" onClick={() => setSettingsOpen(false)}>关闭</button>
            </div>
            <div className="settings-body">
              <label>API Base（默认 https://api.openai.com/v1）</label>
              <input value={apiBase} onChange={(e) => setApiBase(e.target.value)} placeholder="https://api.openai.com/v1" />

              <label>Model（默认 gpt-5）</label>
              <input value={model} onChange={(e) => setModel(e.target.value)} placeholder="gpt-5" />

              <label>Prompt（默认提示已填入，可修改）</label>
              <textarea rows={4} value={prompt} onChange={(e) => setPrompt(e.target.value)} placeholder="请用简洁的中文、分点列出要点。" />

              <label>API Key（不会展示默认值，可留空以使用后端配置）</label>
              <input type="password" value={apiKey} onChange={(e) => setApiKey(e.target.value)} placeholder="可选：自定义你的 API Key" />

              <hr style={{ border: 'none', borderTop: '1px solid var(--gin)', margin: '8px 0' }} />
              <div style={{ fontWeight: 600, color: 'var(--kuro)' }}>翻译设置（全局）</div>
              <label>Translation Mode</label>
              <select value={transMode} onChange={(e) => setTransMode(e.target.value as 'speechmatics' | 'ai_rolling' | 'ai_compressed')}>
                <option value="speechmatics">Speechmatics Translation</option>
                <option value="ai_rolling">AI Rolling Translation</option>
                <option value="ai_compressed">AI Compressed Translation</option>
              </select>
              {(transMode === 'ai_rolling' || transMode === 'ai_compressed') && (
                <>
                  <label>Translation Model</label>
                  <select value={transModel} onChange={(e) => setTransModel(e.target.value)}>
                    <option value="gpt-5">gpt-5</option>
                    <option value="gpt-5-mini">gpt-5-mini</option>
                    <option value="gpt-5-nano">gpt-5-nano</option>
                  </select>
                </>
              )}

              <hr style={{ border: 'none', borderTop: '1px solid var(--gin)', margin: '8px 0' }} />
              <div style={{ fontWeight: 700, color: 'var(--kuro)' }}>实验性设置（谨慎启用）</div>
              <label>
                <input type="checkbox" checked={expStreaming} onChange={(e) => setExpStreaming(e.target.checked)} /> 流式输出（实验，默认关闭）
              </label>
              <label>
                <input type="checkbox" checked={expSmart} onChange={(e) => setExpSmart(e.target.checked)} /> 智能算法（实验，默认关闭）
              </label>
            </div>
            <div className="settings-footer">
              <button className="btn btn-primary" onClick={saveSettings}>保存</button>
            </div>
          </div>
        </div>
      )}

      {historyOpen && (
        <div className="settings-overlay" onClick={() => setHistoryOpen(false)}>
          <div className="settings-modal" onClick={(e) => e.stopPropagation()}>
            <div className="settings-header">
              <div className="settings-title">历史记录</div>
              <div style={{ display: 'flex', gap: 8 }}>
                <button className="btn btn-danger" onClick={clearHistory}>清空</button>
                <button className="btn btn-secondary" onClick={() => setHistoryOpen(false)}>关闭</button>
              </div>
            </div>
            <div className="chat-messages" style={{ maxHeight: '50vh' }}>
              {messages.length === 0 ? (
                fallbackItems.length === 0 ? (
                  <div className="chat-empty">暂无历史记录</div>
                ) : (
                  <div style={{ padding: '8px', color: 'var(--hai)' }}>
                    <div style={{ fontWeight: 600, marginBottom: 6, color: 'var(--kuro)' }}>最近转写片段（只读）</div>
                    {fallbackItems.map((t, i) => (
                      <div key={`f-${i}`} className="chat-msg assistant">
                        <div className="chat-avatar">转</div>
                        <div className="chat-bubble"><span className="chat-text">{t}</span></div>
                      </div>
                    ))}
                  </div>
                )
              ) : (
                messages.map((m, i) => (
                  <div key={`h-${i}`} className={`chat-msg ${m.role}`}>
                    <div className="chat-avatar">{m.role === 'user' ? '你' : '助'}</div>
                    <div className="chat-bubble"><span className="chat-text">{m.content}</span></div>
                  </div>
                ))
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
