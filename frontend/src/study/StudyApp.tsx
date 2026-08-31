import { useEffect, useState } from 'react'
import { getSystemAccess } from '../api'
import { initAuth, type User } from '../pro/api/auth'
import { Icon } from '../unified/components/Icon'
import { StudyView } from './StudyView'
import '../unified/UnifiedApp.css'
import './StudyApp.css'

/**
 * 学习空间：独立于转录主界面的异步页面（课前预习、课后复习）。
 * 转录主打课堂同步；这里承载课程、技能地图和之后的练习。
 * 打开会话时跳回 /pro 工作区（?session= 深链）。
 */
export function StudyApp() {
  const [user, setUser] = useState<User | null>(null)
  const [ragEnabled, setRagEnabled] = useState(false)
  const [ready, setReady] = useState(false)

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
    return <div className="dt-study-page__loading">正在进入学习空间…</div>
  }

  return (
    <div className="dt-study-page">
      <header className="dt-study-topbar">
        <span className="dt-study-topbar__brand">
          <span className="dt-study-topbar__mark"><Icon name="map" size={20} /></span>
          <span>
            <strong>DreamTrans</strong>
            <small>学习空间 · 课前课后</small>
          </span>
        </span>
        <nav className="dt-study-topbar__actions">
          <a className="dt-button dt-button--secondary" href="/pro">
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
          <div className="dt-empty dt-empty--compact">
            <Icon name="sparkles" size={24} />
            <strong>学习空间尚未启用</strong>
            <span>请先在服务端配置 AI 能力。</span>
          </div>
        )}
      </main>
    </div>
  )
}
