// Authentication API wrapper for DreamTrans Pro
import { clearUserApiKey } from '../../utils/userApiKey'

const BACKEND_URL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080'
const isProduction = BACKEND_URL === '/'
const baseUrl = isProduction ? '' : BACKEND_URL

// Token storage keys
const ACCESS_TOKEN_KEY = 'dt_access_token'
const REFRESH_TOKEN_KEY = 'dt_refresh_token'
const USER_KEY = 'dt_user'

const authMemoryStorage = new Map<string, string>()
let authStorageUnavailable = false
let authGeneration = 0

interface RefreshFlight {
  generation: number
  refreshToken: string
  promise: Promise<boolean>
}

let refreshInFlight: RefreshFlight | null = null

function getAuthStorage(): Storage | null {
  if (authStorageUnavailable) return null
  try {
    if (typeof localStorage === 'undefined') {
      authStorageUnavailable = true
      return null
    }
    return localStorage
  } catch {
    authStorageUnavailable = true
    return null
  }
}

function readAuthStorage(key: string): string | null {
  const storage = getAuthStorage()
  if (!storage) return authMemoryStorage.get(key) ?? null
  try {
    const value = storage.getItem(key)
    if (value === null) authMemoryStorage.delete(key)
    else authMemoryStorage.set(key, value)
    return value
  } catch {
    authStorageUnavailable = true
    return authMemoryStorage.get(key) ?? null
  }
}

function writeAuthStorage(key: string, value: string): void {
  authMemoryStorage.set(key, value)
  const storage = getAuthStorage()
  if (!storage) return
  try {
    storage.setItem(key, value)
  } catch {
    // Keep all subsequent auth reads and writes on the coherent in-memory
    // mirror instead of mixing it with a partially writable storage backend.
    authStorageUnavailable = true
  }
}

function removeAuthStorage(key: string): void {
  authMemoryStorage.delete(key)
  const storage = getAuthStorage()
  if (!storage) return
  try {
    storage.removeItem(key)
  } catch {
    authStorageUnavailable = true
  }
}

function advanceAuthGeneration(): number {
  authGeneration += 1
  // Detach the old flight without attempting to cancel its network request.
  // Its identity-checked finally block must not clear a newer flight.
  refreshInFlight = null
  return authGeneration
}

function isCurrentAuthGeneration(generation: number): boolean {
  return authGeneration === generation
}

// Types
export interface User {
  id: string
  tenant_id: string
  email: string
  name: string
  role: 'user' | 'admin' | 'super_admin'
  is_active: boolean
  email_verified: boolean
  last_login_at?: string
  created_at: string
  updated_at: string
}

export interface Tenant {
  id: string
  name: string
  slug: string
  plan: 'free' | 'pro' | 'enterprise'
  api_quota_monthly: number
  storage_quota_gb: number
  max_sessions: number
  created_at: string
  updated_at: string
}

export interface AuthResponse {
  user: User
  access_token: string
  refresh_token: string
  expires_in: number
}

export interface Session {
  id: string
  user_id: string
  tenant_id: string
  title: string
  source_language: string
  target_language: string
  duration_seconds: number
  status: 'active' | 'paused' | 'completed' | 'archived'
  started_at: string
  ended_at?: string
  created_at: string
  updated_at: string
}

export interface Transcript {
  id: string
  session_id: string
  client_segment_id: string
  speaker: string
  text: string
  translation?: string
  start_time: number
  end_time?: number
  status: 'partial' | 'confirmed' | 'translated'
  is_partial: boolean
  created_at: string
  updated_at: string
}

export interface SessionWithTranscripts extends Session {
  transcripts: Transcript[]
}

export interface ApiError {
  error: string
}

export interface TranscriptInput {
  client_segment_id: string
  speaker?: string
  text: string
  translation?: string
  start_time: number
  end_time?: number
  status?: 'partial' | 'confirmed' | 'translated'
  is_partial?: boolean
}

// Token management
export function getAccessToken(): string | null {
  return readAuthStorage(ACCESS_TOKEN_KEY)
}

export function getRefreshToken(): string | null {
  return readAuthStorage(REFRESH_TOKEN_KEY)
}

function storeTokens(accessToken: string, refreshToken: string): void {
  writeAuthStorage(ACCESS_TOKEN_KEY, accessToken)
  writeAuthStorage(REFRESH_TOKEN_KEY, refreshToken)
}

export function setTokens(accessToken: string, refreshToken: string): void {
  advanceAuthGeneration()
  storeTokens(accessToken, refreshToken)
}

export function clearTokens(): void {
  advanceAuthGeneration()
  removeAuthStorage(ACCESS_TOKEN_KEY)
  removeAuthStorage(REFRESH_TOKEN_KEY)
  removeAuthStorage(USER_KEY)
  clearUserApiKey()
  if (typeof window !== 'undefined') {
    try {
      window.dispatchEvent(new CustomEvent('dt-auth-cleared'))
    } catch {
      // Auth invalidation must still succeed in non-browser/test runtimes.
    }
  }
}

export function getStoredUser(): User | null {
  const raw = readAuthStorage(USER_KEY)
  if (!raw) return null
  try {
    return JSON.parse(raw) as User
  } catch {
    removeAuthStorage(USER_KEY)
    return null
  }
}

export function setStoredUser(user: User): void {
  writeAuthStorage(USER_KEY, JSON.stringify(user))
}

function commitAuthResponse(generation: number, data: AuthResponse): boolean {
  if (!isCurrentAuthGeneration(generation)) return false
  // A second edge at commit time invalidates refreshes that began while the
  // login/register request itself was pending.
  advanceAuthGeneration()
  storeTokens(data.access_token, data.refresh_token)
  setStoredUser(data.user)
  return true
}

function clearTokensIfCurrent(generation: number): boolean {
  if (!isCurrentAuthGeneration(generation)) return false
  clearTokens()
  return true
}

function tokenExpiresWithin(token: string, seconds: number): boolean {
  try {
    const encoded = token.split('.')[1]
    if (!encoded) return true
    const normalized = encoded.replace(/-/g, '+').replace(/_/g, '/')
    const padded = normalized.padEnd(Math.ceil(normalized.length / 4) * 4, '=')
    const payload = JSON.parse(atob(padded)) as { exp?: number }
    return typeof payload.exp !== 'number' || payload.exp <= Math.floor(Date.now() / 1000) + seconds
  } catch {
    return true
  }
}

function authStateChangedError(): Error {
  return new Error('Authentication state changed')
}

/**
 * Return an access token that will remain valid for at least the requested
 * window. Refresh-token rotation is protected by a module-wide single flight,
 * so reconnecting sockets and API requests cannot invalidate each other.
 */
export async function ensureValidAccessToken(minValiditySeconds = 60): Promise<string> {
  const generation = authGeneration
  const token = getAccessToken()
  if (!token) throw new Error('Not authenticated')
  if (!tokenExpiresWithin(token, minValiditySeconds)) return token

  const refreshedSuccessfully = await refreshAccessToken()
  if (!isCurrentAuthGeneration(generation)) {
    throw authStateChangedError()
  }
  if (!refreshedSuccessfully) {
    clearTokensIfCurrent(generation)
    throw new Error('Session expired')
  }

  const refreshed = getAccessToken()
  if (!refreshed) {
    clearTokensIfCurrent(generation)
    throw new Error('Session expired')
  }
  return refreshed
}

// Fetch wrapper with auth
async function authFetch<T>(
  endpoint: string,
  options: RequestInit = {},
  acceptedStatuses: readonly number[] = [],
): Promise<T> {
  const generation = authGeneration
  const token = getAccessToken()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }

  // Merge existing headers
  if (options.headers) {
    const existingHeaders = options.headers as Record<string, string>
    Object.assign(headers, existingHeaders)
  }

  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  const response = await fetch(`${baseUrl}${endpoint}`, {
    ...options,
    headers,
  })
  if (!isCurrentAuthGeneration(generation)) {
    throw authStateChangedError()
  }

  // Handle token expiration
  if (response.status === 401 && token) {
    // Another request may already have rotated the refresh token while this
    // request was in flight. In that case retry with the newer access token
    // instead of issuing a second refresh.
    const currentToken = getAccessToken()
    const refreshed = (!!currentToken && token !== currentToken) || await refreshAccessToken()
    if (!isCurrentAuthGeneration(generation)) {
      throw authStateChangedError()
    }
    if (refreshed) {
      // Retry with new token
      const retryToken = getAccessToken()
      if (!retryToken) {
        clearTokensIfCurrent(generation)
        throw new Error('Session expired')
      }
      headers['Authorization'] = `Bearer ${retryToken}`
      const retryResponse = await fetch(`${baseUrl}${endpoint}`, {
        ...options,
        headers,
      })
      if (!isCurrentAuthGeneration(generation)) {
        throw authStateChangedError()
      }
      if (!retryResponse.ok && !acceptedStatuses.includes(retryResponse.status)) {
        const error = await retryResponse.json().catch(() => ({ error: 'Request failed' }))
        throw new Error(error.error || 'Request failed')
      }
      return retryResponse.json()
    } else {
      clearTokensIfCurrent(generation)
      throw new Error('Session expired')
    }
  }

  if (!response.ok && !acceptedStatuses.includes(response.status)) {
    const error = await response.json().catch(() => ({ error: 'Request failed' }))
    throw new Error(error.error || 'Request failed')
  }

  return response.json()
}

// Token refresh
function newerCredentialsAreUsable(generation: number, refreshToken: string): boolean {
  return (
    isCurrentAuthGeneration(generation) &&
    getRefreshToken() !== refreshToken &&
    !!getAccessToken()
  )
}

async function performTokenRefresh(generation: number, refreshToken: string): Promise<boolean> {
  try {
    const response = await fetch(`${baseUrl}/api/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })

    if (!response.ok) {
      return newerCredentialsAreUsable(generation, refreshToken)
    }

    const data: AuthResponse = await response.json()
    if (!isCurrentAuthGeneration(generation) || getRefreshToken() !== refreshToken) {
      return newerCredentialsAreUsable(generation, refreshToken)
    }
    storeTokens(data.access_token, data.refresh_token)
    setStoredUser(data.user)
    return true
  } catch {
    return newerCredentialsAreUsable(generation, refreshToken)
  }
}

export function refreshAccessToken(): Promise<boolean> {
  const generation = authGeneration
  const refreshToken = getRefreshToken()
  if (!refreshToken) return Promise.resolve(false)
  if (
    refreshInFlight &&
    refreshInFlight.generation === generation &&
    refreshInFlight.refreshToken === refreshToken
  ) {
    return refreshInFlight.promise
  }

  const flight: RefreshFlight = {
    generation,
    refreshToken,
    promise: Promise.resolve(false),
  }
  flight.promise = performTokenRefresh(generation, refreshToken).finally(() => {
    if (refreshInFlight === flight) {
      refreshInFlight = null
    }
  })
  refreshInFlight = flight
  return flight.promise
}

// Auth API
export async function register(
  email: string,
  password: string,
  name: string,
  inviteCode?: string,
): Promise<AuthResponse> {
  const generation = advanceAuthGeneration()
  const normalizedInviteCode = inviteCode?.trim()
  const response = await fetch(`${baseUrl}/api/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      email,
      password,
      name,
      ...(normalizedInviteCode ? { invite_code: normalizedInviteCode } : {}),
    }),
  })

  if (!response.ok) {
    const error = await response.json().catch(() => ({ error: 'Registration failed' }))
    throw new Error(error.error || 'Registration failed')
  }

  const data: AuthResponse = await response.json()
  if (!commitAuthResponse(generation, data)) {
    throw authStateChangedError()
  }
  return data
}

export async function login(email: string, password: string): Promise<AuthResponse> {
  const generation = advanceAuthGeneration()
  const response = await fetch(`${baseUrl}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password }),
  })

  if (!response.ok) {
    const error = await response.json().catch(() => ({ error: 'Login failed' }))
    throw new Error(error.error || 'Login failed')
  }

  const data: AuthResponse = await response.json()
  if (!commitAuthResponse(generation, data)) {
    throw authStateChangedError()
  }
  return data
}

export async function logout(): Promise<void> {
  const accessToken = getAccessToken()
  const refreshToken = getRefreshToken()
  // Invalidate local credentials and every older async auth writer before
  // waiting on a best-effort server-side revocation.
  clearTokens()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }
  if (accessToken) {
    headers.Authorization = `Bearer ${accessToken}`
  }
  const controller = new AbortController()
  const timeout = globalThis.setTimeout(() => controller.abort(), 5_000)
  try {
    await fetch(`${baseUrl}/api/auth/logout`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ refresh_token: refreshToken }),
      signal: controller.signal,
    })
  } catch {
    // Ignore logout errors
  } finally {
    globalThis.clearTimeout(timeout)
  }
}

export async function getProfile(): Promise<{ user: User; tenant?: Tenant }> {
  return authFetch('/api/user/profile')
}

export async function updateProfile(name: string): Promise<User> {
  return authFetch('/api/user/profile', {
    method: 'PUT',
    body: JSON.stringify({ name }),
  })
}

export async function updatePassword(
  currentPassword: string,
  newPassword: string
): Promise<void> {
  await authFetch('/api/user/password', {
    method: 'PUT',
    body: JSON.stringify({
      current_password: currentPassword,
      new_password: newPassword,
    }),
  })
}

// Session API
export async function listSessions(
  page = 1,
  pageSize = 20
): Promise<{ sessions: Session[]; page: number; page_size: number }> {
  return authFetch(`/api/sessions?page=${page}&page_size=${pageSize}`)
}

export async function createSession(data?: {
  title?: string
  source_language?: string
  target_language?: string
}): Promise<Session> {
  return authFetch('/api/sessions', {
    method: 'POST',
    body: JSON.stringify(data || {}),
  })
}

export async function getSession(id: string): Promise<SessionWithTranscripts> {
  return authFetch(`/api/sessions/${id}`)
}

export async function updateSession(
  id: string,
  data: {
    title?: string
    status?: 'active' | 'paused' | 'completed' | 'archived'
    duration_seconds?: number
  },
  signal?: AbortSignal,
): Promise<Session> {
  return authFetch(`/api/sessions/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
    signal,
  })
}

export async function deleteSession(id: string): Promise<void> {
  // DELETE is idempotent from the client's perspective. If a previous attempt
  // reached the server but local cleanup failed, retrying must still be able to
  // remove the IndexedDB cache and durable outbox.
  await authFetch(`/api/sessions/${id}`, { method: 'DELETE' }, [404])
}

export async function saveTranscript(
  sessionId: string,
  transcript: TranscriptInput,
): Promise<Transcript> {
  return authFetch(`/api/sessions/${sessionId}/transcripts`, {
    method: 'POST',
    body: JSON.stringify(transcript),
  })
}

export async function saveTranscriptsBatch(
  sessionId: string,
  transcripts: TranscriptInput[],
  signal?: AbortSignal,
): Promise<{ saved: Transcript[]; count: number }> {
  return authFetch(`/api/sessions/${sessionId}/transcripts/batch`, {
    method: 'POST',
    body: JSON.stringify(transcripts),
    signal,
  })
}

export async function exportSession(
  id: string,
  format: 'json' | 'txt' | 'srt' = 'json'
): Promise<Blob> {
  let token = await ensureValidAccessToken()
  const generation = authGeneration
  const request = () => fetch(`${baseUrl}/api/sessions/${id}/export?format=${format}`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  let response = await request()
  if (!isCurrentAuthGeneration(generation)) {
    throw authStateChangedError()
  }

  if (response.status === 401) {
    const currentToken = getAccessToken()
    if (currentToken && currentToken !== token) {
      token = currentToken
    } else {
      const refreshed = await refreshAccessToken()
      if (!isCurrentAuthGeneration(generation)) {
        throw authStateChangedError()
      }
      if (!refreshed) {
        clearTokensIfCurrent(generation)
        throw new Error('Session expired')
      }
      token = getAccessToken() || ''
    }
    if (!token) {
      clearTokensIfCurrent(generation)
      throw new Error('Session expired')
    }
    response = await request()
    if (!isCurrentAuthGeneration(generation)) {
      throw authStateChangedError()
    }
  }

  if (!response.ok) {
    const message = await response.text().catch(() => '')
    throw new Error(message || 'Export failed')
  }

  return response.blob()
}

// Check if user is authenticated
export function isAuthenticated(): boolean {
  return !!getAccessToken() && !!getStoredUser()
}

// Initialize - check and refresh token if needed
export async function initAuth(): Promise<User | null> {
  const generation = authGeneration
  const user = getStoredUser()
  if (!user) {
    if (getAccessToken() || getRefreshToken()) {
      clearTokensIfCurrent(generation)
    }
    return null
  }

  const token = getAccessToken()
  if (!token) {
    clearTokensIfCurrent(generation)
    return null
  }

  // Try to validate token by fetching profile
  try {
    const { user: freshUser } = await getProfile()
    if (!isCurrentAuthGeneration(generation)) return null
    setStoredUser(freshUser)
    return freshUser
  } catch {
    if (!isCurrentAuthGeneration(generation)) return null
    // Token invalid, try refresh
    const refreshed = await refreshAccessToken()
    if (!isCurrentAuthGeneration(generation)) return null
    if (refreshed) {
      return getStoredUser()
    }
    clearTokensIfCurrent(generation)
    return null
  }
}

// ==================== Admin API ====================

// List all users (admin only)
export async function adminListUsers(): Promise<{ users: User[] }> {
  return authFetch('/api/admin/users')
}

// Update user (admin only)
export async function adminUpdateUser(
  userId: string,
  updates: { is_active?: boolean; role?: string }
): Promise<User> {
  return authFetch(`/api/admin/users/${userId}`, {
    method: 'PUT',
    body: JSON.stringify(updates),
  })
}

// Delete user (admin only)
export async function adminDeleteUser(userId: string): Promise<void> {
  await authFetch(`/api/admin/users/${userId}`, { method: 'DELETE' })
}
