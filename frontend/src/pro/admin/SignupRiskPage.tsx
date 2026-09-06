import { useEffect, useState, type FormEvent } from 'react'
import { adminFetch } from '../../admin/api'
import { formatDate, type Runner } from './shared'
import { Modal, Pagination } from './ui'

type Decision = 'allowed' | 'review' | 'approved' | 'denied'
interface Settings { strict_mode: boolean; network_burst_limit: number; prefix_hourly_limit: number; daily_reward_budget_cents: number; enabled: boolean; device_limit: number; network_daily_limit: number; automatic_daily_limit: number }
interface Profile {
  score: number; budget_blocked: boolean; evidence: { network_burst?: number; prefix_hourly?: number; fingerprint_count?: number; linked_denied?: number; browser?: string; platform?: string };
  id: string; user_id: string; email: string; name: string; verified: boolean
  decision: Decision; reasons: string[]; device_count: number; network_count: number; daily_count: number
  rules: Settings; created_at: string; reviewed_at: string | null; review_note: string; promotion: string; channel: string
}
interface AuditEntry { actor: string; created_at: string; details: { before: Decision; after: Decision; note: string } }
const labels: Record<Decision, string> = { allowed: '自动放行', review: '待审核', approved: '人工放行', denied: '拒绝赠送' }
const reasons: Record<string, string> = {
  strict_mode: '严格模式：所有新注册赠送均需审核', network_burst: '同一网络 10 分钟内集中注册', prefix_velocity: '同一网段 1 小时内集中注册', fingerprint_cluster: '浏览器特征与来源网段关联重复注册', linked_denied: '关联到已拒绝赠送的注册', automation: 'UA 或浏览器上报自动化特征', ua_missing: '缺少 UA', browser_missing: '缺少浏览器辅助信号', browser_inconsistent: 'UA、平台或语言信号不一致',
  previous_email: '该邮箱曾注册（包括已删除账号）', missing_device: '缺少有效浏览器标识', missing_network: '无法确认来源网络',
  device_accounts: '同一浏览器重复注册', network_velocity: '同一网络注册过于集中', daily_cap: '达到全站自动放行上限',
}

export function SignupRiskPage({ run }: { run: Runner }) {
  const [settings, setSettings] = useState<Settings | null>(null)
  const [result, setResult] = useState<{ profiles: Profile[]; total: number }>({ profiles: [], total: 0 })
  const [budget, setBudget] = useState<{ limit_cents: number; spent_usd: string; blocked: number } | null>(null)
  const [decision, setDecision] = useState('review')
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [generation, setGeneration] = useState(0)
  const [selected, setSelected] = useState<Profile | null>(null)
  const [audit, setAudit] = useState<AuditEntry[]>([])
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    let active = true
    void run(() => adminFetch<Settings>('/api/admin/signup-risk/settings')).then((data) => { if (active && data) setSettings(data) })
    return () => { active = false }
  }, [run])
  useEffect(() => {
    let active = true
    void run(() => adminFetch<NonNullable<typeof budget>>('/api/admin/signup-risk/budget')).then((data) => { if (active && data) setBudget(data) })
    const params = new URLSearchParams({ decision, search: query, page: String(page) })
    void run(() => adminFetch<typeof result>(`/api/admin/signup-risk?${params}`)).then((data) => { if (active && data) setResult(data) })
    return () => { active = false }
  }, [run, page, query, decision, generation])
  useEffect(() => {
    if (!selected) return
    let active = true
    void run(() => adminFetch<{ audit: AuditEntry[] }>(`/api/admin/signup-risk/${selected.id}`)).then((data) => { if (active && data) setAudit(data.audit || []) })
    return () => { active = false }
  }, [selected, run])
  async function save(event: FormEvent) {
    event.preventDefault()
    if (!settings || busy) return
    setBusy(true)
    await run(() => adminFetch('/api/admin/signup-risk/settings', { method: 'PUT', body: JSON.stringify(settings) }), '风控设置已保存；已待审账号仍需单独审核')
    setBusy(false)
    setGeneration((n) => n + 1)
  }
  async function review(next: 'approved' | 'denied') {
    if (!selected || !note.trim() || busy) return
    setBusy(true)
    const saved = await run(() => adminFetch(`/api/admin/signup-risk/${selected.id}`, { method: 'POST', body: JSON.stringify({ decision: next, note: note.trim() }) }), next === 'approved' ? '审核已保存；预算不足时权益暂缓，可在预算恢复后重试' : '已拒绝自动赠送，账号仍可使用')
    setBusy(false)
    setGeneration((n) => n + 1)
    if (saved) setSelected(null)
  }
  return <div className="pa-stack">
    <section className="pa-card pa-promotion-intro">
      <h2>注册赠送风控</h2>
      <p>疑似重复注册时暂缓试用余额、活动余额和赠送套餐，保留登录与正常付费使用。共享网络不等于同一个人，请结合来源渠道和实际情况复核。</p>
      {budget && <p>滚动 24 小时赠送余额：已发 ${budget.spent_usd} / 上限 ${(budget.limit_cents / 100).toFixed(2)}；预算暂缓 {budget.blocked} 个账号。人工批准也受预算约束，套餐不折算为美元。</p>}
      <details><summary>风控规则与阈值</summary>{settings && <form className="pa-dialog-form" onSubmit={(event) => { void save(event) }}>
        <label><span>严格模式：所有新注册赠送先人工审核</span><input type="checkbox" checked={settings.strict_mode} onChange={(event) => setSettings({ ...settings, strict_mode: event.target.checked })} /></label>
        <label><span>同一网络 10 分钟注册数阈值</span><input type="number" required min={1} max={10000} value={settings.network_burst_limit} onChange={(event) => setSettings({ ...settings, network_burst_limit: Number(event.target.value) })} /></label>
        <label><span>同一网段 1 小时注册数阈值</span><input type="number" required min={1} max={100000} value={settings.prefix_hourly_limit} onChange={(event) => setSettings({ ...settings, prefix_hourly_limit: Number(event.target.value) })} /></label>
        <label><span>滚动 24 小时赠送余额预算（美元）</span><input type="number" required min={0} max={1000000} step="0.01" value={settings.daily_reward_budget_cents / 100} onChange={(event) => setSettings({ ...settings, daily_reward_budget_cents: Math.round(Number(event.target.value) * 100) })} /></label>
        <label><span>自动暂缓可疑注册的赠送权益</span><input type="checkbox" checked={settings.enabled} onChange={(event) => setSettings({ ...settings, enabled: event.target.checked })} /></label>
        <label><span>同一浏览器 30 天内允许自动放行的注册数</span><input type="number" required min={1} max={100} value={settings.device_limit} onChange={(event) => setSettings({ ...settings, device_limit: Number(event.target.value) })} /></label>
        <label><span>同一网络 24 小时注册数阈值</span><input type="number" required min={1} max={10000} value={settings.network_daily_limit} onChange={(event) => setSettings({ ...settings, network_daily_limit: Number(event.target.value) })} /></label>
        <label><span>全站 24 小时自动放行新注册上限</span><input type="number" required min={1} max={100000} value={settings.automatic_daily_limit} onChange={(event) => setSettings({ ...settings, automatic_daily_limit: Number(event.target.value) })} /></label>
        <p className="pa-form-note">浏览器和网络计数包括未验证、已删除账号。关闭严格模式后，自动暂缓开关控制是否仅观察；不会释放已有待审权益。预算独立生效，设为 0 暂停发放正金额的注册赠送。清除 Cookie 可改变浏览器标识，因此它不能单独证明身份。</p>
        <button className="pa-button pa-button--primary" disabled={busy} type="submit">保存风控设置</button>
      </form>}</details>
    </section>
    <section className="pa-card pa-promotion-intro">
      <form className="pa-promotion-actions" onSubmit={(event) => { event.preventDefault(); setQuery(search.trim()); setPage(1) }}>
        <select aria-label="审核状态" value={decision} onChange={(event) => { setDecision(event.target.value); setPage(1) }}><option value="">所有状态</option><option value="budget_hold">预算暂缓</option>{Object.entries(labels).map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select>
        <input aria-label="搜索风控账号" value={search} placeholder="邮箱、昵称或渠道" onChange={(event) => setSearch(event.target.value)} />
        <button className="pa-button" type="submit">搜索</button><button className="pa-button" type="button" onClick={() => setGeneration((n) => n + 1)}>刷新</button>
      </form>
      <div className="pa-table-wrap"><table><thead><tr><th>账号 / 渠道</th><th>风险信号</th><th>注册时间 / 状态</th><th>处理</th></tr></thead><tbody>
        {result.profiles.map((p) => <tr key={p.id}>
          <td>{p.user_id ? <><strong>{p.name}</strong><div>{p.email}</div></> : '账号已删除'}<small>{p.promotion} {p.channel}</small></td>
          <td><strong>风险分数 {p.score ?? 0} / 100</strong>{p.reasons.length ? p.reasons.map((reason) => <div key={reason}>{reasons[reason] || reason}</div>) : '未触发规则'}<small>之前的注册：浏览器 {p.device_count} · 网络 {p.network_count} · 全站放行 {p.daily_count}</small></td>
          <td>{formatDate(p.created_at)}<div>{labels[p.decision]}{p.budget_blocked ? ' · 预算暂缓' : ''} · {p.verified ? '邮箱已验证' : '邮箱未验证'}</div></td>
          <td><button className="pa-button" type="button" onClick={() => { setSelected(p); setNote(''); setAudit([]) }}>查看 / 审核</button></td>
        </tr>)}
        {!result.profiles.length && <tr><td colSpan={4}>暂无匹配记录</td></tr>}
      </tbody></table></div><Pagination page={page} pageSize={20} total={result.total} onChange={setPage} />
    </section>
    {selected && <Modal title="注册赠送审核" onClose={() => { if (!busy) setSelected(null) }} footer={<>
      {selected.user_id && (selected.decision !== 'allowed' || selected.budget_blocked) && <button className="pa-button pa-button--primary" disabled={busy || !note.trim()} type="button" onClick={() => { void review('approved') }}>{selected.decision === 'approved' ? '重试权益发放' : '放行赠送权益'}</button>}
      {selected.user_id && selected.decision === 'review' && <button className="pa-button" disabled={busy || !note.trim()} type="button" onClick={() => { void review('denied') }}>拒绝赠送</button>}
    </>}>
      <p>{selected.name} · {selected.email}</p><p>{selected.reasons.map((reason) => reasons[reason] || reason).join('；') || '未触发规则'}</p>
      <p>风险分数 {selected.score ?? 0} / 100（规则权重分数，不是作弊概率）。{selected.evidence?.browser} / {selected.evidence?.platform}</p>
      <p>注册前关联计数：同网络 10 分钟 {selected.evidence?.network_burst ?? 0}；同网段 1 小时 {selected.evidence?.prefix_hourly ?? 0}；相同浏览器特征 30 天 {selected.evidence?.fingerprint_count ?? 0}；关联拒绝记录 {selected.evidence?.linked_denied ?? 0}。浏览器特征可伪造，相同特征不等于同一个人。</p>
      {selected.budget_blocked && <p role="status">赠送因预算上限暂缓。提高预算或等待额度恢复后重试发放；重复批准不会绕过预算。</p>}
      <p>注册时阈值：浏览器 {selected.rules.device_limit} / 网络 {selected.rules.network_daily_limit} / 全站 {selected.rules.automatic_daily_limit}。</p>
      {selected.reviewed_at && <p>最近审核：{formatDate(selected.reviewed_at)} · {selected.review_note}</p>}
      {audit.length > 0 && <details><summary>审核记录（最近 50 条）</summary>{audit.map((entry, index) => <p key={index}>{formatDate(entry.created_at)} · {entry.actor} · {labels[entry.details.before]} → {labels[entry.details.after]}：{entry.details.note}</p>)}</details>}
      <p>放行后，邮箱已验证且启用的账号会领取权益；未验证账号在完成邮箱验证后领取。此操作不会封禁账号，也不会撤回已经发放的余额。</p>
      <label className="pa-dialog-form"><span>审核备注（必填）</span><textarea aria-label="审核备注" maxLength={500} value={note} onChange={(event) => setNote(event.target.value)} /></label>
    </Modal>}
  </div>
}
