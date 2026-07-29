import { useEffect, useState } from 'react'
import { getSystemSettings } from '../pro/api/system'

export function useAllowUserApiKey(): boolean {
  const [allowed, setAllowed] = useState(false)

  useEffect(() => {
    let active = true
    void getSystemSettings()
      .then(settings => {
        if (active) setAllowed(settings.allow_user_api_key === true)
      })
      .catch(() => {
        if (active) setAllowed(false)
      })
    return () => { active = false }
  }, [])

  return allowed
}
