import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react'
import {
  adminFetch,
  applyBillingCatalog,
  getBillingAnalytics,
  getBillingCatalog,
  getGlobalStats,
  getModelCatalog,
  listTenants,
  listUsers,
  previewBillingConfig,
  previewBillingReset,
  refreshModelCatalog,
  resetBillingDefaults,
  updateBillingConfig,
  updateModelCost,
  updateModelPolicy,
  updateTenant,
  updateUser,
  type BillingAnalytics,
  type BillingCatalog,
  type BillingPreview,
  type GlobalStats,
  type MarkupOverride,
  type ModelCatalog,
  type ModelPolicy,
  type Tenant,
  type User,
} from '../admin/api'
import { initAuth, type User as AuthUser } from './api/auth'
import './pro-admin.css'

type Tab = 'overview' | 'users' | 'tenants' | 'models' | 'billing' | 'settings'
type SystemSettings = Record<string, string>

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

function formatNumber(value: number, digits = 2) {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: digits }).format(value || 0)
}

function formatRate(value: number, unit: string) {
  if (unit.includes('token')) return `${formatNumber(value * 1_000_000, 6)} DP / 百万 token`
  if (unit === 'hour') return `${formatNumber(value, 4)} DP / 小时`
  return `${formatNumber(value, 6)} DP / ${unit}`
}

function previewMarkup(
  rate: { provider: string; service: string; sku: string },
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

function ErrorBanner({ message, onClose }: { message: string; onClose: () => void }) {
  if (!message) return null
  return (
    <div className="pa-banner pa-banner--error">
      <span>{message}</span>
      <button onClick={onClose} type="button">关闭</button>
    </div>
  )
}

export default function ProAdmin() {
  const [viewer, setViewer] = useState<AuthUser | null>(null)
  const [ready, setReady] = useState(false)
  const [tab, setTab] = useState<Tab>('overview')
  const [busy, setBusy] = useState(false)
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

  const run = useCallback(async <T,>(operation: () => Promise<T>, success?: string) => {
    setBusy(true)
    setError('')
    try {
      const value = await operation()
      if (success) {
        setNotice(success)
        window.setTimeout(() => setNotice(''), 3000)
      }
      return value
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '操作失败')
      return undefined
    } finally {
      setBusy(false)
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
              <span>{item.label}</span>
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
            {busy && <span className="pa-busy">正在处理…</span>}
            <a className="pa-button pa-button--quiet" href="/pro">返回工作台</a>
          </div>
        </header>
        <ErrorBanner message={error} onClose={() => setError('')} />
        {notice && <div className="pa-banner pa-banner--success">{notice}</div>}

        {tab === 'overview' && <Overview run={run} />}
        {tab === 'users' && <UsersPage isSuper={isSuper} run={run} />}
        {tab === 'tenants' && <TenantsPage run={run} />}
        {tab === 'models' && <ModelsPage run={run} />}
        {tab === 'billing' && <BillingPage run={run} />}
        {tab === 'settings' && <SettingsPage run={run} />}
      </main>
    </div>
  )
}

type Runner = <T>(operation: () => Promise<T>, success?: string) => Promise<T | undefined>

function Overview({ run }: { run: Runner }) {
  const [stats, setStats] = useState<GlobalStats | null>(null)
  const [billing, setBilling] = useState<BillingAnalytics | null>(null)
  useEffect(() => {
    void run(async () => {
      const [nextStats, nextBilling] = await Promise.all([getGlobalStats(), getBillingAnalytics()])
      setStats(nextStats)
      setBilling(nextBilling)
    })
  }, [run])
  const cards = [
    ['用户', stats?.user_count ?? 0],
    ['组织', stats?.tenant_count ?? 0],
    ['会话', stats?.session_count ?? 0],
    ['累计售出', `${formatNumber(billing?.retail_dp ?? 0)} DP`],
  ]
  return (
    <div className="pa-stack">
      <section className="pa-metrics">
        {cards.map(([label, value]) => (
          <article className="pa-card pa-metric" key={label}>
            <small>{label}</small><strong>{value}</strong>
          </article>
        ))}
      </section>
      <section className="pa-card pa-section">
        <div className="pa-section__heading"><div><h2>计费表现</h2><p>从不可变 usage ledger 汇总。</p></div></div>
        <div className="pa-summary-grid">
          <div><small>上游成本</small><strong>${formatNumber(billing?.upstream_cost_usd ?? 0, 4)}</strong></div>
          <div><small>服务费</small><strong>{formatNumber(billing?.service_fee_dp ?? 0, 4)} DP</strong></div>
          <div><small>用量记录</small><strong>{formatNumber(billing?.usage_count ?? 0, 0)}</strong></div>
        </div>
      </section>
    </div>
  )
}

function UsersPage({ isSuper, run }: { isSuper: boolean; run: Runner }) {
  const [users, setUsers] = useState<Array<User & { dreampoints?: number }>>([])
  const [showCreate, setShowCreate] = useState(false)
  const load = useCallback(() => run(async () => {
    const result = await listUsers(1, 100)
    setUsers(result.users)
  }), [run])
  useEffect(() => { void load() }, [load])

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const created = await run(() => adminFetch('/api/admin/users', {
      method: 'POST',
      body: JSON.stringify({
        email: data.get('email'),
        password: data.get('password'),
        name: data.get('name'),
        role: data.get('role'),
        dreampoints: Number(data.get('dreampoints') || 0),
      }),
    }), '用户已创建')
    if (created) {
      setShowCreate(false)
      await load()
    }
  }

  async function adjust(user: User & { dreampoints?: number }) {
    const raw = window.prompt(`调整 ${user.email} 的 DP（正数充值，负数扣减）`, '0')
    if (raw === null) return
    const amount = Number(raw)
    if (!Number.isFinite(amount) || amount === 0) return
    await run(() => adminFetch('/api/admin/balance', {
      method: 'POST',
      body: JSON.stringify({ user_id: user.id, amount, description: '管理员后台调整' }),
    }), '余额已更新')
    await load()
  }

  return (
    <section className="pa-card pa-section">
      <div className="pa-section__heading">
        <div><h2>账户与权限</h2><p>普通管理员只能管理自己组织内的普通用户。</p></div>
        <button className="pa-button pa-button--primary" onClick={() => setShowCreate(!showCreate)} type="button">创建用户</button>
      </div>
      {showCreate && (
        <form className="pa-inline-form" onSubmit={create}>
          <input name="email" placeholder="邮箱" required type="email" />
          <input name="name" placeholder="姓名" required />
          <input minLength={8} name="password" placeholder="初始密码" required type="password" />
          <select defaultValue="user" name="role">
            <option value="user">用户</option>
            {isSuper && <option value="admin">管理员</option>}
          </select>
          <input defaultValue="100" min="0" name="dreampoints" step="0.01" type="number" />
          <button className="pa-button pa-button--primary" type="submit">保存</button>
        </form>
      )}
      <div className="pa-table-wrap">
        <table>
          <thead><tr><th>用户</th><th>角色</th><th>余额</th><th>状态</th><th>操作</th></tr></thead>
          <tbody>
            {users.map((user) => (
              <tr key={user.id}>
                <td><strong>{user.name || '未命名'}</strong><small>{user.email}</small></td>
                <td><span className="pa-pill">{user.role}</span></td>
                <td>{formatNumber(user.dreampoints ?? 0, 4)} DP</td>
                <td><span className={`pa-status ${user.is_active ? 'is-good' : 'is-muted'}`}>{user.is_active ? '启用' : '停用'}</span></td>
                <td className="pa-actions">
                  {isSuper && <button onClick={() => void adjust(user)} type="button">调整余额</button>}
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
    </section>
  )
}

function TenantsPage({ run }: { run: Runner }) {
  const [tenants, setTenants] = useState<Tenant[]>([])
  const load = useCallback(() => run(async () => {
    setTenants((await listTenants(1, 100)).tenants)
  }), [run])
  useEffect(() => { void load() }, [load])
  return (
    <section className="pa-card pa-section">
      <div className="pa-section__heading"><div><h2>组织与配额</h2><p>套餐变更立即影响后续配额检查。</p></div></div>
      <div className="pa-table-wrap"><table>
        <thead><tr><th>组织</th><th>套餐</th><th>API / 月</th><th>存储</th><th>会话数</th></tr></thead>
        <tbody>{tenants.map((tenant) => (
          <tr key={tenant.id}>
            <td><strong>{tenant.name}</strong><small>{tenant.slug}</small></td>
            <td><select value={tenant.plan} onChange={(event) => void run(async () => {
              await updateTenant(tenant.id, { plan: event.target.value })
              await load()
            }, '套餐已更新')}>
              <option value="free">Free</option><option value="pro">Pro</option><option value="enterprise">Enterprise</option>
            </select></td>
            <td>{formatNumber(tenant.api_quota_monthly, 0)}</td>
            <td>{tenant.storage_quota_gb} GB</td>
            <td>{tenant.max_sessions}</td>
          </tr>
        ))}</tbody>
      </table></div>
    </section>
  )
}

function ModelsPage({ run }: { run: Runner }) {
  const [catalog, setCatalog] = useState<ModelCatalog | null>(null)
  const load = useCallback(() => run(async () => setCatalog(await getModelCatalog())), [run])
  useEffect(() => { void load() }, [load])
  const purposes = Object.keys(purposeLabels) as ModelPolicy['purpose'][]

  async function changePolicy(
    modelId: string,
    purpose: ModelPolicy['purpose'],
    patch: Partial<ModelPolicy>,
  ) {
    const existing = catalog?.models.find((model) => model.model_id === modelId)
      ?.policies.find((policy) => policy.purpose === purpose)
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

  async function configureCost(modelId: string) {
    const embedding = modelId.startsWith('text-embedding-')
    const inputRaw = window.prompt(`${modelId} 输入成本（USD / 百万 token）`, embedding ? '0.02' : '1')
    if (inputRaw === null) return
    const outputRaw = embedding ? '0' : window.prompt(`${modelId} 输出成本（USD / 百万 token）`, '6')
    if (outputRaw === null) return
    const cachedRaw = embedding ? '0' : window.prompt('缓存输入成本（留空或 0 表示按普通输入计）', '0')
    if (cachedRaw === null) return
    const writeRaw = embedding ? '0' : window.prompt('缓存写入成本（留空或 0 表示按普通输入计）', '0')
    if (writeRaw === null) return
    const values = [inputRaw, outputRaw, cachedRaw, writeRaw].map(Number)
    if (values.some((value) => !Number.isFinite(value) || value < 0)) return
    await run(async () => {
      await updateModelCost({
        model_id: modelId,
        service: embedding ? 'embedding' : 'llm',
        input_per_million_usd: values[0],
        output_per_million_usd: values[1],
        cached_input_per_million_usd: values[2],
        cache_write_per_million_usd: values[3],
      })
      await load()
    }, '模型成本已保存，可以继续审批')
  }

  return (
    <div className="pa-stack">
      <section className="pa-card pa-section">
        <div className="pa-section__heading">
          <div><h2>Provider 模型目录</h2><p>每 15 分钟自动同步；新模型默认不开放。</p></div>
          <button className="pa-button pa-button--primary" onClick={() => void run(async () => {
            setCatalog(await refreshModelCatalog())
          }, '模型目录已刷新')} type="button">立即刷新</button>
        </div>
        <div className="pa-provider-status">
          <span className={`pa-status ${catalog?.last_error ? 'is-bad' : 'is-good'}`}>
            {catalog?.last_error ? '同步异常' : '同步正常'}
          </span>
          <span>最近成功：{catalog?.last_success_at ? new Date(catalog.last_success_at).toLocaleString() : '尚未同步'}</span>
          {catalog?.last_error && <span>{catalog.last_error}</span>}
        </div>
        <div className="pa-table-wrap"><table>
          <thead><tr><th>模型</th><th>Provider</th><th>状态</th><th>允许用途</th></tr></thead>
          <tbody>{catalog?.models.map((model) => (
            <tr key={model.model_id}>
              <td>
                <strong>{model.model_id}</strong>
                <small>{model.source} · {model.policies.some((policy) => policy.cost_confirmed) ? '成本已配置' : '缺少成本'}</small>
              </td>
              <td>{model.provider}</td>
              <td>
                <span className={`pa-status ${model.provider_available ? 'is-good' : 'is-muted'}`}>{model.provider_available ? '可用' : 'Provider 未返回'}</span>
                <button className="pa-link-button" onClick={() => void configureCost(model.model_id)} type="button">
                  {model.policies.some((policy) => policy.cost_confirmed) ? '修改成本' : '配置成本'}
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
                        disabled={!policy?.cost_confirmed && !approved}
                        onClick={() => void changePolicy(model.model_id, purpose, {
                          is_approved: !approved,
                          is_default: approved ? false : policy?.is_default ?? false,
                        })}
                        title={!policy?.cost_confirmed ? '请先在计费页配置该模型的上游成本' : ''}
                        type="button"
                      >{purposeLabels[purpose]}{approved ? ' ✓' : ''}</button>
                      {approved && (
                        <button
                          className={policy?.is_default ? 'is-default' : ''}
                          onClick={() => void changePolicy(model.model_id, purpose, { is_default: true })}
                          type="button"
                        >{policy?.is_default ? '默认' : '设为默认'}</button>
                      )}
                    </div>
                  )
                })}
              </div></td>
            </tr>
          ))}</tbody>
        </table></div>
      </section>
    </div>
  )
}

function BillingPage({ run }: { run: Runner }) {
  const [catalog, setCatalog] = useState<BillingCatalog | null>(null)
  const [dpPerUsd, setDPPerUSD] = useState(1)
  const [markup, setMarkup] = useState(100)
  const [overrides, setOverrides] = useState<MarkupOverride[]>([])
  const [preview, setPreview] = useState<BillingPreview | null>(null)
  const [resetPreview, setResetPreview] = useState<BillingPreview | null>(null)
  const [resetText, setResetText] = useState('')
  const [exampleHours, setExampleHours] = useState(10)

  const adopt = useCallback((next: BillingCatalog) => {
    setCatalog(next)
    setDPPerUSD(next.config.dp_per_usd)
    setMarkup(next.config.default_markup_percent)
    setOverrides(next.config.overrides || [])
  }, [])
  const load = useCallback(() => run(async () => adopt(await getBillingCatalog())), [adopt, run])
  useEffect(() => { void load() }, [load])

  const realtime = useMemo(() => catalog?.rates.find(
    (rate) => rate.sku === 'speechmatics-realtime-enhanced' && rate.unit_type === 'hour',
  ), [catalog])
  const realtimeMarkup = realtime ? previewMarkup(realtime, markup, overrides) : markup
  const calculatedRealtime = realtime
    ? realtime.cost_per_unit_usd * dpPerUsd * (1 + realtimeMarkup / 100)
    : 0

  const input = { dp_per_usd: dpPerUsd, default_markup_percent: markup, overrides }

  return (
    <div className="pa-stack">
      <section className="pa-card pa-section">
        <div className="pa-section__heading">
          <div>
            <h2>成本加价设置</h2>
            <p>售价 = 上游成本 × DP/USD ×（1 + 加价率）。加价 100% 对应售价 2 倍、毛利率 50%。</p>
          </div>
          <span className={`pa-version ${catalog?.has_update ? 'has-update' : ''}`}>
            目录 {catalog?.installed_version || '—'} / 内置 {catalog?.builtin_version || '—'}
          </span>
        </div>
        <div className="pa-form-grid">
          <label><span>1 USD 可兑换 DP</span><input min="0.000001" onChange={(e) => setDPPerUSD(Number(e.target.value))} step="0.01" type="number" value={dpPerUsd} /></label>
          <label><span>默认成本加价率</span><div className="pa-input-suffix"><input min="0" onChange={(e) => setMarkup(Number(e.target.value))} step="1" type="number" value={markup} /><i>%</i></div></label>
          <div className="pa-form-result"><small>对应毛利率</small><strong>{formatNumber(markup / (100 + markup) * 100)}%</strong></div>
        </div>
        <div className="pa-example">
          <div><small>Realtime Enhanced 售价</small><strong>{formatNumber(calculatedRealtime, 4)} DP / 小时</strong></div>
          <label><span>示例时长</span><input min="0" onChange={(e) => setExampleHours(Number(e.target.value))} type="number" value={exampleHours} /></label>
          <div><small>用户支付</small><strong>{formatNumber(calculatedRealtime * exampleHours, 4)} DP</strong></div>
        </div>
        <div className="pa-button-row">
          <button className="pa-button pa-button--quiet" onClick={() => void run(async () => setPreview(await previewBillingConfig(input)))} type="button">预览全部售价</button>
          <button className="pa-button pa-button--primary" onClick={() => void run(async () => adopt(await updateBillingConfig(input)), '计费配置已保存')} type="button">保存并应用</button>
          {catalog?.has_update && <button className="pa-button pa-button--quiet" onClick={() => void run(async () => adopt(await applyBillingCatalog()), '官方成本目录已更新')} type="button">应用内置成本更新</button>}
        </div>
      </section>

      <section className="pa-card pa-section">
        <div className="pa-section__heading"><div><h2>分级加价</h2><p>具体 SKU 优先于类别，类别优先于 Provider。</p></div>
          <button className="pa-button pa-button--quiet" onClick={() => setOverrides((current) => [...current, { scope_type: 'provider', scope_key: '', markup_percent: markup }])} type="button">添加覆盖</button>
        </div>
        {overrides.length === 0 ? <div className="pa-empty">当前全部使用全局加价率。</div> : (
          <div className="pa-override-list">{overrides.map((override, index) => (
            <div className="pa-override" key={`${override.scope_type}-${index}`}>
              <select value={override.scope_type} onChange={(e) => setOverrides((current) => current.map((item, i) => i === index ? { ...item, scope_type: e.target.value as MarkupOverride['scope_type'] } : item))}>
                <option value="provider">Provider</option><option value="category">服务类别</option><option value="sku">具体 SKU</option>
              </select>
              <input onChange={(e) => setOverrides((current) => current.map((item, i) => i === index ? { ...item, scope_key: e.target.value } : item))} placeholder="例如 openai-compatible" value={override.scope_key} />
              <input min="0" onChange={(e) => setOverrides((current) => current.map((item, i) => i === index ? { ...item, markup_percent: Number(e.target.value) } : item))} type="number" value={override.markup_percent} />
              <span>%</span>
              <button onClick={() => setOverrides((current) => current.filter((_, i) => i !== index))} type="button">删除</button>
            </div>
          ))}</div>
        )}
      </section>

      <section className="pa-card pa-section">
        <div className="pa-section__heading"><div><h2>官方成本与最终售价</h2><p>成本目录随版本发布；不会抓取网页后静默改价。</p></div></div>
        <div className="pa-table-wrap"><table>
          <thead><tr><th>SKU</th><th>用途</th><th>上游成本</th><th>最终售价</th><th>加价 / 毛利</th></tr></thead>
          <tbody>{(preview?.rates || catalog?.rates || []).filter((rate) => rate.is_active).map((rate) => (
            <tr key={`${rate.provider}-${rate.sku}-${rate.unit_type}`}>
              <td><strong>{rate.sku}</strong><small>{rate.provider}</small></td>
              <td>{rate.service}<small>{rate.unit_type}</small></td>
              <td>{rate.unit_type.includes('token') ? `$${formatNumber(rate.cost_per_unit_usd * 1_000_000, 6)} / 百万` : `$${formatNumber(rate.cost_per_unit_usd, 4)} / ${rate.unit_type}`}</td>
              <td>{formatRate(rate.retail_dp_per_unit, rate.unit_type)}</td>
              <td>{formatNumber(rate.markup_percent)}% <small>毛利 {formatNumber(rate.gross_margin_percent)}%</small></td>
            </tr>
          ))}</tbody>
        </table></div>
      </section>

      <section className="pa-card pa-section pa-danger-zone">
        <div className="pa-section__heading">
          <div><h2>重置为最新默认计费</h2><p>更新全套官方成本、汇率、默认加价及托管售价；余额和历史账单不会改变。</p></div>
          <button className="pa-button pa-button--danger" onClick={() => void run(async () => setResetPreview(await previewBillingReset()))} type="button">预览重置</button>
        </div>
        {resetPreview && (
          <div className="pa-reset">
            <div className="pa-summary-grid">
              <div><small>新增</small><strong>{resetPreview.added}</strong></div>
              <div><small>更新</small><strong>{resetPreview.updated}</strong></div>
              <div><small>停用</small><strong>{resetPreview.disabled}</strong></div>
            </div>
            <p>确认后会清除所有人工加价与售价覆盖。请输入“重置计费”继续。</p>
            <div className="pa-button-row">
              <input onChange={(e) => setResetText(e.target.value)} placeholder="重置计费" value={resetText} />
              <button className="pa-button pa-button--danger" disabled={resetText !== resetPreview.confirmation} onClick={() => void run(async () => {
                adopt(await resetBillingDefaults(resetText))
                setResetPreview(null)
                setResetText('')
              }, '整套计费配置已恢复为最新默认')} type="button">确认重置</button>
            </div>
          </div>
        )}
      </section>
    </div>
  )
}

function SettingsPage({ run }: { run: Runner }) {
  const [settings, setSettings] = useState<SystemSettings>({})
  const load = useCallback(() => run(async () => setSettings(await adminFetch('/api/admin/settings'))), [run])
  useEffect(() => { void load() }, [load])
  const booleanKeys = ['billing_enabled', 'allow_negative_balance', 'allow_user_api_key']
  return (
    <section className="pa-card pa-section">
      <div className="pa-section__heading"><div><h2>系统行为</h2><p>影响全部组织的新请求。</p></div></div>
      <div className="pa-settings-list">
        {booleanKeys.map((key) => (
          <label className="pa-switch" key={key}>
            <span><strong>{key}</strong><small>{key === 'allow_user_api_key' ? '允许用户使用自己的 Provider Key' : '全局系统设置'}</small></span>
            <input checked={settings[key] === 'true'} onChange={(event) => setSettings((current) => ({ ...current, [key]: String(event.target.checked) }))} type="checkbox" />
          </label>
        ))}
        <label className="pa-field"><span>新用户初始 DreamPoints</span><input min="0" onChange={(event) => setSettings((current) => ({ ...current, free_tier_dreampoints: event.target.value }))} type="number" value={settings.free_tier_dreampoints || '0'} /></label>
      </div>
      <div className="pa-button-row"><button className="pa-button pa-button--primary" onClick={() => void run(() => adminFetch('/api/admin/settings', { method: 'PUT', body: JSON.stringify(settings) }), '系统设置已保存')} type="button">保存设置</button></div>
    </section>
  )
}
