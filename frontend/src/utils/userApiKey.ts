const SESSION_KEY = 'dt_user_api_key'
const LEGACY_SETTINGS_KEYS = [
  'dt_settings_v1',
  'dt_pro_settings',
  'dt_unified_settings_v1',
]
let memoryKey = ''

// User-supplied provider credentials are deliberately tab-scoped. Migrate any
// legacy localStorage copy once so it is no longer retained across browser
// restarts.
export function getUserApiKey(): string {
  let current = memoryKey
  try {
    current = sessionStorage.getItem(SESSION_KEY) || current
  } catch {
    // Storage may be unavailable in hardened/private browser contexts.
  }
  let legacy = ''
  for (const key of LEGACY_SETTINGS_KEYS) {
    try {
      const raw = localStorage.getItem(key)
      if (!raw) continue
      const settings = JSON.parse(raw) as Record<string, unknown>
      if (!legacy && typeof settings.apiKey === 'string') legacy = settings.apiKey
      if (!legacy && typeof settings.aiApiKey === 'string') legacy = settings.aiApiKey
      if ('apiKey' in settings || 'aiApiKey' in settings) {
        delete settings.apiKey
        delete settings.aiApiKey
        localStorage.setItem(key, JSON.stringify(settings))
      }
    } catch {
      // A malformed legacy settings object must not prevent cleanup of the
      // other settings namespace or use of an already tab-scoped credential.
    }
  }
  if (current) {
    memoryKey = current
    return current
  }
  if (legacy) {
    memoryKey = legacy
    try {
      sessionStorage.setItem(SESSION_KEY, legacy)
    } catch {
      // Keep the value in component state for this page even if storage is
      // unavailable.
    }
  }
  return legacy
}

export function setUserApiKey(value: string): void {
  const normalized = value.trim()
  memoryKey = normalized
  try {
    if (normalized) sessionStorage.setItem(SESSION_KEY, normalized)
    else sessionStorage.removeItem(SESSION_KEY)
  } catch {
    // The caller still retains the value in component memory.
  }
}

export function clearUserApiKey(): void {
  memoryKey = ''
  try {
    sessionStorage.removeItem(SESSION_KEY)
  } catch {
    // No persisted credential exists when storage is unavailable.
  }
}
