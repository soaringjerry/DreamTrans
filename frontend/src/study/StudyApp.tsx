import { useCallback, useEffect, useState } from 'react'
import { formatUsageUSD, getSystemAccess, getUserBalance, type AccountBalance } from '../api'
import { initAuth, type User } from '../pro/api/auth'
import { Icon } from '../unified/components/Icon'
import { Mascot } from './Mascot'
import { StudyView } from './StudyView'
import './StudyApp.css'

/** Fired by the study view whenever an action may have changed the balance. */
export const STUDY_BILLING_EVENT = 'dt-study-billing-changed'

/**
 * 学习空间：独立于转录主界面的异步页面（课前预习、课后复习），带自己的
 * 视觉系统。打开会话时跳回 /pro 工作区（?session= 深链）。
 */
export function StudyApp() {
  const [user, setUser] = useState<User | null>(null)
  const [ragEnabled, setRagEnabled] = useState(false)
  const [ready, setReady] = useState(false)
  const [balance, setBalance] = useState<AccountBalance | null>(null)

  const refreshBalance = useCallback(() => {
    getUserBalance().then(setBalance).catch(() => { /* chip stays hidden */ })
  }, [])

  // Practice and generation charge the account; the view announces it.
  useEffect(() => {
    if (!ready) return
    refreshBalance()
    window.addEventListener(STUDY_BILLING_EVENT, refreshBalance)
    return () => window.removeEventListener(STUDY_BILLING_EVENT, refreshBalance)
  }, [ready, refreshBalance])

  useEffect(() => {
    void Promise.all([initAuth(), getSystemAccess()])
      .then(([authedUser, access]) => {
        if (!authedUser) {
          window.location.href = '/pro'
          return
        }
        setUser(authedUser)
        setRagEnabled(access.ragEnabled)
        setReady(true)
      })
      .catch(() => {
        window.location.href = '/pro'
      })
  }, [])

  if (!ready) {
    return <div className="dt-study-page__loading">LOADING // 学习空间</div>
  }

  return (
    <div className="dt-study-page">
      <header className="dt-study-topbar">
        <span className="dt-study-topbar__brand">
          <Mascot mood="idle" size={40} />
          <span>
            <strong>学习空间</strong>
            <small>DreamTrans · Study Mode</small>
          </span>
        </span>
        <nav className="dt-study-topbar__actions">
          {balance && (
            <a
              className="dt-study-topbar__balance"
              href="/pro"
              title="可用余额（钱包 + 赠额）。学习模式按模型用量扣费，每一步都会显示花了多少。"
            >
              <small>BALANCE</small>
              <b>{formatUsageUSD(balance.available_usd)}</b>
            </a>
          )}
          <a className="st-btn st-btn--quiet" href="/pro">
            <Icon name="mic" size={15} />
            返回转录
          </a>
          <span className="dt-study-topbar__user" title={user?.email}>
            {user?.name?.trim().slice(0, 1).toUpperCase() || '学'}
          </span>
        </nav>
      </header>

      <main className="dt-study-page__body">
        {ragEnabled ? (
          <StudyView
            onOpenSession={(session) => {
              window.location.assign(`/pro?session=${encodeURIComponent(session.id)}`)
            }}
          />
        ) : (
          <div className="dt-study-page__disabled st-panel">
            <Mascot mood="glitch" size={72} />
            <strong>学习空间尚未启用</strong>
            <span>请先在服务端配置 AI 能力。</span>
          </div>
        )}
      </main>
    </div>
  )
}
