// Admin API wrapper
import { ensureValidAccessToken } from '../pro/api/auth'

const BACKEND_URL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080'
const isProduction = BACKEND_URL === '/'
const baseUrl = isProduction ? '' : BACKEND_URL

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

export interface UserListResponse {
  users: User[]
  total: number
  page: number
  page_size: number
}

export interface TenantListResponse {
  tenants: Tenant[]
  total: number
  page: number
  page_size: number
}

export interface GlobalStats {
  user_count: number
  tenant_count: number
  session_count: number
  transcript_count: number
  current_month: string
}

export interface UsageSummary {
  tenant_id: string
  month_key: string
  transcription_minutes: number
  translation_count: number
  rag_query_count: number
  storage_mb: number
  api_request_count: number
  api_quota_monthly: number
  limits: {
    transcription_minutes: number
    rag_queries: number
    storage_gb: number
    max_sessions: number
  }
  plan: string
}

// Fetch wrapper
async function adminFetch<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const token = await ensureValidAccessToken()

  const response = await fetch(`${baseUrl}${endpoint}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      Authorization: `Bearer ${token}`,
      ...(options.headers || {}),
    },
  })

  if (!response.ok) {
    const error = await response.json().catch(() => ({ error: 'Request failed' }))
    throw new Error(error.error || 'Request failed')
  }

  return response.json()
}

// User APIs
export async function listUsers(page = 1, pageSize = 20): Promise<UserListResponse> {
  return adminFetch(`/api/admin/users?page=${page}&page_size=${pageSize}`)
}

export async function getUser(id: string): Promise<{ user: User; tenant?: Tenant }> {
  return adminFetch(`/api/admin/users/${id}`)
}

export async function updateUser(
  id: string,
  data: { name?: string; role?: string; is_active?: boolean }
): Promise<User> {
  return adminFetch(`/api/admin/users/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export async function deleteUser(id: string): Promise<void> {
  await adminFetch(`/api/admin/users/${id}`, { method: 'DELETE' })
}

// Tenant APIs
export async function listTenants(page = 1, pageSize = 20): Promise<TenantListResponse> {
  return adminFetch(`/api/admin/tenants?page=${page}&page_size=${pageSize}`)
}

export async function updateTenant(
  id: string,
  data: {
    name?: string
    plan?: string
    api_quota_monthly?: number
    storage_quota_gb?: number
    max_sessions?: number
  }
): Promise<Tenant> {
  return adminFetch(`/api/admin/tenants/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

// Stats APIs
export async function getGlobalStats(): Promise<GlobalStats> {
  return adminFetch('/api/admin/stats')
}

export async function getUsage(tenantId?: string, month?: string): Promise<UsageSummary> {
  const params = new URLSearchParams()
  if (tenantId) params.set('tenant_id', tenantId)
  if (month) params.set('month', month)
  return adminFetch(`/api/admin/usage?${params}`)
}
