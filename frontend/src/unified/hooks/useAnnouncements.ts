import { useCallback, useEffect, useState } from 'react'
import { dismissAnnouncement, getAnnouncements, type Announcement } from '../../api'

const GUEST_DISMISSED_KEY = 'yufolo.announcements.dismissed'

function readGuestDismissed(): Set<string> {
  try {
    const raw = window.localStorage.getItem(GUEST_DISMISSED_KEY)
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return new Set(Array.isArray(parsed) ? parsed.filter((id): id is string => typeof id === 'string') : [])
  } catch {
    return new Set()
  }
}

function rememberGuestDismissed(id: string): void {
  try {
    const next = readGuestDismissed()
    next.add(id)
    window.localStorage.setItem(GUEST_DISMISSED_KEY, JSON.stringify([...next].slice(-100)))
  } catch {
    // Storage may be unavailable; the notice simply returns next visit.
  }
}

/**
 * Notices on display for this viewer. Signed-in dismissals are stored on the
 * account; guest dismissals stay in this browser.
 */
export function useAnnouncements(userId: string | null): {
  announcements: Announcement[]
  dismiss: (id: string) => void
} {
  const [announcements, setAnnouncements] = useState<Announcement[]>([])

  useEffect(() => {
    let active = true
    getAnnouncements(Boolean(userId))
      .then((items) => {
        if (!active) return
        if (userId) {
          setAnnouncements(items)
          return
        }
        const hidden = readGuestDismissed()
        setAnnouncements(items.filter((item) => !hidden.has(item.id)))
      })
      .catch(() => { if (active) setAnnouncements([]) })
    return () => { active = false }
  }, [userId])

  const dismiss = useCallback((id: string) => {
    setAnnouncements((current) => current.filter((item) => item.id !== id))
    if (userId) {
      dismissAnnouncement(id).catch(() => {
        // Best effort: the notice is hidden for this session either way.
      })
      return
    }
    rememberGuestDismissed(id)
  }, [userId])

  return { announcements, dismiss }
}
