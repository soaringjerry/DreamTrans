import { useEffect, useState } from 'react'
import { useMessages } from '../../i18n'

interface Offer {
  name: string
  grant_usd: number
  grant_days: number
  plan_code: string
  plan_days: number
}

export function InviteOffer({ code }: { code: string }) {
  const m = useMessages()
  const normalized = code.trim()
  const [attempt, setAttempt] = useState(0)
  const [result, setResult] = useState<{ code: string; offer?: Offer; error?: string } | null>(null)
  useEffect(() => {
    if (!normalized) return
    const controller = new AbortController()
    let active = true
    const timeout = window.setTimeout(() => controller.abort(), 12_000)
    const timer = window.setTimeout(() => {
      const base = (import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080').replace(/\/$/, '')
      void fetch(`${base}/api/auth/invite?${new URLSearchParams({ code: normalized })}`, { signal: controller.signal })
        .then(async (response) => {
          const data = await response.json() as Offer & { error?: string }
          if (!response.ok) throw new Error(data.error || m.auth.inviteFailure)
          if (active) setResult({ code: normalized, offer: data })
        })
        .catch((error: unknown) => {
          if (active) setResult({ code: normalized, error: error instanceof Error && error.name !== 'AbortError' ? error.message : m.auth.inviteFailure })
        })
        .finally(() => window.clearTimeout(timeout))
    }, 350)
    return () => { active = false; window.clearTimeout(timer); window.clearTimeout(timeout); controller.abort() }
  }, [normalized, attempt, m.auth.inviteFailure])
  if (!normalized) return null
  if (!result || result.code !== normalized) return <p className="dt-auth__hint" role="status">{m.auth.inviteLoading}</p>
  if (result.error) return <div className="dt-auth__offer" role="status">{result.error} <button className="dt-button dt-button--text" type="button" onClick={() => { setResult(null); setAttempt((n) => n + 1) }}>{m.auth.inviteRetry}</button></div>
  const offer = result.offer!
  return <div className="dt-auth__offer" role="status">
    <strong>{offer.name}</strong>
    {offer.grant_usd > 0 && <p>{m.auth.inviteCredit(offer.grant_usd, offer.grant_days)}</p>}
    {offer.plan_code && <p>{m.auth.invitePlan(offer.plan_code, offer.plan_days)}</p>}
    {(offer.grant_usd > 0 || offer.plan_code) && <p className="dt-muted">{m.auth.inviteRewards}</p>}
  </div>
}
