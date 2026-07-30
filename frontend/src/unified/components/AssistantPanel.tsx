import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { askRag, getOptionalAuthHeaders, type RagConfig } from '../../api'
import MarkdownView from '../../components/MarkdownView'
import { emitMetric } from '../../utils/metrics'
import {
  chatHistoryKey,
  legacyChatHistoryKey,
} from '../workspace/browserStorageKeys'
import { Icon } from './Icon'

interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  meta?: string
}

interface AssistantPanelProps {
  config?: RagConfig
  ownerId: string | null
  sessionId: string
  suggestedQuestion?: string
  transcriptContext: string
}

const backendURL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080'
const apiBase = backendURL === '/' ? '' : backendURL

function readHistory(ownerId: string | null, sessionId: string): ChatMessage[] {
  if (!sessionId) return []
  try {
    const scopedKey = chatHistoryKey(ownerId, sessionId)
    let serialized = localStorage.getItem(scopedKey)
    // Legacy chat history had no account scope. It is safe to migrate only for
    // anonymous sessions; authenticated users must never inherit that data.
    if (!serialized && ownerId === null) {
      const legacyKey = legacyChatHistoryKey(sessionId)
      serialized = localStorage.getItem(legacyKey)
      if (serialized) {
        localStorage.setItem(scopedKey, serialized)
        localStorage.removeItem(legacyKey)
      }
    }
    const parsed = JSON.parse(serialized || '[]') as ChatMessage[]
    return Array.isArray(parsed) ? parsed.slice(-50) : []
  } catch {
    return []
  }
}

export function AssistantPanel({
  config,
  ownerId,
  sessionId,
  suggestedQuestion,
  transcriptContext,
}: AssistantPanelProps) {
  const [tab, setTab] = useState<'chat' | 'summary'>('chat')
  const [messages, setMessages] = useState<ChatMessage[]>(
    () => readHistory(ownerId, sessionId),
  )
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [summary, setSummary] = useState('')
  const [summaryLoading, setSummaryLoading] = useState(false)
  const listRef = useRef<HTMLDivElement>(null)
  const summaryLoadingRef = useRef(false)

  useEffect(() => {
    setMessages(readHistory(ownerId, sessionId))
    setSummary('')
  }, [ownerId, sessionId])

  useEffect(() => {
    if (!suggestedQuestion) return
    setTab('chat')
    setInput(suggestedQuestion)
  }, [suggestedQuestion])

  useEffect(() => {
    if (!sessionId) return
    try {
      localStorage.setItem(
        chatHistoryKey(ownerId, sessionId),
        JSON.stringify(messages.slice(-50)),
      )
    } catch {
      // Chat history remains available for the current tab.
    }
  }, [messages, ownerId, sessionId])

  useEffect(() => {
    listRef.current?.scrollTo({ top: listRef.current.scrollHeight })
  }, [messages, loading])

  const send = async (event: FormEvent) => {
    event.preventDefault()
    const question = input.trim()
    if (!question || loading) return
    setInput('')
    setLoading(true)
    const userMessage: ChatMessage = {
      id: crypto.randomUUID(),
      role: 'user',
      content: question,
    }
    setMessages((current) => [...current, userMessage])
    const startedAt = performance.now()
    try {
      const context = transcriptContext
        ? `当前会话最近内容：\n${transcriptContext}\n\n用户问题：${question}`
        : question
      const response = await askRag(
        sessionId || 'current_session',
        context,
        5,
        config,
        45_000,
      )
      const details = [
        response.usage?.model,
        response.usage?.total_tokens ? `${response.usage.total_tokens} tokens` : '',
        response.latency_ms ? `${(response.latency_ms / 1_000).toFixed(1)}s` : '',
      ].filter(Boolean).join(' · ')
      emitMetric({
        kind: 'chat',
        latency_ms: response.latency_ms ?? performance.now() - startedAt,
        model: response.usage?.model,
      })
      setMessages((current) => [...current, {
        id: crypto.randomUUID(),
        role: 'assistant',
        content: response.answer,
        meta: details,
      }])
    } catch (reason) {
      setMessages((current) => [...current, {
        id: crypto.randomUUID(),
        role: 'assistant',
        content: `请求失败：${reason instanceof Error ? reason.message : String(reason)}`,
      }])
    } finally {
      setLoading(false)
    }
  }

  const loadSummary = useCallback(async () => {
    if (!sessionId || summaryLoadingRef.current) return
    summaryLoadingRef.current = true
    setSummaryLoading(true)
    try {
      const headers = await getOptionalAuthHeaders()
      const response = await fetch(
        `${apiBase}/api/rag/summary?session_id=${encodeURIComponent(sessionId)}`,
        { headers },
      )
      if (!response.ok) throw new Error(await response.text())
      const data = await response.json() as { summary?: string }
      setSummary(data.summary || '')
    } catch (reason) {
      setSummary(`摘要获取失败：${reason instanceof Error ? reason.message : String(reason)}`)
    } finally {
      summaryLoadingRef.current = false
      setSummaryLoading(false)
    }
  }, [sessionId])

  useEffect(() => {
    if (tab === 'summary' && sessionId && !summary) void loadSummary()
  }, [loadSummary, sessionId, summary, tab])

  return (
    <div className="dt-assistant">
      <div className="dt-segmented dt-segmented--full" role="tablist" aria-label="AI 工具">
        <button
          aria-selected={tab === 'chat'}
          className={tab === 'chat' ? 'is-active' : ''}
          onClick={() => setTab('chat')}
          role="tab"
          type="button"
        >
          对话
        </button>
        <button
          aria-selected={tab === 'summary'}
          className={tab === 'summary' ? 'is-active' : ''}
          onClick={() => setTab('summary')}
          role="tab"
          type="button"
        >
          会话摘要
        </button>
      </div>

      {tab === 'chat' ? (
        <>
          {messages.length > 0 && (
            <div className="dt-summary__toolbar">
              <span className="dt-muted">仅保存在当前浏览器，最多 50 条。</span>
              <button
                className="dt-button dt-button--secondary dt-button--small"
                disabled={loading}
                onClick={() => {
                  setMessages([])
                  try {
                    localStorage.removeItem(chatHistoryKey(ownerId, sessionId))
                  } catch {
                    // Current-tab state is still cleared.
                  }
                }}
                type="button"
              >
                清空对话
              </button>
            </div>
          )}
          <div className="dt-chat__messages" ref={listRef}>
            {messages.length === 0 && (
              <div className="dt-empty dt-empty--compact">
                <Icon name="sparkles" size={24} />
                <strong>基于当前会话提问</strong>
                <span>例如：“刚才讨论了哪些行动项？”</span>
              </div>
            )}
            {messages.map((message) => (
              <article className={`dt-chat__message dt-chat__message--${message.role}`} key={message.id}>
                <div className="dt-chat__bubble">
                  {message.role === 'assistant'
                    ? <MarkdownView text={message.content} />
                    : message.content}
                </div>
                {message.meta && <small>{message.meta}</small>}
              </article>
            ))}
            {loading && (
              <div className="dt-chat__thinking" aria-live="polite">
                <span /><span /><span />
              </div>
            )}
          </div>
          <form className="dt-chat__composer" onSubmit={(event) => { void send(event) }}>
            <textarea
              aria-label="向 AI 提问"
              disabled={loading}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !event.shiftKey) {
                  event.preventDefault()
                  event.currentTarget.form?.requestSubmit()
                }
              }}
              placeholder="询问当前会话…"
              rows={2}
              value={input}
            />
            <button
              aria-label="发送"
              className="dt-button dt-button--primary"
              disabled={!input.trim() || loading}
              type="submit"
            >
              发送
            </button>
          </form>
        </>
      ) : (
        <div className="dt-summary">
          <div className="dt-summary__toolbar">
            <span className="dt-muted">根据当前会话上下文生成</span>
            <button
              className="dt-button dt-button--secondary dt-button--small"
              disabled={summaryLoading}
              onClick={() => { void loadSummary() }}
              type="button"
            >
              {summaryLoading ? '更新中…' : '刷新'}
            </button>
          </div>
          {summary ? (
            <MarkdownView text={summary} />
          ) : (
            <div className="dt-empty dt-empty--compact">
              <span>内容积累后将在这里显示摘要。</span>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
