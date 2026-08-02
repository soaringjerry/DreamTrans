import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from 'react'
import {
  adjustUserBalance,
  applyBillingCatalog,
  buildBillingPreviewDiff,
  createUser,
  deleteCostOverride,
  getBillingAnalytics,
  getBillingCatalog,
  getBillingCatalogEditableConfig,
  getGlobalStats,
  getModelCatalog,
  getModelRateCostPerMillion,
  getRateEffectiveCost,
  getRateEffectiveRetail,
  getRatePublicCost,
  getSystemSettings,
  hasBillingPreviewRevision,
  isBillingEstimateAvailable,
  isStaleBillingPreviewError,
  listAllTenants,
  listTenants,
  listUsers,
  patchSystemSettings,
  previewBillingCatalogApply,
  previewBillingConfig,
  previewBillingReset,
  previewSystemSettingsReset,
  putCostOverride,
  refreshModelCatalog,
  resetBillingDefaults,
  resetSystemSettings,
  updateBillingConfig,
  updateModelCost,
  updateModelPolicy,
  updateTenant,
  updateUser,
  validateBillingConfigInput,
  validateCostOverrideInput,
  type AdminSystemStatsResponse,
  type BillingAnalytics,
  type BillingCatalog,
  type BillingConfigInput,
  type BillingPreview,
  type CostRate,
  type MarkupOverride,
  type ModelCatalog,
  type ModelPolicy,
  type ProviderAvailability,
  type ProviderModel,
  type SystemSettingsPatch,
  type SystemSettingsResetPreview,
  type SystemSettingsResponse,
  type SystemSettingsValues,
  type Tenant,
  type User,
} from '../admin/api'
import { initAuth, type User as AuthUser } from './api/auth'
import './pro-admin.css'

type Tab = 'overview' | 'users' | 'tenants' | 'models' | 'billing' | 'settings'
type Runner = <T>(
  operation: () => Promise<T>,
  success?: string,
  onError?: (message: string) => void,
) => Promise<T | undefined>
type SettingKey = keyof SystemSettingsValues

const nav: Array<{ id: Tab; label: string; superOnly?: boolean }> = [
  { id: 'overview', label: '概览', superOnly: true },
  { id: 'users', label: '用户' },
  { id: 'tenants', label: '组织', superOnly: true },
  { id: 'models', label: '模型', superOnly: true },
  { id: 'billing', label: '计费', superOnly: true },
  { id: 'settings', label: '系统设置', superOnly: true },
]

const purposeLabels: Record<ModelPolicy['purpose'], string> = {
  translation: '翻译',
  summary: '摘要',
  chat: '问答',
  embedding: '向量',
}

const settingCopy: Record<SettingKey, { label: string; description: string; dangerous?: boolean }> = {
  billing_enabled: {
    label: '启用平台计费',
    description: '关闭后，新请求不再从 DreamPoints 余额扣费。已产生的账单不会改变。',
    dangerous: true,
  },
  allow_negative_balance: {
    label: '允许余额透支',
    description: '启用后，余额不足的用户仍可继续使用平台承担费用的服务。',
    dangerous: true,
  },
  allow_user_api_key: {
    label: '允许用户自带 Provider Key',
    description: '用户可使用自己的上游密钥；平台只收取服务费，不承担上游成本。',
  },
  free_tier_dreampoints: {
    label: '新用户初始 DreamPoints',
    description: '仅影响以后创建的账户，不会修改现有用户余额。',
  },
}

const systemSettingsResetConfirmation = '重置系统设置'

function formatNumber(value: number, digits = 2) {
  return new Intl.NumberFormat('zh-CN', {
    maximumFractionDigits: digits,
    minimumFractionDigits: 0,
  }).format(Number.isFinite(value) ? value : 0)
}

function formatInteger(value: number) {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 0 }).format(
    Number.isFinite(value) ? Math.trunc(value) : 0,
  )
}

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN')
}

function toDateTimeLocal(value?: string) {
  const date = value ? new Date(value) : new Date()
  if (Number.isNaN(date.getTime())) return ''
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000)
  return local.toISOString().slice(0, 16)
}

function formatCost(value: number, unit: string) {
  if (unit.includes('token')) return `$${formatNumber(value * 1_000_000, 6)} / 百万 token`
  if (unit === 'hour') return `$${formatNumber(value, 6)} / 小时`
  return `$${formatNumber(value, 6)} / ${unit}`
}

function formatRate(value: number, unit: string) {
  if (unit.includes('token')) return `${formatNumber(value * 1_000_000, 6)} DP / 百万 token`
  if (unit === 'hour') return `${formatNumber(value, 4)} DP / 小时`
  return `${formatNumber(value, 6)} DP / ${unit}`
}

function costEditorScale(unit: string) {
  return unit.includes('token') ? 1_000_000 : 1
}

function costEditorUnit(unit: string) {
  if (unit.includes('token')) return 'USD / 百万 token'
  if (unit === 'hour') return 'USD / 小时'
  if (unit === 'minute') return 'USD / 分钟'
  return `USD / ${unit}`
}

function errorMessage(reason: unknown) {
  return reason instanceof Error ? reason.message : '请求失败'
}

function previewMarkup(
  rate: Pick<CostRate, 'provider' | 'service' | 'sku'>,
  fallback: number,
  overrides: MarkupOverride[],
) {
  let result = fallback
  let rank = 0
  for (const override of overrides) {
    const matched = override.scope_type === 'provider'
      ? override.scope_key === rate.provider
      : override.scope_type === 'category'
        ? override.scope_key === rate.service
        : override.scope_key === rate.sku
          || override.scope_key === `${rate.provider}:${rate.sku}`
    const nextRank = override.scope_type === 'provider'
      ? 1
      : override.scope_type === 'category' ? 2 : 3
    if (matched && nextRank > rank) {
      result = override.markup_percent
      rank = nextRank
    }
  }
  return result
}

function ErrorBanner({ message, onClose }: { message: string; onClose?: () => void }) {
  if (!message) return null
  return (
    <div className="pa-banner pa-banner--error" role="alert">
      <span>{message}</span>
      {onClose && <button onClick={onClose} type="button">关闭</button>}
    </div>
  )
}

function Modal({
  title,
  children,
  footer,
  onClose,
  danger = false,
  wide = false,
}: {
  title: string
  children: ReactNode
  footer: ReactNode
  onClose: () => void
  danger?: boolean
  wide?: boolean
}) {
  return (
    <div className="pa-modal-backdrop" onMouseDown={(event) => {
      if (event.currentTarget === event.target) onClose()
    }}>
      <section
        aria-modal="true"
        className={`pa-modal ${danger ? 'pa-modal--danger' : ''} ${wide ? 'pa-modal--wide' : ''}`}
        role="dialog"
      >
        <header>
          <h2>{title}</h2>
          <button aria-label="关闭" className="pa-modal__close" onClick={onClose} type="button">×</button>
        </header>
        <div className="pa-modal__body">{children}</div>
        <footer>{footer}</footer>
      </section>
    </div>
  )
}

function Pagination({
  page,
  pageSize,
  total,
  onChange,
}: {
  page: number
  pageSize: number
  total: number
  onChange: (page: number) => void
}) {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  return (
    <div className="pa-pagination">
      <span>第 {page} / {pages} 页，共 {formatInteger(total)} 条</span>
      <div>
        <button disabled={page <= 1} onClick={() => onChange(page - 1)} type="button">上一页</button>
        <button disabled={page >= pages} onClick={() => onChange(page + 1)} type="button">下一页</button>
      </div>
    </div>
  )
}

const billingConfigDiffLabels = {
  dp_per_usd: 'DP / USD',
  default_markup_percent: '默认加价',
  catalog_version: '成本目录',
  pricing_state: '计费状态',
} as const

function pricingStateLabel(value: string) {
  if (value === 'legacy_active') return '旧版规则生效中'
  if (value === 'managed_current') return '托管目录已启用'
  if (value === 'managed_outdated') return '托管目录待更新'
  if (value === 'managed_active') return '托管规则'
  return value || '—'
}

function formatBillingConfigDiffValue(
  field: keyof typeof billingConfigDiffLabels,
  value: number | string,
) {
  if (field === 'dp_per_usd') return `${formatNumber(Number(value), 6)} DP`
  if (field === 'default_markup_percent') return `${formatNumber(Number(value), 4)}%`
  if (field === 'pricing_state') return pricingStateLabel(String(value))
  return String(value || '—')
}

function BillingPreviewDiffPanel({
  catalog,
  preview,
}: {
  catalog: BillingCatalog
  preview: BillingPreview
}) {
  const diff = buildBillingPreviewDiff(catalog, preview, 10)
  return (
    <div className="pa-preview-diff">
      <section>
        <h3>配置变化</h3>
        <div className="pa-config-diff-grid">
          {diff.config.map((item) => (
            <div className={item.changed ? 'is-changed' : ''} key={item.field}>
              <small>{billingConfigDiffLabels[item.field]}</small>
              <span>
                {formatBillingConfigDiffValue(item.field, item.current)}
                <i>→</i>
                {formatBillingConfigDiffValue(item.field, item.target)}
              </span>
            </div>
          ))}
        </div>
      </section>
      <section>
        <h3>成本与拟售价变化</h3>
        {diff.total_rate_changes === 0 ? (
          <div className="pa-empty">费率明细没有变化。</div>
        ) : (
          <>
            <div className="pa-table-wrap pa-diff-table"><table>
              <thead><tr><th>类型</th><th>费率</th><th>有效成本 current → target</th><th>实际扣费 current → target 拟售价</th></tr></thead>
              <tbody>{diff.rates.map((rate) => (
                <tr key={rate.key}>
                  <td><span className={`pa-status ${rate.kind === 'added' ? 'is-good' : rate.kind === 'disabled' ? 'is-bad' : 'is-warn'}`}>
                    {rate.kind === 'added' ? '新增' : rate.kind === 'disabled' ? '停用' : '变更'}
                  </span></td>
                  <td><strong>{rate.sku}</strong><small>{rate.provider} · {rate.service} · {rate.unit_type}</small></td>
                  <td className={rate.cost_changed ? 'pa-diff-value is-changed' : 'pa-diff-value'}>
                    <span>{rate.current_effective_cost_usd === null ? '—' : formatCost(rate.current_effective_cost_usd, rate.unit_type)}</span>
                    <i>→</i>
                    <span>{rate.target_effective_cost_usd === null ? '—' : formatCost(rate.target_effective_cost_usd, rate.unit_type)}</span>
                  </td>
                  <td className={rate.retail_changed ? 'pa-diff-value is-changed' : 'pa-diff-value'}>
                    {rate.current_effective_retail_dp !== null ? (
                      <span>{formatRate(rate.current_effective_retail_dp, rate.unit_type)}</span>
                    ) : Object.keys(rate.current_effective_retail_by_action).length > 0 ? (
                      Object.entries(rate.current_effective_retail_by_action).map(([action, value]) => (
                        <span key={action}>{action}：{formatRate(value, rate.unit_type)}</span>
                      ))
                    ) : <span>—（未配置实际规则）</span>}
                    <i>→</i>
                    <span>{rate.target_proposed_retail_dp === null ? '—' : formatRate(rate.target_proposed_retail_dp, rate.unit_type)}</span>
                  </td>
                </tr>
              ))}</tbody>
            </table></div>
            {diff.hidden_rate_changes > 0 && (
              <p className="pa-diff-overflow">另有 {formatInteger(diff.hidden_rate_changes)} 项费率变化未展开；确认操作会应用全部变化。</p>
            )}
          </>
        )}
      </section>
    </div>
  )
}

export default function ProAdmin() {
  const [viewer, setViewer] = useState<AuthUser | null>(null)
  const [ready, setReady] = useState(false)
  const [tab, setTab] = useState<Tab>('overview')
  const [busyCount, setBusyCount] = useState(0)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const isSuper = viewer?.role === 'super_admin'

  useEffect(() => {
    void initAuth().then((user) => {
      if (!user || (user.role !== 'admin' && user.role !== 'super_admin')) {
        window.location.href = '/pro'
        return
      }
      setViewer(user)
      setTab(user.role === 'super_admin' ? 'overview' : 'users')
      setReady(true)
    }).catch(() => {
      window.location.href = '/pro'
    })
  }, [])

  const run = useCallback(async <T,>(
    operation: () => Promise<T>,
    success?: string,
    onError?: (message: string) => void,
  ) => {
    setBusyCount((value) => value + 1)
    setError('')
    try {
      const value = await operation()
      if (success) {
        setNotice(success)
        window.setTimeout(() => setNotice(''), 3000)
      }
      return value
    } catch (reason) {
      const message = errorMessage(reason)
      setError(message)
      onError?.(message)
      return undefined
    } finally {
      setBusyCount((value) => Math.max(0, value - 1))
    }
  }, [])

  if (!ready || !viewer) {
    return <div className="pa-loading">正在验证管理员身份…</div>
  }

  return (
    <div className="pa-shell">
      <aside className="pa-sidebar">
        <a className="pa-brand" href="/pro">
          <span className="pa-brand__mark">D</span>
          <span><strong>DreamTrans</strong><small>Control center</small></span>
        </a>
        <nav>
          {nav.filter((item) => !item.superOnly || isSuper).map((item) => (
            <button
              className={tab === item.id ? 'is-active' : ''}
              key={item.id}
              onClick={() => setTab(item.id)}
              type="button"
            >
              {item.label}
            </button>
          ))}
        </nav>
        <div className="pa-sidebar__account">
          <span>{viewer.name?.slice(0, 1).toUpperCase() || 'A'}</span>
          <div><strong>{viewer.name || viewer.email}</strong><small>{viewer.role}</small></div>
        </div>
      </aside>

      <main className="pa-main">
        <header className="pa-header">
          <div>
            <p>管理后台</p>
            <h1>{nav.find((item) => item.id === tab)?.label}</h1>
          </div>
          <div className="pa-header__actions">
            {busyCount > 0 && <span className="pa-busy">正在处理…</span>}
            <a className="pa-button pa-button--quiet" href="/pro">返回工作台</a>
          </div>
        </header>
        <ErrorBanner message={error} onClose={() => setError('')} />
        {notice && <div className="pa-banner pa-banner--success">{notice}</div>}

        {tab === 'overview' && <Overview />}
        {tab === 'users' && <UsersPage isSuper={isSuper} run={run} />}
        {tab === 'tenants' && <TenantsPage run={run} />}
        {tab === 'models' && <ModelsPage run={run} />}
        {tab === 'billing' && <BillingPage run={run} />}
        {tab === 'settings' && <SettingsPage run={run} />}
      </main>
    </div>
  )
}

function Metric({
  label,
  value,
  loading,
  hint,
}: {
  label: string
  value: ReactNode
  loading?: boolean
  hint?: string
}) {
  return (
    <article className="pa-card pa-metric">
      <small>{label}</small>
      {loading ? <span className="pa-skeleton pa-skeleton--value" /> : <strong>{value}</strong>}
      {hint && <p>{hint}</p>}
    </article>
  )
}

function Overview() {
  const [stats, setStats] = useState<AdminSystemStatsResponse | null>(null)
  const [statsLoading, setStatsLoading] = useState(true)
  const [statsError, setStatsError] = useState('')
  const [billing, setBilling] = useState<BillingAnalytics | null>(null)
  const [billingLoading, setBillingLoading] = useState(true)
  const [billingError, setBillingError] = useState('')

  useEffect(() => {
    let active = true
    void getGlobalStats().then((value) => {
      if (active) setStats(value)
    }).catch((reason) => {
      if (active) setStatsError(errorMessage(reason))
    }).finally(() => {
      if (active) setStatsLoading(false)
    })
    void getBillingAnalytics().then((value) => {
      if (active) setBilling(value)
    }).catch((reason) => {
      if (active) setBillingError(errorMessage(reason))
    }).finally(() => {
      if (active) setBillingLoading(false)
    })
    return () => { active = false }
  }, [])

  const legacyCount = billing?.legacy_unknown_count
  const estimateEligible = billing?.estimate_eligible_count
  const estimateAvailable = billing ? isBillingEstimateAvailable(billing) : false
  const estimateCoverage = estimateAvailable && legacyCount && estimateEligible !== undefined
    ? Math.min(100, estimateEligible / legacyCount * 100)
    : null

  return (
    <div className="pa-stack">
      {statsError && <ErrorBanner message={`基础统计加载失败：${statsError}`} />}
      <section className="pa-metrics">
        <Metric label="用户" loading={statsLoading} value={stats ? formatInteger(stats.basic.user_count) : '—'} />
        <Metric label="组织" loading={statsLoading} value={stats ? formatInteger(stats.basic.tenant_count) : '—'} />
        <Metric label="会话" loading={statsLoading} value={stats ? formatInteger(stats.basic.session_count) : '—'} />
        <Metric
          label="累计用量扣费"
          loading={billingLoading}
          value={billing ? `${formatNumber(billing.retail_dp, 4)} DP` : '—'}
        />
      </section>

      <section className="pa-card pa-section">
        <div className="pa-section__heading">
          <div><h2>计费表现</h2><p>精确金额来自不可变 usage ledger；未知历史记录不会伪装为零成本。</p></div>
        </div>
        {billingError && <ErrorBanner message={`计费分析加载失败：${billingError}`} />}
        {billingLoading ? (
          <div className="pa-summary-grid">
            {[0, 1, 2, 3].map((item) => <span className="pa-skeleton pa-skeleton--panel" key={item} />)}
          </div>
        ) : billing ? (
          <>
            <div className="pa-summary-grid pa-summary-grid--four">
              <div><small>已归因上游成本</small><strong>${formatNumber(billing.upstream_cost_usd, 6)}</strong></div>
              <div><small>已归因服务费</small><strong>{formatNumber(billing.service_fee_dp, 6)} DP</strong></div>
              <div><small>已归因记录</small><strong>{billing.attributed_usage_count === undefined ? '—' : `${formatInteger(billing.attributed_usage_count)} 条`}</strong></div>
              <div><small>全部用量记录</small><strong>{formatInteger(billing.usage_count)} 条</strong></div>
            </div>

            <div className="pa-subsection">
              <div className="pa-subsection__heading">
                <div>
                  <h3>历史成本可见性</h3>
                  <p>估算使用当前有效目录，只用于经营分析，不写回账本。</p>
                </div>
                {estimateAvailable && billing.estimate_catalog_version && (
                  <span className="pa-pill">估算目录 {billing.estimate_catalog_version}</span>
                )}
              </div>
              {!estimateAvailable && (
                <div className="pa-banner pa-banner--warning">
                  <span>当前目录估算暂不可用；精确账本汇总仍然有效。</span>
                  {billing.estimate_error && <small>{billing.estimate_error}</small>}
                </div>
              )}
              <div className="pa-summary-grid pa-summary-grid--four">
                <div><small>历史成本未知</small><strong>{legacyCount === undefined ? '—' : `${formatInteger(legacyCount)} 条`}</strong></div>
                <div><small>对应历史扣费</small><strong>{billing.legacy_unknown_retail_dp === undefined ? '—' : `${formatNumber(billing.legacy_unknown_retail_dp, 6)} DP`}</strong></div>
                <div><small>当前目录估算成本</small><strong>{!estimateAvailable || billing.estimated_legacy_upstream_cost_usd === undefined ? '—' : `$${formatNumber(billing.estimated_legacy_upstream_cost_usd, 6)}`}</strong></div>
                <div><small>估算覆盖率</small><strong>{estimateCoverage === null ? '—' : `${formatNumber(estimateCoverage, 1)}%`}</strong></div>
              </div>
              <div className="pa-footnotes">
                <span>BYOK：{billing.byok_usage_count === undefined ? '—' : `${formatInteger(billing.byok_usage_count)} 条`}</span>
                {billing.byok_service_fee_dp !== undefined && <span>BYOK 服务费：{formatNumber(billing.byok_service_fee_dp, 6)} DP</span>}
                <span>非 Provider：{billing.non_provider_usage_count === undefined ? '—' : `${formatInteger(billing.non_provider_usage_count)} 条`}</span>
                <span>缺少价格：{billing.unpriced_usage_count === undefined ? '—' : `${formatInteger(billing.unpriced_usage_count)} 条`}</span>
                {estimateAvailable && billing.estimated_legacy_service_fee_dp !== undefined && (
                  <span>估算历史服务费：{formatNumber(billing.estimated_legacy_service_fee_dp, 6)} DP</span>
                )}
              </div>
            </div>
          </>
        ) : (
          <div className="pa-empty">计费分析暂不可用。</div>
        )}
      </section>
    </div>
  )
}

interface CreateUserDraft {
  email: string
  name: string
  password: string
  role: 'user' | 'admin'
  tenant_id: string
  dreampoints: string
}

function UsersPage({ isSuper, run }: { isSuper: boolean; run: Runner }) {
  const pageSize = 20
  const [users, setUsers] = useState<User[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [showCreate, setShowCreate] = useState(false)
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [signupDefault, setSignupDefault] = useState(1)
  const [draft, setDraft] = useState<CreateUserDraft>({
    email: '',
    name: '',
    password: '',
    role: 'user',
    tenant_id: '',
    dreampoints: '1',
  })
  const [balanceDraft, setBalanceDraft] = useState<{
    user: User
    amount: string
    description: string
  } | null>(null)

  const load = useCallback(async () => {
    setLoading(true)
    const result = await run(() => listUsers(page, pageSize))
    if (result) {
      setUsers(result.users)
      setTotal(result.total)
    }
    setLoading(false)
  }, [page, run])

  useEffect(() => { void load() }, [load])

  useEffect(() => {
    if (!isSuper) return
    void run(() => Promise.all([listAllTenants(), getSystemSettings()])).then((result) => {
      if (!result) return
      const [allTenants, settings] = result
      setTenants(allTenants)
      setSignupDefault(settings.values.free_tier_dreampoints)
      setDraft((current) => ({
        ...current,
        tenant_id: current.tenant_id || allTenants[0]?.id || '',
        dreampoints: String(settings.values.free_tier_dreampoints),
      }))
    })
  }, [isSuper, run])

  function openCreate() {
    setDraft({
      email: '',
      name: '',
      password: '',
      role: 'user',
      tenant_id: tenants[0]?.id || '',
      dreampoints: String(signupDefault),
    })
    setShowCreate(true)
  }

  async function submitCreate(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const initialBalance = Number(draft.dreampoints)
    if (isSuper && (!draft.tenant_id || !Number.isFinite(initialBalance) || initialBalance < 0)) return
    const created = await run(() => createUser({
      email: draft.email.trim(),
      name: draft.name.trim(),
      password: draft.password,
      role: draft.role,
      ...(isSuper ? {
        tenant_id: draft.tenant_id,
        dreampoints: initialBalance,
      } : {}),
    }), '用户已创建')
    if (created) {
      setShowCreate(false)
      await load()
    }
  }

  async function saveBalance() {
    if (!balanceDraft) return
    const amount = Number(balanceDraft.amount)
    if (!Number.isFinite(amount) || amount === 0) return
    const result = await run(() => adjustUserBalance({
      user_id: balanceDraft.user.id,
      amount,
      description: balanceDraft.description.trim() || '管理员后台调整',
    }), '余额已更新')
    if (result !== undefined) {
      setBalanceDraft(null)
      await load()
    }
  }

  return (
    <>
      <section className="pa-card pa-section">
        <div className="pa-section__heading">
          <div><h2>账户与权限</h2><p>普通管理员只能管理自己组织内的普通用户。</p></div>
          <button className="pa-button pa-button--primary" onClick={openCreate} type="button">创建用户</button>
        </div>
        <div className="pa-table-wrap">
          <table>
            <thead><tr><th>用户</th><th>角色</th><th>余额</th><th>状态</th><th>操作</th></tr></thead>
            <tbody>
              {!loading && users.length === 0 && (
                <tr><td className="pa-table-empty" colSpan={5}>当前页没有用户。</td></tr>
              )}
              {loading && (
                <tr><td className="pa-table-empty" colSpan={5}>正在加载用户…</td></tr>
              )}
              {!loading && users.map((user) => (
                <tr key={user.id}>
                  <td><strong>{user.name || '未命名'}</strong><small>{user.email}</small></td>
                  <td><span className="pa-pill">{user.role}</span></td>
                  <td>{formatNumber(user.dreampoints, 4)} DP</td>
                  <td><span className={`pa-status ${user.is_active ? 'is-good' : 'is-muted'}`}>{user.is_active ? '启用' : '停用'}</span></td>
                  <td className="pa-actions">
                    {isSuper && (
                      <button onClick={() => setBalanceDraft({
                        user,
                        amount: '',
                        description: '',
                      })} type="button">调整余额</button>
                    )}
                    <button onClick={() => void run(async () => {
                      await updateUser(user.id, { is_active: !user.is_active })
                      await load()
                    }, user.is_active ? '用户已停用' : '用户已启用')} type="button">
                      {user.is_active ? '停用' : '启用'}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <Pagination page={page} pageSize={pageSize} total={total} onChange={setPage} />
      </section>

      {showCreate && (
        <Modal
          footer={(
            <>
              <button className="pa-button pa-button--quiet" onClick={() => setShowCreate(false)} type="button">取消</button>
              <button className="pa-button pa-button--primary" form="pa-create-user" type="submit">创建用户</button>
            </>
          )}
          onClose={() => setShowCreate(false)}
          title="创建用户"
        >
          <form className="pa-dialog-form" id="pa-create-user" onSubmit={submitCreate}>
            <label><span>邮箱</span><input autoFocus onChange={(event) => setDraft({ ...draft, email: event.target.value })} required type="email" value={draft.email} /></label>
            <label><span>姓名</span><input maxLength={100} onChange={(event) => setDraft({ ...draft, name: event.target.value })} placeholder="留空时使用邮箱前缀" value={draft.name} /></label>
            <label><span>初始密码</span><input maxLength={72} minLength={10} onChange={(event) => setDraft({ ...draft, password: event.target.value })} placeholder="10–72 个字符" required type="password" value={draft.password} /></label>
            <label><span>角色</span><select onChange={(event) => setDraft({ ...draft, role: event.target.value as CreateUserDraft['role'] })} value={draft.role}>
              <option value="user">用户</option>
              {isSuper && <option value="admin">管理员</option>}
            </select></label>
            {isSuper && (
              <>
                <label><span>所属组织</span><select onChange={(event) => setDraft({ ...draft, tenant_id: event.target.value })} required value={draft.tenant_id}>
                  <option disabled value="">请选择组织</option>
                  {tenants.map((tenant) => <option key={tenant.id} value={tenant.id}>{tenant.name} ({tenant.slug})</option>)}
                </select></label>
                <label>
                  <span>初始余额（单次覆盖）</span>
                  <input min="0" onChange={(event) => setDraft({ ...draft, dreampoints: event.target.value })} required step="0.0001" type="number" value={draft.dreampoints} />
                  <small>系统注册默认值为 {formatNumber(signupDefault, 4)} DP；这里只影响本次创建。</small>
                </label>
              </>
            )}
            {!isSuper && <p className="pa-form-note">初始余额使用系统注册默认值，普通管理员不能覆盖。</p>}
          </form>
        </Modal>
      )}

      {balanceDraft && (
        <Modal
          footer={(
            <>
              <button className="pa-button pa-button--quiet" onClick={() => setBalanceDraft(null)} type="button">取消</button>
              <button className="pa-button pa-button--primary" disabled={!Number(balanceDraft.amount)} onClick={() => void saveBalance()} type="button">确认调整</button>
            </>
          )}
          onClose={() => setBalanceDraft(null)}
          title="调整 DreamPoints 余额"
        >
          <div className="pa-dialog-form">
            <div className="pa-callout"><strong>{balanceDraft.user.email}</strong><span>当前余额 {formatNumber(balanceDraft.user.dreampoints, 4)} DP</span></div>
            <label><span>调整金额</span><input autoFocus onChange={(event) => setBalanceDraft({ ...balanceDraft, amount: event.target.value })} placeholder="正数增加，负数扣减" step="0.0001" type="number" value={balanceDraft.amount} /></label>
            <label><span>操作备注</span><input maxLength={200} onChange={(event) => setBalanceDraft({ ...balanceDraft, description: event.target.value })} placeholder="说明本次调整原因" value={balanceDraft.description} /></label>
          </div>
        </Modal>
      )}
    </>
  )
}

function TenantsPage({ run }: { run: Runner }) {
  const pageSize = 20
  const [tenants, setTenants] = useState<Tenant[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    const result = await run(() => listTenants(page, pageSize))
    if (result) {
      setTenants(result.tenants)
      setTotal(result.total)
    }
    setLoading(false)
  }, [page, run])

  useEffect(() => { void load() }, [load])

  return (
    <section className="pa-card pa-section">
      <div className="pa-section__heading"><div><h2>组织与配额</h2><p>套餐变更立即影响后续配额检查。</p></div></div>
      <div className="pa-table-wrap"><table>
        <thead><tr><th>组织</th><th>套餐</th><th>API / 月</th><th>存储</th><th>会话数</th></tr></thead>
        <tbody>
          {loading && <tr><td className="pa-table-empty" colSpan={5}>正在加载组织…</td></tr>}
          {!loading && tenants.length === 0 && <tr><td className="pa-table-empty" colSpan={5}>当前页没有组织。</td></tr>}
          {!loading && tenants.map((tenant) => (
            <tr key={tenant.id}>
              <td><strong>{tenant.name}</strong><small>{tenant.slug}</small></td>
              <td><select value={tenant.plan} onChange={(event) => void run(async () => {
                await updateTenant(tenant.id, { plan: event.target.value })
                await load()
              }, '套餐已更新')}>
                <option value="free">Free</option><option value="pro">Pro</option><option value="enterprise">Enterprise</option>
              </select></td>
              <td>{formatInteger(tenant.api_quota_monthly)}</td>
              <td>{tenant.storage_quota_gb} GB</td>
              <td>{tenant.max_sessions}</td>
            </tr>
          ))}
        </tbody>
      </table></div>
      <Pagination page={page} pageSize={pageSize} total={total} onChange={setPage} />
    </section>
  )
}

interface ModelCostDraft {
  model: ProviderModel
  service: 'llm' | 'embedding'
  input: string
  cachedInput: string
  cacheWrite: string
  output: string
  original: [number | null, number | null, number | null, number | null]
}

function modelAvailability(model: ProviderModel): ProviderAvailability {
  if (model.availability_status) return model.availability_status
  if (model.provider_available) return 'confirmed'
  if (model.source === 'builtin') return 'unverified'
  return 'stale'
}

function modelAvailabilityCopy(status: ProviderAvailability) {
  if (status === 'confirmed' || status === 'provider_confirmed') return { label: 'Provider 已确认', className: 'is-good' }
  if (status === 'unverified' || status === 'builtin_unverified') return { label: '内置但未验证', className: 'is-warn' }
  return { label: '暂时不可用', className: 'is-muted' }
}

function isModelUnavailable(model: ProviderModel) {
  const status = modelAvailability(model)
  return !model.provider_available
    || status === 'temporarily_unavailable'
    || status === 'unavailable'
    || status === 'stale'
}

function catalogSyncStatusCopy(catalog: ModelCatalog | null) {
  if (catalog?.status === 'provider_confirmed') {
    return { label: 'Provider 同步已确认', className: 'is-good' }
  }
  if (catalog?.status === 'builtin_unverified') {
    return { label: '内置目录尚未验证', className: 'is-warn' }
  }
  if (catalog?.status === 'temporarily_unavailable') {
    return { label: 'Provider 暂时不可用', className: 'is-bad' }
  }
  if (catalog?.last_error) return { label: '最近同步失败', className: 'is-bad' }
  return { label: '等待首次同步', className: 'is-muted' }
}

function costInputValue(value: number | null) {
  return value === null ? '' : String(value)
}

function createModelCostDraft(
  model: ProviderModel,
  service: ModelCostDraft['service'],
  rates: CostRate[],
): ModelCostDraft {
  const values: [number | null, number | null, number | null, number | null] = [
    getModelRateCostPerMillion(rates, service, model.model_id, 'input_token'),
    getModelRateCostPerMillion(rates, service, model.model_id, 'cached_input_token'),
    getModelRateCostPerMillion(rates, service, model.model_id, 'cache_write_token'),
    getModelRateCostPerMillion(rates, service, model.model_id, 'output_token'),
  ]
  return {
    model,
    service,
    input: costInputValue(values[0]),
    cachedInput: costInputValue(values[1]),
    cacheWrite: costInputValue(values[2]),
    output: costInputValue(values[3]),
    original: values,
  }
}

function ModelsPage({ run }: { run: Runner }) {
  const [catalog, setCatalog] = useState<ModelCatalog | null>(null)
  const [billingCatalog, setBillingCatalog] = useState<BillingCatalog | null>(null)
  const [costDraft, setCostDraft] = useState<ModelCostDraft | null>(null)
  const [loading, setLoading] = useState(true)
  const refreshingRef = useRef(false)
  const [refreshing, setRefreshing] = useState(false)
  const purposes = Object.keys(purposeLabels) as ModelPolicy['purpose'][]

  const load = useCallback(async () => {
    setLoading(true)
    const result = await run(() => Promise.all([getModelCatalog(), getBillingCatalog()]))
    if (result) {
      setCatalog(result[0])
      setBillingCatalog(result[1])
    }
    setLoading(false)
  }, [run])

  useEffect(() => { void load() }, [load])

  async function refreshCatalog() {
    if (refreshingRef.current) return
    refreshingRef.current = true
    setRefreshing(true)
    try {
      await run(async () => {
        try {
          await refreshModelCatalog()
        } finally {
          setCatalog(await getModelCatalog())
        }
      }, '模型目录已刷新')
    } finally {
      refreshingRef.current = false
      setRefreshing(false)
    }
  }

  async function changePolicy(
    modelId: string,
    purpose: ModelPolicy['purpose'],
    patch: Partial<ModelPolicy>,
  ) {
    const model = catalog?.models.find((item) => item.model_id === modelId)
    if (!model || isModelUnavailable(model)) return
    const existing = model.policies.find((policy) => policy.purpose === purpose)
    const next: ModelPolicy = {
      purpose,
      model_id: modelId,
      is_approved: existing?.is_approved ?? false,
      is_default: existing?.is_default ?? false,
      cost_confirmed: existing?.cost_confirmed ?? false,
      ...patch,
    }
    const result = await run(() => updateModelPolicy(next), '模型策略已更新')
    if (result) setCatalog(result)
  }

  function openModelCost(model: ProviderModel) {
    const rates = billingCatalog?.rates || []
    const embedding = model.policies.some((policy) => policy.purpose === 'embedding')
      && !model.policies.some((policy) => policy.purpose !== 'embedding')
    setCostDraft(createModelCostDraft(model, embedding ? 'embedding' : 'llm', rates))
  }

  async function saveModelCost() {
    if (!costDraft) return
    const values = [
      Number(costDraft.input),
      costDraft.cachedInput === '' ? 0 : Number(costDraft.cachedInput),
      costDraft.cacheWrite === '' ? 0 : Number(costDraft.cacheWrite),
      costDraft.output === '' ? 0 : Number(costDraft.output),
    ]
    if (costDraft.input === '' || values.some((value) => !Number.isFinite(value) || value < 0)) return
    const result = await run(async () => {
      await updateModelCost({
        model_id: costDraft.model.model_id,
        service: costDraft.service,
        input_per_million_usd: values[0],
        cached_input_per_million_usd: values[1],
        cache_write_per_million_usd: values[2],
        output_per_million_usd: values[3],
      })
      return Promise.all([getModelCatalog(), getBillingCatalog()])
    }, '模型成本已保存')
    if (result) {
      setCatalog(result[0])
      setBillingCatalog(result[1])
      setCostDraft(null)
    }
  }

  const costChanged = costDraft ? [
    Number(costDraft.input || 0),
    Number(costDraft.cachedInput || 0),
    Number(costDraft.cacheWrite || 0),
    Number(costDraft.output || 0),
  ].some((value, index) => value !== (costDraft.original[index] ?? 0)) : false
  const syncStatus = catalogSyncStatusCopy(catalog)

  return (
    <>
      <section className="pa-card pa-section">
        <div className="pa-section__heading">
          <div><h2>Provider 模型目录</h2><p>自动同步状态会持久化；新模型默认不开放，缺少有效成本时不能审批。</p></div>
          <button className="pa-button pa-button--primary" disabled={refreshing} onClick={() => void refreshCatalog()} type="button">
            {refreshing ? '正在刷新…' : '立即刷新'}
          </button>
        </div>
        <div className="pa-provider-status">
          <span className={`pa-status ${syncStatus.className}`}>{syncStatus.label}</span>
          <span>最近尝试：{formatDate(catalog?.last_attempt_at)}</span>
          <span>最近成功：{formatDate(catalog?.last_success_at)}</span>
          {catalog?.last_error && <span className="pa-provider-error">{catalog.last_error}</span>}
        </div>
        <div className="pa-table-wrap"><table>
          <thead><tr><th>模型</th><th>Provider</th><th>状态</th><th>允许用途</th></tr></thead>
          <tbody>
            {loading && <tr><td className="pa-table-empty" colSpan={4}>正在加载模型目录…</td></tr>}
            {!loading && catalog?.models.length === 0 && <tr><td className="pa-table-empty" colSpan={4}>模型目录为空。</td></tr>}
            {!loading && catalog?.models.map((model) => {
              const availability = modelAvailabilityCopy(modelAvailability(model))
              const unavailable = isModelUnavailable(model)
              const costConfirmed = model.policies.some((policy) => policy.cost_confirmed)
              return (
                <tr key={model.model_id}>
                  <td>
                    <strong>{model.model_id}</strong>
                    <small>{model.source} · {costConfirmed ? '有效成本已配置' : '缺少有效成本'}</small>
                  </td>
                  <td>{model.provider}</td>
                  <td>
                    <span className={`pa-status ${availability.className}`}>{availability.label}</span>
                    <button className="pa-link-button" onClick={() => openModelCost(model)} type="button">
                      {costConfirmed ? '查看或修改成本' : '配置成本'}
                    </button>
                  </td>
                  <td><div className="pa-policy-grid">
                    {purposes.map((purpose) => {
                      const policy = model.policies.find((item) => item.purpose === purpose)
                      const approved = policy?.is_approved ?? false
                      return (
                        <div className="pa-policy" key={purpose}>
                          <button
                            className={approved ? 'is-approved' : ''}
                            disabled={unavailable || (!policy?.cost_confirmed && !approved)}
                            onClick={() => void changePolicy(model.model_id, purpose, {
                              is_approved: !approved,
                              is_default: approved ? false : policy?.is_default ?? false,
                            })}
                            title={unavailable
                              ? '模型当前不可用，不能更改审批状态'
                              : !policy?.cost_confirmed ? '请先配置该模型的有效上游成本' : ''}
                            type="button"
                          >{purposeLabels[purpose]}{approved ? ' ✓' : ''}</button>
                          {approved && (
                            <button
                              className={policy?.is_default ? 'is-default' : ''}
                              disabled={unavailable}
                              onClick={() => void changePolicy(model.model_id, purpose, { is_default: true })}
                              title={unavailable ? '模型当前不可用，不能设为默认' : ''}
                              type="button"
                            >{policy?.is_default ? '默认' : '设为默认'}</button>
                          )}
                        </div>
                      )
                    })}
                  </div></td>
                </tr>
              )
            })}
          </tbody>
        </table></div>
      </section>

      {costDraft && (
        <Modal
          footer={(
            <>
              <button className="pa-button pa-button--quiet" onClick={() => setCostDraft(null)} type="button">取消</button>
              <button className="pa-button pa-button--primary" disabled={!costChanged || costDraft.input === ''} onClick={() => void saveModelCost()} type="button">保存成本</button>
            </>
          )}
          onClose={() => setCostDraft(null)}
          title={`模型成本 · ${costDraft.model.model_id}`}
        >
          <div className="pa-dialog-form">
            <p className="pa-form-note">
              以下数值均为 USD / 百万 token，并按当前服务类型从有效目录预填。
              {costDraft.service === 'llm' && ' 缓存价格留空时会保留或恢复目录中的对应缓存价格。'}
            </p>
            <label><span>服务类型</span><select onChange={(event) => setCostDraft(createModelCostDraft(
              costDraft.model,
              event.target.value as ModelCostDraft['service'],
              billingCatalog?.rates || [],
            ))} value={costDraft.service}>
              <option value="llm">LLM</option><option value="embedding">Embedding</option>
            </select></label>
            <div className="pa-dialog-grid">
              <label><span>输入</span><input autoFocus min="0" onChange={(event) => setCostDraft({ ...costDraft, input: event.target.value })} required step="0.000001" type="number" value={costDraft.input} /></label>
              {costDraft.service === 'llm' && (
                <>
                  <label><span>缓存输入</span><input min="0" onChange={(event) => setCostDraft({ ...costDraft, cachedInput: event.target.value })} placeholder="保留/恢复目录价" step="0.000001" type="number" value={costDraft.cachedInput} /></label>
                  <label><span>缓存写入</span><input min="0" onChange={(event) => setCostDraft({ ...costDraft, cacheWrite: event.target.value })} placeholder="保留/恢复目录价" step="0.000001" type="number" value={costDraft.cacheWrite} /></label>
                  <label><span>输出</span><input min="0" onChange={(event) => setCostDraft({ ...costDraft, output: event.target.value })} step="0.000001" type="number" value={costDraft.output} /></label>
                </>
              )}
            </div>
            {costChanged && <div className="pa-callout pa-callout--warning">保存后将以这些数值覆盖该模型当前成本，并影响后续请求的售价。</div>}
          </div>
        </Modal>
      )}
    </>
  )
}

interface CostOverrideDraft {
  rate: CostRate
  cost: string
  sourceLabel: string
  effectiveAt: string
}

function pricingStateCopy(catalog: BillingCatalog | null) {
  const state = catalog?.pricing_state
  if (state === 'managed_current') return { label: '托管规则已是最新', className: 'is-good' }
  if (state === 'managed_outdated') return { label: '托管规则有待应用更新', className: 'is-warn' }
  return { label: '旧版实际规则仍在生效', className: 'is-bad' }
}

function hasCostOverride(rate: CostRate) {
  return Boolean(
    rate.cost_override_id
    || rate.override_id
    || rate.cost_source === 'contract_override',
  )
}

function editableCostSourceLabel(rate: CostRate) {
  return hasCostOverride(rate) ? rate.cost_source_label || '管理员合同价' : '管理员合同价'
}

function costSourceLabel(rate: CostRate) {
  if (hasCostOverride(rate)) {
    return rate.cost_source_label || '管理员合同价'
  }
  if (rate.cost_source === 'manual') return '模型人工成本'
  return '公开目录价'
}

function BillingPage({ run }: { run: Runner }) {
  const [catalog, setCatalog] = useState<BillingCatalog | null>(null)
  const [dpPerUsd, setDPPerUSD] = useState(1)
  const [markup, setMarkup] = useState(50)
  const [overrides, setOverrides] = useState<MarkupOverride[]>([])
  const [configPreview, setConfigPreview] = useState<BillingPreview | null>(null)
  const [applyPreview, setApplyPreview] = useState<BillingPreview | null>(null)
  const [applyText, setApplyText] = useState('')
  const [resetPreview, setResetPreview] = useState<BillingPreview | null>(null)
  const [resetText, setResetText] = useState('')
  const [costDraft, setCostDraft] = useState<CostOverrideDraft | null>(null)
  const [configSaveError, setConfigSaveError] = useState('')
  const [costDraftError, setCostDraftError] = useState('')
  const [configSaving, setConfigSaving] = useState(false)
  const [costSaving, setCostSaving] = useState(false)
  const configWriteInFlight = useRef(false)
  const costWriteInFlight = useRef(false)
  const [exampleHours, setExampleHours] = useState(10)
  const [loading, setLoading] = useState(true)

  const clearPreviews = useCallback(() => {
    setConfigPreview(null)
    setApplyPreview(null)
    setApplyText('')
    setResetPreview(null)
    setResetText('')
  }, [])

  const adopt = useCallback((next: BillingCatalog) => {
    const editable = getBillingCatalogEditableConfig(next)
    setCatalog(next)
    setDPPerUSD(editable.dp_per_usd)
    setMarkup(editable.default_markup_percent)
    setOverrides(editable.overrides)
    clearPreviews()
  }, [clearPreviews])

  const load = useCallback(async () => {
    setLoading(true)
    const result = await run(() => getBillingCatalog())
    if (result) adopt(result)
    setLoading(false)
  }, [adopt, run])

  useEffect(() => { void load() }, [load])

  const markChanged = useCallback(() => {
    clearPreviews()
    setConfigSaveError('')
  }, [clearPreviews])
  const input: BillingConfigInput = { dp_per_usd: dpPerUsd, default_markup_percent: markup, overrides }
  const configInputError = validateBillingConfigInput(input)
  const persistedEditableConfig = catalog ? getBillingCatalogEditableConfig(catalog) : null
  const dirty = persistedEditableConfig
    ? JSON.stringify(input) !== JSON.stringify(persistedEditableConfig)
    : false
  const displayedRates = useMemo(
    () => configPreview?.rates || applyPreview?.rates || resetPreview?.rates || catalog?.rates || [],
    [applyPreview, catalog, configPreview, resetPreview],
  )
  const realtime = useMemo(() => displayedRates.find(
    (rate) => rate.sku === 'speechmatics-realtime-enhanced' && rate.unit_type === 'hour',
  ), [displayedRates])
  const realtimeMarkup = realtime ? previewMarkup(realtime, markup, overrides) : markup
  const calculatedRealtime = realtime
    ? getRateEffectiveCost(realtime) * dpPerUsd * (1 + realtimeMarkup / 100)
    : 0
  const pricingState = pricingStateCopy(catalog)

  async function saveConfig() {
    if (configWriteInFlight.current) return
    setConfigSaveError('')
    if (configInputError) {
      setConfigSaveError(configInputError)
      return
    }
    configWriteInFlight.current = true
    setConfigSaving(true)
    try {
      const managedCurrent = catalog?.pricing_state === 'managed_current'
      const result = await run(
        () => updateBillingConfig(input),
        managedCurrent ? '加价配置已保存并应用' : '加价方案已保存，尚未启用',
        setConfigSaveError,
      )
      if (result) adopt(result)
    } finally {
      configWriteInFlight.current = false
      setConfigSaving(false)
    }
  }

  async function openApplyPreview() {
    const result = await run(() => previewBillingCatalogApply())
    if (result) {
      setApplyPreview(result)
      setApplyText('')
      setConfigPreview(null)
    }
  }

  async function rejectStalePreview(): Promise<never> {
    clearPreviews()
    try {
      adopt(await getBillingCatalog())
    } catch {
      // The stale preview is still invalidated even if the recovery reload fails.
    }
    throw new Error('配置已变化，请重新预览')
  }

  async function confirmApplyCatalog() {
    if (
      !applyPreview
      || applyText !== applyPreview.confirmation
      || !hasBillingPreviewRevision(applyPreview)
    ) return
    const version = applyPreview.target_version
      || applyPreview.catalog_version
      || applyPreview.config.catalog_version
      || catalog?.builtin_version
      || ''
    const result = await run(async () => {
      try {
        await applyBillingCatalog({
          confirmation: applyText,
          catalog_version: version,
          current_revision: applyPreview.current_revision || '',
        })
        return await getBillingCatalog()
      } catch (reason) {
        if (isStaleBillingPreviewError(reason)) await rejectStalePreview()
        throw reason
      }
    }, '成本目录与托管规则已更新')
    if (result) adopt(result)
  }

  async function confirmBillingReset() {
    if (
      !resetPreview
      || resetText !== resetPreview.confirmation
      || !hasBillingPreviewRevision(resetPreview)
    ) return
    const result = await run(async () => {
      try {
        return await resetBillingDefaults(
          resetText,
          resetPreview.current_revision || '',
        )
      } catch (reason) {
        if (isStaleBillingPreviewError(reason)) await rejectStalePreview()
        throw reason
      }
    }, '整套计费配置已恢复为最新默认')
    if (result) adopt(result)
  }

  async function saveCostOverride() {
    if (!costDraft || costWriteInFlight.current) return
    setCostDraftError('')
    const editorCost = Number(costDraft.cost)
    if (!Number.isFinite(editorCost) || editorCost < 0) {
      setCostDraftError('合同成本必须是有效的非负数字')
      return
    }
    const cost = editorCost / costEditorScale(costDraft.rate.unit_type)
    const effectiveAt = costDraft.effectiveAt ? new Date(costDraft.effectiveAt) : null
    if (!costDraft.sourceLabel.trim() || (effectiveAt && Number.isNaN(effectiveAt.getTime()))) {
      setCostDraftError('请填写成本来源，并检查生效时间')
      return
    }
    const overrideInput = {
      provider: costDraft.rate.provider,
      sku: costDraft.rate.sku,
      service: costDraft.rate.service,
      unit_type: costDraft.rate.unit_type,
      cost_per_unit_usd: cost,
      source_label: costDraft.sourceLabel.trim(),
      ...(effectiveAt ? { effective_at: effectiveAt.toISOString() } : {}),
    }
    const validationError = validateCostOverrideInput(overrideInput)
    if (validationError) {
      setCostDraftError(validationError)
      return
    }
    costWriteInFlight.current = true
    setCostSaving(true)
    try {
      const result = await run(
        () => putCostOverride(overrideInput),
        '合同成本覆盖已保存',
        setCostDraftError,
      )
      if (result) {
        adopt(result)
        setCostDraft(null)
      }
    } finally {
      costWriteInFlight.current = false
      setCostSaving(false)
    }
  }

  async function removeCostOverride() {
    if (!costDraft || costWriteInFlight.current) return
    setCostDraftError('')
    costWriteInFlight.current = true
    setCostSaving(true)
    try {
      const result = await run(() => deleteCostOverride({
        provider: costDraft.rate.provider,
        sku: costDraft.rate.sku,
        service: costDraft.rate.service,
        unit_type: costDraft.rate.unit_type,
      }), '合同成本覆盖已撤销', setCostDraftError)
      if (result) {
        adopt(result)
        setCostDraft(null)
      }
    } finally {
      costWriteInFlight.current = false
      setCostSaving(false)
    }
  }

  return (
    <>
      <div className="pa-stack">
        <section className="pa-card pa-section">
          <div className="pa-section__heading">
            <div>
              <h2>成本加价设置</h2>
              <p>售价 = 有效上游成本 × DP/USD ×（1 + 加价率）。50% 加价表示成本 1 DP 时售价 1.5 DP。</p>
            </div>
            <div className="pa-heading-badges">
              <span className={`pa-status ${pricingState.className}`}>{pricingState.label}</span>
              <span className={`pa-version ${catalog?.has_update ? 'has-update' : ''}`}>
                目录 {catalog?.installed_version || '—'} / 内置 {catalog?.builtin_version || '—'}
              </span>
            </div>
          </div>
          {catalog?.pricing_state === 'legacy_active' && (
            <div className="pa-banner pa-banner--warning">
              当前请求仍按旧版实际规则扣费。下面的目录售价是待应用方案，请先预览并确认应用。为避免错误扣费，使用用户自带 Provider Key 的请求会在应用最新目录前被阻止。
            </div>
          )}
          {catalog?.pricing_state === 'managed_outdated' && (
            <div className="pa-banner pa-banner--warning">
              当前托管目录已过期。保存只会更新待应用加价方案；请通过“预览成本目录应用”确认后再启用。在此之前，使用用户自带 Provider Key 的请求会被阻止。
            </div>
          )}
          {catalog?.pending_config && (
            <div className="pa-banner pa-banner--info">
              <strong>已保存待应用方案：</strong>
              1 USD = {formatNumber(catalog.pending_config.dp_per_usd, 6)} DP，
              默认加价 {formatNumber(catalog.pending_config.default_markup_percent, 4)}%。
              当前实际扣费仍使用 1 USD = {formatNumber(catalog.config.dp_per_usd, 6)} DP，
              默认加价 {formatNumber(catalog.config.default_markup_percent, 4)}%。
            </div>
          )}
          <div className="pa-form-grid">
            <label><span>1 USD 可兑换 DP</span><input disabled={loading || configSaving} min="0.000001" onChange={(event) => {
              setDPPerUSD(Number(event.target.value))
              markChanged()
            }} step="0.01" type="number" value={dpPerUsd} /></label>
            <label><span>默认成本加价率</span><div className="pa-input-suffix"><input disabled={loading || configSaving} min="0" onChange={(event) => {
              setMarkup(Number(event.target.value))
              markChanged()
            }} step="1" type="number" value={markup} /><i>%</i></div></label>
            <div className="pa-form-result"><small>对应毛利率</small><strong>{formatNumber(markup / (100 + markup) * 100)}%</strong></div>
          </div>
          <div className="pa-example">
            <div><small>Realtime Enhanced 方案售价</small><strong>{realtime ? `${formatNumber(calculatedRealtime, 4)} DP / 小时` : '—'}</strong></div>
            <label><span>示例时长</span><input min="0" onChange={(event) => setExampleHours(Number(event.target.value))} type="number" value={exampleHours} /></label>
            <div><small>用户支付</small><strong>{realtime ? `${formatNumber(calculatedRealtime * exampleHours, 4)} DP` : '—'}</strong></div>
          </div>
          <div className="pa-button-row">
            <button className="pa-button pa-button--quiet" disabled={loading || configSaving || Boolean(configInputError)} onClick={() => void run(async () => {
              setConfigPreview(await previewBillingConfig(input))
              setApplyPreview(null)
              setResetPreview(null)
            })} type="button">预览全部售价</button>
            <button className="pa-button pa-button--primary" disabled={!dirty || loading || configSaving || Boolean(configInputError)} onClick={() => void saveConfig()} type="button">
              {configSaving ? '正在保存…' : catalog?.pricing_state === 'managed_current' ? '保存并应用加价' : '保存加价方案'}
            </button>
            {(catalog?.has_update || catalog?.pricing_state !== 'managed_current') && (
              <button className="pa-button pa-button--quiet" disabled={dirty || configSaving} onClick={() => void openApplyPreview()} title={dirty ? '请先保存或撤销未保存的加价修改' : ''} type="button">预览成本目录应用</button>
            )}
          </div>
          {(configInputError || configSaveError) && (
            <div className="pa-callout pa-callout--danger" role="alert">
              {configInputError || configSaveError}
            </div>
          )}
          {configPreview && <div className="pa-preview-note">正在显示加价配置预览；尚未保存。任何编辑都会清除此预览。</div>}
        </section>

        <section className="pa-card pa-section">
          <div className="pa-section__heading"><div><h2>分级加价</h2><p>具体 SKU 优先于类别，类别优先于 Provider。</p></div>
            <button className="pa-button pa-button--quiet" disabled={configSaving} onClick={() => {
              setOverrides((current) => [...current, { scope_type: 'provider', scope_key: '', markup_percent: markup }])
              markChanged()
            }} type="button">添加覆盖</button>
          </div>
          {overrides.length === 0 ? <div className="pa-empty">当前全部使用全局加价率。</div> : (
            <div className="pa-override-list">{overrides.map((override, index) => (
              <div className="pa-override" key={`${override.scope_type}-${index}`}>
                <select disabled={configSaving} value={override.scope_type} onChange={(event) => {
                  setOverrides((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, scope_type: event.target.value as MarkupOverride['scope_type'] } : item))
                  markChanged()
                }}>
                  <option value="provider">Provider</option><option value="category">服务类别</option><option value="sku">具体 SKU</option>
                </select>
                <input disabled={configSaving} onChange={(event) => {
                  setOverrides((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, scope_key: event.target.value } : item))
                  markChanged()
                }} placeholder="例如 openai-compatible" value={override.scope_key} />
                <input disabled={configSaving} min="0" onChange={(event) => {
                  setOverrides((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, markup_percent: Number(event.target.value) } : item))
                  markChanged()
                }} type="number" value={override.markup_percent} />
                <span>%</span>
                <button disabled={configSaving} onClick={() => {
                  setOverrides((current) => current.filter((_, itemIndex) => itemIndex !== index))
                  markChanged()
                }} type="button">删除</button>
              </div>
            ))}</div>
          )}
        </section>

        <section className="pa-card pa-section">
          <div className="pa-section__heading">
            <div><h2>公开成本、有效成本与售价</h2><p>公开价来自版本化目录；有效价可能被管理员合同价覆盖。编辑前会从当前值预填。</p></div>
          </div>
          <div className="pa-table-wrap"><table>
            <thead><tr><th>SKU</th><th>公开 / 有效成本</th><th>实际 / 待应用售价</th><th>加价 / 毛利</th><th>操作</th></tr></thead>
            <tbody>
              {loading && <tr><td className="pa-table-empty" colSpan={5}>正在加载计费目录…</td></tr>}
              {!loading && displayedRates.filter((rate) => rate.is_active).map((rate) => {
                const publicCost = getRatePublicCost(rate)
                const effectiveCost = getRateEffectiveCost(rate)
                const effectiveRetail = getRateEffectiveRetail(rate)
                const proposedRetail = rate.proposed_retail_dp_per_unit
                const actualByAction = Object.entries(rate.effective_retail_by_action || {})
                const hasSplitActualRetail = effectiveRetail === null && actualByAction.length > 0
                return (
                  <tr key={`${rate.provider}-${rate.service}-${rate.sku}-${rate.unit_type}`}>
                    <td><strong>{rate.sku}</strong><small>{rate.provider} · {rate.service} · {rate.unit_type}</small></td>
                    <td>
                      <span>{formatCost(publicCost, rate.unit_type)}</span>
                      <small>有效：{formatCost(effectiveCost, rate.unit_type)} · {costSourceLabel(rate)}</small>
                      <small>生效：{formatDate(rate.effective_at)}</small>
                    </td>
                    <td>
                      <span>
                        {effectiveRetail === null
                          ? hasSplitActualRetail ? '当前按用途执行多档价格' : '—（未配置实际规则）'
                          : formatRate(effectiveRetail, rate.unit_type)}
                      </span>
                      {hasSplitActualRetail && actualByAction.map(([action, value]) => (
                        <small key={action}>{action}：{formatRate(value, rate.unit_type)}</small>
                      ))}
                      {proposedRetail !== undefined && (effectiveRetail === null || proposedRetail !== effectiveRetail) && (
                        <small className="pa-text-warning">待应用：{formatRate(proposedRetail, rate.unit_type)}</small>
                      )}
                    </td>
                    <td>{formatNumber(rate.markup_percent)}% <small>毛利 {formatNumber(rate.gross_margin_percent)}%</small></td>
                    <td><button className="pa-link-button" disabled={dirty} onClick={() => {
                      setCostDraftError('')
                      setCostDraft({
                        rate,
                        cost: String(effectiveCost * costEditorScale(rate.unit_type)),
                        sourceLabel: editableCostSourceLabel(rate),
                        effectiveAt: hasCostOverride(rate)
                          ? toDateTimeLocal(rate.effective_at)
                          : '',
                      })
                    }} title={dirty ? '请先保存或撤销未保存的加价修改' : ''} type="button">编辑合同成本</button></td>
                  </tr>
                )
              })}
            </tbody>
          </table></div>
        </section>

        <section className="pa-card pa-section pa-danger-zone">
          <div className="pa-section__heading">
            <div><h2>恢复最新默认计费</h2><p>恢复公开成本、1 DP/USD、50% 加价，清除所有人工覆盖；余额和历史账本不会改变。</p></div>
            <button className="pa-button pa-button--danger" onClick={() => void run(async () => {
              setResetPreview(await previewBillingReset())
              setResetText('')
              setApplyPreview(null)
              setConfigPreview(null)
            })} type="button">预览完整重置</button>
          </div>
        </section>
      </div>

      {costDraft && (
        <Modal
          footer={(
            <>
              {hasCostOverride(costDraft.rate) && (
                <button className="pa-button pa-button--danger-quiet" disabled={costSaving} onClick={() => void removeCostOverride()} type="button">撤销覆盖</button>
              )}
              <span className="pa-modal__spacer" />
              <button className="pa-button pa-button--quiet" disabled={costSaving} onClick={() => setCostDraft(null)} type="button">取消</button>
              <button className="pa-button pa-button--primary" disabled={
                costSaving
                || costDraft.cost === ''
                || costDraft.sourceLabel.trim() === ''
                || (
                  Number(costDraft.cost) === getRateEffectiveCost(costDraft.rate)
                    * costEditorScale(costDraft.rate.unit_type)
                  && costDraft.sourceLabel === editableCostSourceLabel(costDraft.rate)
                  && costDraft.effectiveAt === toDateTimeLocal(costDraft.rate.effective_at)
                )
              } onClick={() => void saveCostOverride()} type="button">
                {costSaving ? '正在保存…' : '保存合同成本'}
              </button>
            </>
          )}
          onClose={() => { if (!costSaving) setCostDraft(null) }}
          title="编辑上游合同成本"
        >
          <div className="pa-dialog-form">
            {costDraftError && <ErrorBanner message={costDraftError} />}
            <div className="pa-callout"><strong>{costDraft.rate.sku}</strong><span>{costDraft.rate.provider} · {costDraft.rate.unit_type}</span></div>
            <label><span>公开目录价（{costEditorUnit(costDraft.rate.unit_type)}）</span><input disabled value={getRatePublicCost(costDraft.rate) * costEditorScale(costDraft.rate.unit_type)} /></label>
            <label><span>有效合同价（{costEditorUnit(costDraft.rate.unit_type)}）</span><input autoFocus disabled={costSaving} min="0" onChange={(event) => setCostDraft({ ...costDraft, cost: event.target.value })} required step="0.000001" type="number" value={costDraft.cost} /></label>
            <label><span>成本来源</span><input disabled={costSaving} maxLength={120} onChange={(event) => setCostDraft({ ...costDraft, sourceLabel: event.target.value })} placeholder="例如：Enterprise Contract 2026" required value={costDraft.sourceLabel} /></label>
            <label><span>生效时间（可选）</span><input disabled={costSaving} onChange={(event) => setCostDraft({ ...costDraft, effectiveAt: event.target.value })} type="datetime-local" value={costDraft.effectiveAt} /></label>
            <p className="pa-form-note">留空表示由服务器立即生效；不支持预设未来时间。保存后仅影响后续请求。</p>
          </div>
        </Modal>
      )}

      {applyPreview && catalog && (
        <Modal
          footer={(
            <>
              <button className="pa-button pa-button--quiet" onClick={() => {
                setApplyPreview(null)
                setApplyText('')
              }} type="button">取消</button>
              <button className="pa-button pa-button--primary" disabled={applyText !== applyPreview.confirmation || !hasBillingPreviewRevision(applyPreview)} onClick={() => void confirmApplyCatalog()} type="button">确认应用目录</button>
            </>
          )}
          onClose={() => {
            setApplyPreview(null)
            setApplyText('')
          }}
          title="应用成本目录与托管规则"
          wide
        >
          <div className="pa-dialog-form">
            <h3 className="pa-preview-section-title">规则变更</h3>
            <div className="pa-summary-grid">
              <div><small>新增</small><strong>{applyPreview.added}</strong></div>
              <div><small>更新</small><strong>{applyPreview.updated}</strong></div>
              <div><small>停用</small><strong>{applyPreview.disabled}</strong></div>
            </div>
            <BillingPreviewDiffPanel catalog={catalog} preview={applyPreview} />
            {!hasBillingPreviewRevision(applyPreview) && (
              <div className="pa-banner pa-banner--warning">预览缺少配置版本，不能安全应用。请关闭后重新预览。</div>
            )}
            <p className="pa-form-note">确认后，待应用售价才会成为后续请求的实际扣费规则。</p>
            <label><span>输入“{applyPreview.confirmation}”确认</span><input autoFocus onChange={(event) => setApplyText(event.target.value)} value={applyText} /></label>
          </div>
        </Modal>
      )}

      {resetPreview && catalog && (
        <Modal
          danger
          footer={(
            <>
              <button className="pa-button pa-button--quiet" onClick={() => {
                setResetPreview(null)
                setResetText('')
              }} type="button">取消</button>
              <button className="pa-button pa-button--danger" disabled={resetText !== resetPreview.confirmation || !hasBillingPreviewRevision(resetPreview)} onClick={() => void confirmBillingReset()} type="button">确认完整重置</button>
            </>
          )}
          onClose={() => {
            setResetPreview(null)
            setResetText('')
          }}
          title="完整重置计费"
          wide
        >
          <div className="pa-dialog-form">
            <h3 className="pa-preview-section-title">规则变更</h3>
            <div className="pa-summary-grid">
              <div><small>新增</small><strong>{resetPreview.added}</strong></div>
              <div><small>更新</small><strong>{resetPreview.updated}</strong></div>
              <div><small>停用</small><strong>{resetPreview.disabled}</strong></div>
            </div>
            <BillingPreviewDiffPanel catalog={catalog} preview={resetPreview} />
            {!hasBillingPreviewRevision(resetPreview) && (
              <div className="pa-banner pa-banner--warning">预览缺少配置版本，不能安全重置。请关闭后重新预览。</div>
            )}
            <div className="pa-callout pa-callout--danger">这会清除成本、加价和人工模型成本覆盖，但不会修改用户余额、充值记录或历史账本。</div>
            <label><span>输入“{resetPreview.confirmation}”确认</span><input autoFocus onChange={(event) => setResetText(event.target.value)} value={resetText} /></label>
          </div>
        </Modal>
      )}
    </>
  )
}

function settingsEqual(left: SystemSettingsValues, right: SystemSettingsValues, key: SettingKey) {
  return left[key] === right[key]
}

function SettingsPage({ run }: { run: Runner }) {
  const [response, setResponse] = useState<SystemSettingsResponse | null>(null)
  const [values, setValues] = useState<SystemSettingsValues | null>(null)
  const [loading, setLoading] = useState(true)
  const [localError, setLocalError] = useState('')
  const [confirmSave, setConfirmSave] = useState(false)
  const [resetPreview, setResetPreview] = useState<SystemSettingsResetPreview | null>(null)
  const [resetText, setResetText] = useState('')

  const adopt = useCallback((next: SystemSettingsResponse) => {
    setResponse(next)
    setValues(next.values)
  }, [])

  const load = useCallback(async () => {
    setLoading(true)
    setLocalError('')
    try {
      adopt(await getSystemSettings())
    } catch (reason) {
      setLocalError(errorMessage(reason))
    } finally {
      setLoading(false)
    }
  }, [adopt])

  useEffect(() => { void load() }, [load])

  const dirtyKeys = values && response
    ? (Object.keys(values) as SettingKey[]).filter((key) => !settingsEqual(values, response.values, key))
    : []
  const dangerousDirtyKeys = dirtyKeys.filter((key) => settingCopy[key].dangerous)
  const freeTierValid = values !== null
    && Number.isFinite(values.free_tier_dreampoints)
    && values.free_tier_dreampoints >= 0
    && values.free_tier_dreampoints <= 1_000_000_000

  function changeSetting<K extends SettingKey>(key: K, value: SystemSettingsValues[K]) {
    setValues((current) => current ? { ...current, [key]: value } : current)
    setResetPreview(null)
    setResetText('')
  }

  async function performSave() {
    if (!values || dirtyKeys.length === 0 || !freeTierValid) return
    const patch = dirtyKeys.reduce<SystemSettingsPatch>((result, key) => {
      if (key === 'free_tier_dreampoints') result.free_tier_dreampoints = values[key]
      else result[key] = values[key]
      return result
    }, {})
    const result = await run(() => patchSystemSettings(patch), '系统设置已保存')
    if (result) {
      adopt(result)
      setConfirmSave(false)
    }
  }

  async function requestSave() {
    if (dangerousDirtyKeys.length > 0) {
      setConfirmSave(true)
      return
    }
    await performSave()
  }

  async function openResetPreview() {
    const result = await run(() => previewSystemSettingsReset())
    if (result) {
      setResetPreview(result)
      setResetText('')
    }
  }

  async function confirmReset() {
    if (!resetPreview || resetText !== systemSettingsResetConfirmation) return
    const result = await run(() => resetSystemSettings(), '系统设置已恢复为安全默认值')
    if (result) {
      adopt(result)
      setResetPreview(null)
      setResetText('')
    }
  }

  return (
    <>
      <div className="pa-stack">
        <section className="pa-card pa-section">
          <div className="pa-section__heading">
            <div><h2>系统行为</h2><p>影响全部组织的新请求；只会提交你实际修改的字段。</p></div>
            {dirtyKeys.length > 0 && <span className="pa-pill pa-pill--accent">{dirtyKeys.length} 项未保存</span>}
          </div>
          {localError && <ErrorBanner message={localError} />}
          {loading && <div className="pa-settings-loading"><span className="pa-skeleton pa-skeleton--panel" /><span className="pa-skeleton pa-skeleton--panel" /></div>}
          {!loading && values && (
            <div className="pa-settings-list">
              {(['billing_enabled', 'allow_negative_balance', 'allow_user_api_key'] as const).map((key) => (
                <label className={`pa-switch ${settingCopy[key].dangerous ? 'is-sensitive' : ''}`} key={key}>
                  <span><strong>{settingCopy[key].label}</strong><small>{settingCopy[key].description}</small></span>
                  <input checked={values[key]} onChange={(event) => changeSetting(key, event.target.checked)} type="checkbox" />
                </label>
              ))}
              <label className="pa-field">
                <span><strong>{settingCopy.free_tier_dreampoints.label}</strong><small>{settingCopy.free_tier_dreampoints.description}</small></span>
                <input max="1000000000" min="0" onChange={(event) => changeSetting('free_tier_dreampoints', Number(event.target.value))} step="0.0001" type="number" value={values.free_tier_dreampoints} />
                <em className={!freeTierValid ? 'pa-field-error' : ''}>
                  {!freeTierValid ? '初始额度必须在 0–1,000,000,000 DP 之间' : `默认值：${formatNumber(response?.defaults.free_tier_dreampoints ?? 1, 4)} DP`}
                </em>
              </label>
            </div>
          )}
          <div className="pa-button-row pa-button-row--split">
            <button className="pa-button pa-button--quiet" disabled={loading || !values} onClick={() => void openResetPreview()} type="button">预览系统设置重置</button>
            <button className="pa-button pa-button--primary" disabled={loading || dirtyKeys.length === 0 || !freeTierValid} onClick={() => void requestSave()} type="button">保存修改</button>
          </div>
        </section>

        <section className="pa-card pa-section pa-info-section">
          <h2>重置范围说明</h2>
          <p>这里的“系统设置重置”只恢复运行开关和新用户赠送额度。成本目录、加价、余额和账本请在计费页分别管理。</p>
        </section>
      </div>

      {confirmSave && values && response && (
        <Modal
          danger
          footer={(
            <>
              <button className="pa-button pa-button--quiet" onClick={() => setConfirmSave(false)} type="button">返回检查</button>
              <button className="pa-button pa-button--danger" onClick={() => void performSave()} type="button">确认保存</button>
            </>
          )}
          onClose={() => setConfirmSave(false)}
          title="确认高风险系统设置"
        >
          <div className="pa-dialog-form">
            <div className="pa-callout pa-callout--danger">以下改动会立即影响全部组织的新请求。</div>
            <ul className="pa-change-list">
              {dangerousDirtyKeys.map((key) => (
                <li key={key}><strong>{settingCopy[key].label}</strong><span>{String(response.values[key])} → {String(values[key])}</span></li>
              ))}
            </ul>
          </div>
        </Modal>
      )}

      {resetPreview && (
        <Modal
          danger
          footer={(
            <>
              <button className="pa-button pa-button--quiet" onClick={() => {
                setResetPreview(null)
                setResetText('')
              }} type="button">取消</button>
              <button className="pa-button pa-button--danger" disabled={resetText !== systemSettingsResetConfirmation} onClick={() => void confirmReset()} type="button">确认重置系统设置</button>
            </>
          )}
          onClose={() => {
            setResetPreview(null)
            setResetText('')
          }}
          title="重置系统设置"
        >
          <div className="pa-dialog-form">
            <p className="pa-form-note">将恢复以下 {resetPreview.changes.length} 项；计费配置与历史数据不受影响。</p>
            <ul className="pa-change-list">
              {resetPreview.changes.map((change) => (
                <li key={change.key}><strong>{settingCopy[change.key].label}</strong><span>{String(change.from)} → {String(change.to)}</span></li>
              ))}
            </ul>
            <label><span>输入“{systemSettingsResetConfirmation}”确认</span><input autoFocus onChange={(event) => setResetText(event.target.value)} value={resetText} /></label>
          </div>
        </Modal>
      )}
    </>
  )
}
