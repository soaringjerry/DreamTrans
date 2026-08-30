import { useCallback, useEffect, useState } from 'react'
import { initAuth, type User as AuthUser } from './api/auth'
import { CostsPage } from './admin/CostsPage'
import { CustomersPage } from './admin/CustomersPage'
import { ModelsPage } from './admin/ModelsPage'
import { OverviewPage } from './admin/OverviewPage'
import { PlansPage } from './admin/PlansPage'
import { SettingsPage } from './admin/SettingsPage'
import { TenantsPage } from './admin/TenantsPage'
import { UsersPage } from './admin/UsersPage'
import { errorMessage } from './admin/shared'
import { ErrorBanner } from './admin/ui'
import './pro-admin.css'

type Tab =
  | 'overview'
  | 'users'
  | 'customers'
  | 'plans'
  | 'costs'
  | 'models'
  | 'tenants'
  | 'settings'

const nav: Array<{ id: Tab; label: string; superOnly?: boolean }> = [
  { id: 'overview', label: '概览', superOnly: true },
  { id: 'users', label: '用户' },
  { id: 'customers', label: '客户', superOnly: true },
  { id: 'plans', label: '会员与充值', superOnly: true },
  { id: 'costs', label: '成本与加价', superOnly: true },
  { id: 'models', label: '模型', superOnly: true },
  { id: 'tenants', label: '组织', superOnly: true },
  { id: 'settings', label: '系统设置', superOnly: true },
]

export default function ProAdmin() {
  const [viewer, setViewer] = useState<AuthUser | null>(null)
  const [ready, setReady] = useState(false)
  const [tab, setTab] = useState<Tab>('overview')
  const [customerFocus, setCustomerFocus] = useState<string | null>(null)
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

  function navigate(next: Tab) {
    setCustomerFocus(null)
    setError('')
    setTab(next)
  }

  function openCustomer(userId: string) {
    setCustomerFocus(userId)
    setError('')
    setTab('customers')
  }

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
              onClick={() => navigate(item.id)}
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

        {tab === 'overview' && <OverviewPage />}
        {tab === 'users' && <UsersPage isSuper={isSuper} onOpenCustomer={openCustomer} run={run} />}
        {tab === 'customers' && <CustomersPage initialUserId={customerFocus} key={customerFocus || 'list'} run={run} />}
        {tab === 'plans' && <PlansPage onOpenSettings={() => navigate('settings')} run={run} />}
        {tab === 'costs' && <CostsPage run={run} />}
        {tab === 'models' && <ModelsPage run={run} />}
        {tab === 'tenants' && <TenantsPage run={run} />}
        {tab === 'settings' && <SettingsPage run={run} />}
      </main>
    </div>
  )
}
