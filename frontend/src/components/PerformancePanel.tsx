import { useEffect, useMemo, useState } from 'react'
import { clamp, formatDuration } from '../utils/format'
import { getMetrics, type MetricEvent } from '../utils/metrics'
import { loadSession } from '../db'

type ApiTotals = { requests: number; prompt_tokens: number; completion_tokens: number; total_tokens: number; per_model?: Record<string, ApiTotals> }
type ApiSnapshot = { chat: ApiTotals; translate: ApiTotals; summarize: ApiTotals; overall: ApiTotals; last_logs: Array<{ ts:string; feature:string; model:string; prompt_tokens:number; completion_tokens:number; total_tokens:number; latency_ms:number }> }

type ChatMessage = { role: 'user' | 'assistant'; content: string; meta?: { tokens?: string; latency?: string; model?: string } }

function parseTokens(s?: string): { prompt: number; completion: number; total: number } | null {
  if (!s) return null
  // Expected format: "p/c (t)" e.g. "123/456 (579)"
  const m = s.match(/(\d+)\/(\d+)\s*\((\d+)\)/)
  if (!m) return null
  return { prompt: Number(m[1]), completion: Number(m[2]), total: Number(m[3]) }
}

function parseLatency(s?: string): number | null {
  if (!s) return null
  const m = s.match(/(\d+(?:\.\d+)?)\s*ms/i)
  return m ? Number(m[1]) : null
}

export default function PerformancePanel({ sessionId }: { sessionId: string }) {
  const [tab, setTab] = useState<'latency'|'api'|'lex'>('latency')
  const HISTORY_KEY = useMemo(() => `dt_chat_history_${sessionId}`, [sessionId])
  const [messages, setMessages] = useState<ChatMessage[]>([])

  useEffect(() => {
    try {
      const raw = localStorage.getItem(HISTORY_KEY)
      if (raw) {
        const arr = JSON.parse(raw) as ChatMessage[]
        if (Array.isArray(arr)) setMessages(arr)
      }
    } catch { /* ignore */ }
  }, [HISTORY_KEY])

  const stats = useMemo(() => {
    const replies = messages.filter(m => m.role === 'assistant' && m.meta)
    let totalTokens = 0
    let tokenReplies = 0
    let totalLatency = 0
    let count = 0
    for (const r of replies) {
      const tk = parseTokens(r.meta?.tokens)
      const lt = parseLatency(r.meta?.latency)
      if (tk) { totalTokens += tk.total; tokenReplies++ }
      if (lt != null) totalLatency += lt
      count++
    }
    const avgLatency = count > 0 ? Math.round((totalLatency / count) * 10) / 10 : 0
    return { turns: messages.length, replies: replies.length, totalTokens, tokenReplies, avgLatency }
  }, [messages])

  const lastFew = useMemo(() => {
    return messages
      .filter(m => m.role === 'assistant' && m.meta)
      .slice(-5)
      .reverse()
  }, [messages])

  const [events, setEvents] = useState<MetricEvent[]>([])
  useEffect(() => {
    // Initialize from global buffer to catch events fired before mount
    setEvents(getMetrics())
    const onMetric = (e: Event) => {
      const ce = e as CustomEvent
      const d = ce.detail as { kind?: 'chat'|'translation'|'transcript'; latency_ms?: number; model?: string; partial?: boolean } | undefined
      if (!d) return
      const item: MetricEvent = { kind: (d.kind ?? 'translation'), latency_ms: d.latency_ms, model: d.model, partial: !!d.partial, at: Date.now() }
      setEvents(prev => {
        const next = [item, ...prev]
        return next.slice(0, 50)
      })
    }
    window.addEventListener('dt-metrics', onMetric as EventListener)
    return () => window.removeEventListener('dt-metrics', onMetric as EventListener)
  }, [])

  // Sanitize events: drop obviously bad latencies (e.g., > 5 minutes)
  const cleanEvents = useMemo(() => events.filter(e => (e.latency_ms ?? 0) >= 0 && (e.latency_ms ?? 0) < 5 * 60_000), [events])
  const byKind = useMemo(() => ({
    transcript: cleanEvents.filter(e => e.kind === 'transcript'),
    translation: cleanEvents.filter(e => e.kind === 'translation' && !e.partial),
    chat: cleanEvents.filter(e => e.kind === 'chat')
  }), [cleanEvents])

  const percentile = (arr: number[], p: number) => {
    if (!arr.length) return 0
    const sorted = [...arr].sort((a,b)=>a-b)
    const idx = Math.min(sorted.length-1, Math.max(0, Math.round((p/100)*(sorted.length-1))))
    return Math.round(sorted[idx]*10)/10
  }
  const tLats = useMemo(()=> byKind.transcript.map(e => e.latency_ms ?? 0).filter(n=>n>0), [byKind])
  const zLats = useMemo(()=> byKind.translation.map(e => e.latency_ms ?? 0).filter(n=>n>0), [byKind])
  const cLats = useMemo(()=> byKind.chat.map(e => e.latency_ms ?? 0).filter(n=>n>0), [byKind])
  const p50T = useMemo(()=> percentile(tLats, 50), [tLats])
  const p95T = useMemo(()=> percentile(tLats, 95), [tLats])
  const p50Z = useMemo(()=> percentile(zLats, 50), [zLats])
  const p95Z = useMemo(()=> percentile(zLats, 95), [zLats])
  const p50C = useMemo(()=> percentile(cLats, 50), [cLats])
  const p95C = useMemo(()=> percentile(cLats, 95), [cLats])

  // Build mini bars per kind (last 24)
  const bars = (list: MetricEvent[]) => {
    const last = list.slice(0, 24)
    const max = Math.max(1, ...last.map(e => e.latency_ms ?? 0, 1))
    return last.map((e, i) => ({ key: i, h: Math.max(3, Math.round(((e.latency_ms ?? 0)/max)*48)) }))
  }

  const [apiSnap, setApiSnap] = useState<ApiSnapshot | null>(null)
  useEffect(() => {
    let timer: number | undefined
    const pull = async () => {
      try {
        const res = await fetch('/api/metrics')
        if (res.ok) {
          const data = await res.json() as ApiSnapshot
          setApiSnap(data)
        }
      } catch { /* ignore */ }
    }
    if (tab === 'api') {
      pull(); timer = window.setInterval(pull, 5000)
    }
    return () => { if (timer) window.clearInterval(timer) }
  }, [tab])

  // -------- Lexicon (word/phrase frequency) --------
  type LexItem = { key: string; count: number }
  const [lexLoading, setLexLoading] = useState(false)
  const [lexWords, setLexWords] = useState<LexItem[]>([])
  const [lexTerms, setLexTerms] = useState<LexItem[]>([])
  const [lexTopN, setLexTopN] = useState(20)
  const [lexMinLen, setLexMinLen] = useState(3)
  const [lexExcludeStop, setLexExcludeStop] = useState(true)

  const STOPWORDS = useMemo(()=> new Set([
    'the','a','an','and','or','of','in','on','at','to','for','from','by','with','as','is','are','was','were','be','being','been','this','that','these','those','it','its','i','you','he','she','we','they','me','him','her','us','them','my','your','his','her','our','their','mine','yours','ours','theirs','not','no','yes','do','does','did','done','have','has','had','having','will','would','can','could','should','shall','may','might','must','if','then','else','than','so','too','very','just','but','because','about','into','over','under','again','more','most','some','any','each','few','who','whom','what','which','when','where','why','how'
  ]),[])

  const computeLexicon = useMemo(() => {
    return async () => {
      setLexLoading(true)
      try {
        const sess = await loadSession(sessionId)
      const parts: string[] = []
      if (sess?.lines?.length) {
        for (const line of sess.lines) {
          const t = line.confirmedSegments.map(s => s.text).join(' ')
          if (t) parts.push(t)
        }
      }
      const text = (parts.join(' ') || '').toLowerCase()
      // tokenize english words only
      const words = Array.from(text.matchAll(/[a-z]+(?:'[a-z]+)?/g)).map(m => m[0])
      const freq = new Map<string, number>()
      for (const w of words) {
        if (w.length < lexMinLen) continue
        if (lexExcludeStop && STOPWORDS.has(w)) continue
        freq.set(w, (freq.get(w) || 0) + 1)
      }
      // top words
      const wordItems = Array.from(freq.entries()).map(([key,count])=>({key,count}))
        .sort((a,b)=> b.count - a.count)
        .slice(0, lexTopN)
      // bigrams as rough "terms"
      const bigrams: string[] = []
      for (let i=0;i<words.length-1;i++) {
        const a = words[i], b = words[i+1]
        if ((lexExcludeStop && (STOPWORDS.has(a) || STOPWORDS.has(b)))) continue
        if (a.length < lexMinLen && b.length < lexMinLen) continue
        bigrams.push(`${a} ${b}`)
      }
      const f2 = new Map<string, number>()
      for (const bg of bigrams) { f2.set(bg, (f2.get(bg) || 0) + 1) }
      const termItems = Array.from(f2.entries()).map(([key,count])=>({key,count}))
        .filter(x => x.count >= 2) // appear at least twice
        .sort((a,b)=> b.count - a.count)
        .slice(0, lexTopN)
      setLexWords(wordItems)
      setLexTerms(termItems)
    } finally {
      setLexLoading(false)
    }
  }
  }
  , [sessionId, lexTopN, lexMinLen, lexExcludeStop])

  useEffect(() => { if (tab==='lex') { void computeLexicon() } }, [tab, computeLexicon])

  return (
    <div className="column-container" style={{ height: '100%' }}>
      <h3>性能监控</h3>
      <div style={{ display:'flex', gap:6, marginBottom: 8 }}>
        <button className={`btn btn-secondary ${tab==='latency'?'active':''}`} onClick={()=>setTab('latency')}>Latency</button>
        <button className={`btn btn-secondary ${tab==='api'?'active':''}`} onClick={()=>setTab('api')}>API Metrics</button>
        <button className={`btn btn-secondary ${tab==='lex'?'active':''}`} onClick={()=>setTab('lex')}>Lexicon</button>
      </div>
      <div className="scrollable-column" style={{ height: '100%' }}>
        {tab === 'latency' && (
          <>
        {/* Summary cards */}
        <div className="perf-cards">
          <div className="perf-card">
            <h4>ASR Final (P50/P95)</h4>
            <div className="big">{formatDuration(p50T)} / {formatDuration(p95T)}</div>
            <div className="perf-bars">
              {bars(byKind.transcript).map(b => <div key={b.key} className="perf-bar" style={{ height: b.h }} />)}
            </div>
          </div>
          <div className="perf-card">
            <h4>Translate (P50/P95)</h4>
            <div className="big">{formatDuration(p50Z)} / {formatDuration(p95Z)}</div>
            <div className="perf-bars">
              {bars(byKind.translation).map(b => <div key={b.key} className="perf-bar" style={{ height: b.h, background: 'linear-gradient(180deg,#34d399,#3b82f6)' }} />)}
            </div>
          </div>
          <div className="perf-card">
            <h4>Chat (P50/P95) · Tokens</h4>
            <div className="big">{formatDuration(p50C)} / {formatDuration(p95C)}{stats.tokenReplies>0 ? ` · ${stats.totalTokens}` : ''}</div>
            <div className="perf-bars">
              {bars(byKind.chat).map(b => <div key={b.key} className="perf-bar" style={{ height: b.h, background: 'linear-gradient(180deg,#f59e0b,#ef4444)' }} />)}
            </div>
          </div>
        </div>

        {/* Recent chat replies short list */}
        {lastFew.length > 0 && (
          <div className="content-list" style={{ marginBottom: 8 }}>
            {lastFew.map((m, i) => (
              <div key={`perf-${i}`} className={`chat-msg assistant`}>
                <div className="chat-avatar">AI</div>
                <div className="chat-bubble">
                  <div style={{ fontWeight: 600 }}>Recent Reply</div>
                  <div style={{ marginTop: 6, fontSize: '12px', color: 'var(--hai)' }}>
                    {m.meta?.model ? `model ${m.meta.model}` : ''}
                    {m.meta?.tokens ? ` · tokens ${m.meta.tokens}` : ''}
                    {m.meta?.latency ? ` · latency ${m.meta.latency}` : ''}
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}

        {/* Live metrics stream, compact */}
        <div className="chat-empty" style={{ margin: '6px 0 4px 0' }}>Live Metrics</div>
        {cleanEvents.length === 0 ? (
          <div className="chat-empty">等待实时指标…</div>
        ) : (
          <div className="content-list">
            {cleanEvents.slice(0, 16).map((ev, i) => {
              const ms = ev.latency_ms ?? 0
              const w = clamp((ms / 10000) * 100, 3, 100) // scale to 10s window
              const label = ev.kind === 'transcript' ? 'T' : ev.kind === 'translation' ? '译' : '聊'
              return (
                <div key={`ev-${i}`} className="chat-msg assistant">
                  <div className="chat-avatar" title={ev.partial ? 'partial' : 'final'}>{label}</div>
                  <div className="chat-bubble" style={{ width: '100%' }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                      <div style={{ fontWeight: 600 }}>{formatDuration(ms)}</div>
                      <div style={{ fontSize: 12, color: 'var(--hai)' }}>{ev.model ?? ''}</div>
                    </div>
                    <div style={{ height: 8, background: '#f1f5f9', borderRadius: 999, marginTop: 6 }}>
                      <div style={{ width: `${w}%`, height: 8, background: 'linear-gradient(90deg,#60a5fa,#f59e0b)', borderRadius: 999 }} />
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        )}
          </>
        )}

        {tab === 'api' && (
          <>
            {!apiSnap ? (
              <div className="chat-empty">Loading API metrics…</div>
            ) : (
              <>
                <div className="perf-cards">
                  <div className="perf-card"><h4>Overall Requests</h4><div className="big">{apiSnap.overall.requests}</div></div>
                  <div className="perf-card"><h4>Overall Tokens</h4><div className="big">{apiSnap.overall.total_tokens}</div></div>
                  <div className="perf-card"><h4>Prompt / Completion</h4><div className="big">{apiSnap.overall.prompt_tokens} / {apiSnap.overall.completion_tokens}</div></div>
                </div>
                <div className="content-list">
                  {(['chat','translate','summarize'] as const).map((k) => {
                    const ft: ApiTotals = (k === 'chat' ? apiSnap.chat : k === 'translate' ? apiSnap.translate : apiSnap.summarize)
                    const models = Object.entries(ft.per_model || {})
                    return (
                      <div key={k} className="chat-msg assistant">
                        <div className="chat-avatar">{k==='chat'?'聊':k==='translate'?'译':'摘'}</div>
                        <div className="chat-bubble" style={{ width:'100%' }}>
                          <div style={{ display:'flex', justifyContent:'space-between' }}>
                            <div style={{ fontWeight:700, textTransform:'capitalize' }}>{k}</div>
                            <div style={{ fontSize:12, color:'var(--hai)' }}>Req {ft.requests} · Tok {ft.total_tokens}</div>
                          </div>
                          {models.length>0 && (
                            <div style={{ marginTop:6, fontSize:12, color:'var(--hai)' }}>
                              {models.map(([m, t]) => (
                                <div key={m} style={{ display:'flex', justifyContent:'space-between' }}>
                                  <span>{m}</span>
                                  <span>Req {t.requests} · {t.total_tokens}</span>
                                </div>
                              ))}
                            </div>
                          )}
                        </div>
                      </div>
                    )
                  })}
                </div>
                {apiSnap.last_logs && apiSnap.last_logs.length>0 && (
                  <div className="content-list" style={{ marginTop:8 }}>
                    <div className="chat-empty" style={{ margin:'4px 0' }}>Recent API Calls</div>
                    {apiSnap.last_logs.slice(0, 20).map((l, i) => (
                      <div key={`log-${i}`} className="chat-msg assistant">
                        <div className="chat-avatar">{l.feature==='chat'?'聊':l.feature==='translate'?'译':'摘'}</div>
                        <div className="chat-bubble" style={{ width:'100%' }}>
                          <div style={{ display:'flex', justifyContent:'space-between' }}>
                            <div style={{ fontWeight:600 }}>{new Date(l.ts).toLocaleTimeString()}</div>
                            <div style={{ fontSize:12, color:'var(--hai)' }}>{l.model}</div>
                          </div>
                          <div style={{ fontSize:12, color:'var(--hai)', marginTop:4 }}>P/C/T {l.prompt_tokens}/{l.completion_tokens}/{l.total_tokens} · {formatDuration(l.latency_ms)}</div>
                        </div>
                      </div>
                    ))}
                  </div>
                )}
              </>
            )}
          </>
        )}

        {tab === 'lex' && (
          <>
            <div style={{ display:'flex', gap:6, marginBottom:8, alignItems:'center' }}>
              <label style={{ fontSize:12, color:'var(--hai)' }}>Top</label>
              <input type="number" min={5} max={100} value={lexTopN} onChange={e=>setLexTopN(Math.max(5, Math.min(100, Number(e.target.value)||20)))} style={{ width:70 }} />
              <label style={{ fontSize:12, color:'var(--hai)' }}>MinLen</label>
              <input type="number" min={1} max={10} value={lexMinLen} onChange={e=>setLexMinLen(Math.max(1, Math.min(10, Number(e.target.value)||3)))} style={{ width:70 }} />
              <label style={{ fontSize:12, color:'var(--hai)' }}>
                <input type="checkbox" checked={lexExcludeStop} onChange={e=>setLexExcludeStop(e.target.checked)} /> Exclude stopwords
              </label>
              <button className="btn btn-secondary" onClick={()=>computeLexicon()} disabled={lexLoading}>{lexLoading?'计算中…':'重新计算'}</button>
            </div>
            <div className="perf-cards">
              <div className="perf-card" style={{ minWidth: 240 }}>
                <h4>Word Frequency</h4>
                {lexWords.length === 0 ? (
                  <div className="chat-empty">暂无数据（切换到该页会自动从当前会话计算）</div>
                ) : (
                  <div style={{ display:'grid', gap:6 }}>
                    {lexWords.map((w, i) => {
                      const max = lexWords[0]?.count || 1
                      const pct = Math.round((w.count / max) * 100)
                      return (
                        <div key={`w-${i}`}>
                          <div style={{ display:'flex', justifyContent:'space-between', fontSize:12, color:'var(--hai)' }}>
                            <span>{w.key}</span><span>{w.count}</span>
                          </div>
                          <div style={{ height:6, background:'#f1f5f9', borderRadius:999 }}>
                            <div style={{ width: `${Math.max(6,pct)}%`, height:6, borderRadius:999, background:'linear-gradient(90deg,#60a5fa,#22d3ee)' }} />
                          </div>
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
              <div className="perf-card" style={{ minWidth: 240 }}>
                <h4>Term (Bi-gram) Frequency</h4>
                {lexTerms.length === 0 ? (
                  <div className="chat-empty">暂无数据</div>
                ) : (
                  <div style={{ display:'grid', gap:6 }}>
                    {lexTerms.map((w, i) => {
                      const max = lexTerms[0]?.count || 1
                      const pct = Math.round((w.count / max) * 100)
                      return (
                        <div key={`t-${i}`}>
                          <div style={{ display:'flex', justifyContent:'space-between', fontSize:12, color:'var(--hai)' }}>
                            <span>{w.key}</span><span>{w.count}</span>
                          </div>
                          <div style={{ height:6, background:'#f1f5f9', borderRadius:999 }}>
                            <div style={{ width: `${Math.max(6,pct)}%`, height:6, borderRadius:999, background:'linear-gradient(90deg,#a78bfa,#f472b6)' }} />
                          </div>
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
