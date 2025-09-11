import { useEffect, useState } from 'react'
import { loadSession, listSessions, getSessionMeta, saveSessionMeta } from '../db'

// No props currently; global overlays listen for events

export default function GlobalOverlays() {
  // Overlays visibility
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)
  const [tab, setTab] = useState<'general'|'prompts'|'experimental'>('general')

  // Settings state (same keys as ChatPanel)
  const SETTINGS_KEY = 'dt_settings_v1'
  const [apiKey, setApiKey] = useState('')
  const [apiBase, setApiBase] = useState('https://api.openai.com/v1')
  const [model, setModel] = useState('gpt-5')
  const [promptChat, setPromptChat] = useState('请用简洁的中文、分点列出要点。')
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
  const [promptTranslate, setPromptTranslate] = useState(DEFAULT_TRANSLATE_PROMPT)
  const [promptSummary, setPromptSummary] = useState(DEFAULT_SUMMARY_PROMPT)
  const [promptLookup, setPromptLookup] = useState('请解释以下单词或短语的含义，并给出词性、常见搭配和 2 个例句（英文+中文）：\n{{text}}')
  const [defaults, setDefaults] = useState<{ chat?: string; translate?: string; summary?: string }>({})
  const [modelDefaults, setModelDefaults] = useState<{ chat?: string; translate?: string; summary?: string }>({})
  const [transMode, setTransMode] = useState<'speechmatics'|'ai_rolling'|'ai_compressed'>('ai_rolling')
  const [transModel, setTransModel] = useState('gpt-5-mini')
  const [expStreaming, setExpStreaming] = useState(false)
  const [expSmart, setExpSmart] = useState(true)
  const [expTypewriter, setExpTypewriter] = useState(false)
  const [expBilingual, setExpBilingual] = useState(true)
  const [expSummary, setExpSummary] = useState(false) // Summarization (LLM) default OFF
  const [expEmbeddings, setExpEmbeddings] = useState(true) // RAG Embeddings default ON
  const [savedBlink, setSavedBlink] = useState(false)

  // Load settings on mount
  useEffect(() => {
    try {
      const raw = localStorage.getItem(SETTINGS_KEY)
      if (raw) {
        const s = JSON.parse(raw) as { apiKey?:string; apiBase?:string; model?:string; prompt?:string; prompt_chat?:string; prompt_translate?:string; prompt_summary?:string; prompt_lookup?: string; transMode?:string; transModel?:string; experimental_streaming?:boolean; experimental_smart?:boolean; experimental_typewriter?: boolean; experimental_bilingual?: boolean; experimental_summary?: boolean; experimental_embeddings?: boolean }
        if (s.apiKey) setApiKey(s.apiKey)
        if (s.apiBase) setApiBase(s.apiBase)
        if (s.model) setModel(s.model)
        if (s.prompt_chat) setPromptChat(s.prompt_chat)
        else if (s.prompt) setPromptChat(s.prompt)
        if (s.prompt_translate) setPromptTranslate(s.prompt_translate)
        if (s.prompt_summary) setPromptSummary(s.prompt_summary)
        if (s.prompt_lookup) setPromptLookup(s.prompt_lookup)
        if (s.transMode === 'speechmatics' || s.transMode === 'ai_rolling' || s.transMode === 'ai_compressed') setTransMode(s.transMode)
        if (s.transModel) setTransModel(s.transModel)
        setExpStreaming(!!s.experimental_streaming)
        setExpSmart(s.experimental_smart !== undefined ? !!s.experimental_smart : true)
        setExpTypewriter(!!s.experimental_typewriter)
        setExpBilingual(s.experimental_bilingual !== undefined ? !!s.experimental_bilingual : true)
        setExpSummary(s.experimental_summary !== undefined ? !!s.experimental_summary : false)
        setExpEmbeddings(s.experimental_embeddings !== undefined ? !!s.experimental_embeddings : true)
      }
    } catch { /* noop */ }
  }, [])

  const loadDefaults = async () => {
    if (defaults.chat && defaults.translate && defaults.summary) return
    try {
      const res = await fetch('/api/prompts/defaults')
      if (res.ok) {
        const j = await res.json() as { prompt_chat_default?: string; prompt_translate_default?: string; prompt_summary_default?: string }
        setDefaults({ chat: j.prompt_chat_default, translate: j.prompt_translate_default, summary: j.prompt_summary_default })
      }
    } catch { /* noop */ }
  }

  const loadModelDefaults = async () => {
    if (modelDefaults.chat && modelDefaults.translate && modelDefaults.summary) return
    try {
      const res = await fetch('/api/models/defaults')
      if (res.ok) {
        const j = await res.json() as { model_chat_default?: string; model_translate_default?: string; model_summary_default?: string }
        setModelDefaults({ chat: j.model_chat_default, translate: j.model_translate_default, summary: j.model_summary_default })
      }
    } catch { /* noop */ }
  }

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
      model_chat: model,
      prompt: promptChat, prompt_chat: promptChat,
      prompt_translate: promptTranslate, prompt_summary: promptSummary, prompt_lookup: promptLookup,
      transMode, transModel,
      experimental_streaming: expStreaming, experimental_smart: expSmart,
      experimental_typewriter: expTypewriter, experimental_bilingual: expBilingual,
      experimental_summary: expSummary,
      experimental_embeddings: expEmbeddings,
    }
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(s))
    setSettingsOpen(false)
    window.dispatchEvent(new CustomEvent('dt-settings-updated'))
    try { setSavedBlink(true); window.setTimeout(()=>setSavedBlink(false), 1200) } catch { /* noop */ }
  }

  // History data: list sessions from IndexedDB
  const [sessions, setSessions] = useState<Array<{ id:string; timestamp:number }>>([])
  const [sessionMeta, setSessionMeta] = useState<Record<string, { title?: string; summary?: string }>>({})
  const [restoring, setRestoring] = useState<string | null>(null)
  useEffect(() => {
    const load = async () => { if (historyOpen) setSessions(await listSessions()) }
    load()
  }, [historyOpen])

  // Fetch title and summary for top sessions (up to 10)
  useEffect(() => {
    const run = async () => {
      if (!historyOpen || sessions.length === 0) return
      const top = sessions.slice(0, 10)
      // read summarization toggle: if OFF, do not request title/summary from server
      let allowRemote = false
      try {
        const raw = localStorage.getItem('dt_settings_v1')
        if (raw) {
          const s = JSON.parse(raw) as { experimental_summary?: boolean }
          allowRemote = !!s.experimental_summary
        }
      } catch { /* noop */ }
      const updates: Record<string, { title?: string; summary?: string }> = {}
      await Promise.all(top.map(async (s) => {
        try {
          // Prefer local cached meta
          const local = await getSessionMeta(s.id)
          if (local.title) { updates[s.id] = { ...(updates[s.id]||{}), title: local.title } }
          if (local.summary) { updates[s.id] = { ...(updates[s.id]||{}), summary: local.summary } }
          // Fetch only missing pieces (and only if allowed)
          const needTitle = !local.title
          const needSummary = !local.summary
          const reqs: Array<Promise<Response>> = []
          if (allowRemote) {
            if (needTitle) reqs.push(fetch(`/api/rag/title?session_id=${encodeURIComponent(s.id)}`))
            if (needSummary) reqs.push(fetch(`/api/rag/summary?session_id=${encodeURIComponent(s.id)}`))
          }
          if (reqs.length > 0) {
            const resps = await Promise.all(reqs)
            let ri = 0
            if (needTitle) {
              const tRes = resps[ri++]
              if (tRes.ok) { const t = await tRes.json() as { title?: string }; const title = (t.title||'').trim(); if (title) { updates[s.id] = { ...(updates[s.id]||{}), title }; await saveSessionMeta(s.id, { title }) } }
            }
            if (needSummary) {
              const sumRes = resps[ri++]
              if (sumRes && sumRes.ok) { const j = await sumRes.json() as { summary?: string }; const summary = (j.summary||'').trim(); if (summary) { updates[s.id] = { ...(updates[s.id]||{}), summary }; await saveSessionMeta(s.id, { summary }) } }
            }
          }
        } catch { /* ignore */ }
      }))
      setSessionMeta(prev => ({ ...prev, ...updates }))
    }
    run()
  }, [historyOpen, sessions])
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
                  <button className={`btn btn-secondary ${tab==='experimental'?'active':''}`} onClick={()=>setTab('experimental')}>Experimental</button>
                </div>
                {savedBlink && <span style={{ color:'var(--ume)', fontWeight:700 }}>已保存 ✓</span>}
                <button className="btn btn-secondary" onClick={() => setSettingsOpen(false)}>关闭</button>
              </div>
            </div>
            <div className="settings-body">
              {tab==='general' ? (
                <>
                  <label>API Base（默认 https://api.openai.com/v1）</label>
                  <input value={apiBase} onChange={(e)=>setApiBase(e.target.value)} placeholder="https://api.openai.com/v1" />
                  <label>Chat Model（默认 {modelDefaults.chat || 'gpt-5-chat-latest'}） <button className="btn btn-secondary" onClick={async()=>{ await loadModelDefaults(); if (modelDefaults.chat) { setModel(modelDefaults.chat) } }}>重置</button></label>
                  <input value={model} onChange={(e)=>setModel(e.target.value)} placeholder={modelDefaults.chat || 'gpt-5-chat-latest'} />
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
                      <label>Translation Model（默认 {modelDefaults.translate || 'gpt-4.1-mini'}）</label>
                      <select value={transModel} onChange={(e)=>setTransModel(e.target.value)}>
                        <option value="gpt-5">gpt-5</option>
                        <option value="gpt-5-mini">gpt-5-mini</option>
                        <option value="gpt-5-nano">gpt-5-nano</option>
                      </select>
                    </>
                  )}
                </>
              ) : tab==='prompts' ? (
                <>
                  <div style={{ fontWeight:600, color:'var(--kuro)' }}>Prompts</div>
                  <label>Chat Prompt <button className="btn btn-secondary" onClick={async()=>{ await loadDefaults(); if (defaults.chat) { setPromptChat(defaults.chat); saveSettings() } }}>重置</button></label>
                  <textarea rows={4} value={promptChat} onChange={(e)=>setPromptChat(e.target.value)} placeholder="请用简洁的中文、分点列出要点。" />
                  <label>Translation Prompt（完整系统提示，将用于替换默认） <button className="btn btn-secondary" onClick={async()=>{ await loadDefaults(); if (defaults.translate) { setPromptTranslate(defaults.translate); saveSettings() } }}>重置</button></label>
                  <textarea rows={6} value={promptTranslate} onChange={(e)=>setPromptTranslate(e.target.value)} />
                  <label>Summary Prompt（完整系统提示，将用于替换默认） <button className="btn btn-secondary" onClick={async()=>{ await loadDefaults(); if (defaults.summary) { setPromptSummary(defaults.summary); saveSettings() } }}>重置</button></label>
                  <textarea rows={6} value={promptSummary} onChange={(e)=>setPromptSummary(e.target.value)} />
                  <label>Lookup Template（词典提问模板，使用 {'{{text}}'} 占位） <button className="btn btn-secondary" onClick={() => { setPromptLookup('请解释以下单词或短语的含义，并给出词性、常见搭配和 2 个例句（英文+中文）：\n{{text}}'); saveSettings() }}>重置</button></label>
                  <textarea rows={4} value={promptLookup} onChange={(e)=>setPromptLookup(e.target.value)} placeholder="例如：请解释 {{text}} 的含义…" />
                </>
              ) : (
                <>
                  <div style={{ fontWeight:700, color:'var(--kuro)' }}>实验性设置（谨慎启用）</div>
                  <div style={{ color:'var(--hai)', fontSize:12, marginBottom:6 }}>这些功能可能影响实时性或稳定性，默认关闭。</div>
                  <label>
                    <input type="checkbox" checked={expTypewriter} onChange={(e)=>setExpTypewriter(e.target.checked)} /> Typewriter Mode（打字机式输出）
                  </label>
                  <div style={{ color:'var(--hai)', fontSize:12, marginLeft:22, marginTop:-6, marginBottom:8 }}>视觉更自然，但可能稍有延迟或不完整。</div>
                  <label>
                    <input type="checkbox" checked={expStreaming} onChange={(e)=>setExpStreaming(e.target.checked)} /> Streaming Output（流式输出）
                  </label>
                  <label>
                    <input type="checkbox" checked={expSmart} onChange={(e)=>setExpSmart(e.target.checked)} /> Smart Algorithm（智能策略）
                  </label>
                  <label>
                    <input type="checkbox" checked={expBilingual} onChange={(e)=>setExpBilingual(e.target.checked)} /> Bilingual Mode（英中对照）
                  </label>
                  <label>
                    <input type="checkbox" checked={expSummary} onChange={(e)=>setExpSummary(e.target.checked)} /> Summarization（摘要 LLM，默认关闭）
                  </label>
                  <label>
                    <input type="checkbox" checked={expEmbeddings} onChange={(e)=>setExpEmbeddings(e.target.checked)} /> RAG Embeddings（学习入库，默认开启）
                  </label>
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
                sessions.slice(0,10).map((s) => {
                  const meta = sessionMeta[s.id] || {}
                  const title = (meta.title && meta.title.trim()) || new Date(s.timestamp).toLocaleString()
                  const summary = meta.summary || ''
                  return (
                    <div key={s.id} className="chat-msg assistant">
                      <div className="chat-avatar">会</div>
                      <div className="chat-bubble" style={{ width:'100%' }}>
                        <div style={{ display:'flex', justifyContent:'space-between', alignItems:'center', gap:8 }}>
                          <div>
                            <div className="chat-text" style={{ fontWeight: 700 }}>{title}</div>
                            <div style={{ fontSize: 12, color:'var(--hai)' }}>{new Date(s.timestamp).toLocaleString()}</div>
                          </div>
                          <button className="btn btn-primary" onClick={() => restore(s.id)} disabled={restoring === s.id}>
                            {restoring === s.id ? '恢复中…' : '恢复'}
                          </button>
                        </div>
                        {summary && (
                          <div style={{ marginTop:6, fontSize: 12, color: 'var(--sumi)', opacity: .85 }}>
                            {summary.length > 140 ? summary.slice(0, 140) + '…' : summary}
                          </div>
                        )}
                        <div style={{ fontSize: 11, color: 'var(--hai)', marginTop:4 }}>{s.id}</div>
                      </div>
                    </div>
                  )
                })
              )}
            </div>
          </div>
        </div>
      )}
    </>
  )
}
