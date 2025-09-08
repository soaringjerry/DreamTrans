import { useEffect, useState } from 'react'
import { loadSession, listSessions } from '../db'

// No props currently; global overlays listen for events

export default function GlobalOverlays() {
  // Overlays visibility
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [tab, setTab] = useState<'general'|'prompts'>('general')

  // Settings state (same keys as ChatPanel)
  const SETTINGS_KEY = 'dt_settings_v1'
  const [apiKey, setApiKey] = useState('')
  const [apiBase, setApiBase] = useState('https://api.openai.com/v1')
  const [model, setModel] = useState('gpt-5')
  const [promptChat, setPromptChat] = useState('请用简洁的中文、分点列出要点。')
  const DEFAULT_TRANSLATE_PROMPT = [
    'You are a professional EN->ZH translator and copy editor.',
    'Use the <context> only to understand semantics and terms.',
    'Translate ONLY the text inside <text>...</text> into Simplified Chinese.',
    'Then polish the Chinese so it is fluent, natural, and easy to read while preserving meaning and tone.',
    'Prefer concise, idiomatic phrasing; merge fragments as needed; fix awkward word order; remove filler.',
    'Keep technical terminology accurate; keep numbers/units; standardize punctuation to Chinese style when appropriate.',
    'Do NOT include any content from <context> in the output.',
    'Do NOT add explanations, quotes, speaker labels, timestamps, or language tags.',
    'Return only the final polished Chinese sentence(s), nothing else.',
  ].join(' ')
  const DEFAULT_SUMMARY_PROMPT = 'You are a precise context compressor. Summarize English conversation text for downstream translation. Keep names, entities, topics, and unresolved references. Keep it concise and information-dense. Output in English.'
  const [promptTranslate, setPromptTranslate] = useState(DEFAULT_TRANSLATE_PROMPT)
  const [promptSummary, setPromptSummary] = useState(DEFAULT_SUMMARY_PROMPT)
  const [transMode, setTransMode] = useState<'speechmatics'|'ai_rolling'|'ai_compressed'>('ai_rolling')
  const [transModel, setTransModel] = useState('gpt-5-mini')
  const [expStreaming, setExpStreaming] = useState(false)
  const [expSmart, setExpSmart] = useState(false)

  // Load settings on mount
  useEffect(() => {
    try {
      const raw = localStorage.getItem(SETTINGS_KEY)
      if (raw) {
        const s = JSON.parse(raw) as { apiKey?:string; apiBase?:string; model?:string; prompt?:string; prompt_chat?:string; prompt_translate?:string; prompt_summary?:string; transMode?:string; transModel?:string; experimental_streaming?:boolean; experimental_smart?:boolean }
        if (s.apiKey) setApiKey(s.apiKey)
        if (s.apiBase) setApiBase(s.apiBase)
        if (s.model) setModel(s.model)
        if (s.prompt_chat) setPromptChat(s.prompt_chat)
        else if (s.prompt) setPromptChat(s.prompt)
        if (s.prompt_translate) setPromptTranslate(s.prompt_translate)
        if (s.prompt_summary) setPromptSummary(s.prompt_summary)
        if (s.transMode === 'speechmatics' || s.transMode === 'ai_rolling' || s.transMode === 'ai_compressed') setTransMode(s.transMode)
        if (s.transModel) setTransModel(s.transModel)
        setExpStreaming(!!s.experimental_streaming)
        setExpSmart(!!s.experimental_smart)
      }
    } catch { /* noop */ }
  }, [])

  // Listen global events
  useEffect(() => {
    const onOpenSettings = () => { setTab('general'); setSettingsOpen(true) }
    const onOpenHistory = () => setHistoryOpen(true)
    window.addEventListener('dt-open-settings', onOpenSettings as EventListener)
    window.addEventListener('dt-open-history', onOpenHistory as EventListener)
    return () => {
      window.removeEventListener('dt-open-settings', onOpenSettings as EventListener)
      window.removeEventListener('dt-open-history', onOpenHistory as EventListener)
    }
  }, [])

  const saveSettings = () => {
    const s = {
      apiKey, apiBase, model,
      prompt: promptChat, prompt_chat: promptChat,
      prompt_translate: promptTranslate, prompt_summary: promptSummary,
      transMode, transModel,
      experimental_streaming: expStreaming, experimental_smart: expSmart,
    }
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(s))
    setSettingsOpen(false)
    window.dispatchEvent(new CustomEvent('dt-settings-updated'))
  }

  // History data: list sessions from IndexedDB
  const [sessions, setSessions] = useState<Array<{ id:string; timestamp:number }>>([])
  const [restoring, setRestoring] = useState<string | null>(null)
  useEffect(() => {
    const load = async () => { if (historyOpen) setSessions(await listSessions()) }
    load()
  }, [historyOpen])
  const restore = async (id: string) => {
    try {
      setRestoring(id)
      const ok = await loadSession(id)
      if (!ok) return
      window.dispatchEvent(new CustomEvent('dt-restore-session', { detail: { session_id: id } }))
      setHistoryOpen(false)
    } finally {
      setRestoring(null)
    }
  }

  return (
    <>
      {settingsOpen && (
        <div className="settings-overlay" onClick={() => setSettingsOpen(false)}>
          <div className="settings-modal" onClick={(e) => e.stopPropagation()}>
            <div className="settings-header">
              <div className="settings-title">设置</div>
              <div style={{ display:'flex', gap:8, alignItems:'center' }}>
                <div style={{ display:'inline-flex', gap:6, marginRight:8, background:'rgba(0,0,0,0.04)', borderRadius:999, padding:2 }}>
                  <button className={`btn btn-secondary ${tab==='general'?'active':''}`} onClick={()=>setTab('general')}>常规</button>
                  <button className={`btn btn-secondary ${tab==='prompts'?'active':''}`} onClick={()=>setTab('prompts')}>Prompts</button>
                </div>
                <button className="btn btn-secondary" onClick={() => setSettingsOpen(false)}>关闭</button>
              </div>
            </div>
            <div className="settings-body">
              {tab==='general' ? (
                <>
                  <label>API Base（默认 https://api.openai.com/v1）</label>
                  <input value={apiBase} onChange={(e)=>setApiBase(e.target.value)} placeholder="https://api.openai.com/v1" />
                  <label>Model（默认 gpt-5）</label>
                  <input value={model} onChange={(e)=>setModel(e.target.value)} placeholder="gpt-5" />
                  <label>API Key（留空使用后端配置）</label>
                  <input type="password" value={apiKey} onChange={(e)=>setApiKey(e.target.value)} placeholder="可选：自定义你的 API Key" />
                  <hr style={{ border:'none', borderTop:'1px solid var(--gin)', margin:'8px 0' }} />
                  <div style={{ fontWeight:600, color:'var(--kuro)' }}>翻译设置（全局）</div>
                  <label>Translation Mode</label>
                  <select value={transMode} onChange={(e)=>setTransMode(e.target.value as 'speechmatics'|'ai_rolling'|'ai_compressed')}>
                    <option value="speechmatics">Speechmatics Translation</option>
                    <option value="ai_rolling">AI Rolling Translation</option>
                    <option value="ai_compressed">AI Compressed Translation</option>
                  </select>
                  {(transMode==='ai_rolling' || transMode==='ai_compressed') && (
                    <>
                      <label>Translation Model</label>
                      <select value={transModel} onChange={(e)=>setTransModel(e.target.value)}>
                        <option value="gpt-5">gpt-5</option>
                        <option value="gpt-5-mini">gpt-5-mini</option>
                        <option value="gpt-5-nano">gpt-5-nano</option>
                      </select>
                    </>
                  )}
                  <hr style={{ border:'none', borderTop:'1px solid var(--gin)', margin:'8px 0' }} />
                  <div style={{ fontWeight:700, color:'var(--kuro)' }}>实验性设置（谨慎启用）</div>
                  <label>
                    <input type="checkbox" checked={expStreaming} onChange={(e)=>setExpStreaming(e.target.checked)} /> 流式输出（实验，默认关闭）
                  </label>
                  <label>
                    <input type="checkbox" checked={expSmart} onChange={(e)=>setExpSmart(e.target.checked)} /> 智能算法（实验，默认关闭）
                  </label>
                </>
              ) : (
                <>
                  <div style={{ fontWeight:600, color:'var(--kuro)' }}>Prompts</div>
                  <label>Chat Prompt</label>
                  <textarea rows={4} value={promptChat} onChange={(e)=>setPromptChat(e.target.value)} placeholder="请用简洁的中文、分点列出要点。" />
                  <label>Translation Prompt（完整系统提示，将用于替换默认）</label>
                  <textarea rows={6} value={promptTranslate} onChange={(e)=>setPromptTranslate(e.target.value)} />
                  <label>Summary Prompt（完整系统提示，将用于替换默认）</label>
                  <textarea rows={6} value={promptSummary} onChange={(e)=>setPromptSummary(e.target.value)} />
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
          <div className="settings-modal" onClick={(e)=>e.stopPropagation()}>
            <div className="settings-header">
              <div className="settings-title">会话历史</div>
              <button className="btn btn-secondary" onClick={() => setHistoryOpen(false)}>关闭</button>
            </div>
            <div className="chat-messages" style={{ maxHeight: '50vh' }}>
              {sessions.length === 0 ? (
                <div className="chat-empty">暂无会话记录</div>
              ) : (
                sessions.map((s) => (
                  <div key={s.id} className="chat-msg assistant">
                    <div className="chat-avatar">会</div>
                    <div className="chat-bubble" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 8 }}>
                      <div>
                        <div className="chat-text" style={{ fontWeight: 600 }}>{new Date(s.timestamp).toLocaleString()}</div>
                        <div style={{ fontSize: 12, color: 'var(--hai)' }}>{s.id}</div>
                      </div>
                      <button className="btn btn-primary" onClick={() => restore(s.id)} disabled={restoring === s.id}>
                        {restoring === s.id ? '恢复中…' : '恢复'}
                      </button>
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
        </div>
      )}
    </>
  )
}
