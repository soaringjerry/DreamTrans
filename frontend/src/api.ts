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
  usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number; model?: string }
  latency_ms?: number
}

export async function askRag(sessionId: string, query: string, topK: number = 5, config?: RagConfig, timeoutMs?: number): Promise<RagAskResponse> {
  const base = isProduction ? '' : BACKEND_URL
  const controller = new AbortController()
  const t = timeoutMs && timeoutMs > 0 ? window.setTimeout(() => controller.abort(), timeoutMs) : undefined
  try {
    const authHeaders = await getOptionalAuthHeaders()
    const res = await fetch(`${base}/api/rag/ask`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', ...authHeaders },
      body: JSON.stringify({ session_id: sessionId, query, top_k: topK, config }),
      signal: controller.signal,
    })
    if (!res.ok) throw new Error(await res.text())
    return await res.json()
  } finally {
    if (t) window.clearTimeout(t)
  }
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
