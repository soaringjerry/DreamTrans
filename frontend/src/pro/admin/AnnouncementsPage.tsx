import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { adminFetch } from '../../admin/api'
import { formatDate, type Runner } from './shared'
import { Modal } from './ui'

type Level = 'info' | 'success' | 'warning'

interface Announcement {
  id: string
  title: string
  body: string
  link_url: string
  link_label: string
  level: Level
  active: boolean
  starts_at: string
  ends_at: string | null
  created_at: string
  updated_at: string
}

interface ListResult { announcements: Announcement[] }

interface Draft {
  title: string
  body: string
  link_url: string
  link_label: string
  level: Level
  active: boolean
  starts_at: string
  ends_at: string
}

const levelLabels: Record<Level, string> = { info: '通知', success: '好消息', warning: '提醒' }

function toLocalInput(value: string | null): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function newDraft(): Draft {
  return { title: '', body: '', link_url: '', link_label: '', level: 'info', active: true, starts_at: toLocalInput(new Date().toISOString()), ends_at: '' }
}

function draftFrom(item: Announcement): Draft {
  return {
    title: item.title, body: item.body, link_url: item.link_url, link_label: item.link_label,
    level: item.level, active: item.active, starts_at: toLocalInput(item.starts_at), ends_at: toLocalInput(item.ends_at),
  }
}

function stateLabel(item: Announcement): { text: string; good: boolean } {
  const now = Date.now()
  if (!item.active) return { text: '已暂停', good: false }
  if (Date.parse(item.starts_at) > now) return { text: '待发布', good: false }
  if (item.ends_at && Date.parse(item.ends_at) <= now) return { text: '已结束', good: false }
  return { text: '展示中', good: true }
}

export function AnnouncementsPage({ run }: { run: Runner }) {
  const [items, setItems] = useState<Announcement[]>([])
  const [generation, setGeneration] = useState(0)
  const [editing, setEditing] = useState<{ id: string | null; draft: Draft } | null>(null)
  const [deleting, setDeleting] = useState<Announcement | null>(null)
  const [busy, setBusy] = useState(false)
  const reload = useCallback(() => setGeneration((n) => n + 1), [])

  useEffect(() => {
    let current = true
    void run(() => adminFetch<ListResult>('/api/admin/announcements')).then((data) => { if (current && data) setItems(data.announcements) })
    return () => { current = false }
  }, [generation, run])

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!editing || busy) return
    const { id, draft } = editing
    setBusy(true)
    const payload = {
      title: draft.title, body: draft.body, link_url: draft.link_url, link_label: draft.link_label,
      level: draft.level, active: draft.active,
      starts_at: new Date(draft.starts_at).toISOString(),
      ends_at: draft.ends_at ? new Date(draft.ends_at).toISOString() : null,
    }
    const saved = await run(
      () => adminFetch<Announcement>(id ? `/api/admin/announcements/${id}` : '/api/admin/announcements', {
        method: id ? 'PUT' : 'POST', body: JSON.stringify(payload),
      }),
      id ? '公告已更新' : '公告已发布',
    )
    setBusy(false)
    if (saved) { setEditing(null); reload() }
  }

  async function toggle(item: Announcement) {
    if (busy) return
    setBusy(true)
    await run(() => adminFetch(`/api/admin/announcements/${item.id}`, { method: 'PATCH', body: JSON.stringify({ active: !item.active }) }), item.active ? '公告已暂停' : '公告已恢复')
    setBusy(false)
    reload()
  }

  async function remove() {
    if (!deleting || busy) return
    setBusy(true)
    const ok = await run(() => adminFetch(`/api/admin/announcements/${deleting.id}`, { method: 'DELETE' }), '公告已删除')
    setBusy(false)
    if (ok) { setDeleting(null); reload() }
  }

  const draft = editing?.draft

  return <div className="pa-stack">
    <section className="pa-card">
      <div className="pa-list-heading"><div><h2>站内公告</h2><p>发布后登录用户会在工作区顶部看到横幅，关闭后不再出现；未登录访客的关闭记录只保存在浏览器里。</p></div><span className="pa-count">{items.length} 条</span></div>
      <p className="pa-form-note">适合新功能上线、价格调整、维护通知。涉及价格或权利的变更，记得按条款第 13 节留足通知期。</p>
      <div className="pa-promotion-actions">
        <button className="pa-button pa-button--primary" onClick={() => setEditing({ id: null, draft: newDraft() })} type="button">发布公告</button>
        <button className="pa-button" onClick={reload} type="button">刷新</button>
      </div>
    </section>
    <section className="pa-card">
      <div className="pa-table-wrap"><table className="pa-table"><thead><tr>
        <th>标题 / 内容</th><th>类型</th><th>展示时间</th><th>状态</th><th>操作</th>
      </tr></thead><tbody>
        {items.map((item) => {
          const state = stateLabel(item)
          return <tr key={item.id}>
            <td><strong>{item.title}</strong>{item.body && <div>{item.body.length > 140 ? `${item.body.slice(0, 140)}…` : item.body}</div>}{item.link_url && <small><code>{item.link_url}</code></small>}</td>
            <td>{levelLabels[item.level]}</td>
            <td><div>{formatDate(item.starts_at)}</div><small>{item.ends_at ? `至 ${formatDate(item.ends_at)}` : '不自动结束'}</small></td>
            <td><span className={`pa-status ${state.good ? 'pa-status--good' : ''}`}>{state.text}</span></td>
            <td><div className="pa-promotion-actions">
              <button className="pa-button" disabled={busy} onClick={() => setEditing({ id: item.id, draft: draftFrom(item) })} type="button">编辑</button>
              <button className="pa-button" disabled={busy} onClick={() => { void toggle(item) }} type="button">{item.active ? '暂停' : '恢复'}</button>
              <button className="pa-button pa-button--danger" disabled={busy} onClick={() => setDeleting(item)} type="button">删除</button>
            </div></td>
          </tr>
        })}
        {items.length === 0 && <tr><td colSpan={5} className="pa-table-empty">还没有公告</td></tr>}
      </tbody></table></div>
    </section>
    {editing && draft && <Modal wide title={editing.id ? '编辑公告' : '发布公告'} onClose={() => { if (!busy) setEditing(null) }} footer={<button className="pa-button pa-button--primary" disabled={busy} form="announcement-form" type="submit">{busy ? '保存中…' : editing.id ? '保存修改' : '发布'}</button>}>
      <form className="pa-dialog-form" id="announcement-form" onSubmit={(event) => { void save(event) }}>
        <label className="pa-full-width"><span>标题</span><input required maxLength={120} value={draft.title} onChange={(event) => setEditing({ ...editing, draft: { ...draft, title: event.target.value } })} placeholder="训练计划上线：加入可享转录折扣" /></label>
        <label className="pa-full-width"><span>正文（可留空，支持换行）</span><textarea maxLength={2000} rows={5} value={draft.body} onChange={(event) => setEditing({ ...editing, draft: { ...draft, body: event.target.value } })} /></label>
        <label><span>类型</span><select value={draft.level} onChange={(event) => setEditing({ ...editing, draft: { ...draft, level: event.target.value as Level } })}>
          {(Object.keys(levelLabels) as Level[]).map((level) => <option key={level} value={level}>{levelLabels[level]}</option>)}
        </select></label>
        <label><span>状态</span><select value={draft.active ? 'on' : 'off'} onChange={(event) => setEditing({ ...editing, draft: { ...draft, active: event.target.value === 'on' } })}>
          <option value="on">展示</option><option value="off">暂停</option>
        </select></label>
        <label><span>链接（可选）</span><input maxLength={500} value={draft.link_url} onChange={(event) => setEditing({ ...editing, draft: { ...draft, link_url: event.target.value } })} placeholder="/privacy 或 https://…" /></label>
        <label><span>链接文字</span><input maxLength={60} value={draft.link_label} onChange={(event) => setEditing({ ...editing, draft: { ...draft, link_label: event.target.value } })} placeholder="查看详情" /></label>
        <label><span>开始展示</span><input required type="datetime-local" value={draft.starts_at} onChange={(event) => setEditing({ ...editing, draft: { ...draft, starts_at: event.target.value } })} /></label>
        <label><span>结束展示（留空则手动下线）</span><input type="datetime-local" value={draft.ends_at} onChange={(event) => setEditing({ ...editing, draft: { ...draft, ends_at: event.target.value } })} /></label>
      </form>
    </Modal>}
    {deleting && <Modal danger title="删除公告" onClose={() => { if (!busy) setDeleting(null) }} footer={<button className="pa-button pa-button--danger" disabled={busy} onClick={() => { void remove() }} type="button">{busy ? '删除中…' : '确认删除'}</button>}>
      <p>删除「{deleting.title}」后用户端立即消失，且无法恢复。只是暂时下线的话请用「暂停」。</p>
    </Modal>}
  </div>
}
