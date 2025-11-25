// Authentication API wrapper for DreamTrans Pro
const BACKEND_URL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080'
const isProduction = BACKEND_URL === '/'
const baseUrl = isProduction ? '' : BACKEND_URL

// Token storage keys
const ACCESS_TOKEN_KEY = 'dt_access_token'
const REFRESH_TOKEN_KEY = 'dt_refresh_token'
const USER_KEY = 'dt_user'

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

// Token management
export function getAccessToken(): string | null {
  return localStorage.getItem(ACCESS_TOKEN_KEY)
}

export function getRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_TOKEN_KEY)
}

export function setTokens(accessToken: string, refreshToken: string): void {
  localStorage.setItem(ACCESS_TOKEN_KEY, accessToken)
  localStorage.setItem(REFRESH_TOKEN_KEY, refreshToken)
}

export function clearTokens(): void {
  localStorage.removeItem(ACCESS_TOKEN_KEY)
  localStorage.removeItem(REFRESH_TOKEN_KEY)
  localStorage.removeItem(USER_KEY)
}

export function getStoredUser(): User | null {
  try {
    const raw = localStorage.getItem(USER_KEY)
    return raw ? JSON.parse(raw) : null
  } catch {
    return null
  }
}

export function setStoredUser(user: User): void {
  localStorage.setItem(USER_KEY, JSON.stringify(user))
}

// Fetch wrapper with auth
async function authFetch<T>(
  endpoint: string,
  options: RequestInit = {}
): Promise<T> {
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

  // Handle token expiration
  if (response.status === 401 && token) {
    const refreshed = await tryRefreshToken()
    if (refreshed) {
      // Retry with new token
      headers['Authorization'] = `Bearer ${getAccessToken()}`
      const retryResponse = await fetch(`${baseUrl}${endpoint}`, {
        ...options,
        headers,
      })
      if (!retryResponse.ok) {
        const error = await retryResponse.json().catch(() => ({ error: 'Request failed' }))
        throw new Error(error.error || 'Request failed')
      }
      return retryResponse.json()
    } else {
      clearTokens()
      throw new Error('Session expired')
    }
  }

  if (!response.ok) {
    const error = await response.json().catch(() => ({ error: 'Request failed' }))
    throw new Error(error.error || 'Request failed')
  }

  return response.json()
}

// Token refresh
async function tryRefreshToken(): Promise<boolean> {
  const refreshToken = getRefreshToken()
  if (!refreshToken) return false

  try {
    const response = await fetch(`${baseUrl}/api/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })

    if (!response.ok) return false

    const data: AuthResponse = await response.json()
    setTokens(data.access_token, data.refresh_token)
    setStoredUser(data.user)
    return true
  } catch {
    return false
  }
}

// Auth API
export async function register(
  email: string,
  password: string,
  name: string
): Promise<AuthResponse> {
  const response = await fetch(`${baseUrl}/api/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password, name }),
  })

  if (!response.ok) {
    const error = await response.json().catch(() => ({ error: 'Registration failed' }))
    throw new Error(error.error || 'Registration failed')
  }

  const data: AuthResponse = await response.json()
  setTokens(data.access_token, data.refresh_token)
  setStoredUser(data.user)
  return data
}

export async function login(email: string, password: string): Promise<AuthResponse> {
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
  setTokens(data.access_token, data.refresh_token)
  setStoredUser(data.user)
  return data
}

export async function logout(): Promise<void> {
  const refreshToken = getRefreshToken()
  try {
    await fetch(`${baseUrl}/api/auth/logout`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${getAccessToken()}`,
      },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
  } catch {
    // Ignore logout errors
  }
  clearTokens()
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
  }
): Promise<Session> {
  return authFetch(`/api/sessions/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export async function deleteSession(id: string): Promise<void> {
  await authFetch(`/api/sessions/${id}`, { method: 'DELETE' })
}

export async function saveTranscript(
  sessionId: string,
  transcript: {
    speaker?: string
    text: string
    translation?: string
    start_time: number
    end_time?: number
    status?: 'partial' | 'confirmed' | 'translated'
    is_partial?: boolean
  }
): Promise<Transcript> {
  return authFetch(`/api/sessions/${sessionId}/transcripts`, {
    method: 'POST',
    body: JSON.stringify(transcript),
  })
}

export async function saveTranscriptsBatch(
  sessionId: string,
  transcripts: Array<{
    speaker?: string
    text: string
    translation?: string
    start_time: number
    end_time?: number
    status?: 'partial' | 'confirmed' | 'translated'
    is_partial?: boolean
  }>
): Promise<{ saved: Transcript[]; count: number }> {
  return authFetch(`/api/sessions/${sessionId}/transcripts/batch`, {
    method: 'POST',
    body: JSON.stringify(transcripts),
  })
}

export async function exportSession(
  id: string,
  format: 'json' | 'txt' | 'srt' = 'json'
): Promise<Blob> {
  const token = getAccessToken()
  const response = await fetch(`${baseUrl}/api/sessions/${id}/export?format=${format}`, {
    headers: token ? { Authorization: `Bearer ${token}` } : {},
  })

  if (!response.ok) {
    throw new Error('Export failed')
  }

  return response.blob()
}

// Check if user is authenticated
export function isAuthenticated(): boolean {
  return !!getAccessToken() && !!getStoredUser()
}

// Initialize - check and refresh token if needed
export async function initAuth(): Promise<User | null> {
  const user = getStoredUser()
  if (!user) return null

  const token = getAccessToken()
  if (!token) {
    clearTokens()
    return null
  }

  // Try to validate token by fetching profile
  try {
    const { user: freshUser } = await getProfile()
    setStoredUser(freshUser)
    return freshUser
  } catch {
    // Token invalid, try refresh
    const refreshed = await tryRefreshToken()
    if (refreshed) {
      return getStoredUser()
    }
    clearTokens()
    return null
  }
}
