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
export async function adminFetch<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
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

export interface MarkupOverride {
  scope_type: 'provider' | 'category' | 'sku'
  scope_key: string
  markup_percent: number
}

export interface CostRate {
  id?: string
  provider: string
  sku: string
  service: string
  unit_type: string
  cost_per_unit_usd: number
  retail_dp_per_unit: number
  markup_percent: number
  gross_margin_percent: number
  catalog_version: string
  source_url: string
  is_builtin: boolean
  is_active: boolean
  override_source: string
}

export interface BillingConfig {
  dp_per_usd: number
  default_markup_percent: number
  catalog_version: string
  overrides: MarkupOverride[]
}

export interface BillingCatalog {
  builtin_version: string
  installed_version: string
  has_update: boolean
  config: BillingConfig
  rates: CostRate[]
}

export interface BillingPreview {
  config: BillingConfig
  rates: CostRate[]
  added: number
  updated: number
  disabled: number
  confirmation: string
}

export interface BillingAnalytics {
  upstream_cost_usd: number
  service_fee_dp: number
  retail_dp: number
  usage_count: number
}

export interface ModelPolicy {
  purpose: 'translation' | 'summary' | 'chat' | 'embedding'
  model_id: string
  is_approved: boolean
  is_default: boolean
  cost_confirmed: boolean
}

export interface ProviderModel {
  provider: string
  model_id: string
  source: string
  provider_available: boolean
  first_seen_at: string
  last_seen_at: string
  policies: ModelPolicy[]
}

export interface ModelCatalog {
  provider: string
  models: ProviderModel[]
  last_success_at?: string
  last_attempt_at?: string
  last_error?: string
  refresh_minutes: number
}

export async function getBillingCatalog(): Promise<BillingCatalog> {
  return adminFetch('/api/admin/billing/catalog')
}

export async function updateBillingConfig(config: {
  dp_per_usd: number
  default_markup_percent: number
  overrides: MarkupOverride[]
}): Promise<BillingCatalog> {
  return adminFetch('/api/admin/billing/config', {
    method: 'PUT',
    body: JSON.stringify(config),
  })
}

export async function previewBillingConfig(config: {
  dp_per_usd: number
  default_markup_percent: number
  overrides: MarkupOverride[]
}): Promise<BillingPreview> {
  return adminFetch('/api/admin/billing/preview', {
    method: 'POST',
    body: JSON.stringify(config),
  })
}

export async function applyBillingCatalog(): Promise<BillingCatalog> {
  return adminFetch('/api/admin/billing/catalog/apply', { method: 'POST' })
}

export async function previewBillingReset(): Promise<BillingPreview> {
  return adminFetch('/api/admin/billing/reset/preview', { method: 'POST' })
}

export async function resetBillingDefaults(confirmation: string): Promise<BillingCatalog> {
  return adminFetch('/api/admin/billing/reset', {
    method: 'POST',
    body: JSON.stringify({ confirmation }),
  })
}

export async function getBillingAnalytics(): Promise<BillingAnalytics> {
  return adminFetch('/api/admin/billing/analytics')
}

export async function getModelCatalog(): Promise<ModelCatalog> {
  return adminFetch('/api/admin/models')
}

export async function refreshModelCatalog(): Promise<ModelCatalog> {
  return adminFetch('/api/admin/models/refresh', { method: 'POST' })
}

export async function updateModelPolicy(policy: ModelPolicy): Promise<ModelCatalog> {
  return adminFetch('/api/admin/models/policies', {
    method: 'PUT',
    body: JSON.stringify(policy),
  })
}

export async function updateModelCost(input: {
  model_id: string
  service: 'llm' | 'embedding'
  input_per_million_usd: number
  cached_input_per_million_usd: number
  cache_write_per_million_usd: number
  output_per_million_usd: number
}): Promise<void> {
  await adminFetch('/api/admin/billing/catalog/model-cost', {
    method: 'PUT',
    body: JSON.stringify(input),
  })
}
