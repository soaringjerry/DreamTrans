import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { askRag } from '../api'
import type { RagConfig, RagAskResponse } from '../api'
import { formatDuration } from '../utils/format'
import { emitMetric } from '../utils/metrics'
import { loadSession } from '../db'
import MarkdownView from './MarkdownView'

interface ChatMessage { role: 'user' | 'assistant'; content: string; meta?: { tokens?: string; latency?: string; model?: string } }

type ChatPanelProps = { sessionId: string; compact?: boolean }

export default function ChatPanel({ sessionId, compact }: ChatPanelProps) {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settingsTab, setSettingsTab] = useState<'general' | 'prompts'>('general')
  const listRef = useRef<HTMLDivElement | null>(null)
  const [autoScroll, setAutoScroll] = useState(true)
  // Chat history persistence (per session)
  const HISTORY_KEY = useMemo(() => `dt_chat_history_${sessionId}`, [sessionId])
  const [historyOpen, setHistoryOpen] = useState(false)
  const hasLoadedHistoryRef = useRef(false)
  const [fallbackItems, setFallbackItems] = useState<string[]>([])
  const [apiKey, setApiKey] = useState<string>('')
  const [apiBase, setApiBase] = useState<string>('https://api.openai.com/v1')
  const [model, setModel] = useState<string>('')
  const [hydrated, setHydrated] = useState(false)
  const [promptChat, setPromptChat] = useState<string>('请用简洁的中文、分点列出要点。')
  const DEFAULT_TRANSLATE_PROMPT = (
    '您是一位专业的同声传译翻译，你正在把英文的口语内容翻译成中文易于理解的话，' +
    '请使用 <context> 来帮助你理解上下文和当前场景并作出适当的纠错和润色。' +
    '请仅翻译 <text>...</text> 里的文本变成中文，然后对中文进行润色，使其流畅、自然、易读，同时保留原文含义和语气。' +
    '请尽量使用简洁、地道的措辞；根据需要合并不完整的句子；修改不合适的词序；删除填充词。' +
    '请保持专业术语的准确性；保留数字/单位；并在适当的情况下将标点符号标准化为中文格式。' +
    '请勿在输出中包含 <context> 中的任何内容。请勿添加解释、引述、说话者标签、时间戳或语言标签。' +
    '仅返回最终润色后的中文句子，其他内容请勿返回。'
  )
  const DEFAULT_SUMMARY_PROMPT = 'You are a precise context compressor. Summarize English conversation text for downstream translation. Keep names, entities, topics, and unresolved references. Keep it concise and information-dense. Output in English.'
  const [promptTranslate, setPromptTranslate] = useState<string>(DEFAULT_TRANSLATE_PROMPT)
  const [promptSummary, setPromptSummary] = useState<string>(DEFAULT_SUMMARY_PROMPT)
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
        const s = JSON.parse(raw) as { apiKey?: string; apiBase?: string; model?: string; model_chat?: string; prompt?: string; prompt_chat?: string; prompt_translate?: string; prompt_summary?: string; transMode?: string; transModel?: string; experimental_streaming?: boolean; experimental_smart?: boolean }
        if (s.apiKey) setApiKey(s.apiKey)
        if (s.apiBase) setApiBase(s.apiBase)
        if (s.model_chat) setModel(s.model_chat)
        else if (s.model) setModel(s.model)
        if (s.prompt_chat) setPromptChat(s.prompt_chat)
        else if (s.prompt) setPromptChat(s.prompt)
        if (s.prompt_translate) setPromptTranslate(s.prompt_translate)
        if (s.prompt_summary) setPromptSummary(s.prompt_summary)
        if (s.transMode === 'speechmatics' || s.transMode === 'ai_rolling' || s.transMode === 'ai_compressed') setTransMode(s.transMode)
        if (s.transModel) setTransModel(s.transModel)
        setExpStreaming(!!s.experimental_streaming)
        setExpSmart(!!s.experimental_smart)
      }
      setHydrated(true)
    } catch { /* ignore */ }
  }, [])

  // React to external settings changes (e.g., user updates model in Settings)
  useEffect(() => {
    const onUpdated = () => {
      try {
        const raw = localStorage.getItem(SETTINGS_KEY)
        if (!raw) return
        const s = JSON.parse(raw) as { apiKey?: string; apiBase?: string; model?: string; model_chat?: string; prompt?: string; prompt_chat?: string }
        if (s.apiKey !== undefined) setApiKey(s.apiKey || '')
        if (s.apiBase) setApiBase(s.apiBase)
        if (s.model_chat) setModel(s.model_chat)
        else if (s.model) setModel(s.model || '')
        if (s.prompt_chat) setPromptChat(s.prompt_chat)
        else if (s.prompt) setPromptChat(s.prompt)
      } catch { /* noop */ }
    }
    window.addEventListener('dt-settings-updated', onUpdated as EventListener)
    return () => window.removeEventListener('dt-settings-updated', onUpdated as EventListener)
  }, [])

  // ChatPanel no longer responds to global open events; global overlays handle them

  const saveSettings = () => {
    const s = { apiKey, apiBase, model, model_chat: model, prompt: promptChat, prompt_chat: promptChat, prompt_translate: promptTranslate, prompt_summary: promptSummary, transMode, transModel, experimental_streaming: expStreaming, experimental_smart: expSmart }
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
        prompt: promptChat || undefined,
      }
      const res: RagAskResponse = await askRag(sessionId, q, 5, cfg, 45000)
      setMessages((m) => {
        const mm = [...m]
        // replace last typing indicator
        if (mm.length && mm[mm.length - 1].role === 'assistant' && mm[mm.length - 1].content === '…') {
          const hasUsage = !!res.usage && ((res.usage.total_tokens ?? 0) > 0 || (res.usage.prompt_tokens ?? 0) > 0 || (res.usage.completion_tokens ?? 0) > 0)
          const tokens = hasUsage ? `${res.usage!.prompt_tokens}/${res.usage!.completion_tokens} (${res.usage!.total_tokens})` : undefined
          const latency = res.latency_ms !== undefined ? formatDuration(res.latency_ms) : undefined
          const modelShown = (cfg.model || model) || res.usage?.model
          mm[mm.length - 1] = { role: 'assistant', content: res.answer, meta: { tokens, latency, model: modelShown } }
        } else {
          const hasUsage = !!res.usage && ((res.usage.total_tokens ?? 0) > 0 || (res.usage.prompt_tokens ?? 0) > 0 || (res.usage.completion_tokens ?? 0) > 0)
          const tokens = hasUsage ? `${res.usage!.prompt_tokens}/${res.usage!.completion_tokens} (${res.usage!.total_tokens})` : undefined
          const latency = res.latency_ms !== undefined ? formatDuration(res.latency_ms) : undefined
          const modelShown = (cfg.model || model) || res.usage?.model
          mm.push({ role: 'assistant', content: res.answer, meta: { tokens, latency, model: modelShown } })
        }
        return mm
      })
      // emit metrics event
      if (res.latency_ms != null) {
        emitMetric({ kind: 'chat', latency_ms: res.latency_ms, model: res.usage?.model })
      }
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

  // External trigger: allow other components to programmatically send a question
  const sendText = useCallback(async (text: string) => {
    const q = (text || '').trim()
    if (!q || loading) return
    setMessages((m) => [...m, { role: 'user', content: q }])
    setLoading(true)
    try {
      // typing indicator
      setMessages((m) => [...m, { role: 'assistant', content: '…' }])
      const cfg: RagConfig = {
        api_key: apiKey || undefined,
        api_base: apiBase || undefined,
        model: model || undefined,
        prompt: promptChat || undefined,
      }
      const res: RagAskResponse = await askRag(sessionId, q, 5, cfg, 45000)
      setMessages((m) => {
        const mm = [...m]
        if (mm.length && mm[mm.length - 1].role === 'assistant' && mm[mm.length - 1].content === '…') {
          const hasUsage = !!res.usage && ((res.usage.total_tokens ?? 0) > 0 || (res.usage.prompt_tokens ?? 0) > 0 || (res.usage.completion_tokens ?? 0) > 0)
          const tokens = hasUsage ? `${res.usage!.prompt_tokens}/${res.usage!.completion_tokens} (${res.usage!.total_tokens})` : undefined
          const latency = res.latency_ms !== undefined ? formatDuration(res.latency_ms) : undefined
          const modelShown = (cfg.model || model) || res.usage?.model
          mm[mm.length - 1] = { role: 'assistant', content: res.answer, meta: { tokens, latency, model: modelShown } }
        } else {
          const hasUsage = !!res.usage && ((res.usage.total_tokens ?? 0) > 0 || (res.usage.prompt_tokens ?? 0) > 0 || (res.usage.completion_tokens ?? 0) > 0)
          const tokens = hasUsage ? `${res.usage!.prompt_tokens}/${res.usage!.completion_tokens} (${res.usage!.total_tokens})` : undefined
          const latency = res.latency_ms !== undefined ? formatDuration(res.latency_ms) : undefined
          const modelShown = (cfg.model || model) || res.usage?.model
          mm.push({ role: 'assistant', content: res.answer, meta: { tokens, latency, model: modelShown } })
        }
        return mm
      })
      if (res.latency_ms != null) {
        emitMetric({ kind: 'chat', latency_ms: res.latency_ms, model: res.usage?.model })
      }
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
  }, [apiKey, apiBase, model, promptChat, sessionId, loading])

  const sendTextRef = useRef(sendText)
  useEffect(() => { sendTextRef.current = sendText }, [sendText])
  // Flush any pending questions queued before Chat mounted
  useEffect(() => {
    if (!hydrated) return
    try {
      const w = window as unknown as { __dt_pending_chat?: string[] }
      const arr: string[] | undefined = w.__dt_pending_chat
      if (Array.isArray(arr) && arr.length) {
        const pending = [...arr]
        w.__dt_pending_chat = []
        // Fire sequentially after hydration so the latest model applies
        setTimeout(() => { pending.forEach(t => { if (t && t.trim()) void sendTextRef.current(t) }) }, 0)
      }
    } catch { /* noop */ }
  }, [hydrated])
  useEffect(() => {
    const handler = (e: Event) => {
      const ce = e as CustomEvent
      const payload = ce.detail as { text?: string } | undefined
      const text = (payload?.text || '').toString()
      if (text.trim()) { void sendTextRef.current(text) }
    }
    window.addEventListener('dt-chat-send', handler as EventListener)
    return () => window.removeEventListener('dt-chat-send', handler as EventListener)
  }, [])

  // Smart auto-scroll: follow tail unless user scrolled up
  useEffect(() => {
    const el = listRef.current
    if (!el) return
    if (autoScroll) {
      el.scrollTop = el.scrollHeight
    }
  }, [messages, autoScroll])
  const onScroll = useCallback(() => {
    const el = listRef.current
    if (!el) return
    const threshold = 40
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < threshold
    setAutoScroll(nearBottom)
  }, [])

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
      {!compact && (
        <div className="chat-header">
          <div className="chat-title">学习助手（RAG）</div>
          <div className="chat-subtitle">结合上下文的实时学习助理</div>
        </div>
      )}
      <div className="chat-messages" ref={listRef} onScroll={onScroll}>
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
                m.role === 'assistant' ? (
                  <div className="chat-text markdown"><MarkdownView text={m.content} /></div>
                ) : (
                  <span className="chat-text">{m.content}</span>
                )
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
              <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                <div style={{ display: 'inline-flex', gap: 6, marginRight: 8, background: 'rgba(0,0,0,0.04)', borderRadius: 999, padding: 2 }}>
                  <button className={`btn btn-secondary ${settingsTab === 'general' ? 'active' : ''}`} onClick={() => setSettingsTab('general')}>常规</button>
                  <button className={`btn btn-secondary ${settingsTab === 'prompts' ? 'active' : ''}`} onClick={() => setSettingsTab('prompts')}>Prompts</button>
                </div>
                <button className="btn btn-secondary" onClick={() => setSettingsOpen(false)}>关闭</button>
              </div>
            </div>
            <div className="settings-body">
              {settingsTab === 'general' ? (
                <>
                  <label>API Base（默认 https://api.openai.com/v1）</label>
                  <input value={apiBase} onChange={(e) => setApiBase(e.target.value)} placeholder="https://api.openai.com/v1" />

                  <label>Model（默认 gpt-5）</label>
                  <input value={model} onChange={(e) => setModel(e.target.value)} placeholder="gpt-5" />

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
                </>
              ) : (
                <>
                  <div style={{ fontWeight: 600, color: 'var(--kuro)' }}>Prompts</div>
                  <label>Chat Prompt</label>
                  <textarea rows={4} value={promptChat} onChange={(e) => setPromptChat(e.target.value)} placeholder="请用简洁的中文、分点列出要点。" />

                  <label>Translation Prompt（完整系统提示，将用于替换默认）</label>
                  <textarea rows={4} value={promptTranslate} onChange={(e) => setPromptTranslate(e.target.value)} placeholder="例如：术语一致、保留数字单位、技术文风等" />

                  <label>Summary Prompt（完整系统提示，将用于替换默认）</label>
                  <textarea rows={4} value={promptSummary} onChange={(e) => setPromptSummary(e.target.value)} placeholder="例如：更侧重保留实体名、主题、疑问点" />
                </>
              )}
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
