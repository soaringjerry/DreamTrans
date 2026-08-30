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
  cancelAIIndexJob,
  createAIIndexJob,
  createAIProject,
  deleteAIArtifact,
  deleteAIProject,
  deleteKnowledgeSource,
  generateAIArtifact,
  getAIIndexJob,
  linkProjectSession,
  listAIArtifacts,
  listAIProjects,
  listKnowledgeSources,
  previewAIContext,
  previewAIIndex,
  retryAIIndexJob,
  retryKnowledgeSource,
  unlinkProjectSession,
  updateAIProject,
  updateProjectMemory,
  uploadKnowledgeFile,
  AIRequestError,
  formatUSD,
  type AIArtifact,
  type AIIndexJob,
  type AIIndexPreview,
  type AIIndexTarget,
  type AIProject,
  type AIReasoningEffort,
  type AIRetrievalMode,
  type AIRetrievalPreference,
  type KnowledgeSource,
  type OCRLanguage,
  type RagConfig,
  type RagContextMetadata,
  type RagContextMode,
  type RagContextPolicy,
  type RagHistoryMessage,
  type RagTranscriptSegment,
} from '../../api'
import MarkdownView from '../../components/MarkdownView'
import { emitMetric } from '../../utils/metrics'
import {
  INSUFFICIENT_BALANCE_MESSAGE,
  isInsufficientBalanceError,
  isInsufficientBalanceMessage,
} from '../workspace/billingErrors'
import {
  aiReasoningPreferenceKey,
  chatHistoryKey,
  legacyChatHistoryKey,
} from '../workspace/browserStorageKeys'
import {
  deleteLocalArtifact,
  listLocalArtifacts,
  saveLocalArtifact,
} from '../workspace/AiArtifactStore'
import {
  AssistantScopeGuard,
  type AssistantScopeSnapshot,
} from '../workspace/AssistantScopeGuard'
import { Icon } from './Icon'

interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  meta?: string
}

interface ScopedChatMessages {
  messages: ChatMessage[]
  scope: AssistantScopeSnapshot
}

interface AssistantPanelProps {
  config?: RagConfig
  /** Opens the account panel when a request is rejected for insufficient balance. */
  onTopUp?: () => void
  ownerId: string | null
  sessionId: string
  sourceLanguage?: string
  suggestedQuestion?: string
  transcriptContext: string
}

interface PendingActionBase {
  clientRequestId: string
  indexClientRequestId: string
  policy: RagContextPolicy
  projectId?: string
  reasoningEffort: AIReasoningEffort
}

interface PendingChatAction extends PendingActionBase {
  kind: 'chat'
  question: string
  history: RagHistoryMessage[]
}

interface PendingArtifactAction extends PendingActionBase {
  kind: 'artifact'
  artifactType: AIArtifact['artifact_type']
}

type PendingAIAction = PendingChatAction | PendingArtifactAction

interface StoredPendingIndex {
  sessionId: string
  job: AIIndexJob
  action: PendingAIAction
}

const contextPresets = [
  { value: 16_000, label: '16K · 精简' },
  { value: 64_000, label: '64K · 推荐' },
  { value: 128_000, label: '128K · 长会话' },
  { value: 256_000, label: '256K · 超长会话' },
] as const
const reasoningOptions: Array<{
  value: AIReasoningEffort
  label: string
  description: string
  recommended?: boolean
}> = [
  { value: 'low', label: '快速', description: '直接回答，更快更省' },
  {
    value: 'medium',
    label: '标准',
    description: '日常够用，速度与完整性平衡',
    recommended: true,
  },
  { value: 'high', label: '深入', description: '复杂梳理，更慢、用量更高' },
]
const starterQuestions = [
  '总结刚才讨论的关键结论',
  '提取行动项、负责人和时间点',
  '还有哪些问题没有解决？',
]
const ocrOptions: Array<{ value: OCRLanguage; label: string }> = [
  { value: 'eng', label: 'English' },
  { value: 'chi_sim', label: '简体中文' },
  { value: 'jpn', label: '日本語' },
  { value: 'kor', label: '한국어' },
]
/** Shared accept list for chat + knowledge uploads. */
const knowledgeFileAccept = '.pdf,.docx,.xlsx,.csv,.tsv,.txt,.md,.json,.png,.jpg,.jpeg,.webp'
const knowledgeFileAcceptLabel = 'PDF、文档、表格、文本或图片'

function defaultOCRLanguages(sourceLanguage?: string): OCRLanguage[] {
  const normalized = sourceLanguage?.trim().toLowerCase().replace('_', '-') ?? ''
  if (normalized === 'ja' || normalized.startsWith('ja-') || normalized === 'jpn') {
    return ['jpn']
  }
  if (normalized === 'ko' || normalized.startsWith('ko-') || normalized === 'kor') {
    return ['kor']
  }
  if (
    normalized === 'zh'
    || normalized.startsWith('zh-')
    || normalized === 'cmn'
    || normalized === 'chi'
    || normalized === 'zho'
  ) {
    return ['chi_sim']
  }
  if (normalized === 'en' || normalized.startsWith('en-') || normalized === 'eng') {
    return ['eng']
  }
  return ['eng', 'chi_sim']
}

function isReasoningEffort(value: unknown): value is AIReasoningEffort {
  return value === 'low' || value === 'medium' || value === 'high'
}

function readReasoningEffort(ownerId: string | null): AIReasoningEffort {
  try {
    const stored = localStorage.getItem(aiReasoningPreferenceKey(ownerId))
    return isReasoningEffort(stored) ? stored : 'medium'
  } catch {
    return 'medium'
  }
}

function reasoningEffortLabel(value: AIReasoningEffort): string {
  return reasoningOptions.find((option) => option.value === value)?.label ?? '标准'
}

function reasoningRequestTimeout(value: AIReasoningEffort): number {
  if (value === 'high') return 130_000
  if (value === 'medium') return 100_000
  return 70_000
}

function artifactTypeLabel(type: AIArtifact['artifact_type']): string {
  switch (type) {
    case 'summary': return '会话摘要'
    case 'notes': return '结构化笔记'
    case 'action_items': return '行动项'
  }
}

function contextModeLabel(value: RagContextMode): string {
  switch (value) {
    case 'full': return '尽量看全文'
    case 'retrieval': return '只找相关段落'
    default: return '智能选取'
  }
}

function defaultSessionProjectName(): string {
  return '本会话资料'
}
const indexPollIntervalMs = 1_500
const indexPollLimit = 400

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
      if (!match) return { id: `context-${index}`, text: line.trim() }
      return {
        id: `context-${index}`,
        start_time: Number(match[1]),
        speaker: match[2].trim(),
        text: match[3].trim(),
      }
    })
    .filter((segment) => segment.text)
}

function retrievalModeLabel(mode?: AIRetrievalMode): string {
  switch (mode) {
    case 'hybrid': return '混合检索'
    case 'semantic': return '语义检索'
    case 'lexical_fallback': return '词法检索'
    case 'legacy': return '兼容检索'
    case 'none': return '未检索'
    default: return '检索方式未知'
  }
}

function indexStatusLabel(status?: string): string {
  switch (status) {
    case 'unindexed': return '未建立语义索引'
    case 'queued': return '等待索引'
    case 'processing': return '索引处理中'
    case 'ready': return '索引可用'
    case 'stale': return '索引需要更新'
    case 'error': return '索引失败'
    case 'cancelled': return '索引已取消'
    default: return status || '索引状态未知'
  }
}

function sourceStatusLabel(status: KnowledgeSource['status']): string {
  switch (status) {
    case 'queued': return '等待提取'
    case 'processing': return '内容提取中'
    case 'ready': return '内容可用'
    case 'error': return '内容提取失败'
  }
}

function contextDescription(metadata?: RagContextMetadata): string {
  if (!metadata) return ''
  const truncated = metadata.truncated ? ' · 已截断' : ''
  const sources = metadata.sources?.length ? ` · ${metadata.sources.length} 个来源` : ''
  return [
    metadata.effective_mode,
    `约 ${metadata.estimated_tokens.toLocaleString()} tokens`,
    retrievalModeLabel(metadata.retrieval_mode),
    indexStatusLabel(metadata.index_status),
  ].join(' · ') + sources + truncated
}

function readableError(reason: unknown): string {
  if (isInsufficientBalanceError(reason)) return INSUFFICIENT_BALANCE_MESSAGE
  return reason instanceof Error ? reason.message : String(reason)
}

function newRequestId(): string {
  return globalThis.crypto.randomUUID()
}

function toAIIndexTarget(
  targetType: 'project' | 'session',
  targetId: string,
  currentSessionId?: string,
): AIIndexTarget {
  if (targetType === 'project') {
    return {
      targetType: 'project',
      targetId,
      projectId: targetId,
      sessionId: currentSessionId || undefined,
    }
  }
  return {
    targetType: 'session',
    targetId,
    sessionId: targetId,
  }
}

function sleep(milliseconds: number): Promise<void> {
  return new Promise((resolve) => globalThis.setTimeout(resolve, milliseconds))
}

function safeArtifactFilename(title: string): string {
  return title
    .trim()
    .replace(/[<>:"/\\|?*]/g, '-')
    .replace(/\p{Cc}/gu, '')
    .replace(/\s+/g, ' ')
    .slice(0, 80) || 'DreamTrans-AI'
}

function downloadArtifact(artifact: AIArtifact): void {
  const url = URL.createObjectURL(new Blob(
    [`# ${artifact.title}\n\n${artifact.content}\n`],
    { type: 'text/markdown;charset=utf-8' },
  ))
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = `${safeArtifactFilename(artifact.title)}.md`
  anchor.style.display = 'none'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  globalThis.setTimeout(() => URL.revokeObjectURL(url), 1_000)
}

async function copyText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text)
    return
  }
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.style.position = 'fixed'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.select()
  const copied = document.execCommand('copy')
  textarea.remove()
  if (!copied) throw new Error('浏览器不允许复制到剪贴板')
}

function pendingIndexStorageKey(ownerId: string, sessionId: string): string {
  return `dreamtrans:ai-index:${encodeURIComponent(ownerId)}:${encodeURIComponent(sessionId)}`
}

function storePendingIndex(
  ownerId: string | null,
  sessionId: string,
  job: AIIndexJob,
  action: PendingAIAction,
): void {
  if (!ownerId || !sessionId) return
  try {
    const stored: StoredPendingIndex = { sessionId, job, action }
    sessionStorage.setItem(
      pendingIndexStorageKey(ownerId, sessionId),
      JSON.stringify(stored),
    )
  } catch {
    // The in-memory poll still works when session storage is unavailable.
  }
}

function readPendingIndex(
  ownerId: string | null,
  sessionId: string,
): StoredPendingIndex | undefined {
  if (!ownerId || !sessionId) return undefined
  try {
    const serialized = sessionStorage.getItem(pendingIndexStorageKey(ownerId, sessionId))
    if (!serialized) return undefined
    const stored = JSON.parse(serialized) as StoredPendingIndex
    if (
      stored.sessionId !== sessionId
      || !stored.job?.id
      || !stored.action?.clientRequestId
      || !stored.action?.indexClientRequestId
    ) return undefined
    if (!isReasoningEffort(stored.action.reasoningEffort)) {
      stored.action.reasoningEffort = 'medium'
    }
    return stored
  } catch {
    return undefined
  }
}

function clearPendingIndex(ownerId: string | null, sessionId: string): void {
  if (!ownerId || !sessionId) return
  try {
    sessionStorage.removeItem(pendingIndexStorageKey(ownerId, sessionId))
  } catch {
    // Nothing else is required when storage is unavailable.
  }
}

export function AssistantPanel({
  config,
  ownerId,
  sessionId,
  sourceLanguage,
  suggestedQuestion,
  transcriptContext,
  onTopUp,
}: AssistantPanelProps) {
  const scopeGuardRef = useRef<AssistantScopeGuard | null>(null)
  if (!scopeGuardRef.current) {
    scopeGuardRef.current = new AssistantScopeGuard(ownerId, sessionId)
  }
  const renderScope = scopeGuardRef.current.update(ownerId, sessionId)
  const isCurrentScope = useCallback((scope: AssistantScopeSnapshot) => (
    scopeGuardRef.current?.isCurrent(scope) ?? false
  ), [])

  const [tab, setTab] = useState<'chat' | 'artifacts' | 'memory'>('chat')
  const [scopedMessages, setScopedMessages] = useState<ScopedChatMessages>(() => ({
    messages: readHistory(ownerId, sessionId),
    scope: renderScope,
  }))
  const messages = useMemo(() => (
    scopedMessages.scope.key === renderScope.key
    && scopedMessages.scope.generation === renderScope.generation
  ) ? scopedMessages.messages : [], [renderScope, scopedMessages])
  const setMessages = useCallback((
    update: ChatMessage[] | ((current: ChatMessage[]) => ChatMessage[]),
  ) => {
    setScopedMessages((current) => {
      if (
        !isCurrentScope(renderScope)
        || current.scope.key !== renderScope.key
        || current.scope.generation !== renderScope.generation
      ) return current
      return {
        messages: typeof update === 'function'
          ? update(current.messages)
          : update,
        scope: current.scope,
      }
    })
  }, [isCurrentScope, renderScope])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [contextMode, setContextMode] = useState<RagContextMode>('smart')
  const [maxContextTokens, setMaxContextTokens] = useState(64_000)
  const [reasoningEffort, setReasoningEffort] = useState<AIReasoningEffort>(
    () => readReasoningEffort(ownerId),
  )
  const reasoningOwnerRef = useRef(ownerId)
  const [policyOverridden, setPolicyOverridden] = useState(false)
  const [contextMetadata, setContextMetadata] = useState<RagContextMetadata>()
  const [contextPreview, setContextPreview] = useState('')
  const [contextPreviewBusy, setContextPreviewBusy] = useState(false)
  const [artifacts, setArtifacts] = useState<AIArtifact[]>([])
  const [artifactLoading, setArtifactLoading] = useState<AIArtifact['artifact_type'] | null>(null)
  const [artifactError, setArtifactError] = useState('')
  const [projects, setProjects] = useState<AIProject[]>([])
  const [projectRestoreBusy, setProjectRestoreBusy] = useState(Boolean(ownerId))
  const [projectRestoreError, setProjectRestoreError] = useState('')
  const [linkedProjectId, setLinkedProjectId] = useState('')
  const [projectId, setProjectId] = useState('')
  const [newProjectName, setNewProjectName] = useState('')
  const [projectName, setProjectName] = useState('')
  const [projectDescription, setProjectDescription] = useState('')
  const [projectContextMode, setProjectContextMode] = useState<RagContextMode>('smart')
  const [projectMaxTokens, setProjectMaxTokens] = useState(64_000)
  const [sources, setSources] = useState<KnowledgeSource[]>([])
  const [memoryName, setMemoryName] = useState('')
  const [memoryContent, setMemoryContent] = useState('')
  const [editingSourceId, setEditingSourceId] = useState('')
  const [editingMemoryName, setEditingMemoryName] = useState('')
  const [editingMemoryContent, setEditingMemoryContent] = useState('')
  const [ocrLanguages, setOcrLanguages] = useState<OCRLanguage[]>(
    () => defaultOCRLanguages(sourceLanguage),
  )
  const [memoryBusy, setMemoryBusy] = useState(false)
  const [memoryError, setMemoryError] = useState('')
  const [indexConfirmation, setIndexConfirmation] = useState<AIIndexPreview>()
  const [pendingIndexAction, setPendingIndexAction] = useState<PendingAIAction>()
  const [indexJob, setIndexJob] = useState<AIIndexJob>()
  const [indexBusy, setIndexBusy] = useState(false)
  const [indexError, setIndexError] = useState('')
  const [chatBalanceBlocked, setChatBalanceBlocked] = useState(false)
  const [notice, setNotice] = useState('')
  const listRef = useRef<HTMLDivElement>(null)
  const indexGateRef = useRef<HTMLElement>(null)
  const indexPollGenerationRef = useRef(0)
  const resumedIndexJobRef = useRef('')
  const requestActionRef = useRef<
    ((action: PendingAIAction) => Promise<void>) | undefined
  >(undefined)

  const policy: RagContextPolicy = useMemo(
    () => ({ mode: contextMode, max_tokens: maxContextTokens }),
    [contextMode, maxContextTokens],
  )
  const clientTranscript = useMemo(
    () => transcriptSegments(transcriptContext),
    [transcriptContext],
  )
  const selectedProject = useMemo(
    () => projects.find((project) => project.id === projectId),
    [projectId, projects],
  )

  useEffect(() => {
    setOcrLanguages(defaultOCRLanguages(sourceLanguage))
  }, [sessionId, sourceLanguage])

  useEffect(() => {
    if (reasoningOwnerRef.current !== ownerId) {
      reasoningOwnerRef.current = ownerId
      setReasoningEffort(readReasoningEffort(ownerId))
      return
    }
    try {
      localStorage.setItem(
        aiReasoningPreferenceKey(ownerId),
        reasoningEffort,
      )
    } catch {
      // Keep the in-memory preference when browser storage is unavailable.
    }
  }, [ownerId, reasoningEffort])

  const applyProjectPolicy = useCallback((project?: AIProject) => {
    if (!project) return
    setContextMode(project.context_mode)
    setMaxContextTokens(project.max_context_tokens)
    setPolicyOverridden(false)
  }, [])

  useEffect(() => {
    indexPollGenerationRef.current += 1
    setScopedMessages({
      messages: readHistory(ownerId, sessionId),
      scope: renderScope,
    })
    setLoading(false)
    setInput('')
    setArtifacts([])
    setArtifactLoading(null)
    setArtifactError('')
    setContextMetadata(undefined)
    setContextPreview('')
    setContextPreviewBusy(false)
    setProjects([])
    setProjectRestoreBusy(Boolean(ownerId))
    setLinkedProjectId('')
    setProjectId('')
    setNewProjectName('')
    setProjectName('')
    setProjectDescription('')
    setProjectContextMode('smart')
    setProjectMaxTokens(64_000)
    setSources([])
    setMemoryName('')
    setMemoryContent('')
    setMemoryBusy(false)
    setMemoryError('')
    setEditingSourceId('')
    setEditingMemoryName('')
    setEditingMemoryContent('')
    setContextMode('smart')
    setMaxContextTokens(64_000)
    setPolicyOverridden(false)
    setIndexConfirmation(undefined)
    setPendingIndexAction(undefined)
    setIndexJob(undefined)
    setIndexBusy(false)
    setIndexError('')
    setNotice('')
    setProjectRestoreError('')
    resumedIndexJobRef.current = ''
  }, [ownerId, renderScope, sessionId])

  useEffect(() => {
    if (!suggestedQuestion) return
    setTab('chat')
    setInput(suggestedQuestion)
  }, [suggestedQuestion])

  useEffect(() => {
    if (
      !sessionId
      || !isCurrentScope(scopedMessages.scope)
      || scopedMessages.scope.key !== renderScope.key
      || scopedMessages.scope.generation !== renderScope.generation
    ) return
    try {
      localStorage.setItem(
        chatHistoryKey(ownerId, sessionId),
        JSON.stringify(scopedMessages.messages.slice(-50)),
      )
    } catch {
      // Keep current-tab state when browser storage is unavailable.
    }
  }, [
    isCurrentScope,
    ownerId,
    renderScope,
    scopedMessages,
    sessionId,
  ])

  useEffect(() => {
    listRef.current?.scrollTo({ top: listRef.current.scrollHeight })
  }, [messages, loading])

  useEffect(() => {
    if (!notice) return
    const timeout = globalThis.setTimeout(() => setNotice(''), 4_000)
    return () => globalThis.clearTimeout(timeout)
  }, [notice])

  useEffect(() => {
    if (!indexConfirmation) return
    const frame = globalThis.requestAnimationFrame(() => indexGateRef.current?.focus())
    return () => globalThis.cancelAnimationFrame(frame)
  }, [indexConfirmation])

  useEffect(() => () => {
    indexPollGenerationRef.current += 1
  }, [])

  useEffect(() => {
    if (!selectedProject) {
      setProjectName('')
      setProjectDescription('')
      return
    }
    setProjectName(selectedProject.name)
    setProjectDescription(selectedProject.description)
    setProjectContextMode(selectedProject.context_mode)
    setProjectMaxTokens(selectedProject.max_context_tokens)
  }, [selectedProject])

  const refreshArtifacts = useCallback(async () => {
    const requestScope = renderScope
    if (!sessionId || !isCurrentScope(requestScope)) return
    setArtifactError('')
    try {
      const nextArtifacts = ownerId
        ? await listAIArtifacts(sessionId)
        : await listLocalArtifacts(sessionId)
      if (!isCurrentScope(requestScope)) return
      setArtifacts(nextArtifacts)
    } catch (reason) {
      if (!isCurrentScope(requestScope)) return
      setArtifactError(readableError(reason))
    }
  }, [isCurrentScope, ownerId, renderScope, sessionId])

  useEffect(() => {
    if (tab === 'artifacts') void refreshArtifacts()
  }, [refreshArtifacts, tab])

  const refreshProjects = useCallback(async (restoreLinkedProject: boolean) => {
    const requestScope = renderScope
    if (!isCurrentScope(requestScope)) return
    if (!ownerId) {
      setProjectRestoreBusy(false)
      setProjectRestoreError('')
      setProjects([])
      setProjectId('')
      setLinkedProjectId('')
      return
    }
    if (restoreLinkedProject) {
      setProjectRestoreBusy(true)
      setProjectRestoreError('')
    }
    setMemoryError('')
    try {
      const response = await listAIProjects(sessionId || undefined)
      if (!isCurrentScope(requestScope)) return
      setProjects(response.projects)
      const linkedId = response.linked_project_id ?? ''
      setLinkedProjectId(linkedId)
      if (restoreLinkedProject) {
        const linkedProject = response.projects.find((project) => project.id === linkedId)
        setProjectId(linkedProject?.id ?? '')
        if (linkedProject) {
          applyProjectPolicy(linkedProject)
        } else {
          setContextMode('smart')
          setMaxContextTokens(64_000)
          setPolicyOverridden(false)
        }
      } else {
        setProjectId((current) => (
          current && response.projects.some((project) => project.id === current)
            ? current
            : linkedId
        ))
      }
    } catch (reason) {
      if (!isCurrentScope(requestScope)) return
      const message = readableError(reason)
      if (restoreLinkedProject) setProjectRestoreError(message)
      else setMemoryError(message)
    } finally {
      if (restoreLinkedProject && isCurrentScope(requestScope)) {
        setProjectRestoreBusy(false)
      }
    }
  }, [
    applyProjectPolicy,
    isCurrentScope,
    ownerId,
    renderScope,
    sessionId,
  ])

  useEffect(() => {
    void refreshProjects(true)
  }, [refreshProjects])

  const refreshSources = useCallback(async () => {
    const requestScope = renderScope
    if (!isCurrentScope(requestScope)) return
    if (!projectId) {
      setSources([])
      return
    }
    try {
      const nextSources = await listKnowledgeSources(projectId)
      if (!isCurrentScope(requestScope)) return
      setSources(nextSources)
    } catch (reason) {
      if (!isCurrentScope(requestScope)) return
      setMemoryError(readableError(reason))
    }
  }, [isCurrentScope, projectId, renderScope])

  useEffect(() => {
    setEditingSourceId('')
    void refreshSources()
  }, [refreshSources])

  useEffect(() => {
    if (!sources.some((source) => (
      source.status === 'queued' || source.status === 'processing'
    ))) return
    const timer = globalThis.setInterval(() => { void refreshSources() }, 3_000)
    return () => globalThis.clearInterval(timer)
  }, [refreshSources, sources])

  const actionTranscript = useCallback((action: PendingAIAction) => (
    action.policy.mode === 'retrieval' ? [] : clientTranscript
  ), [clientTranscript])

  const indexTargetForAction = useCallback((
    action: PendingAIAction,
  ): AIIndexTarget | undefined => {
    if (action.projectId) {
      return {
        targetType: 'project',
        targetId: action.projectId,
        projectId: action.projectId,
        sessionId: sessionId || undefined,
      }
    }
    if (ownerId && sessionId) {
      return {
        targetType: 'session',
        targetId: sessionId,
        sessionId,
      }
    }
    return undefined
  }, [ownerId, sessionId])

  const executeAction = useCallback(async (
    action: PendingAIAction,
    retrievalPreference: AIRetrievalPreference,
  ) => {
    const actionScope = renderScope
    if (!isCurrentScope(actionScope)) return
    if (action.kind === 'chat') {
      setLoading(true)
      setIndexError('')
      setInput((current) => current.trim() === action.question ? '' : current)
      setMessages((current) => [...current, {
        id: newRequestId(),
        role: 'user',
        content: action.question,
      }])
      const startedAt = performance.now()
      try {
        const response = await askRag(
          sessionId || 'current_session',
          action.question,
          6,
          config,
          reasoningRequestTimeout(action.reasoningEffort),
          {
            history: action.history,
            clientTranscript: actionTranscript(action),
            contextPolicy: action.policy,
            projectId: action.projectId,
            retrievalPreference,
            clientRequestId: action.clientRequestId,
            reasoningEffort: action.reasoningEffort,
          },
        )
        if (!isCurrentScope(actionScope)) return
        setContextMetadata(response.context)
        const details = [
          response.usage?.model,
          response.usage?.total_tokens
            ? `${response.usage.total_tokens.toLocaleString()} tokens`
            : '',
          response.usage?.cached_tokens
            ? `${response.usage.cached_tokens.toLocaleString()} cached`
            : '',
          response.context?.retrieval_mode
            ? retrievalModeLabel(response.context.retrieval_mode)
            : '',
          response.latency_ms ? `${(response.latency_ms / 1_000).toFixed(1)}s` : '',
        ].filter(Boolean).join(' · ')
        emitMetric({
          kind: 'chat',
          latency_ms: response.latency_ms ?? performance.now() - startedAt,
          model: response.usage?.model,
        })
        setMessages((current) => [...current, {
          id: newRequestId(),
          role: 'assistant',
          content: response.answer,
          meta: details,
        }])
      } catch (reason) {
        if (!isCurrentScope(actionScope)) return
        if (isInsufficientBalanceError(reason)) setChatBalanceBlocked(true)
        setMessages((current) => [...current, {
          id: newRequestId(),
          role: 'assistant',
          content: `请求失败：${readableError(reason)}`,
        }])
      } finally {
        if (isCurrentScope(actionScope)) setLoading(false)
      }
      return
    }

    // Artifact generation runs in the background from the UI's perspective:
    // chat and settings stay usable while the long LLM call completes.
    setArtifactLoading(action.artifactType)
    setArtifactError('')
    setNotice('')
    try {
      const response = await generateAIArtifact(
        sessionId,
        action.artifactType,
        actionTranscript(action),
        action.policy,
        config,
        action.projectId,
        retrievalPreference,
        action.clientRequestId,
        action.reasoningEffort,
        reasoningRequestTimeout(action.reasoningEffort),
      )
      if (!isCurrentScope(actionScope)) return
      setContextMetadata(response.context)
      if (!ownerId) {
        await saveLocalArtifact(sessionId, response.artifact)
        if (!isCurrentScope(actionScope)) return
      }
      await refreshArtifacts()
      if (!isCurrentScope(actionScope)) return
      const title = response.artifact.title?.trim()
        || artifactTypeLabel(action.artifactType)
      setNotice(`「${title}」已生成，可在「内容」查看。`)
    } catch (reason) {
      if (!isCurrentScope(actionScope)) return
      setArtifactError(readableError(reason))
    } finally {
      if (isCurrentScope(actionScope)) setArtifactLoading(null)
    }
  }, [
    actionTranscript,
    config,
    isCurrentScope,
    ownerId,
    refreshArtifacts,
    renderScope,
    setMessages,
    sessionId,
  ])

  const pollIndexJob = useCallback(async (
    initialJob: AIIndexJob,
    action: PendingAIAction,
  ) => {
    const pollScope = renderScope
    if (!isCurrentScope(pollScope)) return
    const generation = ++indexPollGenerationRef.current
    let currentJob = initialJob
    setPendingIndexAction(action)
    setIndexConfirmation(undefined)
    setIndexJob(currentJob)
    setIndexBusy(false)
    setIndexError('')
    storePendingIndex(ownerId, sessionId, currentJob, action)
    try {
      for (let attempt = 0; attempt < indexPollLimit; attempt += 1) {
        if (
          generation !== indexPollGenerationRef.current
          || !isCurrentScope(pollScope)
        ) return
        setIndexJob(currentJob)
        storePendingIndex(ownerId, sessionId, currentJob, action)
        if (currentJob.status === 'ready') {
          clearPendingIndex(ownerId, sessionId)
          setIndexJob(undefined)
          setPendingIndexAction(undefined)
          setIndexBusy(false)
          const nextAction = {
            ...action,
            indexClientRequestId: newRequestId(),
          }
          if (requestActionRef.current) {
            await requestActionRef.current(nextAction)
          } else {
            await executeAction(action, 'auto')
          }
          return
        }
        if (
          currentJob.status === 'error'
          || currentJob.status === 'cancelled'
        ) {
          setIndexError(currentJob.error_message || '语义索引未能完成。')
          return
        }
        await sleep(indexPollIntervalMs)
        if (
          generation !== indexPollGenerationRef.current
          || !isCurrentScope(pollScope)
        ) return
        currentJob = await getAIIndexJob(currentJob.id)
      }
      if (!isCurrentScope(pollScope)) return
      setIndexError('索引仍在后台运行。你可以稍后重试检查，或本次仅使用词法检索。')
    } catch (reason) {
      if (!isCurrentScope(pollScope)) return
      setIndexError(readableError(reason))
    } finally {
      if (
        generation === indexPollGenerationRef.current
        && isCurrentScope(pollScope)
      ) setIndexBusy(false)
    }
  }, [
    executeAction,
    isCurrentScope,
    ownerId,
    renderScope,
    sessionId,
  ])

  useEffect(() => {
    const stored = readPendingIndex(ownerId, sessionId)
    if (!stored || resumedIndexJobRef.current === stored.job.id) return
    resumedIndexJobRef.current = stored.job.id
    void pollIndexJob(stored.job, stored.action)
  }, [ownerId, pollIndexJob, sessionId])

  const requestAction = useCallback(async (action: PendingAIAction) => {
    const actionScope = renderScope
    if (!isCurrentScope(actionScope)) return
    const defaultTarget = indexTargetForAction(action)
    if (!defaultTarget) {
      await executeAction(action, 'auto')
      return
    }
    let target: AIIndexTarget = defaultTarget
    setIndexBusy(true)
    setIndexError('')
    let retrievalNeeded: boolean
    try {
      const assembledPreview = await previewAIContext(
        sessionId,
        actionTranscript(action),
        action.policy,
        {
          question: action.kind === 'chat' ? action.question : undefined,
          history: action.kind === 'chat' ? action.history : undefined,
          projectId: action.projectId,
          artifactType: action.kind === 'artifact' ? action.artifactType : undefined,
          topK: action.kind === 'chat' ? 6 : 20,
          config,
          executeSemantic: false,
        },
      )
      if (!isCurrentScope(actionScope)) return
      setContextMetadata(assembledPreview)
      const pendingTarget = assembledPreview.index_targets?.find(
        (candidate) => candidate.index_status !== 'ready',
      )
      if (pendingTarget) {
        target = toAIIndexTarget(
          pendingTarget.target_type,
          pendingTarget.target_id,
          sessionId,
        )
      }
      // Selecting a project means its knowledge is part of this operation even
      // when free lexical search happens to return no match. Always inspect the
      // project index in that case so the first semantic use cannot silently
      // degrade to an empty lexical result.
      retrievalNeeded = Boolean(action.projectId)
        || assembledPreview.rag_used
        || (
          assembledPreview.retrieval_mode !== undefined
          && assembledPreview.retrieval_mode !== 'none'
        )
        || action.policy.mode === 'retrieval'
    } catch (reason) {
      if (!isCurrentScope(actionScope)) return
      setIndexBusy(false)
      setIndexError(`无法装配 AI 上下文：${readableError(reason)}`)
      return
    }
    if (!retrievalNeeded) {
      if (!isCurrentScope(actionScope)) return
      setIndexBusy(false)
      await executeAction(action, 'auto')
      return
    }
    try {
      const preview = await previewAIIndex(target)
      if (!isCurrentScope(actionScope)) return
      if (
        preview.index_status === 'ready'
        || preview.chunk_count === 0
        || preview.requires_indexing === false
      ) {
        setIndexBusy(false)
        await executeAction(action, 'auto')
        return
      }
      const activeJob = preview.active_job
      if (
        activeJob
        && (activeJob.status === 'queued' || activeJob.status === 'processing')
      ) {
        await pollIndexJob(activeJob, action)
        return
      }
      setPendingIndexAction(action)
      setIndexConfirmation(preview)
    } catch (reason) {
      if (!isCurrentScope(actionScope)) return
      if (
        target.targetType === 'session'
        && reason instanceof AIRequestError
        && reason.status === 404
      ) {
        // A new/local session may not exist in PostgreSQL yet. The backend
        // keeps explicit indexing disabled and uses compatibility retrieval.
        await executeAction(action, 'auto')
      } else {
        setIndexError(`无法检查语义索引：${readableError(reason)}`)
      }
    } finally {
      if (isCurrentScope(actionScope)) setIndexBusy(false)
    }
  }, [
    actionTranscript,
    config,
    executeAction,
    indexTargetForAction,
    isCurrentScope,
    pollIndexJob,
    renderScope,
    sessionId,
  ])
  requestActionRef.current = requestAction

  const send = (event: FormEvent) => {
    event.preventDefault()
    const question = input.trim()
    if (!question || loading || indexBusy || pendingIndexAction) return
    const action: PendingChatAction = {
      kind: 'chat',
      question,
      history: messages.slice(-12).map(({ role, content }) => ({ role, content })),
      clientRequestId: newRequestId(),
      indexClientRequestId: newRequestId(),
      policy: { ...policy },
      projectId: projectId || undefined,
      reasoningEffort,
    }
    void requestAction(action)
  }

  const previewContext = async () => {
    const previewScope = renderScope
    if (!isCurrentScope(previewScope)) return
    setContextPreviewBusy(true)
    setContextPreview('')
    try {
      const preview = await previewAIContext(
        sessionId,
        contextMode === 'retrieval' ? [] : clientTranscript,
        policy,
        {
          question: input.trim() || undefined,
          history: messages
            .slice(-12)
            .map(({ role, content }) => ({ role, content })),
          projectId: projectId || undefined,
          // Context inspection is a free lexical preflight. Paid semantic
          // work only starts after the user confirms an index or submits the
          // actual chat/artifact action.
          executeSemantic: false,
        },
      )
      if (!isCurrentScope(previewScope)) return
      setContextMetadata(preview)
      const sourceLines = preview.sources?.map((source) => (
        `- ${source.label || source.kind}${source.start_time === undefined
          ? ''
          : `（${source.start_time.toFixed(1)}s）`}`
      )) ?? []
      setContextPreview([
        contextDescription(preview),
        `${preview.segment_count.toLocaleString()} 个完整片段`,
        sourceLines.length ? `实际来源：\n${sourceLines.join('\n')}` : '实际来源：无',
        preview.preview,
      ].filter(Boolean).join('\n\n'))
    } catch (reason) {
      if (!isCurrentScope(previewScope)) return
      setContextPreview(readableError(reason))
    } finally {
      if (isCurrentScope(previewScope)) setContextPreviewBusy(false)
    }
  }

  const generateArtifact = (artifactType: AIArtifact['artifact_type']) => {
    if (!sessionId || artifactLoading || indexBusy || pendingIndexAction) return
    const action: PendingArtifactAction = {
      kind: 'artifact',
      artifactType,
      clientRequestId: newRequestId(),
      indexClientRequestId: newRequestId(),
      policy: { ...policy },
      projectId: projectId || undefined,
      reasoningEffort,
    }
    void requestAction(action)
  }

  const reopenIndexConfirmation = async (
    action: PendingAIAction,
    target: AIIndexTarget,
    operationScope: AssistantScopeSnapshot,
  ) => {
    const renewedAction = {
      ...action,
      indexClientRequestId: newRequestId(),
    }
    const preview = await previewAIIndex(target)
    if (!isCurrentScope(operationScope)) return

    indexPollGenerationRef.current += 1
    clearPendingIndex(ownerId, sessionId)
    setIndexJob(undefined)
    setIndexError('')
    if (
      preview.index_status === 'ready'
      || preview.chunk_count === 0
      || preview.requires_indexing === false
    ) {
      setIndexConfirmation(undefined)
      setPendingIndexAction(undefined)
      setIndexBusy(false)
      await executeAction(renewedAction, 'auto')
      return
    }
    if (
      preview.active_job
      && (
        preview.active_job.status === 'queued'
        || preview.active_job.status === 'processing'
      )
    ) {
      await pollIndexJob(preview.active_job, renewedAction)
      return
    }
    setPendingIndexAction(renewedAction)
    setIndexConfirmation(preview)
    setIndexBusy(false)
  }

  const buildSemanticIndex = async () => {
    const operationScope = renderScope
    if (!isCurrentScope(operationScope)) return
    const action = pendingIndexAction
    const confirmedTarget = (
      indexConfirmation?.target_type
      && indexConfirmation.target_id
    )
      ? toAIIndexTarget(
        indexConfirmation.target_type,
        indexConfirmation.target_id,
        sessionId,
      )
      : undefined
    const target = confirmedTarget ?? (action ? indexTargetForAction(action) : undefined)
    const confirmationToken = indexConfirmation?.confirmation_token
    if (!action || !target || !confirmationToken || indexBusy) return
    setIndexBusy(true)
    setIndexError('')
    try {
      const job = await createAIIndexJob({
        ...target,
        clientRequestId: action.indexClientRequestId,
        confirmationToken,
      })
      if (!isCurrentScope(operationScope)) return
      await pollIndexJob(job, action)
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      if (reason instanceof AIRequestError && reason.status === 409) {
        try {
          await reopenIndexConfirmation(action, target, operationScope)
        } catch (previewReason) {
          if (!isCurrentScope(operationScope)) return
          setIndexError(readableError(previewReason))
          setIndexBusy(false)
        }
        return
      }
      setIndexError(readableError(reason))
      setIndexBusy(false)
    }
  }

  const useLexicalOnce = () => {
    const action = pendingIndexAction
    if (!action) return
    indexPollGenerationRef.current += 1
    clearPendingIndex(ownerId, sessionId)
    setIndexConfirmation(undefined)
    setIndexJob(undefined)
    setPendingIndexAction(undefined)
    setIndexError('')
    setIndexBusy(false)
    void executeAction(action, 'lexical_only')
  }

  const cancelPendingAction = async () => {
    const operationScope = renderScope
    if (!isCurrentScope(operationScope)) return
    indexPollGenerationRef.current += 1
    clearPendingIndex(ownerId, sessionId)
    const activeJob = indexJob
    setIndexConfirmation(undefined)
    setIndexJob(undefined)
    setPendingIndexAction(undefined)
    setIndexError('')
    setIndexBusy(false)
    if (activeJob && (activeJob.status === 'queued' || activeJob.status === 'processing')) {
      try {
        await cancelAIIndexJob(activeJob.id)
      } catch (reason) {
        if (!isCurrentScope(operationScope)) return
        setIndexError(`取消索引任务失败：${readableError(reason)}`)
      }
    }
  }

  const retryIndexJob = async () => {
    const operationScope = renderScope
    if (!indexJob || !pendingIndexAction || indexBusy) return
    if (!isCurrentScope(operationScope)) return
    setIndexBusy(true)
    setIndexError('')
    try {
      const job = (
        indexJob.status === 'error'
        || indexJob.status === 'cancelled'
      )
        ? await retryAIIndexJob(indexJob.id)
        : await getAIIndexJob(indexJob.id)
      if (!isCurrentScope(operationScope)) return
      await pollIndexJob(job, pendingIndexAction)
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      if (reason instanceof AIRequestError && reason.status === 409) {
        try {
          await reopenIndexConfirmation(
            pendingIndexAction,
            toAIIndexTarget(indexJob.target_type, indexJob.target_id, sessionId),
            operationScope,
          )
        } catch (previewReason) {
          if (!isCurrentScope(operationScope)) return
          setIndexError(readableError(previewReason))
          setIndexBusy(false)
        }
        return
      }
      setIndexError(readableError(reason))
      setIndexBusy(false)
    }
  }

  const createProject = async () => {
    const operationScope = renderScope
    const name = newProjectName.trim()
    if (!name || memoryBusy || !isCurrentScope(operationScope)) return
    setMemoryBusy(true)
    setMemoryError('')
    try {
      const project = await createAIProject(name, {
        context_mode: contextMode,
        max_context_tokens: maxContextTokens,
      })
      if (sessionId) await linkProjectSession(project.id, sessionId)
      if (!isCurrentScope(operationScope)) return
      setProjects((current) => [project, ...current])
      setProjectId(project.id)
      setLinkedProjectId(project.id)
      applyProjectPolicy(project)
      setNewProjectName('')
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      setMemoryError(readableError(reason))
    } finally {
      if (isCurrentScope(operationScope)) setMemoryBusy(false)
    }
  }

  const selectProject = async (nextProjectId: string) => {
    const operationScope = renderScope
    if (
      memoryBusy
      || nextProjectId === projectId
      || !isCurrentScope(operationScope)
    ) return
    setMemoryBusy(true)
    setMemoryError('')
    try {
      if (!nextProjectId) {
        if (linkedProjectId && sessionId) {
          await unlinkProjectSession(linkedProjectId, sessionId)
        }
        if (!isCurrentScope(operationScope)) return
        setProjectId('')
        setLinkedProjectId('')
        setSources([])
        setContextMode('smart')
        setMaxContextTokens(64_000)
        setPolicyOverridden(false)
        return
      }
      if (sessionId) await linkProjectSession(nextProjectId, sessionId)
      if (!isCurrentScope(operationScope)) return
      setProjectId(nextProjectId)
      setLinkedProjectId(nextProjectId)
      applyProjectPolicy(projects.find((project) => project.id === nextProjectId))
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      setMemoryError(readableError(reason))
    } finally {
      if (isCurrentScope(operationScope)) setMemoryBusy(false)
    }
  }

  const saveProject = async () => {
    const operationScope = renderScope
    if (
      !projectId
      || !projectName.trim()
      || memoryBusy
      || !isCurrentScope(operationScope)
    ) return
    setMemoryBusy(true)
    setMemoryError('')
    try {
      const updated = await updateAIProject(projectId, {
        name: projectName.trim(),
        description: projectDescription.trim(),
        context_mode: projectContextMode,
        max_context_tokens: projectMaxTokens,
      })
      if (!isCurrentScope(operationScope)) return
      setProjects((current) => current.map((project) => (
        project.id === updated.id ? updated : project
      )))
      if (!policyOverridden) applyProjectPolicy(updated)
      setNotice('项目设置已保存。')
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      setMemoryError(readableError(reason))
    } finally {
      if (isCurrentScope(operationScope)) setMemoryBusy(false)
    }
  }

  const removeProject = async () => {
    const operationScope = renderScope
    if (
      !projectId
      || memoryBusy
      || !isCurrentScope(operationScope)
      || !globalThis.confirm('删除项目及其中的所有记忆和文件？此操作无法撤销。')
    ) return
    if (!isCurrentScope(operationScope)) return
    setMemoryBusy(true)
    setMemoryError('')
    try {
      await deleteAIProject(projectId)
      if (!isCurrentScope(operationScope)) return
      setProjectId('')
      setLinkedProjectId('')
      setSources([])
      setContextMode('smart')
      setMaxContextTokens(64_000)
      setPolicyOverridden(false)
      await refreshProjects(false)
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      setMemoryError(readableError(reason))
    } finally {
      if (isCurrentScope(operationScope)) setMemoryBusy(false)
    }
  }

  const addMemory = async () => {
    const operationScope = renderScope
    if (
      !projectId
      || !memoryName.trim()
      || !memoryContent.trim()
      || memoryBusy
      || !isCurrentScope(operationScope)
    ) return
    setMemoryBusy(true)
    setMemoryError('')
    try {
      await addProjectMemory(projectId, memoryName.trim(), memoryContent.trim())
      if (!isCurrentScope(operationScope)) return
      setMemoryName('')
      setMemoryContent('')
      await refreshSources()
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      setMemoryError(readableError(reason))
    } finally {
      if (isCurrentScope(operationScope)) setMemoryBusy(false)
    }
  }

  const startEditingMemory = (source: KnowledgeSource) => {
    setEditingSourceId(source.id)
    setEditingMemoryName(source.name)
    setEditingMemoryContent(source.content ?? '')
  }

  const saveMemoryEdit = async () => {
    const operationScope = renderScope
    if (
      !projectId
      || !editingSourceId
      || !editingMemoryName.trim()
      || !editingMemoryContent.trim()
      || memoryBusy
      || !isCurrentScope(operationScope)
    ) return
    setMemoryBusy(true)
    setMemoryError('')
    try {
      await updateProjectMemory(projectId, editingSourceId, {
        name: editingMemoryName.trim(),
        content: editingMemoryContent.trim(),
      })
      if (!isCurrentScope(operationScope)) return
      setEditingSourceId('')
      await refreshSources()
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      setMemoryError(readableError(reason))
    } finally {
      if (isCurrentScope(operationScope)) setMemoryBusy(false)
    }
  }

  /**
   * Ensure a project exists and is linked for knowledge uploads.
   * Prefer the current selection; otherwise create a default session project.
   * Does not remove any existing project selection UX — only fills a gap.
   */
  const ensureProjectForKnowledge = async (
    operationScope: AssistantScopeSnapshot,
  ): Promise<string | null> => {
    if (projectId) return projectId
    if (!ownerId) {
      setNotice('上传文件需要先登录。登录后资料会进入你的知识库。')
      return null
    }
    if (!sessionId) {
      setMemoryError('当前没有可关联的会话。')
      return null
    }
    const existing = projects.find((project) => project.id === linkedProjectId)
    if (existing) {
      setProjectId(existing.id)
      applyProjectPolicy(existing)
      return existing.id
    }
    const project = await createAIProject(defaultSessionProjectName(), {
      description: '对话里上传的文件与图片会自动归到这里，也可在「资料」中管理。',
      context_mode: contextMode,
      max_context_tokens: maxContextTokens,
    })
    await linkProjectSession(project.id, sessionId)
    if (!isCurrentScope(operationScope)) return null
    setProjects((current) => [project, ...current.filter((item) => item.id !== project.id)])
    setProjectId(project.id)
    setLinkedProjectId(project.id)
    applyProjectPolicy(project)
    return project.id
  }

  const uploadFile = async (file?: File, options?: { fromChat?: boolean }) => {
    const operationScope = renderScope
    if (!file || memoryBusy || !isCurrentScope(operationScope)) return
    if (ocrLanguages.length === 0) {
      const message = '请至少选择一种扫描件识别语言（在回答偏好 · 高级选项，或「资料」页）。'
      if (options?.fromChat) setNotice(message)
      else setMemoryError(message)
      return
    }
    setMemoryBusy(true)
    setMemoryError('')
    try {
      const targetProjectId = await ensureProjectForKnowledge(operationScope)
      if (!targetProjectId || !isCurrentScope(operationScope)) return
      await uploadKnowledgeFile(
        targetProjectId,
        file,
        ocrLanguages,
        sessionId || undefined,
      )
      if (!isCurrentScope(operationScope)) return
      // Use the project id we just uploaded to — React state may not have
      // flushed projectId yet when auto-creating from chat.
      const nextSources = await listKnowledgeSources(targetProjectId)
      if (!isCurrentScope(operationScope)) return
      setSources(nextSources)
      if (options?.fromChat) {
        setNotice(`已添加「${file.name}」，可直接问这份资料。完整列表在「资料」。`)
      }
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      const message = readableError(reason)
      if (options?.fromChat) setNotice(`上传失败：${message}`)
      else setMemoryError(message)
    } finally {
      if (isCurrentScope(operationScope)) setMemoryBusy(false)
    }
  }

  const onChatFileSelected = (file?: File) => {
    if (!file) return
    void uploadFile(file, { fromChat: true })
  }

  const retrySource = async (source: KnowledgeSource) => {
    const operationScope = renderScope
    if (!projectId || memoryBusy || !isCurrentScope(operationScope)) return
    setMemoryBusy(true)
    setMemoryError('')
    try {
      await retryKnowledgeSource(projectId, source.id)
      if (!isCurrentScope(operationScope)) return
      await refreshSources()
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      setMemoryError(readableError(reason))
    } finally {
      if (isCurrentScope(operationScope)) setMemoryBusy(false)
    }
  }

  const removeSource = async (source: KnowledgeSource) => {
    const operationScope = renderScope
    if (
      !projectId
      || memoryBusy
      || !isCurrentScope(operationScope)
      || !globalThis.confirm(`从项目中移除“${source.name}”？`)
    ) return
    if (!isCurrentScope(operationScope)) return
    setMemoryBusy(true)
    setMemoryError('')
    try {
      await deleteKnowledgeSource(projectId, source.id)
      if (!isCurrentScope(operationScope)) return
      await refreshSources()
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      setMemoryError(readableError(reason))
    } finally {
      if (isCurrentScope(operationScope)) setMemoryBusy(false)
    }
  }

  const copyArtifact = async (artifact: AIArtifact) => {
    const operationScope = renderScope
    if (
      !isCurrentScope(operationScope)
      || !artifacts.some((candidate) => candidate.id === artifact.id)
    ) return
    try {
      await copyText(artifact.content)
      if (!isCurrentScope(operationScope)) return
      setNotice(`已复制“${artifact.title}”。`)
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      setArtifactError(`复制失败：${readableError(reason)}`)
    }
  }

  const removeArtifact = async (artifact: AIArtifact) => {
    const operationScope = renderScope
    if (
      !isCurrentScope(operationScope)
      || !artifacts.some((candidate) => candidate.id === artifact.id)
    ) return
    if (!globalThis.confirm(`删除“${artifact.title}”？`)) return
    if (!isCurrentScope(operationScope)) return
    setArtifactError('')
    try {
      if (ownerId) await deleteAIArtifact(artifact.id)
      else await deleteLocalArtifact(sessionId, artifact.id)
      if (!isCurrentScope(operationScope)) return
      await refreshArtifacts()
    } catch (reason) {
      if (!isCurrentScope(operationScope)) return
      setArtifactError(readableError(reason))
    }
  }

  const indexProgress = indexJob?.chunk_count
    ? Math.min(100, Math.round(indexJob.processed_chunks / indexJob.chunk_count * 100))
    : 0
  // Keep chat and settings free while long-running artifact generation works
  // in the background. Index confirmation still gates both flows because it
  // decides how the next paid request will retrieve context.
  const indexGateOpen = indexBusy
    || pendingIndexAction !== undefined
    || projectRestoreBusy
  const chatBlocked = loading || indexGateOpen
  const artifactStartBlocked = artifactLoading !== null || indexGateOpen
  const settingsBlocked = indexGateOpen

  const chatContextHint = selectedProject
    ? `会结合当前转录与资料「${selectedProject.name}」${sources.length ? `（${sources.length} 项）` : ''}`
    : ownerId
      ? '默认使用当前转录；可点 📎 上传文件/图片到资料库'
      : '默认使用当前转录；上传文件需先登录'

  const balanceBlocked = chatBalanceBlocked
    || [indexError, memoryError, artifactError].some(isInsufficientBalanceMessage)

  return (
    <div className="dt-assistant">
      {balanceBlocked && (
        <div className="dt-ai-notice dt-ai-notice--balance" role="alert">
          <span>{INSUFFICIENT_BALANCE_MESSAGE}</span>
          <span className="dt-ai-notice__actions">
            {onTopUp && (
              <button
                className="dt-button dt-button--primary dt-button--small"
                onClick={onTopUp}
                type="button"
              >
                去充值
              </button>
            )}
            <button
              aria-label="关闭余额提示"
              className="dt-button dt-button--text dt-button--small"
              onClick={() => setChatBalanceBlocked(false)}
              type="button"
            >
              知道了
            </button>
          </span>
        </div>
      )}
      <div className="dt-ai-tabs" role="tablist" aria-label="AI 助手功能">
        {([
          ['chat', '对话', 'message'],
          ['artifacts', '整理', 'sparkles'],
          ['memory', '资料', 'archive'],
        ] as const).map(([value, label, icon]) => (
          <button
            aria-selected={tab === value}
            className={tab === value ? 'is-active' : ''}
            key={value}
            onClick={() => setTab(value)}
            role="tab"
            type="button"
          >
            <Icon name={icon} size={15} />
            <span>{label}</span>
          </button>
        ))}
      </div>

      <details className="dt-ai-settings">
        <summary>
          <span className="dt-ai-settings__summary-icon">
            <Icon name="settings" size={16} />
          </span>
          <span className="dt-ai-settings__summary-copy">
            <strong>回答偏好</strong>
            <small>
              {reasoningEffortLabel(reasoningEffort)}
              {' · '}
              {contextModeLabel(contextMode)}
              {' · '}
              {Math.round(maxContextTokens / 1_000)}K
              {' · 需要时再展开'}
            </small>
          </span>
        </summary>
        <div className="dt-ai-settings__body">
          <p className="dt-ai-settings__hint">
            大多数情况用默认即可。想更快或更深入时，改下面的「回答深度」就行。
          </p>
          <fieldset className="dt-ai-reasoning">
            <legend>回答深度</legend>
            <div aria-label="AI 思考程度" role="radiogroup">
              {reasoningOptions.map((option) => (
                <button
                  aria-checked={reasoningEffort === option.value}
                  className={reasoningEffort === option.value ? 'is-active' : ''}
                  disabled={settingsBlocked}
                  key={option.value}
                  onClick={() => setReasoningEffort(option.value)}
                  role="radio"
                  type="button"
                >
                  <span>
                    <strong>{option.label}</strong>
                    {option.recommended && <em>推荐</em>}
                  </span>
                  <small>{option.description}</small>
                </button>
              ))}
            </div>
            <small>
              系统会按档位自动分配回答预算；更深入通常更慢，也可能消耗更多额度。
            </small>
          </fieldset>

          <details className="dt-ai-advanced">
            <summary>
              <span>
                <strong>高级选项</strong>
                <small>读取范围、上下文长度、预览、扫描件识别语言 — 能力全部保留</small>
              </span>
            </summary>
            <div className="dt-ai-advanced__body">
              <div className="dt-ai-context-controls">
                <label>
                  <span>读取方式</span>
                  <select
                    aria-label="AI 上下文模式"
                    disabled={settingsBlocked}
                    onChange={(event) => {
                      setContextMode(event.target.value as RagContextMode)
                      setPolicyOverridden(true)
                    }}
                    value={contextMode}
                  >
                    <option value="smart">智能选取（推荐）</option>
                    <option value="full">尽量看全文</option>
                    <option value="retrieval">只找相关段落</option>
                  </select>
                </label>
                <label>
                  <span>上下文长度</span>
                  <select
                    aria-label="AI 上下文 token 上限"
                    disabled={settingsBlocked}
                    onChange={(event) => {
                      setMaxContextTokens(Number(event.target.value))
                      setPolicyOverridden(true)
                    }}
                    value={maxContextTokens}
                  >
                    {contextPresets.map((preset) => (
                      <option key={preset.value} value={preset.value}>{preset.label}</option>
                    ))}
                  </select>
                </label>
              </div>
              <fieldset className="dt-ai-language-picker">
                <legend>扫描件 OCR 语言</legend>
                {ocrOptions.map((option) => (
                  <label key={option.value}>
                    <input
                      checked={ocrLanguages.includes(option.value)}
                      onChange={(event) => setOcrLanguages((current) => (
                        event.target.checked
                          ? [...current, option.value]
                          : current.filter((language) => language !== option.value)
                      ))}
                      type="checkbox"
                    />
                    {option.label}
                  </label>
                ))}
              </fieldset>
              {ocrLanguages.length === 0 && (
                <small className="dt-inline-error">上传图片/扫描件前，至少选择一种识别语言。</small>
              )}
              <div className="dt-ai-settings__actions">
                <button
                  className="dt-button dt-button--secondary dt-button--small"
                  disabled={contextPreviewBusy || projectRestoreBusy}
                  onClick={() => { void previewContext() }}
                  type="button"
                >
                  {contextPreviewBusy ? '正在预览…' : '预览实际读取内容'}
                </button>
                {policyOverridden && selectedProject && (
                  <button
                    className="dt-button dt-button--text dt-button--small"
                    onClick={() => applyProjectPolicy(selectedProject)}
                    type="button"
                  >
                    恢复项目默认值
                  </button>
                )}
              </div>
              {contextMetadata && (
                <div className="dt-ai-context-status" role="status">
                  <strong>最近一次读取</strong>
                  <span>{contextDescription(contextMetadata)}</span>
                </div>
              )}
              {contextPreview && (
                <details className="dt-ai-context-preview" open>
                  <summary>AI 实际读取的内容</summary>
                  <pre>{contextPreview}</pre>
                </details>
              )}
            </div>
          </details>
        </div>
      </details>

      {(indexConfirmation || indexJob) && pendingIndexAction && (
        <section
          aria-labelledby="dt-index-confirmation-title"
          className="dt-ai-index-gate"
          ref={indexGateRef}
          role="dialog"
          tabIndex={-1}
        >
          <strong id="dt-index-confirmation-title">
            {indexJob ? '正在准备资料…' : '首次用知识库，需要先准备一下'}
          </strong>
          {indexConfirmation && !indexJob && (
            <>
              <p>
                为了更准确地回答，需要给
                {pendingIndexAction.projectId ? '你上传的资料' : '当前会话转录'}
                建一次语义索引。这是显式操作（可能产生少量费用），不会偷偷自动跑。
              </p>
              <details className="dt-ai-index-details">
                <summary>查看费用与技术明细</summary>
                <dl>
                  <div>
                    <dt>模型</dt>
                    <dd>{indexConfirmation.model} · {indexConfirmation.dimensions}d</dd>
                  </div>
                  <div>
                    <dt>待索引分块</dt>
                    <dd>
                      {(indexConfirmation.pending_chunks ?? indexConfirmation.chunk_count).toLocaleString()}
                      {' / '}
                      {indexConfirmation.chunk_count.toLocaleString()}
                    </dd>
                  </div>
                  <div><dt>预计 tokens</dt><dd>{indexConfirmation.estimated_tokens.toLocaleString()}</dd></div>
                  <div>
                    <dt>预计费用</dt>
                    <dd>{formatUSD(indexConfirmation.estimated_dp, 4)}</dd>
                  </div>
                </dl>
              </details>
            </>
          )}
          {indexJob && (
            <>
              <p aria-live="polite">
                {indexStatusLabel(indexJob.status)}：{indexJob.processed_chunks.toLocaleString()}
                /{indexJob.chunk_count.toLocaleString()} 个分块（{indexProgress}%）
              </p>
              <progress max={100} value={indexProgress}>{indexProgress}%</progress>
            </>
          )}
          {indexError && <div className="dt-inline-error" role="alert">{indexError}</div>}
          <div className="dt-ai-action-row">
            {!indexJob && (
              <button
                className="dt-button dt-button--primary"
                disabled={indexBusy}
                onClick={() => { void buildSemanticIndex() }}
                type="button"
              >
                {indexBusy ? '创建中…' : '确认并继续'}
              </button>
            )}
            {indexJob && indexError && (
              <button
                className="dt-button dt-button--primary"
                disabled={indexBusy}
                onClick={() => { void retryIndexJob() }}
                type="button"
              >
                重新检查或重试
              </button>
            )}
            <button
              className="dt-button dt-button--secondary"
              disabled={indexBusy}
              onClick={useLexicalOnce}
              type="button"
            >
              本次先简单搜索
            </button>
            <button
              className="dt-button dt-button--text"
              disabled={indexBusy}
              onClick={() => { void cancelPendingAction() }}
              type="button"
            >
              取消
            </button>
          </div>
        </section>
      )}
      {indexBusy && !indexConfirmation && !indexJob && (
        <div className="dt-ai-notice" aria-live="polite">正在检查资料是否就绪…</div>
      )}
      {projectRestoreBusy && (
        <div className="dt-ai-notice" aria-live="polite">正在恢复会话关联的资料…</div>
      )}
      {projectRestoreError && (
        <div className="dt-inline-error dt-ai-retry-notice" role="alert">
          <span>无法恢复会话资料：{projectRestoreError}</span>
          <button
            className="dt-button dt-button--secondary dt-button--small"
            onClick={() => { void refreshProjects(true) }}
            type="button"
          >
            重试
          </button>
        </div>
      )}
      {indexError && !pendingIndexAction && (
        <div className="dt-inline-error" role="alert">{indexError}</div>
      )}
      {artifactLoading && (
        <div className="dt-ai-notice dt-ai-notice--progress" aria-live="polite">
          正在后台生成{artifactTypeLabel(artifactLoading)}
          … 可继续对话，完成后会出现在「整理」。
        </div>
      )}
      {artifactError && tab !== 'artifacts' && (
        <div className="dt-inline-error dt-ai-retry-notice" role="alert">
          <span>生成失败：{artifactError}</span>
          <button
            className="dt-button dt-button--secondary dt-button--small"
            onClick={() => setArtifactError('')}
            type="button"
          >
            关闭
          </button>
        </div>
      )}
      {notice && !artifactLoading && (
        <div className="dt-ai-notice" aria-live="polite">{notice}</div>
      )}

      {tab === 'chat' && (
        <section aria-label="AI 对话" className="dt-chat" role="tabpanel">
          <header className="dt-chat__header">
            <span>
              <strong>问这场会话</strong>
              <small>{chatContextHint}</small>
            </span>
            {messages.length > 0 && (
              <button
                className="dt-button dt-button--text dt-button--small"
                disabled={loading}
                onClick={() => {
                  setMessages([])
                  localStorage.removeItem(chatHistoryKey(ownerId, sessionId))
                }}
                type="button"
              >
                清空对话
              </button>
            )}
          </header>
          <div className="dt-chat__messages" ref={listRef}>
            {messages.length === 0 && (
              <div className="dt-ai-starter">
                <span className="dt-ai-starter__icon"><Icon name="sparkles" size={22} /></span>
                <strong>直接问就行</strong>
                <span>可以总结、找行动项，也可以先 📎 上传文件或图片再问。</span>
                <div>
                  {starterQuestions.map((question) => (
                    <button
                      disabled={chatBlocked}
                      key={question}
                      onClick={() => setInput(question)}
                      type="button"
                    >
                      {question}
                    </button>
                  ))}
                </div>
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
              <div className="dt-chat__thinking" aria-label="AI 正在回答">
                <span /><span /><span />
                <small>{reasoningEffortLabel(reasoningEffort)}思考中</small>
              </div>
            )}
          </div>
          <form className="dt-chat__composer" onSubmit={send}>
            {ownerId && sources.length > 0 && (
              <div className="dt-chat__attachments" aria-label="已关联资料">
                {sources.slice(0, 4).map((source) => (
                  <button
                    className="dt-chat__attachment-chip"
                    key={source.id}
                    onClick={() => setTab('memory')}
                    type="button"
                  >
                    <Icon name={source.source_type === 'file' ? 'paperclip' : 'archive'} size={12} />
                    <span>{source.name}</span>
                    <small>{sourceStatusLabel(source.status)}</small>
                  </button>
                ))}
                {sources.length > 4 && (
                  <button
                    className="dt-chat__attachment-chip dt-chat__attachment-chip--more"
                    onClick={() => setTab('memory')}
                    type="button"
                  >
                    +{sources.length - 4} 项
                  </button>
                )}
              </div>
            )}
            <textarea
              aria-label="向 AI 提问"
              disabled={chatBlocked}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !event.shiftKey) {
                  event.preventDefault()
                  event.currentTarget.form?.requestSubmit()
                }
              }}
              placeholder="问这场会话… 也可附上文件或图片"
              rows={3}
              value={input}
            />
            <footer className="dt-chat__composer-footer">
              <div className="dt-chat__composer-tools">
                <label
                  className={
                    memoryBusy || chatBlocked
                      ? 'dt-chat__attach is-disabled'
                      : 'dt-chat__attach'
                  }
                  title={
                    ownerId
                      ? `上传${knowledgeFileAcceptLabel}`
                      : '上传文件需要先登录'
                  }
                >
                  <Icon name="paperclip" size={16} />
                  <span>附件</span>
                  <input
                    accept={knowledgeFileAccept}
                    disabled={memoryBusy || chatBlocked}
                    onChange={(event) => {
                      onChatFileSelected(event.target.files?.[0])
                      event.currentTarget.value = ''
                    }}
                    type="file"
                  />
                </label>
                {memoryBusy && (
                  <small className="dt-chat__upload-status">上传中…</small>
                )}
              </div>
              <div className="dt-chat__composer-send">
                <small>Enter 发送 · Shift+Enter 换行</small>
                <button
                  className="dt-button dt-button--primary"
                  disabled={!input.trim() || chatBlocked}
                  type="submit"
                >
                  <Icon name="sparkles" size={15} />
                  发送
                </button>
              </div>
            </footer>
          </form>
        </section>
      )}

      {tab === 'artifacts' && (
        <section aria-label="会话内容生成" className="dt-summary dt-ai-artifacts" role="tabpanel">
          <header className="dt-ai-section-heading">
            <span>
              <strong>一键整理</strong>
              <small>点一下后台生成，可继续对话；不会自动扣额度。</small>
            </span>
          </header>
          <div className="dt-ai-artifact-actions">
            {([
              ['summary', '会话摘要', '快速回顾结论与重点', 'sparkles'],
              ['notes', '结构化笔记', '整理主题、细节与脉络', 'archive'],
              ['action_items', '行动项', '提取负责人、时间与下一步', 'check'],
            ] as const).map(([type, label, description, icon]) => (
              <button
                aria-busy={artifactLoading === type}
                className={
                  artifactLoading === type
                    ? 'dt-ai-action-card is-loading'
                    : 'dt-ai-action-card'
                }
                disabled={artifactStartBlocked}
                key={type}
                onClick={() => generateArtifact(type)}
                type="button"
              >
                <span className="dt-ai-action-card__icon"><Icon name={icon} size={18} /></span>
                <span>
                  <strong>{artifactLoading === type ? '后台生成中…' : label}</strong>
                  <small>
                    {artifactLoading === type
                      ? '可切换到对话继续使用'
                      : description}
                  </small>
                </span>
              </button>
            ))}
          </div>
          {artifactError && <div className="dt-inline-error" role="alert">{artifactError}</div>}
          {artifactLoading && (
            <article
              aria-busy="true"
              aria-label={`${artifactTypeLabel(artifactLoading)}生成中`}
              className="dt-ai-artifact dt-ai-artifact--pending"
            >
              <header>
                <span>
                  <strong>{artifactTypeLabel(artifactLoading)}生成中…</strong>
                  <small>后台处理中，完成后会自动出现在下方列表。</small>
                </span>
              </header>
              <div className="dt-ai-pending-pulse" aria-hidden="true">
                <span /><span /><span />
              </div>
            </article>
          )}
          {artifacts.length === 0 && !artifactError && !artifactLoading && (
            <div className="dt-ai-empty-state">
              <Icon name="archive" size={20} />
              <span>生成的内容会保存在这里，方便复制或下载。</span>
            </div>
          )}
          {artifacts.map((artifact) => (
            <article className="dt-ai-artifact" key={artifact.id}>
              <header>
                <span>
                  <strong>{artifact.title}</strong>
                  <small>
                    {new Date(artifact.created_at).toLocaleString()}
                    {artifact.model ? ` · ${artifact.model}` : ''}
                    {artifact.context_tokens
                      ? ` · 上下文约 ${artifact.context_tokens.toLocaleString()} tokens`
                      : ''}
                  </small>
                </span>
                <div className="dt-ai-action-row">
                  <button className="dt-button dt-button--text dt-button--small" onClick={() => { void copyArtifact(artifact) }} type="button">复制</button>
                  <button className="dt-button dt-button--text dt-button--small" onClick={() => downloadArtifact(artifact)} type="button">下载</button>
                  <button className="dt-button dt-button--text dt-button--small" onClick={() => { void removeArtifact(artifact) }} type="button">删除</button>
                </div>
              </header>
              {artifact.content.trim() ? (
                <MarkdownView text={artifact.content} />
              ) : (
                <div className="dt-inline-error" role="alert">
                  此次生成没有返回正文，请删除后重新生成。
                </div>
              )}
            </article>
          ))}
        </section>
      )}

      {tab === 'memory' && (
        <section aria-label="项目知识库" className="dt-summary dt-ai-memory" role="tabpanel">
          <header className="dt-ai-section-heading">
            <span>
              <strong>资料库</strong>
              <small>文件、图片和文字记忆。对话里上传的也会出现在这里。</small>
            </span>
          </header>
          {!ownerId ? (
            <div className="dt-empty dt-empty--compact">
              <strong>资料库需要登录</strong>
              <span>未登录仍可对话，并用本地保存生成的摘要/笔记。</span>
            </div>
          ) : (
            <>
              <section className="dt-ai-project-switcher">
                <div className="dt-summary__toolbar">
                  <label className="dt-ai-field">
                    当前项目
                    <select
                      disabled={memoryBusy}
                      onChange={(event) => { void selectProject(event.target.value) }}
                      value={projectId}
                    >
                      <option value="">不关联项目</option>
                      {projects.map((project) => (
                        <option key={project.id} value={project.id}>{project.name}</option>
                      ))}
                    </select>
                  </label>
                  {linkedProjectId && (
                    <button
                      className="dt-button dt-button--text dt-button--small"
                      disabled={memoryBusy}
                      onClick={() => { void selectProject('') }}
                      type="button"
                    >
                      解除关联
                    </button>
                  )}
                </div>
                <div className="dt-ai-memory-row">
                  <input
                    aria-label="新项目名称"
                    onChange={(event) => setNewProjectName(event.target.value)}
                    placeholder="给资料起个项目名，例如「产品发布」"
                    value={newProjectName}
                  />
                  <button className="dt-button dt-button--secondary" disabled={!newProjectName.trim() || memoryBusy} onClick={() => { void createProject() }} type="button">
                    新建项目
                  </button>
                </div>
                {!selectedProject && (
                  <p className="dt-ai-settings__hint">
                    也可以直接在「对话」里点附件上传：会自动创建「{defaultSessionProjectName()}」并关联本会话。
                  </p>
                )}
              </section>
              {selectedProject && (
                <>
                  <details className="dt-ai-project-editor">
                    <summary>
                      <span>
                        <strong id="dt-project-settings-title">项目设置</strong>
                        <small>{contextModeLabel(projectContextMode)} · {Math.round(projectMaxTokens / 1_000)}K</small>
                      </span>
                    </summary>
                    <div className="dt-ai-project-editor__body">
                      <label className="dt-ai-field">
                        名称
                        <input onChange={(event) => setProjectName(event.target.value)} value={projectName} />
                      </label>
                      <label className="dt-ai-field">
                        描述
                        <textarea onChange={(event) => setProjectDescription(event.target.value)} rows={3} value={projectDescription} />
                      </label>
                      <div className="dt-ai-memory-row">
                        <label className="dt-ai-field">
                          默认读取方式
                          <select onChange={(event) => setProjectContextMode(event.target.value as RagContextMode)} value={projectContextMode}>
                            <option value="smart">智能选取（推荐）</option>
                            <option value="full">尽量看全文</option>
                            <option value="retrieval">只找相关段落</option>
                          </select>
                        </label>
                        <label className="dt-ai-field">
                          默认上下文长度
                          <select onChange={(event) => setProjectMaxTokens(Number(event.target.value))} value={projectMaxTokens}>
                            {contextPresets.map((preset) => (
                              <option key={preset.value} value={preset.value}>{preset.label}</option>
                            ))}
                          </select>
                        </label>
                      </div>
                      <div className="dt-ai-action-row">
                        <button className="dt-button dt-button--secondary" disabled={memoryBusy || !projectName.trim()} onClick={() => { void saveProject() }} type="button">
                          保存项目设置
                        </button>
                        <button className="dt-button dt-button--text" disabled={memoryBusy} onClick={() => { void removeProject() }} type="button">
                          删除项目
                        </button>
                      </div>
                    </div>
                  </details>

                  <section className="dt-ai-knowledge-add">
                    <header>
                      <span>
                        <strong>添加资料</strong>
                        <small>上传文件/图片，或写一条需要长期记住的文字。</small>
                      </span>
                    </header>
                    <label className="dt-button dt-button--secondary dt-ai-file-button">
                      <Icon name="paperclip" size={16} />
                      上传{knowledgeFileAcceptLabel}
                      <input
                        accept={knowledgeFileAccept}
                        disabled={memoryBusy || ocrLanguages.length === 0}
                        onChange={(event) => {
                          void uploadFile(event.target.files?.[0])
                          event.currentTarget.value = ''
                        }}
                        type="file"
                      />
                    </label>
                    <details className="dt-ai-memory-disclosure">
                      <summary>扫描件 OCR 语言（高级）</summary>
                      <fieldset className="dt-ai-language-picker">
                        <legend className="visually-hidden">扫描件 OCR 语言</legend>
                        {ocrOptions.map((option) => (
                          <label key={option.value}>
                            <input
                              checked={ocrLanguages.includes(option.value)}
                              onChange={(event) => setOcrLanguages((current) => (
                                event.target.checked
                                  ? [...current, option.value]
                                  : current.filter((language) => language !== option.value)
                              ))}
                              type="checkbox"
                            />
                            {option.label}
                          </label>
                        ))}
                      </fieldset>
                      {ocrLanguages.length === 0 && (
                        <small className="dt-inline-error">至少选择一种 OCR 语言。</small>
                      )}
                    </details>
                    <details className="dt-ai-memory-disclosure">
                      <summary>添加一条文字记忆</summary>
                      <div className="dt-ai-memory-form">
                        <input onChange={(event) => setMemoryName(event.target.value)} placeholder="记忆名称" value={memoryName} />
                        <textarea onChange={(event) => setMemoryContent(event.target.value)} placeholder="需要跨会话复用的明确内容" rows={4} value={memoryContent} />
                        <button className="dt-button dt-button--secondary" disabled={memoryBusy || !memoryName.trim() || !memoryContent.trim()} onClick={() => { void addMemory() }} type="button">
                          添加记忆
                        </button>
                      </div>
                    </details>
                  </section>
                  {memoryError && <div className="dt-inline-error" role="alert">{memoryError}</div>}
                  <div className="dt-ai-source-list__heading">
                    <strong>已添加</strong>
                    <small>{sources.length} 项</small>
                  </div>
                  <div className="dt-ai-source-list">
                    {sources.length === 0 && (
                      <div className="dt-ai-empty-state">
                        <span>还没有文件或记忆。在对话里点附件，或在上方上传。</span>
                      </div>
                    )}
                    {sources.map((source) => (
                      <article key={source.id}>
                        <div className="dt-ai-source-title">
                          <span>
                            <strong>{source.name}</strong>
                            <small>
                              {source.source_type === 'memory' ? '文字记忆' : '文件'}
                              {' · '}{sourceStatusLabel(source.status)}
                              {source.chunk_count ? ` · ${source.chunk_count} chunks` : ''}
                              {source.index_status
                                ? ` · 语义索引：${indexStatusLabel(source.index_status)}`
                                : ''}
                              {source.ocr_languages?.length
                                ? ` · OCR ${source.ocr_languages.join('+')}`
                                : ''}
                            </small>
                          </span>
                          <div className="dt-ai-action-row">
                            {source.source_type === 'memory' && (
                              <button className="dt-button dt-button--text dt-button--small" disabled={memoryBusy} onClick={() => startEditingMemory(source)} type="button">
                                编辑
                              </button>
                            )}
                            {source.source_type === 'file' && source.status === 'error' && (
                              <button className="dt-button dt-button--text dt-button--small" disabled={memoryBusy} onClick={() => { void retrySource(source) }} type="button">
                                重试
                              </button>
                            )}
                            <button className="dt-button dt-button--text dt-button--small" disabled={memoryBusy} onClick={() => { void removeSource(source) }} type="button">
                              移除
                            </button>
                          </div>
                        </div>
                        {editingSourceId === source.id && (
                          <div className="dt-ai-memory-form">
                            <input aria-label="记忆名称" onChange={(event) => setEditingMemoryName(event.target.value)} value={editingMemoryName} />
                            <textarea aria-label="记忆内容" onChange={(event) => setEditingMemoryContent(event.target.value)} rows={5} value={editingMemoryContent} />
                            {!source.content && (
                              <small className="dt-muted">保存前请填写完整记忆内容。</small>
                            )}
                            <div className="dt-ai-action-row">
                              <button className="dt-button dt-button--secondary dt-button--small" disabled={memoryBusy || !editingMemoryName.trim() || !editingMemoryContent.trim()} onClick={() => { void saveMemoryEdit() }} type="button">
                                保存
                              </button>
                              <button className="dt-button dt-button--text dt-button--small" disabled={memoryBusy} onClick={() => setEditingSourceId('')} type="button">
                                取消
                              </button>
                            </div>
                          </div>
                        )}
                        {source.error_message && <small className="dt-inline-error">{source.error_message}</small>}
                        {source.index_error_message && (
                          <small className="dt-inline-error">{source.index_error_message}</small>
                        )}
                      </article>
                    ))}
                  </div>
                </>
              )}
            </>
          )}
        </section>
      )}
    </div>
  )
}
