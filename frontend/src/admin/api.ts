// Typed API wrapper for the React administration console.
import { ensureValidAccessToken } from '../pro/api/auth'

const BACKEND_URL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080'
const isProduction = BACKEND_URL === '/'
const baseUrl = isProduction ? '' : BACKEND_URL
const ADMIN_READ_RETRY_DELAYS_MS = [250, 1_000] as const
const TRANSIENT_GATEWAY_STATUSES = new Set([502, 503, 504])

export interface User {
  id: string
  tenant_id: string
  email: string
  name: string
  role: 'user' | 'admin' | 'super_admin'
  is_active: boolean
  email_verified: boolean
  dreampoints: number
  dreampoints_used?: number
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

export interface AdminBasicStats {
  user_count: number
  tenant_count: number
  session_count: number
  transcript_count: number
}

export interface AdminBillingStats {
  total_dreampoints: number
  total_used: number
  total_users: number
  active_users: number
  total_sessions: number
  total_transcripts: number
  usage_by_action: Record<string, number>
  usage_by_model: Record<string, number>
}

export interface AdminSystemStatsResponse {
  basic: AdminBasicStats
  billing: AdminBillingStats
  billing_error?: string
  time: string
}

// Kept as a source-compatible name for imports outside the admin page.
export type GlobalStats = AdminSystemStatsResponse

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

export interface MarkupOverride {
  scope_type: 'provider' | 'category' | 'sku'
  scope_key: string
  markup_percent: number
}

export type PricingState = 'legacy_active' | 'managed_current' | 'managed_outdated'

export interface CostRate {
  id?: string
  provider: string
  sku: string
  service: string
  unit_type: string
  // Legacy field; remains readable while the backend rolls out the split fields.
  cost_per_unit_usd: number
  public_cost_per_unit_usd?: number
  effective_cost_per_unit_usd?: number
  retail_dp_per_unit: number
  effective_retail_dp_per_unit?: number | null
  effective_retail_by_action?: Record<string, number>
  proposed_retail_dp_per_unit?: number
  markup_percent: number
  gross_margin_percent: number
  catalog_version: string
  source_url: string
  effective_at?: string
  public_effective_at?: string
  is_builtin: boolean
  is_active: boolean
  override_source: string
  cost_source?: 'public_catalog' | 'contract_override' | 'manual' | string
  cost_source_label?: string
  cost_override_id?: string
  // Transitional alias accepted from early builds of the reliability API.
  override_id?: string
}

export interface BillingConfig {
  dp_per_usd: number
  default_markup_percent: number
  catalog_version: string
  pricing_state?: string
  updated_at?: string
  overrides: MarkupOverride[]
}

export interface BillingCatalog {
  builtin_version: string
  installed_version: string
  has_update: boolean
  pricing_state?: PricingState
  // This is the configuration currently used by the charging path.
  config: BillingConfig
  // Legacy/outdated catalogs stage edits here until an apply is confirmed.
  pending_config?: BillingConfigInput | null
  rates: CostRate[]
}

export interface BillingConfigInput {
  dp_per_usd: number
  default_markup_percent: number
  overrides: MarkupOverride[]
}

export interface BillingPreview {
  config: BillingConfig
  rates: CostRate[]
  added: number
  updated: number
  disabled: number
  confirmation: string
  catalog_version?: string
  target_version?: string
  current_revision?: string
}

export interface BillingPreviewConfigComparison {
  field: 'dp_per_usd' | 'default_markup_percent' | 'catalog_version' | 'pricing_state'
  current: number | string
  target: number | string
  changed: boolean
}

export interface BillingPreviewRateChange {
  key: string
  kind: 'added' | 'disabled' | 'changed'
  provider: string
  service: string
  sku: string
  unit_type: string
  current_effective_cost_usd: number | null
  target_effective_cost_usd: number | null
  current_effective_retail_dp: number | null
  current_effective_retail_by_action: Record<string, number>
  target_proposed_retail_dp: number | null
  cost_changed: boolean
  retail_changed: boolean
}

export interface BillingPreviewDiff {
  config: BillingPreviewConfigComparison[]
  rates: BillingPreviewRateChange[]
  total_rate_changes: number
  hidden_rate_changes: number
}

export interface BillingAnalytics {
  upstream_cost_usd: number
  service_fee_dp: number
  retail_dp: number
  usage_count: number
  attributed_usage_count?: number
  legacy_unknown_count?: number
  legacy_unknown_retail_dp?: number
  byok_usage_count?: number
  byok_service_fee_dp?: number
  non_provider_usage_count?: number
  unpriced_usage_count?: number
  estimated_legacy_upstream_cost_usd?: number
  estimated_legacy_service_fee_dp?: number
  estimate_eligible_count?: number
  estimate_catalog_version?: string
  estimate_available?: boolean
  estimate_error?: string
}

export interface ModelPolicy {
  purpose: 'translation' | 'summary' | 'chat' | 'embedding'
  model_id: string
  is_approved: boolean
  is_default: boolean
  cost_confirmed: boolean
}

export type ProviderAvailability =
  | 'provider_confirmed'
  | 'builtin_unverified'
  | 'temporarily_unavailable'
  | 'unavailable'
  | 'confirmed'
  | 'unverified'
  | 'stale'

export interface ProviderModel {
  provider: string
  model_id: string
  source: string
  provider_available: boolean
  availability_status?: ProviderAvailability
  first_seen_at: string
  last_seen_at: string
  policies: ModelPolicy[]
}

export interface ModelCatalog {
  provider: string
  status?: string
  models: ProviderModel[]
  last_success_at?: string
  last_attempt_at?: string
  last_error?: string
  refresh_minutes: number
}

export interface SystemSettingsValues {
  billing_enabled: boolean
  allow_negative_balance: boolean
  allow_user_api_key: boolean
  free_tier_dreampoints: number
}

export interface SystemSettingsResponse {
  values: SystemSettingsValues
  defaults: SystemSettingsValues
}

export interface SystemSettingsResetPreview {
  current: SystemSettingsValues
  defaults: SystemSettingsValues
  changes: Array<{
    key: keyof SystemSettingsValues
    from: boolean | number
    to: boolean | number
  }>
}

export type SystemSettingsPatch = Partial<SystemSettingsValues>

export interface CostOverrideInput {
  provider: string
  sku: string
  service: string
  unit_type: string
  cost_per_unit_usd: number
  source_label?: string
  effective_at?: string
}

export function validateBillingConfigInput(input: BillingConfigInput): string | null {
  if (!Number.isFinite(input.dp_per_usd) || input.dp_per_usd <= 0 || input.dp_per_usd > 1_000_000) {
    return 'DP/USD 必须是大于 0 的有效数字'
  }
  if (
    !Number.isFinite(input.default_markup_percent)
    || input.default_markup_percent < 0
    || input.default_markup_percent > 100_000
  ) {
    return '默认加价率必须在 0 到 100000 之间'
  }
  const seen = new Set<string>()
  for (const override of input.overrides) {
    if (!['provider', 'category', 'sku'].includes(override.scope_type)) {
      return '分级加价的范围类型无效'
    }
    const scopeKey = override.scope_key.trim()
    if (!scopeKey) return '请填写每一条分级加价的匹配值，或删除空白项'
    if (scopeKey.length > 260) return '分级加价的匹配值不能超过 260 个字符'
    if (
      !Number.isFinite(override.markup_percent)
      || override.markup_percent < 0
      || override.markup_percent > 100_000
    ) {
      return '分级加价率必须在 0 到 100000 之间'
    }
    const key = `${override.scope_type}\u0000${scopeKey}`
    if (seen.has(key)) return '同一范围不能添加重复的分级加价'
    seen.add(key)
  }
  return null
}

export function validateCostOverrideInput(
  input: CostOverrideInput,
  nowMs = Date.now(),
): string | null {
  if (!input.provider.trim() || !input.sku.trim() || !input.service.trim()) {
    return '合同成本缺少 Provider、SKU 或服务标识'
  }
  const provider = input.provider.trim()
  const sku = input.sku.trim()
  const service = input.service.trim()
  const unitType = input.unit_type.trim()
  if (provider.length > 60 || sku.length > 200 || service.length > 50) {
    return '合同成本的 Provider、SKU 或服务标识过长'
  }
  if (![
    'hour',
    'minute',
    'input_token',
    'cached_input_token',
    'cache_write_token',
    'output_token',
  ].includes(unitType)) {
    return '合同成本的计量单位无效'
  }
  if ([provider, sku, service, unitType].join(':').length > 320) {
    return '合同成本标识过长'
  }
  if (
    !Number.isFinite(input.cost_per_unit_usd)
    || input.cost_per_unit_usd < 0
    || input.cost_per_unit_usd >= 100_000_000
  ) {
    return '合同成本必须是有效的非负数字'
  }
  if ((input.source_label || '').trim().length > 120) return '成本来源不能超过 120 个字符'
  if (input.effective_at) {
    const effectiveAtMs = Date.parse(input.effective_at)
    if (!Number.isFinite(effectiveAtMs)) return '生效时间格式无效'
    if (effectiveAtMs > nowMs + 5 * 60_000) return '生效时间不能设为未来时间'
  }
  return null
}

export class AdminAPIError extends Error {
  readonly status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'AdminAPIError'
    this.status = status
  }
}

type LegacySettings = Partial<Record<keyof SystemSettingsValues, string | number | boolean>>

const defaultSystemSettings: SystemSettingsValues = {
  billing_enabled: true,
  allow_negative_balance: false,
  allow_user_api_key: false,
  free_tier_dreampoints: 1,
}

function abortError(signal: AbortSignal): Error {
  return signal.reason instanceof Error
    ? signal.reason
    : new DOMException('Request aborted', 'AbortError')
}

function waitForAdminRetry(delayMs: number, signal?: AbortSignal | null): Promise<void> {
  if (signal?.aborted) return Promise.reject(abortError(signal))
  return new Promise((resolve, reject) => {
    const timer = globalThis.setTimeout(() => {
      signal?.removeEventListener('abort', onAbort)
      resolve()
    }, delayMs)
    const onAbort = () => {
      globalThis.clearTimeout(timer)
      reject(abortError(signal as AbortSignal))
    }
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}

function isRetryableAdminRead(options: RequestInit): boolean {
  const method = (options.method || 'GET').toUpperCase()
  return method === 'GET' || method === 'HEAD'
}

function isRetryableNetworkError(reason: unknown): boolean {
  return reason instanceof TypeError
    || (reason instanceof DOMException
      && (reason.name === 'NetworkError' || reason.name === 'TimeoutError'))
}

export async function adminFetch<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const token = await ensureValidAccessToken()
  const retryable = isRetryableAdminRead(options)
  let response: Response
  for (let attempt = 0; ; attempt += 1) {
    try {
      response = await fetch(`${baseUrl}${endpoint}`, {
        ...options,
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${token}`,
          ...(options.headers || {}),
        },
      })
    } catch (reason) {
      if (
        !retryable
        || attempt >= ADMIN_READ_RETRY_DELAYS_MS.length
        || options.signal?.aborted
        || !isRetryableNetworkError(reason)
      ) {
        throw reason
      }
      await waitForAdminRetry(ADMIN_READ_RETRY_DELAYS_MS[attempt], options.signal)
      continue
    }

    if (
      retryable
      && TRANSIENT_GATEWAY_STATUSES.has(response.status)
      && attempt < ADMIN_READ_RETRY_DELAYS_MS.length
    ) {
      await response.body?.cancel().catch(() => undefined)
      await waitForAdminRetry(ADMIN_READ_RETRY_DELAYS_MS[attempt], options.signal)
      continue
    }
    break
  }

  if (!response.ok) {
    const error = await response.json().catch(() => ({ error: 'Request failed' }))
    throw new AdminAPIError(error.error || 'Request failed', response.status)
  }

  if (response.status === 204) return undefined as T
  return response.json()
}

function asBoolean(value: unknown, fallback: boolean): boolean {
  if (typeof value === 'boolean') return value
  if (typeof value === 'string') {
    if (value === 'true') return true
    if (value === 'false') return false
  }
  return fallback
}

function asFiniteNumber(value: unknown, fallback: number): number {
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(parsed) ? parsed : fallback
}

function normalizeSettingsValues(
  raw: LegacySettings | undefined,
  fallback: SystemSettingsValues,
): SystemSettingsValues {
  return {
    billing_enabled: asBoolean(raw?.billing_enabled, fallback.billing_enabled),
    allow_negative_balance: asBoolean(raw?.allow_negative_balance, fallback.allow_negative_balance),
    allow_user_api_key: asBoolean(raw?.allow_user_api_key, fallback.allow_user_api_key),
    free_tier_dreampoints: asFiniteNumber(
      raw?.free_tier_dreampoints,
      fallback.free_tier_dreampoints,
    ),
  }
}

export function normalizeSystemSettings(
  raw: SystemSettingsResponse | LegacySettings,
): SystemSettingsResponse {
  if ('values' in raw && 'defaults' in raw) {
    const defaults = normalizeSettingsValues(raw.defaults, defaultSystemSettings)
    return {
      defaults,
      values: normalizeSettingsValues(raw.values, defaults),
    }
  }
  return {
    defaults: defaultSystemSettings,
    values: normalizeSettingsValues(raw as LegacySettings, defaultSystemSettings),
  }
}

export function getRatePublicCost(rate: CostRate): number {
  return rate.public_cost_per_unit_usd ?? rate.cost_per_unit_usd
}

export function getRateEffectiveCost(rate: CostRate): number {
  return rate.effective_cost_per_unit_usd ?? rate.cost_per_unit_usd
}

export function getRateEffectiveRetail(rate: CostRate): number | null {
  if (rate.effective_retail_dp_per_unit !== undefined) {
    return rate.effective_retail_dp_per_unit
  }
  if (rate.effective_retail_by_action !== undefined) {
    return null
  }
  // Compatibility with catalog responses predating the effective/proposed split.
  return rate.retail_dp_per_unit
}

export function getModelRateCostPerMillion(
  rates: CostRate[],
  service: 'llm' | 'embedding',
  modelId: string,
  unitType: string,
): number | null {
  const rate = rates.find((item) => (
    item.service === service
    && item.sku === modelId
    && item.unit_type === unitType
  ))
  return rate ? getRateEffectiveCost(rate) * 1_000_000 : null
}

function billingRateKey(rate: CostRate): string {
  return [rate.provider, rate.service, rate.sku, rate.unit_type].join('\u0000')
}

function proposedRetail(rate: CostRate): number {
  return rate.proposed_retail_dp_per_unit ?? rate.retail_dp_per_unit
}

function materiallyDifferent(left: number, right: number): boolean {
  return Math.abs(left - right) > 1e-12
}

function actualRetailSnapshot(rate: CostRate): {
  common: number | null
  byAction: Record<string, number>
} {
  return {
    common: getRateEffectiveRetail(rate),
    byAction: { ...(rate.effective_retail_by_action || {}) },
  }
}

function actualRetailDiffersFromTarget(
  current: { common: number | null; byAction: Record<string, number> },
  target: number,
): boolean {
  if (current.common !== null) return materiallyDifferent(current.common, target)
  const actionValues = Object.values(current.byAction)
  if (actionValues.length === 0) return true
  return actionValues.some((value) => materiallyDifferent(value, target))
}

export function buildBillingPreviewDiff(
  current: BillingCatalog,
  preview: BillingPreview,
  maxRateRows = 12,
): BillingPreviewDiff {
  const currentPricingState = current.pricing_state ?? current.config.pricing_state ?? ''
  const currentCatalogVersion = current.config.catalog_version || current.installed_version
  const targetCatalogVersion = preview.config.catalog_version
    || preview.target_version
    || preview.catalog_version
    || ''
  const rawTargetPricingState = preview.config.pricing_state ?? currentPricingState
  const targetPricingState = rawTargetPricingState === 'managed_active'
    ? targetCatalogVersion === current.builtin_version ? 'managed_current' : 'managed_outdated'
    : rawTargetPricingState
  const config: BillingPreviewConfigComparison[] = [
    {
      field: 'dp_per_usd',
      current: current.config.dp_per_usd,
      target: preview.config.dp_per_usd,
      changed: materiallyDifferent(current.config.dp_per_usd, preview.config.dp_per_usd),
    },
    {
      field: 'default_markup_percent',
      current: current.config.default_markup_percent,
      target: preview.config.default_markup_percent,
      changed: materiallyDifferent(
        current.config.default_markup_percent,
        preview.config.default_markup_percent,
      ),
    },
    {
      field: 'catalog_version',
      current: currentCatalogVersion,
      target: targetCatalogVersion,
      changed: currentCatalogVersion !== targetCatalogVersion,
    },
    {
      field: 'pricing_state',
      current: currentPricingState,
      target: targetPricingState,
      changed: currentPricingState !== targetPricingState,
    },
  ]

  const currentRates = new Map(
    current.rates.filter((rate) => rate.is_active).map((rate) => [billingRateKey(rate), rate]),
  )
  const targetRates = new Map(
    preview.rates.filter((rate) => rate.is_active).map((rate) => [billingRateKey(rate), rate]),
  )
  const keys = [...new Set([...currentRates.keys(), ...targetRates.keys()])].sort()
  const rateChanges: BillingPreviewRateChange[] = []
  for (const key of keys) {
    const before = currentRates.get(key)
    const after = targetRates.get(key)
    const identity = after ?? before
    if (!identity) continue
    if (!before && after) {
      rateChanges.push({
        key,
        kind: 'added',
        provider: after.provider,
        service: after.service,
        sku: after.sku,
        unit_type: after.unit_type,
        current_effective_cost_usd: null,
        target_effective_cost_usd: getRateEffectiveCost(after),
        current_effective_retail_dp: null,
        current_effective_retail_by_action: {},
        target_proposed_retail_dp: proposedRetail(after),
        cost_changed: true,
        retail_changed: true,
      })
      continue
    }
    if (before && !after) {
      const beforeActualRetail = actualRetailSnapshot(before)
      rateChanges.push({
        key,
        kind: 'disabled',
        provider: before.provider,
        service: before.service,
        sku: before.sku,
        unit_type: before.unit_type,
        current_effective_cost_usd: getRateEffectiveCost(before),
        target_effective_cost_usd: null,
        current_effective_retail_dp: beforeActualRetail.common,
        current_effective_retail_by_action: beforeActualRetail.byAction,
        target_proposed_retail_dp: null,
        cost_changed: true,
        retail_changed: true,
      })
      continue
    }
    if (!before || !after) continue
    const beforeCost = getRateEffectiveCost(before)
    const afterCost = getRateEffectiveCost(after)
    const beforeRetail = actualRetailSnapshot(before)
    const afterRetail = proposedRetail(after)
    const costChanged = materiallyDifferent(beforeCost, afterCost)
    const retailChanged = actualRetailDiffersFromTarget(beforeRetail, afterRetail)
    if (costChanged || retailChanged) {
      rateChanges.push({
        key,
        kind: 'changed',
        provider: after.provider,
        service: after.service,
        sku: after.sku,
        unit_type: after.unit_type,
        current_effective_cost_usd: beforeCost,
        target_effective_cost_usd: afterCost,
        current_effective_retail_dp: beforeRetail.common,
        current_effective_retail_by_action: beforeRetail.byAction,
        target_proposed_retail_dp: afterRetail,
        cost_changed: costChanged,
        retail_changed: retailChanged,
      })
    }
  }

  const limit = Math.max(0, Math.min(100, Math.trunc(maxRateRows)))
  return {
    config,
    rates: rateChanges.slice(0, limit),
    total_rate_changes: rateChanges.length,
    hidden_rate_changes: Math.max(0, rateChanges.length - limit),
  }
}

export function getBillingCatalogEditableConfig(catalog: BillingCatalog): BillingConfigInput {
  const source = catalog.pending_config ?? catalog.config
  return {
    dp_per_usd: source.dp_per_usd,
    default_markup_percent: source.default_markup_percent,
    overrides: [...(source.overrides || [])],
  }
}

export function hasBillingPreviewRevision(preview: BillingPreview): boolean {
  return Boolean(preview.current_revision?.trim())
}

export function isStaleBillingPreviewError(reason: unknown): boolean {
  return reason instanceof AdminAPIError && reason.status === 409
}

export function isBillingEstimateAvailable(analytics: BillingAnalytics): boolean {
  return analytics.estimate_available ?? Boolean(analytics.estimate_catalog_version)
}

export async function listUsers(page = 1, pageSize = 20): Promise<UserListResponse> {
  return adminFetch(`/api/admin/users?page=${page}&page_size=${pageSize}`)
}

export async function createUser(input: {
  tenant_id?: string
  email: string
  password: string
  name: string
  role: 'user' | 'admin'
  dreampoints?: number
}): Promise<User> {
  return adminFetch('/api/admin/users', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export async function updateUser(
  id: string,
  data: { name?: string; role?: string; is_active?: boolean },
): Promise<User> {
  return adminFetch(`/api/admin/users/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export async function adjustUserBalance(input: {
  user_id: string
  amount: number
  description: string
}): Promise<{ dreampoints?: number }> {
  return adminFetch('/api/admin/balance', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export async function listTenants(page = 1, pageSize = 20): Promise<TenantListResponse> {
  return adminFetch(`/api/admin/tenants?page=${page}&page_size=${pageSize}`)
}

export async function listAllTenants(pageSize = 100): Promise<Tenant[]> {
  const requestedPageSize = Math.min(100, Math.max(1, Math.trunc(pageSize)))
  const first = await listTenants(1, requestedPageSize)
  const tenants = [...first.tenants]
  const effectivePageSize = Math.max(1, first.page_size || requestedPageSize)
  const totalPages = Math.ceil(first.total / effectivePageSize)
  for (let page = 2; page <= totalPages; page += 1) {
    const result = await listTenants(page, effectivePageSize)
    tenants.push(...result.tenants)
  }
  return tenants
}

export async function updateTenant(
  id: string,
  data: {
    name?: string
    plan?: string
    api_quota_monthly?: number
    storage_quota_gb?: number
    max_sessions?: number
  },
): Promise<Tenant> {
  return adminFetch(`/api/admin/tenants/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export async function getGlobalStats(): Promise<AdminSystemStatsResponse> {
  return adminFetch('/api/admin/stats')
}

export async function getUsage(tenantId?: string, month?: string): Promise<UsageSummary> {
  const params = new URLSearchParams()
  if (tenantId) params.set('tenant_id', tenantId)
  if (month) params.set('month', month)
  return adminFetch(`/api/admin/usage?${params}`)
}

export async function getBillingCatalog(): Promise<BillingCatalog> {
  return adminFetch('/api/admin/billing/catalog')
}

export async function updateBillingConfig(config: BillingConfigInput): Promise<BillingCatalog> {
  const validationError = validateBillingConfigInput(config)
  if (validationError) throw new Error(validationError)
  return adminFetch('/api/admin/billing/config', {
    method: 'PUT',
    body: JSON.stringify(config),
  })
}

export async function previewBillingConfig(config: BillingConfigInput): Promise<BillingPreview> {
  const validationError = validateBillingConfigInput(config)
  if (validationError) throw new Error(validationError)
  return adminFetch('/api/admin/billing/preview', {
    method: 'POST',
    body: JSON.stringify(config),
  })
}

export async function previewBillingCatalogApply(): Promise<BillingPreview> {
  return adminFetch('/api/admin/billing/catalog/apply/preview', { method: 'POST' })
}

export async function applyBillingCatalog(input: {
  confirmation: string
  catalog_version: string
  current_revision: string
}): Promise<BillingCatalog> {
  return adminFetch('/api/admin/billing/catalog/apply', {
    method: 'POST',
    body: JSON.stringify(input),
  })
}

export async function previewBillingReset(): Promise<BillingPreview> {
  return adminFetch('/api/admin/billing/reset/preview', { method: 'POST' })
}

export async function resetBillingDefaults(
  confirmation: string,
  currentRevision: string,
): Promise<BillingCatalog> {
  return adminFetch('/api/admin/billing/reset', {
    method: 'POST',
    body: JSON.stringify({ confirmation, current_revision: currentRevision }),
  })
}

export async function getBillingAnalytics(): Promise<BillingAnalytics> {
  return adminFetch('/api/admin/billing/analytics')
}

export async function putCostOverride(input: CostOverrideInput): Promise<BillingCatalog> {
  const validationError = validateCostOverrideInput(input)
  if (validationError) throw new Error(validationError)
  return adminFetch('/api/admin/billing/cost-overrides', {
    method: 'PUT',
    body: JSON.stringify(input),
  })
}

export async function deleteCostOverride(input: {
  provider: string
  sku: string
  service: string
  unit_type: string
}): Promise<BillingCatalog> {
  const params = new URLSearchParams(input)
  return adminFetch(`/api/admin/billing/cost-overrides?${params}`, { method: 'DELETE' })
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

export async function getSystemSettings(): Promise<SystemSettingsResponse> {
  const raw = await adminFetch<SystemSettingsResponse | LegacySettings>('/api/admin/settings')
  return normalizeSystemSettings(raw)
}

export async function patchSystemSettings(
  patch: SystemSettingsPatch,
): Promise<SystemSettingsResponse> {
  const raw = await adminFetch<SystemSettingsResponse | LegacySettings | { success: boolean }>(
    '/api/admin/settings',
    {
      method: 'PATCH',
      body: JSON.stringify(patch),
    },
  )
  if ('values' in raw || 'billing_enabled' in raw) {
    return normalizeSystemSettings(raw as SystemSettingsResponse | LegacySettings)
  }
  return getSystemSettings()
}

export async function previewSystemSettingsReset(): Promise<SystemSettingsResetPreview> {
  const raw = await adminFetch<SystemSettingsResetPreview>(
    '/api/admin/settings/reset/preview',
  )
  return {
    ...raw,
    defaults: normalizeSettingsValues(raw.defaults, defaultSystemSettings),
    current: normalizeSettingsValues(raw.current, defaultSystemSettings),
  }
}

export async function resetSystemSettings(): Promise<SystemSettingsResponse> {
  const raw = await adminFetch<SystemSettingsResponse | LegacySettings>(
    '/api/admin/settings/reset',
    {
      method: 'POST',
      body: JSON.stringify({ confirm: true }),
    },
  )
  return normalizeSystemSettings(raw)
}
