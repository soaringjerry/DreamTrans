import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  createBillingCheckout,
  createBillingPortal,
  formatHours,
  formatUSD,
  formatUsageUSD,
  getSessionCostSummaries,
  getUserBillingLedger,
  getUserBillingPlans,
  getUserUsage,
  setUserAutoTopup,
  type AccountBalance,
  type AccountSummary,
  type BalanceTransaction,
  type GrantKind,
  type PaymentRow,
  type Plan,
  type PlanHourlyExample,
  type SessionCostSummary,
  type TopupTier,
  type UserBillingPlans,
  type UserUsageItem,
} from '../../api'
import { ApiRequestError } from '../../pro/api/auth'
import { Icon } from './Icon'

export interface AccountPanelProps {
  account: AccountSummary | null
  balance: AccountBalance | null
  open: boolean
  paymentsEnabled: boolean
  sessionId: string
  onRefreshAccount: () => Promise<void>
}

const PAYMENTS_DISABLED_HINT = '尚未开通在线支付'
const FREE_PLAN_CODE = 'free'

const grantKindLabels: Record<GrantKind, string> = {
  trial: '试用赠送',
  topup_bonus: '充值赠送',
  promo: '活动赠送',
  adjustment: '人工调整',
  settle_return: '结算返还',
}

const planFeatureLabels: Record<string, string> = {
  premium_models: '高级模型',
  byok: '自带 API Key',
  batch: '批量处理',
  custom_prompt: '自定义提示词',
  auto_topup: '自动充值',
  export_ledger: '导出流水',
  api_access: 'API 访问',
}

const usageActionLabels: Record<string, string> = {
  transcription: '实时转录',
  translation: 'AI 翻译',
  chat: 'AI 助手',
  rag: 'AI 助手',
  summary: 'AI 摘要',
  embedding: '语义索引',
  title: 'AI 标题',
  artifact: 'AI 生成',
}

const ledgerTypeLabels: Record<BalanceTransaction['transaction_type'], string> = {
  credit: '入账',
  debit: '扣费',
  refund: '退款',
  adjustment: '调整',
}

/**
 * One display row of the balance ledger. Usage entries arrive as a
 * reserve-then-settle pair (debit at the estimated maximum, refund of the
 * over-reserved part seconds later); showing both raw legs reads like
 * "charged then refunded the same amount" once rounded to two decimals, so
 * rows sharing a usage id are folded into a single net-amount row.
 */
interface LedgerDisplayRow {
  key: string
  primary: BalanceTransaction
  netUSD: number
  buckets: Set<BalanceTransaction['bucket']>
  merged: boolean
}

function mergeUsageLedger(entries: BalanceTransaction[]): LedgerDisplayRow[] {
  const rows: LedgerDisplayRow[] = []
  const byUsage = new Map<string, LedgerDisplayRow>()
  for (const entry of entries) {
    const usageId = entry.reference_type === 'usage' ? entry.reference_id : null
    const foldable = usageId
      && (entry.transaction_type === 'debit' || entry.transaction_type === 'refund')
    if (!foldable) {
      rows.push({
        key: entry.id,
        primary: entry,
        netUSD: entry.amount_usd,
        buckets: new Set([entry.bucket]),
        merged: false,
      })
      continue
    }
    const existing = byUsage.get(usageId)
    if (existing) {
      existing.netUSD += entry.amount_usd
      existing.buckets.add(entry.bucket)
      existing.merged = true
      // Entries are newest-first, so the debit (the older leg) is visited
      // last; it carries the action name and the moment the usage happened.
      if (entry.transaction_type === 'debit') existing.primary = entry
      continue
    }
    const row: LedgerDisplayRow = {
      key: `usage:${usageId}`,
      primary: entry,
      netUSD: entry.amount_usd,
      buckets: new Set([entry.bucket]),
      merged: false,
    }
    byUsage.set(usageId, row)
    rows.push(row)
  }
  return rows
}

function ledgerBucketLabel(buckets: Set<BalanceTransaction['bucket']>): string {
  if (buckets.size > 1) return '赠送 + 钱包'
  return buckets.has('grant') ? '赠送' : '钱包'
}

const paymentKindLabels: Record<PaymentRow['kind'], string> = {
  topup: '充值',
  membership: '会员',
  refund: '退款',
}

function formatDate(value?: string | null): string {
  if (!value) return ''
  const time = Date.parse(value)
  if (!Number.isFinite(time)) return value
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: 'numeric',
    day: 'numeric',
  }).format(time)
}

function formatDateTime(value?: string | null): string {
  if (!value) return ''
  const time = Date.parse(value)
  if (!Number.isFinite(time)) return value
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(time)
}

function discountLabel(percent: number): string | null {
  if (!(percent > 0) || percent >= 100) return null
  const factor = (100 - percent) / 10
  return `用量 ${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(factor)} 折`
}

function tierLabel(tier: TopupTier): string {
  const bonus = tier.amount_usd * (tier.bonus_percent / 100)
  if (bonus > 0) {
    return `充 ${formatUSD(tier.amount_usd, 0)} 送 ${formatUSD(bonus)} (${tier.bonus_percent}%)`
  }
  return `充 ${formatUSD(tier.amount_usd, 0)}`
}

function paidFromLabel(item: UserUsageItem): string {
  if (item.refunded) return '已退款'
  if (item.grant_usd > 0 && item.wallet_usd > 0) return '赠送 + 钱包'
  if (item.grant_usd > 0) return '赠送额度'
  if (item.wallet_usd > 0) return '钱包'
  if (!item.settled) return '预扣中'
  return '免费'
}

function billingErrorMessage(reason: unknown, fallback: string): string {
  if (reason instanceof ApiRequestError) {
    switch (reason.status) {
      case 503:
        return PAYMENTS_DISABLED_HINT
      case 409:
        return '当前已有会员订阅，请通过“管理会员”调整。'
      case 404:
        return '还没有支付记录，完成一次充值后即可管理账单。'
      case 403:
        return '当前套餐不包含此功能。'
      case 400:
        return reason.message.includes('payment method')
          ? '需要先保存一张支付卡（完成一次充值即可）。'
          : reason.message
      default:
        return reason.message || fallback
    }
  }
  return reason instanceof Error && reason.message ? reason.message : fallback
}

function estimatedHours(account: AccountSummary | null, balance: AccountBalance | null): number {
  if (!account) return 0
  const available = balance?.available_usd ?? account.available_usd
  if (account.realtime_hour_usd > 0) return Math.max(0, available / account.realtime_hour_usd)
  return Math.max(0, account.estimated_realtime_hours)
}

export function AccountPanel({
  account,
  balance,
  open,
  paymentsEnabled,
  sessionId,
  onRefreshAccount,
}: AccountPanelProps) {
  const [plans, setPlans] = useState<UserBillingPlans | null>(null)
  const [usage, setUsage] = useState<UserUsageItem[]>([])
  const [sessionCost, setSessionCost] = useState<SessionCostSummary | null>(null)
  const [ledger, setLedger] = useState<{ ledger: BalanceTransaction[]; payments: PaymentRow[] } | null>(null)
  const [ledgerOpen, setLedgerOpen] = useState(false)
  const [ledgerLoading, setLedgerLoading] = useState(false)
  const [busy, setBusy] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [autoTopupThreshold, setAutoTopupThreshold] = useState('')
  const [autoTopupAmount, setAutoTopupAmount] = useState('')

  useEffect(() => {
    if (!open) return
    let active = true
    void getUserBillingPlans()
      .then((next) => { if (active) setPlans(next) })
      .catch(() => { if (active) setPlans(null) })
    void getUserUsage(sessionId || undefined)
      .then((items) => { if (active) setUsage(items.slice(0, 8)) })
      .catch(() => { if (active) setUsage([]) })
    if (sessionId) {
      void getSessionCostSummaries([sessionId])
        .then((summaries) => { if (active) setSessionCost(summaries[0] ?? null) })
        .catch(() => { if (active) setSessionCost(null) })
    } else {
      setSessionCost(null)
    }
    return () => { active = false }
  }, [open, sessionId, account?.lifetime_charged_usd])

  useEffect(() => {
    if (!open) {
      setNotice(null)
      setLedgerOpen(false)
      return
    }
    setAutoTopupThreshold(
      account?.auto_topup_threshold_usd !== undefined ? String(account.auto_topup_threshold_usd) : '5',
    )
    setAutoTopupAmount(
      account?.auto_topup_amount_usd !== undefined ? String(account.auto_topup_amount_usd) : '20',
    )
  }, [open, account?.auto_topup_threshold_usd, account?.auto_topup_amount_usd])

  useEffect(() => {
    if (!ledgerOpen || !open) return
    let active = true
    setLedgerLoading(true)
    void getUserBillingLedger()
      .then((next) => { if (active) setLedger(next) })
      .catch((reason: unknown) => {
        if (active) setNotice(billingErrorMessage(reason, '流水读取失败'))
      })
      .finally(() => { if (active) setLedgerLoading(false) })
    return () => { active = false }
  }, [ledgerOpen, open, account?.lifetime_charged_usd])

  const paymentsReady = paymentsEnabled && (plans?.payments_enabled ?? true)
  const memberActive = balance?.member_active ?? account?.member_active ?? false
  const availableUsd = balance?.available_usd ?? account?.available_usd ?? 0
  const walletUsd = balance?.wallet_usd ?? account?.wallet_usd ?? 0
  const grantUsd = balance?.grant_usd ?? account?.grant_usd ?? 0
  const hours = estimatedHours(account, balance)
  const standardHourly = useMemo<PlanHourlyExample | undefined>(
    () => plans?.hourly.find((example) => example.plan_code === FREE_PLAN_CODE),
    [plans],
  )
  const publicPlans = useMemo<Plan[]>(
    () => (plans?.plans ?? [])
      .filter((plan) => plan.is_public && plan.active && plan.code !== FREE_PLAN_CODE)
      .sort((a, b) => a.sort - b.sort),
    [plans],
  )
  const topupTiers = useMemo<TopupTier[]>(
    () => (plans?.topup_tiers ?? [])
      .filter((tier) => tier.active)
      .sort((a, b) => a.sort - b.sort || a.amount_usd - b.amount_usd),
    [plans],
  )
  const autoTopupAllowed = account?.effective_plan?.features?.auto_topup === true

  const redirect = useCallback(async (key: string, request: () => Promise<string>) => {
    setBusy(key)
    setNotice(null)
    try {
      const url = await request()
      if (!url) throw new Error('支付页面地址无效')
      window.location.assign(url)
    } catch (reason) {
      setNotice(billingErrorMessage(reason, '无法打开支付页面，请稍后重试。'))
      setBusy(null)
    }
  }, [])

  const startTopup = (tier: TopupTier) => redirect(
    `topup:${tier.amount_usd}`,
    () => createBillingCheckout({ kind: 'topup', amount_usd: tier.amount_usd }),
  )
  const startMembership = (plan: Plan, interval: 'month' | 'year') => redirect(
    `plan:${plan.code}:${interval}`,
    () => createBillingCheckout({ kind: 'membership', plan_code: plan.code, interval }),
  )
  const openPortal = () => redirect('portal', createBillingPortal)

  const saveAutoTopup = async (enabled: boolean) => {
    setBusy('auto-topup')
    setNotice(null)
    try {
      const threshold = Number.parseFloat(autoTopupThreshold)
      const amount = Number.parseFloat(autoTopupAmount)
      if (enabled && (!Number.isFinite(threshold) || threshold < 0)) {
        throw new Error('触发余额需要是一个非负数字。')
      }
      if (enabled && (!Number.isFinite(amount) || amount <= 0)) {
        throw new Error('自动充值金额需要大于 0。')
      }
      await setUserAutoTopup(
        enabled
          ? { enabled: true, threshold_usd: threshold, amount_usd: amount }
          : { enabled: false },
      )
      await onRefreshAccount()
      setNotice(enabled ? '自动充值已开启。' : '自动充值已关闭。')
    } catch (reason) {
      setNotice(billingErrorMessage(reason, '自动充值设置失败。'))
    } finally {
      setBusy(null)
    }
  }

  if (!account) {
    return (
      <div className="dt-billing-card">
        <p className="dt-muted">正在读取账户信息…</p>
        <button
          className="dt-button dt-button--secondary dt-button--small"
          onClick={() => { void onRefreshAccount() }}
          type="button"
        >
          重新读取
        </button>
      </div>
    )
  }

  const discount = discountLabel(account.discount_percent)
  const membership = account.membership
  const memberUntil = balance?.member_until ?? account.member_until

  return (
    <>
      {notice && (
        <div className="dt-billing-notice" role="status">
          <span>{notice}</span>
          <button aria-label="关闭提示" onClick={() => setNotice(null)} type="button">
            <Icon name="close" size={15} />
          </button>
        </div>
      )}

      <section className="dt-billing-card" aria-label="余额">
        <div className="dt-billing-card__head">
          <div>
            <strong>可用余额</strong>
            <small>赠送额度优先扣除，钱包余额不会过期</small>
          </div>
          {memberActive && <span className="dt-pro-badge">Pro</span>}
        </div>
        <div className="dt-billing-amount">
          <strong>{formatUSD(availableUsd)}</strong>
          <span>{formatHours(hours)} 实时转录</span>
        </div>
        <dl className="dt-billing-rows">
          <div>
            <dt>钱包</dt>
            <dd>{formatUSD(walletUsd)}</dd>
          </div>
          <div>
            <dt>赠送</dt>
            <dd>{formatUSD(grantUsd)}</dd>
          </div>
          {account.grants.filter((grant) => grant.remaining_usd > 0).map((grant) => (
            <div className="dt-billing-rows__sub" key={grant.id}>
              <dt>
                {grantKindLabels[grant.kind] ?? grant.kind}
                {grant.expires_at
                  ? <small>{formatDate(grant.expires_at)} 到期</small>
                  : <small>长期有效</small>}
              </dt>
              <dd>{formatUSD(grant.remaining_usd)}</dd>
            </div>
          ))}
          <div>
            <dt>实时转录单价</dt>
            <dd>
              {formatUSD(account.realtime_hour_usd)} / 小时
              {discount && <small>{discount}</small>}
            </dd>
          </div>
        </dl>
      </section>

      <section className="dt-billing-card" aria-label="会员">
        <div className="dt-billing-card__head">
          <div>
            <strong>会员</strong>
            <small>
              当前方案：{account.plan?.name || account.plan_code}
              {memberActive && memberUntil ? ` · 有效期至 ${formatDate(memberUntil)}` : ''}
            </small>
          </div>
        </div>
        {memberActive ? (
          <>
            {membership && (
              <p className="dt-muted">
                {membership.interval === 'year' ? '按年付费' : '按月付费'}
                {membership.current_period_start && membership.current_period_end
                  ? ` · 本期 ${formatDate(membership.current_period_start)} – ${formatDate(membership.current_period_end)}`
                  : ''}
                {membership.cancel_at_period_end ? ' · 到期后不再续费' : ''}
                {membership.status && membership.status !== 'active' ? ` · 状态：${membership.status}` : ''}
              </p>
            )}
            {!membership && memberUntil && (
              <p className="dt-muted">由管理员手动开通，到期日 {formatDate(memberUntil)}。</p>
            )}
            <button
              className="dt-button dt-button--secondary dt-button--wide"
              disabled={!paymentsReady || busy === 'portal'}
              onClick={() => { void openPortal() }}
              title={paymentsReady ? undefined : PAYMENTS_DISABLED_HINT}
              type="button"
            >
              {busy === 'portal' ? '正在打开…' : '管理会员 / 发票'}
            </button>
            {!paymentsReady && <p className="dt-muted">{PAYMENTS_DISABLED_HINT}</p>}
          </>
        ) : publicPlans.length === 0 ? (
          <p className="dt-muted">{plans ? '暂无可开通的会员方案。' : '正在读取会员方案…'}</p>
        ) : publicPlans.map((plan) => {
          const hourly = plans?.hourly.find((example) => example.plan_code === plan.code)
          const features = Object.entries(plan.features ?? {})
            .filter(([, enabled]) => enabled === true)
            .map(([key]) => planFeatureLabels[key] ?? key)
          const planDiscount = discountLabel(plan.usage_discount_percent)
          return (
            <div className="dt-billing-plan" key={plan.code}>
              <div className="dt-billing-plan__head">
                <strong>{plan.name}</strong>
                <span>
                  {plan.price_usd_month > 0 && `${formatUSD(plan.price_usd_month)} / 月`}
                  {plan.price_usd_month > 0 && plan.price_usd_year > 0 && ' · '}
                  {plan.price_usd_year > 0 && `${formatUSD(plan.price_usd_year)} / 年`}
                </span>
              </div>
              {planDiscount && <span className="dt-billing-plan__discount">{planDiscount}</span>}
              {hourly && (
                <p className="dt-muted">
                  实时转录 {formatUSD(hourly.realtime_hour_usd)} / 小时
                  {standardHourly && standardHourly.realtime_hour_usd > hourly.realtime_hour_usd
                    ? `（标准价 ${formatUSD(standardHourly.realtime_hour_usd)} / 小时）`
                    : ''}
                </p>
              )}
              {features.length > 0 && (
                <ul className="dt-billing-plan__features">
                  {features.map((feature) => (
                    <li key={feature}><Icon name="check" size={14} />{feature}</li>
                  ))}
                </ul>
              )}
              <div className="dt-billing-actions">
                {plan.price_usd_month > 0 && (
                  <button
                    className="dt-button dt-button--primary"
                    disabled={!paymentsReady || busy !== null}
                    onClick={() => { void startMembership(plan, 'month') }}
                    title={paymentsReady ? undefined : PAYMENTS_DISABLED_HINT}
                    type="button"
                  >
                    {busy === `plan:${plan.code}:month` ? '正在跳转…' : '开通会员（月付）'}
                  </button>
                )}
                {plan.price_usd_year > 0 && (
                  <button
                    className="dt-button dt-button--secondary"
                    disabled={!paymentsReady || busy !== null}
                    onClick={() => { void startMembership(plan, 'year') }}
                    title={paymentsReady ? undefined : PAYMENTS_DISABLED_HINT}
                    type="button"
                  >
                    {busy === `plan:${plan.code}:year` ? '正在跳转…' : '开通会员（年付）'}
                  </button>
                )}
              </div>
              {!paymentsReady && <p className="dt-muted">{PAYMENTS_DISABLED_HINT}</p>}
            </div>
          )
        })}
      </section>

      <section className="dt-billing-card" aria-label="充值">
        <div className="dt-billing-card__head">
          <div>
            <strong>充值</strong>
            <small>充值金额进入钱包，赠送部分有到期日</small>
          </div>
        </div>
        {topupTiers.length === 0 ? (
          <p className="dt-muted">{plans ? '暂无可用的充值档位。' : '正在读取充值档位…'}</p>
        ) : (
          <div className="dt-billing-tiers">
            {topupTiers.map((tier) => (
              <button
                className="dt-button dt-button--secondary"
                disabled={!paymentsReady || busy !== null}
                key={tier.amount_usd}
                onClick={() => { void startTopup(tier) }}
                title={paymentsReady ? undefined : PAYMENTS_DISABLED_HINT}
                type="button"
              >
                {busy === `topup:${tier.amount_usd}` ? '正在跳转…' : tierLabel(tier)}
              </button>
            ))}
          </div>
        )}
        {!paymentsReady && <p className="dt-muted">{PAYMENTS_DISABLED_HINT}</p>}
        {autoTopupAllowed && (
          <div className="dt-billing-autotopup">
            <label className={`dt-toggle${busy === 'auto-topup' ? ' is-disabled' : ''}`}>
              <span>
                <strong>自动充值</strong>
                <small>
                  {account.has_payment_method
                    ? '余额低于阈值时，用已保存的支付卡自动充值'
                    : '需要先完成一次充值以保存支付卡'}
                </small>
              </span>
              <input
                checked={account.auto_topup_enabled}
                disabled={busy === 'auto-topup' || !paymentsReady}
                onChange={(event) => { void saveAutoTopup(event.target.checked) }}
                type="checkbox"
              />
              <span aria-hidden="true" className="dt-toggle__track"><span /></span>
            </label>
            <div className="dt-billing-autotopup__form">
              <label className="dt-field">
                <span>余额低于（$）</span>
                <input
                  inputMode="decimal"
                  min={0}
                  onChange={(event) => setAutoTopupThreshold(event.target.value)}
                  step="1"
                  type="number"
                  value={autoTopupThreshold}
                />
              </label>
              <label className="dt-field">
                <span>自动充值（$）</span>
                <input
                  inputMode="decimal"
                  min={1}
                  onChange={(event) => setAutoTopupAmount(event.target.value)}
                  step="1"
                  type="number"
                  value={autoTopupAmount}
                />
              </label>
              <button
                className="dt-button dt-button--secondary dt-button--small"
                disabled={busy === 'auto-topup' || !paymentsReady}
                onClick={() => { void saveAutoTopup(true) }}
                type="button"
              >
                {account.auto_topup_enabled ? '保存设置' : '开启并保存'}
              </button>
            </div>
          </div>
        )}
      </section>

      <section className="dt-account-usage" aria-label="用量">
        <div>
          <strong>最近用量</strong>
          <small>实际扣费按秒和 token 结算</small>
        </div>
        {sessionCost && sessionCost.total_usd > 0 && (
          <div className="dt-account-usage__row dt-account-usage__row--session">
            <span>
              <strong>本场会话</strong>
              <small>
                {[
                  sessionCost.transcription_usd > 0
                    ? `转录 ${formatUsageUSD(sessionCost.transcription_usd)}（≈ ${
                      Math.max(1, Math.round(sessionCost.transcription_seconds / 60))
                    } 分钟）`
                    : '',
                  sessionCost.translation_usd > 0
                    ? `翻译 ${formatUsageUSD(sessionCost.translation_usd)}`
                    : '',
                  sessionCost.ai_usd > 0
                    ? `AI 功能 ${formatUsageUSD(sessionCost.ai_usd)}（另计）`
                    : '',
                ].filter(Boolean).join(' · ')}
              </small>
            </span>
            <strong>{formatUsageUSD(sessionCost.transcription_usd + sessionCost.translation_usd)}</strong>
          </div>
        )}
        {usage.length === 0 ? (
          <p className="dt-muted">当前会话暂无计费用量。</p>
        ) : usage.map((item) => (
          <div className="dt-account-usage__row" key={item.id}>
            <span>
              <strong>{usageActionLabels[item.action] ?? item.action}</strong>
              <small>
                {item.model || '默认服务'} · {formatDateTime(item.created_at)}
                {' · '}
                <em className={`dt-usage-source dt-usage-source--${item.grant_usd > 0 ? 'grant' : 'wallet'}`}>
                  {paidFromLabel(item)}
                </em>
              </small>
            </span>
            <strong>{formatUsageUSD(item.cost_usd)}</strong>
          </div>
        ))}
        <button
          className="dt-button dt-button--text dt-button--small"
          onClick={() => setLedgerOpen((value) => !value)}
          type="button"
        >
          {ledgerOpen ? '收起流水' : '查看余额流水'}
        </button>
        {ledgerOpen && (
          <div className="dt-billing-ledger">
            {ledgerLoading && !ledger && <p className="dt-muted">正在读取流水…</p>}
            {ledger && ledger.payments.length > 0 && (
              <>
                <small className="dt-billing-ledger__title">支付记录</small>
                {ledger.payments.map((payment) => (
                  <div className="dt-account-usage__row" key={payment.id}>
                    <span>
                      <strong>{paymentKindLabels[payment.kind] ?? payment.kind}</strong>
                      <small>
                        {payment.description || payment.status} · {formatDateTime(payment.created_at)}
                        {payment.bonus_usd > 0 ? ` · 赠送 ${formatUSD(payment.bonus_usd)}` : ''}
                      </small>
                    </span>
                    <strong>{formatUSD(payment.amount_usd)}</strong>
                  </div>
                ))}
              </>
            )}
            {ledger && (
              <>
                <small className="dt-billing-ledger__title">余额变动</small>
                {ledger.ledger.length === 0 && <p className="dt-muted">暂无余额变动。</p>}
                {mergeUsageLedger(ledger.ledger).map((row) => {
                  const entry = row.primary
                  // Float residue below display precision reads as zero.
                  const netUSD = Math.abs(row.netUSD) < 0.000005 ? 0 : row.netUSD
                  const fullyRefunded = row.merged && netUSD === 0
                  return (
                    <div className="dt-account-usage__row" key={row.key}>
                      <span>
                        <strong>
                          {ledgerTypeLabels[entry.transaction_type] ?? entry.transaction_type}
                          {' · '}
                          {ledgerBucketLabel(row.buckets)}
                        </strong>
                        <small>
                          {(entry.description || '—').replace(' usage settlement', '')}
                          {row.merged && (fullyRefunded ? ' · 已全额退回' : ' · 已结算')}
                          {' · '}
                          {formatDateTime(entry.created_at)}
                        </small>
                      </span>
                      <strong className={netUSD < 0 ? 'dt-billing-debit' : 'dt-billing-credit'}>
                        {netUSD > 0 ? '+' : ''}{formatUsageUSD(netUSD)}
                      </strong>
                    </div>
                  )
                })}
              </>
            )}
          </div>
        )}
      </section>
    </>
  )
}
