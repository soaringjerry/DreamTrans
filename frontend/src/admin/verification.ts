import {
  AdminAPIError,
  buildBillingPreviewDiff,
  getBillingCatalogEditableConfig,
  getRateEffectiveCost,
  getRateEffectiveRetail,
  getRatePublicCost,
  getModelRateCostPerMillion,
  hasBillingPreviewRevision,
  isBillingEstimateAvailable,
  isStaleBillingPreviewError,
  normalizeSystemSettings,
  type AdminSystemStatsResponse,
  type BillingAnalytics,
  type BillingCatalog,
  type BillingPreview,
  type CostRate,
} from './api'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(`admin verification failed: ${message}`)
}

const legacy = normalizeSystemSettings({
  billing_enabled: 'false',
  allow_negative_balance: 'true',
  allow_user_api_key: 'false',
  free_tier_dreampoints: '3.5',
})

assert(!legacy.values.billing_enabled, 'legacy boolean false is normalized')
assert(legacy.values.allow_negative_balance, 'legacy boolean true is normalized')
assert(legacy.values.free_tier_dreampoints === 3.5, 'legacy numeric setting is normalized')
assert(legacy.defaults.free_tier_dreampoints === 1, 'safe signup default remains one DP')

const typed = normalizeSystemSettings({
  values: {
    billing_enabled: true,
    allow_negative_balance: false,
    allow_user_api_key: true,
    free_tier_dreampoints: 2,
  },
  defaults: {
    billing_enabled: true,
    allow_negative_balance: false,
    allow_user_api_key: false,
    free_tier_dreampoints: 1,
  },
})

assert(typed.values.allow_user_api_key, 'typed settings remain typed')
assert(typed.defaults.free_tier_dreampoints === 1, 'server reset defaults are retained')

const stats: AdminSystemStatsResponse = {
  basic: {
    user_count: 3,
    tenant_count: 1,
    session_count: 7,
    transcript_count: 12,
  },
  billing: {
    total_dreampoints: 8,
    total_used: 4,
    total_users: 3,
    active_users: 2,
    total_sessions: 7,
    total_transcripts: 12,
    usage_by_action: {},
    usage_by_model: {},
  },
  time: '2026-07-31T00:00:00Z',
}

assert(stats.basic.user_count === 3, 'overview reads the nested basic statistics contract')
assert(stats.basic.tenant_count === 1, 'overview does not infer tenants from users')

const analytics: BillingAnalytics = {
  upstream_cost_usd: 0.43,
  service_fee_dp: 0.215,
  retail_dp: 0.645,
  usage_count: 4,
  attributed_usage_count: 1,
  legacy_unknown_count: 3,
  legacy_unknown_retail_dp: 1.2,
  estimated_legacy_upstream_cost_usd: 0.8,
  estimated_legacy_service_fee_dp: 0.4,
  estimate_eligible_count: 2,
  estimate_catalog_version: '2026-07-31',
  estimate_available: true,
}

assert(analytics.attributed_usage_count === 1, 'exact usage remains separate from legacy usage')
assert(analytics.legacy_unknown_count === 3, 'legacy unknown usage remains visible')
assert(analytics.estimate_eligible_count === 2, 'estimate coverage has an explicit denominator')
assert(isBillingEstimateAvailable(analytics), 'explicitly available estimates are visible')
assert(
  isBillingEstimateAvailable({ ...analytics, estimate_available: undefined }),
  'older analytics infer estimate availability from the catalog version',
)
assert(
  !isBillingEstimateAvailable({
    ...analytics,
    estimate_available: false,
    estimated_legacy_upstream_cost_usd: 0,
    estimated_legacy_service_fee_dp: 0,
  }),
  'explicit estimate failure wins over zero-valued estimate fields',
)

const legacyRate: CostRate = {
  provider: 'speechmatics',
  sku: 'speechmatics-realtime-enhanced',
  service: 'transcription',
  unit_type: 'hour',
  cost_per_unit_usd: 0.43,
  retail_dp_per_unit: 0.645,
  markup_percent: 50,
  gross_margin_percent: 33.3333,
  catalog_version: '2026-07-31',
  source_url: 'https://www.speechmatics.com/pricing',
  is_builtin: true,
  is_active: true,
  override_source: 'global',
}

assert(getRatePublicCost(legacyRate) === 0.43, 'legacy rate falls back to cost_per_unit_usd')
assert(getRateEffectiveCost(legacyRate) === 0.43, 'legacy effective cost remains readable')
assert(getRateEffectiveRetail(legacyRate) === 0.645, 'legacy retail remains readable')

const managedRate: CostRate = {
  ...legacyRate,
  public_cost_per_unit_usd: 0.43,
  effective_cost_per_unit_usd: 0.31,
  effective_retail_dp_per_unit: 0.465,
  proposed_retail_dp_per_unit: 0.465,
  cost_source: 'contract_override',
}

assert(getRatePublicCost(managedRate) === 0.43, 'public cost remains visible under an override')
assert(getRateEffectiveCost(managedRate) === 0.31, 'contract override becomes effective cost')
assert(getRateEffectiveRetail(managedRate) === 0.465, 'managed effective retail is preferred')

const unconfiguredActualRate: CostRate = {
  ...managedRate,
  effective_retail_dp_per_unit: null,
  effective_retail_by_action: {},
}

assert(
  getRateEffectiveRetail(unconfiguredActualRate) === null,
  'explicitly missing actual retail never falls back to proposed retail',
)

const splitActualRate: CostRate = {
  ...managedRate,
  effective_retail_dp_per_unit: null,
  effective_retail_by_action: {
    chat: 0.46,
    summarize: 0.48,
  },
}

assert(
  getRateEffectiveRetail(splitActualRate) === null,
  'per-action actual retail remains separate from the common retail field',
)

const sharedModelRates: CostRate[] = [
  {
    ...managedRate,
    sku: 'shared-model',
    service: 'llm',
    unit_type: 'input_token',
    effective_cost_per_unit_usd: 2e-6,
  },
  {
    ...managedRate,
    sku: 'shared-model',
    service: 'embedding',
    unit_type: 'input_token',
    effective_cost_per_unit_usd: 0.12e-6,
  },
]

assert(
  getModelRateCostPerMillion(sharedModelRates, 'llm', 'shared-model', 'input_token') === 2,
  'model cost lookup includes the llm service identity',
)
assert(
  getModelRateCostPerMillion(sharedModelRates, 'embedding', 'shared-model', 'input_token') === 0.12,
  'switching service reloads the matching embedding cost',
)

const disabledRate: CostRate = {
  ...managedRate,
  sku: 'retired-model',
}
const changedTargetRate: CostRate = {
  ...managedRate,
  effective_cost_per_unit_usd: 0.35,
  proposed_retail_dp_per_unit: 0.525,
}
const addedRate: CostRate = {
  ...managedRate,
  sku: 'new-model',
  effective_cost_per_unit_usd: 0.2,
  proposed_retail_dp_per_unit: 0.3,
}
const currentBillingCatalog: BillingCatalog = {
  builtin_version: '2026-07-31',
  installed_version: '2026-07-30',
  has_update: true,
  pricing_state: 'managed_outdated',
  config: {
    dp_per_usd: 1,
    default_markup_percent: 50,
    catalog_version: '2026-07-30',
    pricing_state: 'managed_active',
    overrides: [],
  },
  rates: [managedRate, disabledRate],
}
const catalogWithPendingConfig: BillingCatalog = {
  ...currentBillingCatalog,
  pending_config: {
    dp_per_usd: 2,
    default_markup_percent: 40,
    overrides: [{ scope_type: 'provider', scope_key: 'speechmatics', markup_percent: 25 }],
  },
}
const pendingEditableConfig = getBillingCatalogEditableConfig(catalogWithPendingConfig)
assert(
  pendingEditableConfig.dp_per_usd === 2
    && pendingEditableConfig.default_markup_percent === 40
    && pendingEditableConfig.overrides[0]?.markup_percent === 25,
  'staged billing config is restored into the editor without pretending it is applied',
)
const appliedEditableConfig = getBillingCatalogEditableConfig({
  ...currentBillingCatalog,
  pending_config: null,
})
assert(
  appliedEditableConfig.dp_per_usd === currentBillingCatalog.config.dp_per_usd
    && appliedEditableConfig.default_markup_percent
      === currentBillingCatalog.config.default_markup_percent,
  'editor falls back to the applied config after apply clears the pending config',
)
const targetBillingPreview: BillingPreview = {
  config: {
    dp_per_usd: 2,
    default_markup_percent: 40,
    catalog_version: '2026-07-31',
    pricing_state: 'managed_active',
    overrides: [],
  },
  rates: [changedTargetRate, addedRate],
  added: 1,
  updated: 1,
  disabled: 1,
  confirmation: '应用成本更新',
  current_revision: 'revision-1',
}

const stagedPreviewDiff = buildBillingPreviewDiff(
  catalogWithPendingConfig,
  targetBillingPreview,
  10,
)
const stagedDPDiff = stagedPreviewDiff.config.find((item) => item.field === 'dp_per_usd')
assert(
  stagedDPDiff?.current === currentBillingCatalog.config.dp_per_usd
    && stagedDPDiff.target === catalogWithPendingConfig.pending_config?.dp_per_usd,
  'apply preview compares the applied config against the staged config',
)

const fullPreviewDiff = buildBillingPreviewDiff(
  currentBillingCatalog,
  targetBillingPreview,
  10,
)
assert(fullPreviewDiff.config.length === 4, 'preview always compares all governed config fields')
assert(
  fullPreviewDiff.config.find((item) => item.field === 'dp_per_usd')?.changed,
  'preview exposes DP/USD current-to-target changes',
)
assert(
  fullPreviewDiff.config.find((item) => item.field === 'pricing_state')?.target
    === 'managed_current',
  'preview projects the target catalog into its user-visible pricing state',
)
assert(fullPreviewDiff.total_rate_changes === 3, 'preview compares added, disabled, and changed rates')
assert(
  fullPreviewDiff.rates.some((rate) => (
    rate.kind === 'changed'
    && rate.current_effective_cost_usd === 0.31
    && rate.target_effective_cost_usd === 0.35
    && rate.current_effective_retail_dp === 0.465
    && rate.target_proposed_retail_dp === 0.525
  )),
  'preview uses target effective cost and proposed retail from preview rates',
)
assert(
  fullPreviewDiff.rates.some((rate) => rate.kind === 'added')
    && fullPreviewDiff.rates.some((rate) => rate.kind === 'disabled'),
  'preview rates are authoritative for additions and removals',
)

const limitedPreviewDiff = buildBillingPreviewDiff(
  currentBillingCatalog,
  targetBillingPreview,
  2,
)
assert(limitedPreviewDiff.rates.length === 2, 'preview limits rendered rate rows')
assert(limitedPreviewDiff.hidden_rate_changes === 1, 'preview reports omitted rate differences')
assert(hasBillingPreviewRevision(targetBillingPreview), 'revisioned preview can be confirmed')
assert(
  !hasBillingPreviewRevision({ ...targetBillingPreview, current_revision: undefined }),
  'preview without a revision is invalidated before confirmation',
)
assert(
  !hasBillingPreviewRevision({ ...targetBillingPreview, current_revision: '   ' }),
  'blank preview revision is treated as missing',
)
assert(
  isStaleBillingPreviewError(new AdminAPIError('stale preview', 409)),
  'HTTP 409 invalidates an expired billing preview',
)
assert(
  !isStaleBillingPreviewError(new AdminAPIError('bad request', 400)),
  'non-conflict API errors do not masquerade as stale previews',
)

const legacySplitCatalog: BillingCatalog = {
  ...currentBillingCatalog,
  pricing_state: 'legacy_active',
  rates: [{
    ...managedRate,
    effective_retail_dp_per_unit: null,
    effective_retail_by_action: {
      chat: 0.7,
      summarize: 0.8,
    },
  }],
}
const legacySplitDiff = buildBillingPreviewDiff(legacySplitCatalog, {
  ...targetBillingPreview,
  rates: [{
    ...managedRate,
    proposed_retail_dp_per_unit: 0.5,
  }],
}, 10)
const legacySplitChange = legacySplitDiff.rates[0]
assert(
  legacySplitChange?.current_effective_retail_dp === null
    && legacySplitChange.current_effective_retail_by_action.chat === 0.7
    && legacySplitChange.current_effective_retail_by_action.summarize === 0.8
    && legacySplitChange.target_proposed_retail_dp === 0.5,
  'legacy preview compares actual per-action rules against the target proposed retail',
)
