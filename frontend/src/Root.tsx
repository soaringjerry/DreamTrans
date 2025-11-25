import { useCallback, useMemo, useState } from 'react'
import App from './App'
import ProShell from './pro/ProShell'
import './ui-switcher.css'

type UiMode = 'classic' | 'pro'

const STORAGE_KEY = 'dt_ui_mode'

export default function Root() {
  const [mode, setMode] = useState<UiMode>(() => {
    const saved = localStorage.getItem(STORAGE_KEY)
    return saved === 'pro' ? 'pro' : 'classic'
  })

  const label = useMemo(() => (mode === 'pro' ? '切回经典版' : '试用 Pro UI'), [mode])

  const switchMode = useCallback(
    (next: UiMode) => {
      setMode(next)
      localStorage.setItem(STORAGE_KEY, next)
    },
    [setMode],
  )

  return (
    <>
      {mode === 'pro' ? (
        <ProShell onBackToClassic={() => switchMode('classic')} />
      ) : (
        <App />
      )}
      <button
        className="ui-switcher"
        type="button"
        onClick={() => switchMode(mode === 'pro' ? 'classic' : 'pro')}
      >
        <span className="ui-switcher__dot" />
        <span className="ui-switcher__label">{label}</span>
      </button>
    </>
  )
}
