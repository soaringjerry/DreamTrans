import { useCallback, useEffect, useState } from 'react'
import { formatUsageUSD, getSystemAccess, getUserBalance, type AccountBalance } from '../api'
import { initAuth, type User } from '../pro/api/auth'
import { Icon } from '../unified/components/Icon'
import { Mascot } from './Mascot'
import { StudyView } from './StudyView'
import { useStudySound } from './useStudySound'
import './StudyApp.css'

/** Fired by the study view whenever an action may have changed the balance. */
export const STUDY_BILLING_EVENT = 'dt-study-billing-changed'

/**
 * 学习空间: the study terminal. Its own page (课前预习、课后复习) with its
 * own visual system; the navigation lives in the top bar. Opening a session
 * jumps back to the /pro workspace (?session= deep link).
 */
export function StudyApp() {
  const [user, setUser] = useState<User | null>(null)
  const [ragEnabled, setRagEnabled] = useState(false)
  const [ready, setReady] = useState(false)
  const [balance, setBalance] = useState<AccountBalance | null>(null)
  const sound = useStudySound()

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

  // Browsers only let audio start after a gesture.
  useEffect(() => {
    const resume = () => sound.resume()
    window.addEventListener('pointerdown', resume, { once: true })
    return () => window.removeEventListener('pointerdown', resume)
  }, [sound])

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
        <a className="dt-study-topbar__brand" href="/pro/study">
          <Mascot mood="idle" size={38} />
          <span>
            <strong>学习空间</strong>
            <small>STUDY TERMINAL</small>
          </span>
        </a>
        <nav aria-label="学习空间" className="dt-study-topbar__nav">
          {balance && (
            <a
              className="dt-study-topbar__balance"
              href="/pro"
              title="可用余额（钱包 + 赠额）。学习模式按模型用量扣费，每一步都会显示花了多少。看解析、看讲解不扣费。"
            >
              <small>BALANCE</small>
              <b>{formatUsageUSD(balance.available_usd)}</b>
            </a>
          )}
          <span className="dt-study-topbar__sound" role="group" aria-label="声音">
            <button
              aria-pressed={sound.sfx}
              className={sound.sfx ? 'is-on' : ''}
              onClick={() => sound.setSfx(!sound.sfx)}
              title="音效"
              type="button"
            >
              SFX
            </button>
            <button
              aria-pressed={sound.bgm}
              className={sound.bgm ? 'is-on' : ''}
              onClick={() => sound.setBgm(!sound.bgm)}
              title="背景音乐（电钢琴循环，很轻）"
              type="button"
            >
              BGM
            </button>
          </span>
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
