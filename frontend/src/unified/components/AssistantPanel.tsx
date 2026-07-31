import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from 'react'
import {
  addProjectMemory,
  askRag,
  createAIProject,
  deleteAIProject,
  deleteKnowledgeSource,
  generateAIArtifact,
  linkProjectSession,
  listAIArtifacts,
  listAIProjects,
  listKnowledgeSources,
  previewAIContext,
  uploadKnowledgeFile,
  type AIArtifact,
  type AIProject,
  type KnowledgeSource,
  type RagConfig,
  type RagContextMetadata,
  type RagContextMode,
  type RagContextPolicy,
  type RagTranscriptSegment,
} from '../../api'
import MarkdownView from '../../components/MarkdownView'
import { emitMetric } from '../../utils/metrics'
import {
  chatHistoryKey,
  legacyChatHistoryKey,
} from '../workspace/browserStorageKeys'
import {
  listLocalArtifacts,
  saveLocalArtifact,
} from '../workspace/AiArtifactStore'
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

const contextPresets = [16_000, 64_000, 128_000, 256_000]

function readHistory(ownerId: string | null, sessionId: string): ChatMessage[] {
  if (!sessionId) return []
  try {
    const scopedKey = chatHistoryKey(ownerId, sessionId)
    let serialized = localStorage.getItem(scopedKey)
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

function transcriptSegments(context: string): RagTranscriptSegment[] {
  return context
    .split('\n')
    .map((line, index) => {
      const match = line.match(/^\[(\d+(?:\.\d+)?)s\]\s+([^:]+):\s*(.*)$/)
      if (!match) {
        return { id: `context-${index}`, text: line.trim() }
      }
      return {
        id: `context-${index}`,
        start_time: Number(match[1]),
        speaker: match[2].trim(),
        text: match[3].trim(),
      }
    })
    .filter((segment) => segment.text)
}

function contextDescription(metadata?: RagContextMetadata): string {
  if (!metadata) return ''
  const retrieval = metadata.rag_used ? 'RAG 已使用' : '未使用 RAG'
  const truncated = metadata.truncated ? ' · 已截断' : ''
  const sources = metadata.sources?.length ? ` · ${metadata.sources.length} 个来源` : ''
  return `${metadata.effective_mode} · 约 ${metadata.estimated_tokens.toLocaleString()} tokens · ${retrieval}${sources}${truncated}`
}

export function AssistantPanel({
  config,
  ownerId,
  sessionId,
  suggestedQuestion,
  transcriptContext,
}: AssistantPanelProps) {
  const [tab, setTab] = useState<'chat' | 'artifacts' | 'memory'>('chat')
  const [messages, setMessages] = useState<ChatMessage[]>(
    () => readHistory(ownerId, sessionId),
  )
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [contextMode, setContextMode] = useState<RagContextMode>('smart')
  const [maxContextTokens, setMaxContextTokens] = useState(64_000)
  const [contextMetadata, setContextMetadata] = useState<RagContextMetadata>()
  const [contextPreview, setContextPreview] = useState('')
  const [artifacts, setArtifacts] = useState<AIArtifact[]>([])
  const [artifactLoading, setArtifactLoading] = useState<string | null>(null)
  const [artifactError, setArtifactError] = useState('')
  const [projects, setProjects] = useState<AIProject[]>([])
  const [projectId, setProjectId] = useState('')
  const [newProjectName, setNewProjectName] = useState('')
  const [sources, setSources] = useState<KnowledgeSource[]>([])
  const [memoryName, setMemoryName] = useState('')
  const [memoryContent, setMemoryContent] = useState('')
  const [memoryBusy, setMemoryBusy] = useState(false)
  const [memoryError, setMemoryError] = useState('')
  const listRef = useRef<HTMLDivElement>(null)
  const policy: RagContextPolicy = useMemo(
    () => ({ mode: contextMode, max_tokens: maxContextTokens }),
    [contextMode, maxContextTokens],
  )
  const clientTranscript = useMemo(
    () => transcriptSegments(transcriptContext),
    [transcriptContext],
  )
  const effectiveClientTranscript = contextMode === 'retrieval' ? [] : clientTranscript

  useEffect(() => {
    setMessages(readHistory(ownerId, sessionId))
    setArtifacts([])
    setContextMetadata(undefined)
    setContextPreview('')
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
      // Keep current-tab state when browser storage is unavailable.
    }
  }, [messages, ownerId, sessionId])

  useEffect(() => {
    listRef.current?.scrollTo({ top: listRef.current.scrollHeight })
  }, [messages, loading])

  const refreshArtifacts = useCallback(async () => {
    if (!sessionId) return
    setArtifactError('')
    try {
      setArtifacts(ownerId
        ? await listAIArtifacts(sessionId)
        : await listLocalArtifacts(sessionId))
    } catch (reason) {
      setArtifactError(reason instanceof Error ? reason.message : String(reason))
    }
  }, [ownerId, sessionId])

  useEffect(() => {
    if (tab === 'artifacts') void refreshArtifacts()
  }, [refreshArtifacts, tab])

  const refreshProjects = useCallback(async () => {
    if (!ownerId) return
    try {
      const next = await listAIProjects()
      setProjects(next)
      setProjectId((current) => (
        current && next.some((project) => project.id === current) ? current : ''
      ))
    } catch (reason) {
      setMemoryError(reason instanceof Error ? reason.message : String(reason))
    }
  }, [ownerId])

  useEffect(() => {
    if (tab === 'memory' || ownerId) void refreshProjects()
  }, [ownerId, refreshProjects, tab])

  const refreshSources = useCallback(async () => {
    if (!projectId) {
      setSources([])
      return
    }
    try {
      setSources(await listKnowledgeSources(projectId))
    } catch (reason) {
      setMemoryError(reason instanceof Error ? reason.message : String(reason))
    }
  }, [projectId])

  useEffect(() => {
    void refreshSources()
  }, [refreshSources])

  useEffect(() => {
    if (!sources.some((source) => source.status === 'queued' || source.status === 'processing')) {
      return
    }
    const timer = window.setInterval(() => { void refreshSources() }, 3_000)
    return () => window.clearInterval(timer)
  }, [refreshSources, sources])

  const send = async (event: FormEvent) => {
    event.preventDefault()
    const question = input.trim()
    if (!question || loading) return
    const history = messages
      .slice(-12)
      .map(({ role, content }) => ({ role, content }))
    setInput('')
    setLoading(true)
    setMessages((current) => [...current, {
      id: crypto.randomUUID(),
      role: 'user',
      content: question,
    }])
    const startedAt = performance.now()
    try {
      const response = await askRag(
        sessionId || 'current_session',
        question,
        6,
        config,
        90_000,
        {
          history,
          clientTranscript: effectiveClientTranscript,
          contextPolicy: policy,
          projectId: projectId || undefined,
        },
      )
      setContextMetadata(response.context)
      const details = [
        response.usage?.model,
        response.usage?.total_tokens
          ? `${response.usage.total_tokens.toLocaleString()} tokens`
          : '',
        response.usage?.cached_tokens
          ? `${response.usage.cached_tokens.toLocaleString()} cached`
          : '',
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

  const previewContext = async () => {
    if (contextMode === 'retrieval') {
      setContextPreview('仅检索模式不会发送全文。系统会根据你下一条问题，从已建立的会话索引和所选项目知识库中选择相关分块。')
      return
    }
    try {
      const preview = await previewAIContext(sessionId, effectiveClientTranscript, policy)
      setContextPreview(
        `${preview.effective_mode} · ${preview.segment_count} 段 · 约 ${preview.estimated_tokens.toLocaleString()} tokens${preview.truncated ? ' · 已截断' : ''}\n\n${preview.preview}`,
      )
    } catch (reason) {
      setContextPreview(reason instanceof Error ? reason.message : String(reason))
    }
  }

  const generateArtifact = async (artifactType: AIArtifact['artifact_type']) => {
    if (!sessionId || artifactLoading) return
    setArtifactLoading(artifactType)
    setArtifactError('')
    try {
      const response = await generateAIArtifact(
        sessionId,
        artifactType,
        effectiveClientTranscript,
        policy,
        config,
        projectId || undefined,
      )
      setContextMetadata(response.context)
      if (!ownerId) await saveLocalArtifact(sessionId, response.artifact)
      await refreshArtifacts()
    } catch (reason) {
      setArtifactError(reason instanceof Error ? reason.message : String(reason))
    } finally {
      setArtifactLoading(null)
    }
  }

  const createProject = async () => {
    const name = newProjectName.trim()
    if (!name || memoryBusy) return
    setMemoryBusy(true)
    setMemoryError('')
    try {
      const project = await createAIProject(name)
      if (sessionId) await linkProjectSession(project.id, sessionId)
      setProjects((current) => [project, ...current])
      setProjectId(project.id)
      setNewProjectName('')
    } catch (reason) {
      setMemoryError(reason instanceof Error ? reason.message : String(reason))
    } finally {
      setMemoryBusy(false)
    }
  }

  const removeProject = async () => {
    if (!projectId || memoryBusy || !window.confirm('删除项目及其中的所有记忆和文件？此操作无法撤销。')) {
      return
    }
    setMemoryBusy(true)
    setMemoryError('')
    try {
      await deleteAIProject(projectId)
      setProjectId('')
      setSources([])
      await refreshProjects()
    } catch (reason) {
      setMemoryError(reason instanceof Error ? reason.message : String(reason))
    } finally {
      setMemoryBusy(false)
    }
  }

  const removeSource = async (source: KnowledgeSource) => {
    if (!projectId || memoryBusy || !window.confirm(`从项目中移除“${source.name}”？`)) {
      return
    }
    setMemoryBusy(true)
    setMemoryError('')
    try {
      await deleteKnowledgeSource(projectId, source.id)
      await refreshSources()
    } catch (reason) {
      setMemoryError(reason instanceof Error ? reason.message : String(reason))
    } finally {
      setMemoryBusy(false)
    }
  }

  const addMemory = async () => {
    if (!projectId || !memoryName.trim() || !memoryContent.trim() || memoryBusy) return
    setMemoryBusy(true)
    setMemoryError('')
    try {
      await addProjectMemory(projectId, memoryName.trim(), memoryContent.trim())
      setMemoryName('')
      setMemoryContent('')
      await refreshSources()
    } catch (reason) {
      setMemoryError(reason instanceof Error ? reason.message : String(reason))
    } finally {
      setMemoryBusy(false)
    }
  }

  const uploadFile = async (file?: File) => {
    if (!projectId || !file || memoryBusy) return
    setMemoryBusy(true)
    setMemoryError('')
    try {
      await uploadKnowledgeFile(projectId, file)
      await refreshSources()
    } catch (reason) {
      setMemoryError(reason instanceof Error ? reason.message : String(reason))
    } finally {
      setMemoryBusy(false)
    }
  }

  return (
    <div className="dt-assistant">
      <div className="dt-segmented dt-segmented--full" role="tablist" aria-label="AI 工具">
        {([
          ['chat', '对话'],
          ['artifacts', '生成'],
          ['memory', '项目与记忆'],
        ] as const).map(([value, label]) => (
          <button
            aria-selected={tab === value}
            className={tab === value ? 'is-active' : ''}
            key={value}
            onClick={() => setTab(value)}
            role="tab"
            type="button"
          >
            {label}
          </button>
        ))}
      </div>

      <div className="dt-summary__toolbar dt-ai-context-controls">
        <label>
          上下文
          <select
            onChange={(event) => setContextMode(event.target.value as RagContextMode)}
            value={contextMode}
          >
            <option value="smart">智能</option>
            <option value="full">全文</option>
            <option value="retrieval">仅检索</option>
          </select>
        </label>
        <label>
          上限
          <select
            onChange={(event) => setMaxContextTokens(Number(event.target.value))}
            value={maxContextTokens}
          >
            {contextPresets.map((tokens) => (
              <option key={tokens} value={tokens}>{tokens / 1_000}K</option>
            ))}
          </select>
        </label>
        <button
          className="dt-button dt-button--secondary dt-button--small"
          onClick={() => { void previewContext() }}
          type="button"
        >
          预览
        </button>
      </div>
      {contextMetadata && (
        <small className="dt-ai-context-status">{contextDescription(contextMetadata)}</small>
      )}
      {contextPreview && (
        <details className="dt-ai-context-preview" open>
          <summary>AI 将读取的内容</summary>
          <pre>{contextPreview}</pre>
        </details>
      )}

      {tab === 'chat' && (
        <>
          {messages.length > 0 && (
            <div className="dt-summary__toolbar">
              <span className="dt-muted">保留当前会话最近 50 条对话</span>
              <button
                className="dt-button dt-button--secondary dt-button--small"
                disabled={loading}
                onClick={() => {
                  setMessages([])
                  localStorage.removeItem(chatHistoryKey(ownerId, sessionId))
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
            {loading && <div className="dt-chat__thinking"><span /><span /><span /></div>}
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
            <button className="dt-button dt-button--primary" disabled={!input.trim() || loading} type="submit">
              发送
            </button>
          </form>
        </>
      )}

      {tab === 'artifacts' && (
        <div className="dt-summary">
          <div className="dt-ai-artifact-actions">
            {([
              ['summary', '生成摘要'],
              ['notes', '生成笔记'],
              ['action_items', '提取行动项'],
            ] as const).map(([type, label]) => (
              <button
                className="dt-button dt-button--secondary"
                disabled={artifactLoading !== null || clientTranscript.length === 0}
                key={type}
                onClick={() => { void generateArtifact(type) }}
                type="button"
              >
                {artifactLoading === type ? '生成中…' : label}
              </button>
            ))}
          </div>
          <small className="dt-muted">只在点击后生成；旧版本的自动摘要不会在这里触发。</small>
          {artifactError && <div className="dt-inline-error">{artifactError}</div>}
          {artifacts.length === 0 && !artifactError && (
            <div className="dt-empty dt-empty--compact"><span>尚未生成任何内容。</span></div>
          )}
          {artifacts.map((artifact) => (
            <article className="dt-ai-artifact" key={artifact.id}>
              <header>
                <strong>{artifact.title}</strong>
                <small>{new Date(artifact.created_at).toLocaleString()}</small>
              </header>
              <MarkdownView text={artifact.content} />
            </article>
          ))}
        </div>
      )}

      {tab === 'memory' && (
        <div className="dt-summary">
          {!ownerId ? (
            <div className="dt-empty dt-empty--compact">
              <strong>项目知识库需要登录</strong>
              <span>匿名会话仍可使用聊天和本地保存的生成内容。</span>
            </div>
          ) : (
            <>
              <div className="dt-summary__toolbar">
                <select
                  onChange={(event) => {
                    const nextProjectId = event.target.value
                    setProjectId(nextProjectId)
                    if (nextProjectId && sessionId) {
                      void linkProjectSession(nextProjectId, sessionId).catch((reason) => {
                        setMemoryError(reason instanceof Error ? reason.message : String(reason))
                      })
                    }
                  }}
                  value={projectId}
                >
                  <option value="">选择项目</option>
                  {projects.map((project) => (
                    <option key={project.id} value={project.id}>{project.name}</option>
                  ))}
                </select>
                {projectId && (
                  <button
                    className="dt-button dt-button--secondary dt-button--small"
                    disabled={memoryBusy}
                    onClick={() => { void removeProject() }}
                    type="button"
                  >
                    删除项目
                  </button>
                )}
              </div>
              <div className="dt-ai-memory-row">
                <input
                  onChange={(event) => setNewProjectName(event.target.value)}
                  placeholder="新项目名称"
                  value={newProjectName}
                />
                <button className="dt-button dt-button--secondary" disabled={!newProjectName.trim() || memoryBusy} onClick={() => { void createProject() }} type="button">
                  新建项目
                </button>
              </div>
              {projectId && (
                <>
                  <div className="dt-ai-memory-form">
                    <input onChange={(event) => setMemoryName(event.target.value)} placeholder="记忆名称" value={memoryName} />
                    <textarea onChange={(event) => setMemoryContent(event.target.value)} placeholder="明确写入项目的记忆内容" rows={4} value={memoryContent} />
                    <button className="dt-button dt-button--secondary" disabled={memoryBusy || !memoryName.trim() || !memoryContent.trim()} onClick={() => { void addMemory() }} type="button">
                      添加显式记忆
                    </button>
                  </div>
                  <label className="dt-button dt-button--secondary dt-ai-file-button">
                    上传 PDF、DOCX、XLSX、文本或图片
                    <input
                      accept=".pdf,.docx,.xlsx,.csv,.tsv,.txt,.md,.json,.png,.jpg,.jpeg,.webp"
                      disabled={memoryBusy}
                      onChange={(event) => {
                        void uploadFile(event.target.files?.[0])
                        event.currentTarget.value = ''
                      }}
                      type="file"
                    />
                  </label>
                  {memoryError && <div className="dt-inline-error">{memoryError}</div>}
                  <div className="dt-ai-source-list">
                    {sources.map((source) => (
                      <div key={source.id}>
                        <span>{source.name}</span>
                        <small>{source.status}{source.chunk_count ? ` · ${source.chunk_count} chunks` : ''}</small>
                        <button
                          className="dt-button dt-button--secondary dt-button--small"
                          disabled={memoryBusy}
                          onClick={() => { void removeSource(source) }}
                          type="button"
                        >
                          移除
                        </button>
                        {source.error_message && <small>{source.error_message}</small>}
                      </div>
                    ))}
                  </div>
                </>
              )}
            </>
          )}
        </div>
      )}
    </div>
  )
}
