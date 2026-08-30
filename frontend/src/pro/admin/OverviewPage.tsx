import { useEffect, useState } from 'react'
import {
  formatUSD,
  getBillingAnalytics,
  getGlobalStats,
  type AdminSystemStatsResponse,
  type BillingAnalytics,
} from '../../admin/api'
import { currentMonthKey, errorMessage, formatInteger, formatPercent } from './shared'
import { ErrorBanner, Metric } from './ui'

function marginPercent(margin: number, charged: number) {
  return charged > 0 ? formatPercent(margin / charged * 100) : '—'
}

function topEntries(record: Record<string, number>, limit = 6) {
  return Object.entries(record)
    .filter(([, value]) => Number.isFinite(value) && value !== 0)
    .sort((left, right) => right[1] - left[1])
    .slice(0, limit)
}

export function OverviewPage() {
  const [stats, setStats] = useState<AdminSystemStatsResponse | null>(null)
  const [statsLoading, setStatsLoading] = useState(true)
  const [statsError, setStatsError] = useState('')
  const [month, setMonth] = useState(currentMonthKey)
  const [analytics, setAnalytics] = useState<BillingAnalytics | null>(null)
  const [analyticsLoading, setAnalyticsLoading] = useState(true)
  const [analyticsError, setAnalyticsError] = useState('')

  useEffect(() => {
    let active = true
    void getGlobalStats().then((value) => {
      if (active) setStats(value)
    }).catch((reason) => {
      if (active) setStatsError(errorMessage(reason))
    }).finally(() => {
      if (active) setStatsLoading(false)
    })
    return () => { active = false }
  }, [])

  useEffect(() => {
    let active = true
    setAnalyticsLoading(true)
    setAnalyticsError('')
    void getBillingAnalytics(month).then((value) => {
      if (active) setAnalytics(value)
    }).catch((reason) => {
      if (active) setAnalyticsError(errorMessage(reason))
    }).finally(() => {
      if (active) setAnalyticsLoading(false)
    })
    return () => { active = false }
  }, [month])

  const billing = stats?.billing
  const billingUnavailable = Boolean(stats?.billing_error)
  const money = (value?: number) => (billing && !billingUnavailable && value !== undefined ? formatUSD(value) : '—')
  const byAction = billing ? topEntries(billing.usage_by_action) : []
  const byModel = billing ? topEntries(billing.usage_by_model) : []

  return (
    <div className="pa-stack">
      {statsError && <ErrorBanner message={`基础统计加载失败：${statsError}`} />}
      {stats?.billing_error && <ErrorBanner message="计费统计暂时不可用，仅显示基础计数。" />}
      <section className="pa-metrics">
        <Metric label="用户" loading={statsLoading} value={stats ? formatInteger(stats.basic.user_count) : '—'} hint={billing ? `近 30 天活跃 ${formatInteger(billing.active_users)}` : undefined} />
        <Metric label="组织" loading={statsLoading} value={stats ? formatInteger(stats.basic.tenant_count) : '—'} />
        <Metric label="会话" loading={statsLoading} value={stats ? formatInteger(stats.basic.session_count) : '—'} hint={stats ? `转写 ${formatInteger(stats.basic.transcript_count)} 条` : undefined} />
        <Metric label="有效会员" loading={statsLoading} value={billing && !billingUnavailable ? formatInteger(billing.active_members) : '—'} />
      </section>
      <section className="pa-metrics">
        <Metric label="本月扣费" loading={statsLoading} value={money(billing?.month_charged_usd)} hint={billing ? `累计扣费 ${formatUSD(billing.total_charged_usd)}` : undefined} />
        <Metric label="本月上游成本" loading={statsLoading} value={money(billing?.month_upstream_usd)} />
        <Metric label="本月毛利" loading={statsLoading} value={money(billing?.month_margin_usd)} hint={billing ? `毛利率 ${marginPercent(billing.month_margin_usd, billing.month_charged_usd)}` : undefined} />
        <Metric label="本月充值收入" loading={statsLoading} value={money(billing?.month_topup_usd)} />
        <Metric label="本月会员收入" loading={statsLoading} value={money(billing?.month_membership_usd)} />
        <Metric label="钱包余额总额" loading={statsLoading} value={money(billing?.total_wallet_usd)} hint="用户已付费、尚未消耗的金额" />
        <Metric label="赠送余额总额" loading={statsLoading} value={money(billing?.total_grant_usd)} hint="试用、充值赠送与活动额度（未过期）" />
        <Metric label="累计用量记录" loading={statsLoading} value={billing && !billingUnavailable ? formatInteger(billing.total_sessions) : '—'} hint="会话计数（含所有组织）" />
      </section>

      <section className="pa-card pa-section">
        <div className="pa-section__heading">
          <div><h2>月度经营分析</h2><p>金额来自不可变的用量与支付账本；退款不会计入扣费。</p></div>
          <label className="pa-month-picker">
            <span>月份</span>
            <input max={currentMonthKey()} onChange={(event) => { if (event.target.value) setMonth(event.target.value) }} type="month" value={month} />
          </label>
        </div>
        {analyticsError && <ErrorBanner message={`月度分析加载失败：${analyticsError}`} />}
        {analyticsLoading ? (
          <div className="pa-summary-grid pa-summary-grid--four">
            {[0, 1, 2, 3].map((item) => <span className="pa-skeleton pa-skeleton--panel" key={item} />)}
          </div>
        ) : analytics ? (
          <>
            <div className="pa-summary-grid pa-summary-grid--four">
              <div>
                <small>用量扣费</small>
                <strong>{formatUSD(analytics.charged_usd)}</strong>
                <em>赠送 {formatUSD(analytics.charged_from_grant_usd)} · 钱包 {formatUSD(analytics.charged_from_wallet_usd)}</em>
              </div>
              <div><small>上游成本</small><strong>{formatUSD(analytics.upstream_cost_usd)}</strong></div>
              <div>
                <small>毛利</small>
                <strong>{formatUSD(analytics.margin_usd)}</strong>
                <em>毛利率 {marginPercent(analytics.margin_usd, analytics.charged_usd)}</em>
              </div>
              <div>
                <small>用量记录</small>
                <strong>{formatInteger(analytics.usage_count)} 条</strong>
                <em>BYOK {formatInteger(analytics.byok_usage_count)} 条</em>
              </div>
            </div>
            <div className="pa-summary-grid pa-summary-grid--four pa-summary-grid--spaced">
              <div><small>充值收入</small><strong>{formatUSD(analytics.topup_revenue_usd)}</strong></div>
              <div><small>会员收入</small><strong>{formatUSD(analytics.membership_revenue_usd)}</strong></div>
              <div><small>退款</small><strong>{formatUSD(analytics.refunded_usd)}</strong></div>
              <div>
                <small>会员</small>
                <strong>{formatInteger(analytics.active_members)}</strong>
                <em>本月新增 {formatInteger(analytics.new_members)}</em>
              </div>
            </div>
            <div className="pa-footnotes">
              <span>月末未消耗钱包：{formatUSD(analytics.outstanding_wallet_usd)}</span>
              <span>月末未消耗赠送：{formatUSD(analytics.outstanding_grant_usd)}</span>
              <span>统计月份：{analytics.month_key}</span>
            </div>
          </>
        ) : (
          <div className="pa-empty">该月没有可用的分析数据。</div>
        )}
      </section>

      {billing && !billingUnavailable && (byAction.length > 0 || byModel.length > 0) && (
        <section className="pa-card pa-section">
          <div className="pa-section__heading">
            <div><h2>累计扣费分布</h2><p>按用途与模型汇总的历史扣费金额（不含退款）。</p></div>
          </div>
          <div className="pa-split">
            <div className="pa-table-wrap pa-table-wrap--compact"><table>
              <thead><tr><th>用途</th><th>累计扣费</th></tr></thead>
              <tbody>
                {byAction.length === 0 && <tr><td className="pa-table-empty" colSpan={2}>暂无记录。</td></tr>}
                {byAction.map(([action, value]) => (
                  <tr key={action}><td>{action || '—'}</td><td>{formatUSD(value)}</td></tr>
                ))}
              </tbody>
            </table></div>
            <div className="pa-table-wrap pa-table-wrap--compact"><table>
              <thead><tr><th>模型</th><th>累计扣费</th></tr></thead>
              <tbody>
                {byModel.length === 0 && <tr><td className="pa-table-empty" colSpan={2}>暂无记录。</td></tr>}
                {byModel.map(([model, value]) => (
                  <tr key={model}><td>{model || '（无模型）'}</td><td>{formatUSD(value)}</td></tr>
                ))}
              </tbody>
            </table></div>
          </div>
        </section>
      )}
    </div>
  )
}
