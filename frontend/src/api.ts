import {
  authFetch,
  ensureValidAccessToken,
  getAccessToken,
} from './pro/api/auth'

// In production, use relative URLs to work with the same origin
const BACKEND_URL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080';
const isProduction = BACKEND_URL === '/';

export interface SystemAccessCapabilities {
  anonymousAPIEnabled: boolean
  authenticationEnabled: boolean
  registrationEnabled: boolean
  ragEnabled: boolean
}

export async function getSystemAccess(): Promise<SystemAccessCapabilities> {
  const base = isProduction ? '' : BACKEND_URL
  try {
    const response = await fetch(`${base}/api/system/access`, { cache: 'no-store' })
    if (!response.ok) throw new Error(`System access request failed: ${response.status}`)
    const access = await response.json() as {
      anonymous_api_enabled?: boolean
      authentication_enabled?: boolean
      registration_enabled?: boolean
      rag_enabled?: boolean
    }
    return {
      anonymousAPIEnabled: access.anonymous_api_enabled === true,
      authenticationEnabled: access.authentication_enabled === true,
      registrationEnabled: access.registration_enabled === true,
      ragEnabled: access.rag_enabled === true,
    }
  } catch {
    return {
      anonymousAPIEnabled: false,
      authenticationEnabled: false,
      registrationEnabled: false,
      ragEnabled: false,
    }
  }
}

export async function getOptionalAuthHeaders(): Promise<Record<string, string>> {
  if (!getAccessToken()) return {}
  const token = await ensureValidAccessToken()
  return { Authorization: `Bearer ${token}` }
}

export async function canUseAnonymousAPI(): Promise<boolean> {
  if (getAccessToken()) return false
  return (await getSystemAccess()).anonymousAPIEnabled
}

export async function getJwt(): Promise<string> {
  try {
    const url = isProduction ? '/api/token/rt' : `${BACKEND_URL}/api/token/rt`;
    const authHeaders = await getOptionalAuthHeaders()
    const response = await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...authHeaders,
      },
    });

    if (!response.ok) {
      throw new Error(`Failed to get JWT: ${response.statusText}`);
    }

    const data = await response.json();
    return data.token;
  } catch (error) {
    console.error('Error fetching JWT:', error);
    throw error;
  }
}

export type RagConfig = {
  api_key?: string
  api_base?: string
  model?: string
  prompt?: string
}

export type RagAskResponse = {
  answer: string
  usage?: {
    prompt_tokens: number
    completion_tokens: number
    total_tokens: number
    model?: string
    cached_tokens?: number
    cache_write_tokens?: number
  }
  latency_ms?: number
  context?: RagContextMetadata
}

export type RagContextMode = 'smart' | 'full' | 'retrieval'
export type AIReasoningEffort = 'low' | 'medium' | 'high'

export interface RagContextPolicy {
  mode: RagContextMode
  max_tokens: number
}

export interface RagTranscriptSegment {
  id?: string
  speaker?: string
  text: string
  start_time?: number
  end_time?: number
}

export interface RagHistoryMessage {
  role: 'user' | 'assistant'
  content: string
}

export interface RagContextMetadata {
  effective_mode: RagContextMode
  rag_used: boolean
  retrieval_mode?: AIRetrievalMode
  index_status: AIIndexStatus
  estimated_tokens: number
  truncated: boolean
  index_targets?: Array<{
    target_type: 'project' | 'session'
    target_id: string
    index_status: AIIndexStatus
  }>
  sources?: Array<{
    kind: string
    id?: string
    label?: string
    start_time?: number
    end_time?: number
  }>
}

export interface RagAskOptions {
  history?: RagHistoryMessage[]
  clientTranscript?: RagTranscriptSegment[]
  contextPolicy?: RagContextPolicy
  projectId?: string
  retrievalPreference?: AIRetrievalPreference
  clientRequestId?: string
  reasoningEffort?: AIReasoningEffort
}

export async function askRag(
  sessionId: string,
  query: string,
  topK: number = 5,
  config?: RagConfig,
  timeoutMs?: number,
  options?: RagAskOptions,
): Promise<RagAskResponse> {
  const base = isProduction ? '' : BACKEND_URL
  const controller = new AbortController()
  const timeout = timeoutMs && timeoutMs > 0
    ? globalThis.setTimeout(() => controller.abort(), timeoutMs)
    : undefined
  try {
    const authHeaders = await getOptionalAuthHeaders()
    const res = await fetch(`${base}/api/rag/ask`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...authHeaders },
      body: JSON.stringify({
        session_id: sessionId,
        question: query,
        top_k: topK,
        config,
        history: options?.history,
        client_transcript: options?.clientTranscript,
        context_policy: options?.contextPolicy,
        project_id: options?.projectId,
        retrieval_preference: options?.retrievalPreference ?? 'auto',
        client_request_id: options?.clientRequestId,
        reasoning_effort: options?.reasoningEffort,
      }),
      signal: controller.signal,
    })
    if (!res.ok) throw new Error(await res.text())
    return await res.json()
  } catch (reason) {
    if (reason instanceof DOMException && reason.name === 'AbortError') {
      throw new Error('AI 回答超时，请重试。', { cause: reason })
    }
    throw reason
  } finally {
    if (timeout) globalThis.clearTimeout(timeout)
  }
}

export interface AIArtifact {
  id: string
  artifact_type: 'summary' | 'notes' | 'action_items'
  title: string
  content: string
  context_tokens: number
  model?: string
  created_at: string
}

export type AIRetrievalPreference = 'auto' | 'lexical_only'
export type AIRetrievalMode =
  | 'none'
  | 'hybrid'
  | 'semantic'
  | 'lexical_fallback'
  | 'legacy'
export type AIIndexStatus =
  | 'unindexed'
  | 'queued'
  | 'processing'
  | 'ready'
  | 'stale'
  | 'error'
export type AIIndexJobStatus =
  | 'queued'
  | 'processing'
  | 'ready'
  | 'error'
  | 'cancelled'

export async function generateAIArtifact(
  sessionId: string,
  artifactType: AIArtifact['artifact_type'],
  clientTranscript: RagTranscriptSegment[],
  contextPolicy: RagContextPolicy,
  config?: RagConfig,
  projectId?: string,
  retrievalPreference: AIRetrievalPreference = 'auto',
  clientRequestId?: string,
  reasoningEffort: AIReasoningEffort = 'medium',
  timeoutMs = 120_000,
): Promise<{ artifact: AIArtifact; context?: RagContextMetadata; latency_ms?: number }> {
  return aiFetchJSON('/api/ai/artifacts', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      session_id: sessionId,
      artifact_type: artifactType,
      client_transcript: clientTranscript,
      context_policy: contextPolicy,
      config,
      project_id: projectId,
      retrieval_preference: retrievalPreference,
      client_request_id: clientRequestId,
      reasoning_effort: reasoningEffort,
    }),
  }, timeoutMs, Boolean(clientRequestId && getAccessToken()))
}

export interface AIContextPreview extends RagContextMetadata {
  segment_count: number
  preview: string
  requested_retrieval_preference?: AIRetrievalPreference
  preview_retrieval_preference?: AIRetrievalPreference
  semantic_query_executed?: boolean
  semantic_skipped?: boolean
  preview_truncated?: boolean
}

export async function previewAIContext(
  sessionId: string,
  clientTranscript: RagTranscriptSegment[],
  contextPolicy: RagContextPolicy,
  options?: {
    question?: string
    history?: RagHistoryMessage[]
    projectId?: string
    retrievalPreference?: AIRetrievalPreference
    artifactType?: 'summary' | 'notes' | 'action_items'
    topK?: number
    config?: RagConfig
    executeSemantic?: boolean
  },
): Promise<AIContextPreview> {
  const body = await aiFetchJSON<AIContextPreview | {
    context: AIContextPreview
    preview?: string
    segment_count?: number
  }>('/api/ai/context/preview', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      session_id: sessionId,
      client_transcript: clientTranscript,
      context_policy: contextPolicy,
      question: options?.question,
      history: options?.history,
      project_id: options?.projectId,
      retrieval_preference: options?.retrievalPreference ?? 'auto',
      artifact_type: options?.artifactType,
      top_k: options?.topK,
      config: options?.config,
      execute_semantic: options?.executeSemantic ?? false,
    }),
  }, 30_000)
  if ('context' in body) {
    return {
      ...body.context,
      preview: body.preview ?? body.context.preview ?? '',
      segment_count: body.segment_count ?? body.context.segment_count ?? 0,
    }
  }
  return body
}

export async function listAIArtifacts(sessionId: string): Promise<AIArtifact[]> {
  const body = await aiFetchJSON<{ artifacts?: AIArtifact[] }>(
    `/api/ai/artifacts?session_id=${encodeURIComponent(sessionId)}`,
  )
  return body.artifacts ?? []
}

export async function deleteAIArtifact(artifactId: string): Promise<void> {
  await aiFetchJSON(`/api/ai/artifacts/${encodeURIComponent(artifactId)}`, {
    method: 'DELETE',
  })
}

export interface AIProject {
  id: string
  name: string
  description: string
  context_mode: RagContextMode
  max_context_tokens: number
}

export interface AIProjectListResponse {
  projects: AIProject[]
  linked_project_id: string | null
}

export interface KnowledgeSource {
  id: string
  name: string
  source_type: 'file' | 'memory'
  status: 'queued' | 'processing' | 'ready' | 'error'
  error_message?: string
  chunk_count: number
  content?: string
  media_type?: string
  size_bytes?: number
  extracted_text_bytes?: number
  vector_bytes?: number
  created_at?: string
  updated_at?: string
  ocr_languages?: OCRLanguage[]
  index_status?: AIIndexStatus
  embedding_model?: string
  embedding_dimensions?: number
  embedded_chunk_count?: number
  index_error_message?: string
}

const DEFAULT_AI_TIMEOUT_MS = 30_000
const AI_GATEWAY_RETRY_DELAYS_MS = [250, 1_000] as const
const AI_TRANSIENT_GATEWAY_STATUSES = new Set([502, 503, 504])

export class AIRequestError extends Error {
  readonly status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'AIRequestError'
    this.status = status
  }
}

function waitForAIRetry(delayMs: number, signal: AbortSignal): Promise<void> {
  if (signal.aborted) {
    return Promise.reject(signal.reason ?? new DOMException('Request aborted', 'AbortError'))
  }
  return new Promise((resolve, reject) => {
    const onAbort = () => {
      globalThis.clearTimeout(timer)
      reject(signal.reason ?? new DOMException('Request aborted', 'AbortError'))
    }
    const timer = globalThis.setTimeout(() => {
      signal.removeEventListener('abort', onAbort)
      resolve()
    }, delayMs)
    signal.addEventListener('abort', onAbort, { once: true })
  })
}

function retryAfterMilliseconds(response: Response): number | null {
  const raw = response.headers.get('Retry-After')?.trim()
  if (!raw) return null
  const seconds = Number(raw)
  if (Number.isFinite(seconds) && seconds >= 0) {
    return Math.min(Math.max(seconds * 1_000, 250), 10_000)
  }
  const retryAt = Date.parse(raw)
  if (!Number.isFinite(retryAt)) return null
  return Math.min(Math.max(retryAt - Date.now(), 250), 10_000)
}

async function aiFetchJSON<T = unknown>(
  path: string,
  init?: RequestInit,
  timeoutMs = DEFAULT_AI_TIMEOUT_MS,
  retryTransientWrite = false,
): Promise<T> {
  const base = isProduction ? '' : BACKEND_URL
  const controller = new AbortController()
  const timeout = globalThis.setTimeout(() => controller.abort(), timeoutMs)
  const authHeaders = await getOptionalAuthHeaders()
  try {
    const method = (init?.method ?? 'GET').toUpperCase()
    const retryable = method === 'GET' || method === 'HEAD' || retryTransientWrite
    let response: Response
    let gatewayRetries = 0
    for (;;) {
      try {
        response = await fetch(`${base}${path}`, {
          ...init,
          headers: { ...authHeaders, ...(init?.headers ?? {}) },
          signal: controller.signal,
        })
      } catch (reason) {
        if (
          !retryable
          || gatewayRetries >= AI_GATEWAY_RETRY_DELAYS_MS.length
          || controller.signal.aborted
          || !(reason instanceof TypeError)
        ) {
          throw reason
        }
        await waitForAIRetry(
          AI_GATEWAY_RETRY_DELAYS_MS[gatewayRetries],
          controller.signal,
        )
        gatewayRetries += 1
        continue
      }
      if (
        retryable
        && AI_TRANSIENT_GATEWAY_STATUSES.has(response.status)
        && gatewayRetries < AI_GATEWAY_RETRY_DELAYS_MS.length
      ) {
        await response.body?.cancel().catch(() => undefined)
        await waitForAIRetry(
          AI_GATEWAY_RETRY_DELAYS_MS[gatewayRetries],
          controller.signal,
        )
        gatewayRetries += 1
        continue
      }
      const retryAfterMs = retryTransientWrite && response.status === 409
        ? retryAfterMilliseconds(response)
        : null
      if (retryAfterMs !== null) {
        await response.body?.cancel().catch(() => undefined)
        await waitForAIRetry(retryAfterMs, controller.signal)
        continue
      }
      break
    }
    if (!response.ok) {
      const responseText = await response.text()
      let message = responseText
      try {
        const parsed = JSON.parse(responseText) as { error?: string; message?: string }
        message = parsed.error ?? parsed.message ?? responseText
      } catch {
        // Plain-text API errors remain useful to the user.
      }
      throw new AIRequestError(
        response.status,
        message || `AI request failed: ${response.status}`,
      )
    }
    if (response.status === httpNoContent) return undefined as T
    const responseText = await response.text()
    return (responseText ? JSON.parse(responseText) : undefined) as T
  } catch (reason) {
    if (reason instanceof DOMException && reason.name === 'AbortError') {
      throw new Error('请求超时，请稍后重试。', { cause: reason })
    }
    throw reason
  } finally {
    globalThis.clearTimeout(timeout)
  }
}

const httpNoContent = 204

export async function listAIProjects(sessionId?: string): Promise<AIProjectListResponse> {
  const query = sessionId ? `?session_id=${encodeURIComponent(sessionId)}` : ''
  const body = await aiFetchJSON<{
    projects?: AIProject[]
    linked_project_id?: string | null
  }>(`/api/ai/projects${query}`)
  return {
    projects: body.projects ?? [],
    linked_project_id: body.linked_project_id ?? null,
  }
}

export async function createAIProject(
  name: string,
  options?: Partial<Pick<
    AIProject,
    'description' | 'context_mode' | 'max_context_tokens'
  >>,
): Promise<AIProject> {
  const body = await aiFetchJSON<{ project: AIProject }>('/api/ai/projects', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name,
      description: options?.description ?? '',
      context_mode: options?.context_mode ?? 'smart',
      max_context_tokens: options?.max_context_tokens ?? 64000,
    }),
  })
  return body.project
}

export async function updateAIProject(
  projectId: string,
  update: Partial<Pick<
    AIProject,
    'name' | 'description' | 'context_mode' | 'max_context_tokens'
  >>,
): Promise<AIProject> {
  const body = await aiFetchJSON<{ project: AIProject }>(
    `/api/ai/projects/${encodeURIComponent(projectId)}`,
    {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(update),
    },
  )
  return body.project
}

export async function deleteAIProject(projectId: string): Promise<void> {
  await aiFetchJSON(`/api/ai/projects/${encodeURIComponent(projectId)}`, {
    method: 'DELETE',
  })
}

export async function linkProjectSession(projectId: string, sessionId: string): Promise<void> {
  await aiFetchJSON(`/api/ai/projects/${encodeURIComponent(projectId)}/sessions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ session_id: sessionId }),
  })
}

export async function unlinkProjectSession(
  projectId: string,
  sessionId: string,
): Promise<void> {
  await aiFetchJSON(
    `/api/ai/projects/${encodeURIComponent(projectId)}/sessions/${encodeURIComponent(sessionId)}`,
    { method: 'DELETE' },
  )
}

export async function listKnowledgeSources(projectId: string): Promise<KnowledgeSource[]> {
  const body = await aiFetchJSON<{ sources: KnowledgeSource[] }>(
    `/api/ai/projects/${encodeURIComponent(projectId)}/sources`,
  )
  return body.sources
}

export async function addProjectMemory(
  projectId: string, name: string, content: string,
): Promise<KnowledgeSource> {
  const body = await aiFetchJSON<{ source: KnowledgeSource }>(
    `/api/ai/projects/${encodeURIComponent(projectId)}/sources`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, content }),
    },
  )
  return body.source
}

export async function updateProjectMemory(
  projectId: string,
  sourceId: string,
  update: { name?: string; content?: string },
): Promise<KnowledgeSource> {
  const body = await aiFetchJSON<{ source: KnowledgeSource }>(
    `/api/ai/projects/${encodeURIComponent(projectId)}/sources/${encodeURIComponent(sourceId)}`,
    {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(update),
    },
  )
  return body.source
}

export type OCRLanguage = 'eng' | 'chi_sim' | 'jpn' | 'kor'

export async function uploadKnowledgeFile(
  projectId: string,
  file: File,
  ocrLanguages?: OCRLanguage[],
  sessionId?: string,
): Promise<KnowledgeSource> {
  const form = new FormData()
  form.append('file', file)
  for (const language of ocrLanguages ?? []) form.append('ocr_language', language)
  if (sessionId?.trim()) form.append('session_id', sessionId.trim())
  const body = await aiFetchJSON<{ source: KnowledgeSource }>(
    `/api/ai/projects/${encodeURIComponent(projectId)}/sources`,
    { method: 'POST', body: form },
    120_000,
  )
  return body.source
}

export async function retryKnowledgeSource(
  projectId: string,
  sourceId: string,
): Promise<KnowledgeSource> {
  const body = await aiFetchJSON<{ source: KnowledgeSource }>(
    `/api/ai/projects/${encodeURIComponent(projectId)}/sources/${encodeURIComponent(sourceId)}/retry`,
    { method: 'POST' },
  )
  return body.source
}

export async function deleteKnowledgeSource(
  projectId: string, sourceId: string,
): Promise<void> {
  await aiFetchJSON(
    `/api/ai/projects/${encodeURIComponent(projectId)}/sources/${encodeURIComponent(sourceId)}`,
    { method: 'DELETE' },
  )
}

export interface AIIndexJob {
  id: string
  target_type: 'project' | 'session'
  target_id: string
  model: string
  dimensions: number
  status: AIIndexJobStatus
  chunk_count: number
  processed_chunks: number
  estimated_tokens: number
  actual_tokens?: number
  estimated_dp: number
  content_digest?: string
  client_request_id?: string
  error_message?: string
  lease_expires_at?: string
  attempt_count: number
  max_attempts: number
  cancel_requested_at?: string
  started_at?: string
  finished_at?: string
  created_at: string
  updated_at: string
}

export type AIIndexTarget =
  | { targetType: 'project'; targetId: string; projectId: string; sessionId?: string }
  | { targetType: 'session'; targetId: string; sessionId: string; projectId?: undefined }

export interface AIIndexPreview {
  target_type: 'project' | 'session'
  target_id: string
  model: string
  dimensions: number
  source_count: number
  chunk_count: number
  indexed_chunks: number
  pending_chunks: number
  estimated_tokens: number
  estimated_dp: number
  content_digest?: string
  confirmation_token?: string
  index_status: AIIndexStatus
  current_model?: string
  requires_indexing: boolean
  active_job?: AIIndexJob
}

export async function previewAIIndex(
  target: AIIndexTarget,
): Promise<AIIndexPreview> {
  return aiFetchJSON('/api/ai/index/preview', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      target_type: target.targetType,
      target_id: target.targetId,
      project_id: target.projectId,
      session_id: target.sessionId,
    }),
  })
}

export async function createAIIndexJob(request: AIIndexTarget & {
  clientRequestId: string
  confirmationToken: string
}): Promise<AIIndexJob> {
  const body = await aiFetchJSON<{ job: AIIndexJob }>('/api/ai/index/jobs', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      target_type: request.targetType,
      target_id: request.targetId,
      project_id: request.projectId,
      session_id: request.sessionId,
      client_request_id: request.clientRequestId,
      confirmation_token: request.confirmationToken,
      confirmed: true,
    }),
  })
  return body.job
}

export async function getAIIndexJob(jobId: string): Promise<AIIndexJob> {
  const body = await aiFetchJSON<{ job: AIIndexJob }>(
    `/api/ai/index/jobs/${encodeURIComponent(jobId)}`,
  )
  return body.job
}

export async function retryAIIndexJob(jobId: string): Promise<AIIndexJob> {
  const body = await aiFetchJSON<{ job: AIIndexJob }>(
    `/api/ai/index/jobs/${encodeURIComponent(jobId)}/retry`,
    { method: 'POST' },
  )
  return body.job
}

export async function cancelAIIndexJob(jobId: string): Promise<AIIndexJob> {
  const body = await aiFetchJSON<{ job: AIIndexJob }>(
    `/api/ai/index/jobs/${encodeURIComponent(jobId)}`,
    { method: 'DELETE' },
  )
  return body.job
}

// Ingest transcript for RAG vector memory
export async function ingestRag(
  sessionId: string,
  speaker: string,
  text: string,
  startTime: number,
  endTime: number,
  timeoutMs = 15_000,
): Promise<void> {
  const base = isProduction ? '' : BACKEND_URL
  const controller = new AbortController()
  const timeout = globalThis.setTimeout(() => controller.abort(), timeoutMs)
  try {
    const authHeaders = await getOptionalAuthHeaders()
    const res = await fetch(`${base}/api/rag/ingest`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...authHeaders },
      body: JSON.stringify({
        session_id: sessionId,
        speaker,
        text,
        start_time: startTime,
        end_time: endTime,
      }),
      signal: controller.signal,
    })
    if (!res.ok) {
      throw new Error(await res.text())
    }
  } finally {
    globalThis.clearTimeout(timeout)
  }
}

// Reset server-side API metrics (overall counters/logs). Useful to start a fresh session view.
export async function resetMetrics(): Promise<void> {
  const base = isProduction ? '' : BACKEND_URL
  try {
    const authHeaders = await getOptionalAuthHeaders()
    await fetch(`${base}/api/metrics/reset`, { method: 'POST', headers: authHeaders })
  } catch {
    // ignore best-effort errors
  }
}

// Dictionary APIs
// Dictionary API removed: we'll integrate with cloud API directly outside this project when needed

// User balance API
export interface UserBalance {
  user_id: string
  email: string
  name: string
  dreampoints: number
  dreampoints_used: number
}

export async function getUserBalance(): Promise<UserBalance> {
  return authFetch<UserBalance>('/api/user/balance')
}

export interface UserBillingSummary {
  dreampoints: number
  dreampoints_used: number
  estimated_realtime_hours: number
  realtime_rate_dp_per_hour: number
  estimate_profile: string
}

export async function getUserBillingSummary(): Promise<UserBillingSummary> {
  return authFetch<UserBillingSummary>('/api/user/billing/summary')
}

export interface UserUsageItem {
  id: string
  session_id?: string
  action: string
  model: string
  quantity: number
  input_tokens: number
  cached_input_tokens: number
  cache_write_tokens: number
  output_tokens: number
  cost_dp: number
  created_at: string
}

export async function getUserUsage(sessionId?: string): Promise<UserUsageItem[]> {
  const suffix = sessionId ? `?session_id=${encodeURIComponent(sessionId)}` : ''
  const result = await authFetch<{ usage: UserUsageItem[] }>(`/api/user/billing/usage${suffix}`)
  return result.usage || []
}

export type ModelPurpose = 'translation' | 'summary' | 'chat' | 'embedding'

export interface AvailableModel {
  model_id: string
  purpose: ModelPurpose
  is_default: boolean
}

export interface UserModelPreferences {
  translation_model: string
  summary_model: string
  chat_model: string
}

export async function getAvailableModels(): Promise<AvailableModel[]> {
  const result = await authFetch<{ models: AvailableModel[] }>('/api/models/available')
  return result.models || []
}

export async function getUserModelPreferences(): Promise<UserModelPreferences> {
  return authFetch<UserModelPreferences>('/api/user/model-preferences')
}

export async function saveUserModelPreferences(
  preferences: UserModelPreferences,
): Promise<UserModelPreferences> {
  return authFetch<UserModelPreferences>('/api/user/model-preferences', {
    method: 'PUT',
    body: JSON.stringify(preferences),
  })
}
