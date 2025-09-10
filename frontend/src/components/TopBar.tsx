import { useEffect, useRef, useState } from 'react'

export default function TopBar() {
  const openSettings = () => {
    window.dispatchEvent(new CustomEvent('dt-open-settings'))
  }
  const openHistory = () => {
    window.dispatchEvent(new CustomEvent('dt-open-history'))
  }

  // Hover flyout for quick settings
  const [showFlyout, setShowFlyout] = useState(false)
  const hideTimer = useRef<number | null>(null)
  const onEnter = () => {
    if (hideTimer.current) { window.clearTimeout(hideTimer.current); hideTimer.current = null }
    setShowFlyout(true)
  }
  const onLeave = () => {
    if (hideTimer.current) window.clearTimeout(hideTimer.current)
    hideTimer.current = window.setTimeout(() => setShowFlyout(false), 150)
  }
  useEffect(() => () => { if (hideTimer.current) window.clearTimeout(hideTimer.current) }, [])

  return (
    <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, marginBottom: '8px', position: 'relative' }}>
      <button className="btn btn-secondary" onClick={openHistory}>历史</button>
      <div onMouseEnter={onEnter} onMouseLeave={onLeave} style={{ position: 'relative' }}>
        <button className="btn btn-secondary">设置</button>
        {showFlyout && <SettingsFlyout />}
      </div>
      {/* Fallback full settings on click */}
      <button className="btn btn-secondary" onClick={openSettings} style={{ display: 'none' }}>设置</button>
    </div>
  )
}

type SettingsStore = {
  apiKey?: string
  apiBase?: string
  model?: string
  model_chat?: string
  model_translate?: string
  model_summary?: string
  prompt?: string
  prompt_chat?: string
  prompt_translate?: string
  prompt_summary?: string
  prompt_lookup?: string
  experimental_streaming?: boolean
  experimental_smart?: boolean
  experimental_typewriter?: boolean
  transModel?: string // back-compat
}

function SettingsFlyout() {
  // read and write dt_settings_v1
  const KEY = 'dt_settings_v1'
  const [activeGroup, setActiveGroup] = useState<'models'|'prompts'|'experimental'|'api'>('models')
  const [activeModelTab, setActiveModelTab] = useState<'translate'|'summary'|'chat'>('translate')
  const [modelTranslate, setModelTranslate] = useState('')
  const [modelSummary, setModelSummary] = useState('')
  const [modelChat, setModelChat] = useState('')
  const [promptChat, setPromptChat] = useState('')
  const [promptTranslate, setPromptTranslate] = useState('')
  const [promptSummary, setPromptSummary] = useState('')
  const [promptLookup, setPromptLookup] = useState('')
  const [defaults, setDefaults] = useState<{ chat?: string; translate?: string; summary?: string }>({})
  const [apiBase, setApiBase] = useState('https://api.openai.com/v1')
  const [apiKey, setApiKey] = useState('')
  const [expStreaming, setExpStreaming] = useState(false)
  const [expSmart, setExpSmart] = useState(false)
  const [expTypewriter, setExpTypewriter] = useState(false)

  useEffect(() => {
    try {
      const raw = localStorage.getItem(KEY)
      if (raw) {
        const s = JSON.parse(raw) as SettingsStore
        setModelTranslate(s.model_translate || s.transModel || '')
        setModelSummary(s.model_summary || '')
        setModelChat(s.model_chat || s.model || '')
        setPromptChat(s.prompt_chat || s.prompt || '')
        setPromptTranslate(s.prompt_translate || '')
        setPromptSummary(s.prompt_summary || '')
        setPromptLookup(s.prompt_lookup || '')
        setApiBase(s.apiBase || 'https://api.openai.com/v1')
        setApiKey(s.apiKey || '')
        setExpStreaming(!!s.experimental_streaming)
        setExpSmart(!!s.experimental_smart)
        setExpTypewriter(!!s.experimental_typewriter)
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

  const presets = [
    'gpt-4o-2024-08-06', 'gpt-4o-mini-2024-07-18',
    'gpt-5', 'gpt-5-mini', 'gpt-5-nano'
  ]
  const save = () => {
    const raw = localStorage.getItem(KEY)
    const base = (raw ? (JSON.parse(raw) as SettingsStore) : {}) as SettingsStore
    const next: SettingsStore = {
      ...base,
      apiBase, apiKey,
      model_chat: modelChat,
      model_translate: modelTranslate,
      model_summary: modelSummary,
      prompt_chat: promptChat,
      prompt_translate: promptTranslate,
      prompt_summary: promptSummary,
      prompt_lookup: promptLookup,
      experimental_streaming: expStreaming,
      experimental_smart: expSmart,
      experimental_typewriter: expTypewriter,
    }
    localStorage.setItem(KEY, JSON.stringify(next))
    window.dispatchEvent(new CustomEvent('dt-settings-updated'))
  }

  const ModelColumn = (label: string, value: string, setValue: (v:string)=>void) => (
    <div className="flyout-right">
      <div className="flyout-section-title">{label} 模型</div>
      <div className="flyout-presets">
        {presets.map(p => (
          <button key={p} className={`flyout-pill ${value===p?'active':''}`} onClick={()=>setValue(p)}>{p}</button>
        ))}
      </div>
      <div className="flyout-input">
        <input value={value} onChange={e=>setValue(e.target.value)} placeholder="自定义模型 ID，如 gpt-4o-2024-08-06" />
        <button className="btn btn-primary" onClick={save}>保存</button>
      </div>
      <div className="flyout-hint">可直接输入供应商支持的模型名称；保存后即刻生效。</div>
    </div>
  )

  return (
    <div className="settings-flyout" onClick={(e)=>e.stopPropagation()}>
      <div className="flyout-inner">
        <div className="flyout-left">
          <div className={`flyout-tab ${activeGroup==='models'?'active':''}`} onMouseEnter={()=>setActiveGroup('models')}>模型</div>
          <div className={`flyout-tab ${activeGroup==='prompts'?'active':''}`} onMouseEnter={()=>setActiveGroup('prompts')}>Prompts</div>
          <div className={`flyout-tab ${activeGroup==='experimental'?'active':''}`} onMouseEnter={()=>setActiveGroup('experimental')}>实验</div>
          <div className={`flyout-tab ${activeGroup==='api'?'active':''}`} onMouseEnter={()=>setActiveGroup('api')}>API</div>
        </div>
        {activeGroup==='models' && (
          <div className="flyout-content">
            <div className="flyout-subtabs">
              <button className={`sub ${activeModelTab==='translate'?'active':''}`} onMouseEnter={()=>setActiveModelTab('translate')}>翻译</button>
              <button className={`sub ${activeModelTab==='summary'?'active':''}`} onMouseEnter={()=>setActiveModelTab('summary')}>总结</button>
              <button className={`sub ${activeModelTab==='chat'?'active':''}`} onMouseEnter={()=>setActiveModelTab('chat')}>Chat</button>
            </div>
            {activeModelTab==='translate' && ModelColumn('翻译', modelTranslate, setModelTranslate)}
            {activeModelTab==='summary' && ModelColumn('总结', modelSummary, setModelSummary)}
            {activeModelTab==='chat' && ModelColumn('聊天', modelChat, setModelChat)}
          </div>
        )}
        {activeGroup==='prompts' && (
          <div className="flyout-content">
            <div className="flyout-section-title">系统提示</div>
            <label>Chat Prompt <button className="btn btn-secondary" onClick={async()=>{ await loadDefaults(); if (defaults.chat) { setPromptChat(defaults.chat); save() } }}>重置</button></label>
            <textarea rows={3} value={promptChat} onChange={e=>setPromptChat(e.target.value)} />
            <label>Translation Prompt <button className="btn btn-secondary" onClick={async()=>{ await loadDefaults(); if (defaults.translate) { setPromptTranslate(defaults.translate); save() } }}>重置</button></label>
            <textarea rows={3} value={promptTranslate} onChange={e=>setPromptTranslate(e.target.value)} />
            <label>Summary Prompt <button className="btn btn-secondary" onClick={async()=>{ await loadDefaults(); if (defaults.summary) { setPromptSummary(defaults.summary); save() } }}>重置</button></label>
            <textarea rows={3} value={promptSummary} onChange={e=>setPromptSummary(e.target.value)} />
            <label>Lookup Template（词典提问模板，使用 {'{{text}}'} 占位） <button className="btn btn-secondary" onClick={()=>{ setPromptLookup('请解释以下单词或短语的含义，并给出词性、常见搭配和 2 个例句（英文+中文）：\n{{text}}'); save() }}>重置</button></label>
            <textarea rows={3} value={promptLookup} onChange={e=>setPromptLookup(e.target.value)} placeholder="例如：请解释 {{text}} 的含义…" />
            <div style={{ textAlign:'right' }}><button className="btn btn-primary" onClick={save}>保存</button></div>
          </div>
        )}
        {activeGroup==='experimental' && (
          <div className="flyout-content">
            <div className="flyout-section-title">实验功能</div>
            <label><input type="checkbox" checked={expTypewriter} onChange={e=>setExpTypewriter(e.target.checked)} /> Typewriter</label>
            <label><input type="checkbox" checked={expStreaming} onChange={e=>setExpStreaming(e.target.checked)} /> Streaming Output</label>
            <label><input type="checkbox" checked={expSmart} onChange={e=>setExpSmart(e.target.checked)} /> Smart</label>
            <div style={{ textAlign:'right' }}><button className="btn btn-primary" onClick={save}>保存</button></div>
          </div>
        )}
        {activeGroup==='api' && (
          <div className="flyout-content">
            <div className="flyout-section-title">API</div>
            <label>API Base</label>
            <input value={apiBase} onChange={e=>setApiBase(e.target.value)} placeholder="https://api.openai.com/v1" />
            <label>API Key（仅本地保存）</label>
            <input value={apiKey} onChange={e=>setApiKey(e.target.value)} placeholder="sk-..." />
            <div style={{ textAlign:'right' }}><button className="btn btn-primary" onClick={save}>保存</button></div>
          </div>
        )}
      </div>
    </div>
  )
}
