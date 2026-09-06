import { useCallback, useEffect, useState } from 'react'
import { formatUsageUSD, getSystemAccess, getUserBalance, type AccountBalance } from '../api'
import { initAuth, type User } from '../pro/api/auth'
import { Icon } from '../unified/components/Icon'
import { LocaleSwitch } from '../i18n/LocaleSwitch'
import { useMessages } from '../i18n'
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
  const m = useMessages()
  const copy = m.study.app
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
    return <div className="dt-study-page__loading">{copy.loading}</div>
  }

  return (
    <div className="dt-study-page">
      <header className="dt-study-topbar">
        <a className="dt-study-topbar__brand" href="/pro/study">
          <Mascot mood="idle" size={38} />
          <span>
            <strong>{copy.title}</strong>
            <small>STUDY TERMINAL</small>
          </span>
        </a>
        <nav aria-label={copy.navAria} className="dt-study-topbar__nav">
          {balance && (
            <a
              className="dt-study-topbar__balance"
              href="/pro"
              title={copy.balanceTitle}
            >
              <small>BALANCE</small>
              <b>{formatUsageUSD(balance.available_usd)}</b>
            </a>
          )}
          <LocaleSwitch />
          <span className="dt-study-topbar__sound" role="group" aria-label={copy.soundAria}>
            <button
              aria-pressed={sound.sfx}
              className={sound.sfx ? 'is-on' : ''}
              onClick={() => sound.setSfx(!sound.sfx)}
              title={copy.sfxTitle}
              type="button"
            >
              SFX
            </button>
            <button
              aria-pressed={sound.bgm}
              className={sound.bgm ? 'is-on' : ''}
              onClick={() => sound.setBgm(!sound.bgm)}
              title={copy.bgmTitle}
              type="button"
            >
              BGM
            </button>
          </span>
          <a className="st-btn st-btn--quiet" href="/pro">
            <Icon name="mic" size={15} />
            {copy.back}
          </a>
          <span className="dt-study-topbar__user" title={user?.email}>
            {user?.name?.trim().slice(0, 1).toUpperCase() || copy.userInitial}
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
            <strong>{copy.disabled}</strong>
            <span>{copy.disabledBody}</span>
          </div>
        )}
      </main>
    </div>
  )
}
