import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { adminFetch, formatUSD, listPlans, type Plan } from '../../admin/api'
import { formatDate, type Runner } from './shared'
import { Modal, Pagination } from './ui'

interface Promotion {
  id: string
  code: string
  name: string
  channel: string
  tags: string[]
  enabled: boolean
  expires_at: string
  max_registrations: number
  grant_usd: number
  grant_days: number
  plan_code: string
  plan_days: number
  registrations: number
  verified: number
  rewarded: number
}
interface Registration {
  id: string
  user_id: string
  email: string
  name: string
  verified: boolean
  registered_at: string
  rewarded_at: string | null
  plan_until: string | null
}
interface ListResult { invites: Promotion[]; total: number }
interface RecipientsResult { registrations: Registration[]; total: number }

function newDraft() {
  return { name: '', channel: '', tags: '', code: '', expires_at: '', max_registrations: '100', grant_usd: '0', grant_days: '30', plan_code: '', plan_days: '30' }
}
function inviteLink(code: string) {
  const url = new URL('/pro', window.location.origin)
  url.searchParams.set('invite', code)
  return url.href
}
function stateLabel(p: Promotion) {
  if (!p.enabled) return '已暂停'
  if (Date.parse(p.expires_at) <= Date.now()) return '已过期'
  if (p.registrations >= p.max_registrations) return '已满'
  return '启用中'
}

export function PromotionsPage({ run }: { run: Runner }) {
  const [result, setResult] = useState<ListResult>({ invites: [], total: 0 })
  const [page, setPage] = useState(1)
  const [search, setSearch] = useState('')
  const [query, setQuery] = useState('')
  const [generation, setGeneration] = useState(0)
  const [creating, setCreating] = useState(false)
  const [busy, setBusy] = useState(false)
  const [plans, setPlans] = useState<Plan[]>([])
  const [draft, setDraft] = useState(newDraft)
  const [selected, setSelected] = useState<Promotion | null>(null)
  const [recipientPage, setRecipientPage] = useState(1)
  const [recipients, setRecipients] = useState<RecipientsResult>({ registrations: [], total: 0 })
  const [created, setCreated] = useState<Promotion | null>(null)
  const reload = useCallback(() => setGeneration((n) => n + 1), [])

  useEffect(() => {
    let current = true
    const params = new URLSearchParams({ page: String(page), search: query })
    void run(() => adminFetch<ListResult>(`/api/admin/promotions?${params}`)).then((data) => { if (current && data) setResult(data) })
    return () => { current = false }
  }, [page, query, generation, run])
  useEffect(() => {
    let current = true
    void run(listPlans).then((data) => { if (current && data) setPlans(data) })
    return () => { current = false }
  }, [run])
  useEffect(() => {
    if (!selected) return
    let current = true
    void run(() => adminFetch<RecipientsResult>(`/api/admin/promotions/${selected.id}?page=${recipientPage}`)).then((data) => { if (current && data) setRecipients(data) })
    return () => { current = false }
  }, [selected, recipientPage, run])

  async function create(event: FormEvent) {
    event.preventDefault()
    if (busy) return
    setBusy(true)
    const saved = await run(() => adminFetch<Promotion>('/api/admin/promotions', {
      method: 'POST', body: JSON.stringify({ ...draft,
        tags: draft.tags.split(/[,，]/).map((tag) => tag.trim()).filter(Boolean),
        expires_at: new Date(draft.expires_at).toISOString(),
        max_registrations: Number(draft.max_registrations), grant_usd: Number(draft.grant_usd),
        grant_days: Number(draft.grant_days), plan_days: Number(draft.plan_days),
      }),
    }), '推广邀请已创建')
    setBusy(false)
    if (saved) { setCreating(false); setCreated(saved); reload() }
  }

  return <div className="pa-stack">
    <section className="pa-card pa-promotion-intro">
      <div className="pa-list-heading"><div><h2>渠道活动</h2><p>用独立邀请链接追踪来源，为新用户提供活动权益。</p></div><span className="pa-count">{result.total} 个匹配活动</span></div>
      <p className="pa-form-note pa-promotion-help">成功注册即占用名额，赠送需通过邮箱验证与风控审核。暂停或到期不影响已接受的邀请。</p>
      <div className="pa-promotion-actions">
        <form onSubmit={(event) => { event.preventDefault(); setQuery(search.trim()); setPage(1) }}>
          <input aria-label="搜索活动、渠道、标签或邀请码" onChange={(event) => setSearch(event.target.value)} placeholder="活动、渠道、标签或邀请码" value={search} />
          <button className="pa-button" type="submit">搜索</button>
        </form>
        <button className="pa-button pa-button--primary" onClick={() => { setDraft(newDraft()); setCreating(true) }} type="button">创建推广邀请</button>
        <button className="pa-button" onClick={reload} type="button">刷新</button>
      </div>
    </section>
    {created && <section className="pa-card pa-promotion-intro" role="status">
      <strong>{created.name} · {created.channel}</strong>
      <p>邀请码：{created.code}</p>
      <input aria-label="新建邀请链接" readOnly value={inviteLink(created.code)} onFocus={(event) => event.target.select()} />
      <button className="pa-button" onClick={() => { void run(() => navigator.clipboard.writeText(inviteLink(created.code)), '链接已复制') }} type="button">复制邀请链接</button>
    </section>}
    <section className="pa-card">
      <div className="pa-table-wrap"><table className="pa-table"><thead><tr>
        <th>活动 / 渠道 / 标签</th><th>赠送权益</th><th>注册 / 验证 / 领取</th><th>状态 / 截止时间</th><th>操作</th>
      </tr></thead><tbody>
        {result.invites.map((p) => <tr key={p.id}>
          <td><strong>{p.name}</strong><div>{p.channel}</div><div className="pa-tag-list">{p.tags.map((tag) => <span className="pa-tag" key={tag}>{tag}</span>)}</div><small><code>{p.code}</code></small></td>
          <td>{p.grant_usd > 0 && <div>{formatUSD(p.grant_usd)} / {p.grant_days} 天</div>}{p.plan_code && <div>{p.plan_code} / {p.plan_days} 天</div>}{!p.grant_usd && !p.plan_code && '仅渠道归因'}</td>
          <td><strong className="pa-tabular">{p.registrations} / {p.verified} / {p.rewarded}</strong><small>注册上限 {p.max_registrations} 人</small><progress className="pa-progress" aria-label={`${p.name} 注册名额使用情况`} value={p.registrations} max={p.max_registrations} /></td>
          <td><span className={`pa-status ${stateLabel(p) === '启用中' ? 'pa-status--good' : ''}`}>{stateLabel(p)}</span><small>{formatDate(p.expires_at)}</small></td>
          <td><div className="pa-promotion-actions">
            <button className="pa-button" type="button" onClick={() => { setCreated(p); void run(() => navigator.clipboard.writeText(inviteLink(p.code)), '链接已复制') }}>复制链接</button>
            <button className="pa-button" type="button" onClick={() => { setRecipientPage(1); setRecipients({ registrations: [], total: 0 }); setSelected(p) }}>注册记录</button>
            <button className="pa-button" disabled={busy} type="button" onClick={() => {
              setBusy(true)
              void run(() => adminFetch(`/api/admin/promotions/${p.id}`, { method: 'PATCH', body: JSON.stringify({ enabled: !p.enabled }) }), p.enabled ? '已暂停新注册' : '已启用').then(() => { setBusy(false); reload() })
            }}>{p.enabled ? '暂停' : '启用'}</button>
          </div></td>
        </tr>)}
        {result.invites.length === 0 && <tr><td colSpan={5} className="pa-table-empty">暂无匹配的推广邀请</td></tr>}
      </tbody></table></div>
      <Pagination page={page} pageSize={20} total={result.total} onChange={setPage} />
    </section>
    {creating && <Modal wide title="创建推广邀请" onClose={() => { if (!busy) setCreating(false) }} footer={<button className="pa-button pa-button--primary" disabled={busy} form="create-promotion" type="submit">{busy ? '创建中…' : '创建邀请'}</button>}>
      <form className="pa-dialog-form pa-promotion-form" id="create-promotion" onSubmit={(event) => { void create(event) }}>
        <label><span>活动名称</span><input required maxLength={100} value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} placeholder="2026 开学季" /></label>
        <label><span>渠道</span><input required maxLength={100} value={draft.channel} onChange={(event) => setDraft({ ...draft, channel: event.target.value })} placeholder="小红书 / 博主 A" /></label>
        <label><span>用户来源标签</span><input value={draft.tags} onChange={(event) => setDraft({ ...draft, tags: event.target.value })} placeholder="开学季, 小红书, 博主A" /><small>逗号分隔，最多 20 个；注册来源固定保留。</small></label>
        <label><span>邀请码（留空自动生成）</span><input maxLength={48} minLength={6} pattern="[A-Za-z0-9][A-Za-z0-9_-]{5,47}" value={draft.code} onChange={(event) => setDraft({ ...draft, code: event.target.value })} placeholder="XHS2026A" /></label>
        <label><span>注册截止时间</span><input required type="datetime-local" value={draft.expires_at} onChange={(event) => setDraft({ ...draft, expires_at: event.target.value })} /></label>
        <label><span>最多注册人数</span><input required type="number" min={1} max={1000000} value={draft.max_registrations} onChange={(event) => setDraft({ ...draft, max_registrations: event.target.value })} /></label>
        <label><span>额外活动余额（USD）</span><input required type="number" min={0} max={10000} step="0.01" value={draft.grant_usd} onChange={(event) => setDraft({ ...draft, grant_usd: event.target.value })} /></label>
        <label><span>余额有效天数（从领取起）</span><input required type="number" min={1} max={3650} value={draft.grant_days} onChange={(event) => setDraft({ ...draft, grant_days: event.target.value })} /></label>
        <label><span>赠送套餐</span><select aria-label="赠送套餐" value={draft.plan_code} onChange={(event) => setDraft({ ...draft, plan_code: event.target.value })}><option value="">不赠送套餐</option>{plans.filter((p) => p.active && p.code !== 'free').map((p) => <option key={p.code} value={p.code}>{p.name}</option>)}</select></label>
        {draft.plan_code && <label><span>套餐有效天数（从领取起）</span><input required type="number" min={1} max={3650} value={draft.plan_days} onChange={(event) => setDraft({ ...draft, plan_days: event.target.value })} /></label>}
        <p className="pa-form-note pa-full-width pa-callout">最多赠送 {formatUSD(Number(draft.grant_usd) * Number(draft.max_registrations))} 活动余额{draft.plan_code ? `，以及 ${draft.max_registrations} 份 ${draft.plan_days} 天套餐` : ''}。余额额外叠加注册试用额度；套餐不含消费余额、不自动续费，付费套餐优先生效。活动创建后渠道和权益不可修改，如需调整请暂停并新建。</p>
      </form>
    </Modal>}
    {selected && <Modal wide footer={null} title={`${selected.name} · ${selected.channel} · 注册记录`} onClose={() => setSelected(null)}>
      <p>{selected.tags.join(' · ')}</p>
      <div className="pa-table-wrap"><table className="pa-table"><thead><tr><th>昵称 / 邮箱</th><th>注册时间</th><th>验证 / 权益</th></tr></thead><tbody>
        {recipients.registrations.map((r) => <tr key={r.id}><td>{r.user_id ? <>{r.name}<div>{r.email}</div></> : '账号已删除'}</td><td>{formatDate(r.registered_at)}</td><td>{r.verified ? '已验证' : '待验证'}<div>{r.rewarded_at ? `已领取 ${formatDate(r.rewarded_at)}` : '待领取'}</div>{r.plan_until && <small>套餐至 {formatDate(r.plan_until)}</small>}</td></tr>)}
        {!recipients.registrations.length && <tr><td colSpan={3}>暂无注册记录</td></tr>}
      </tbody></table></div>
      <Pagination page={recipientPage} pageSize={20} total={recipients.total} onChange={setRecipientPage} />
    </Modal>}
  </div>
}
