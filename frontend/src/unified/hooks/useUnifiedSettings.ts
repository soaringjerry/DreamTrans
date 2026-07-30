import { useCallback, useEffect, useState } from 'react'
import {
  AUTH_STATE_CHANGED_EVENT,
  type AuthStateChangedDetail,
} from '../../pro/api/auth'
import { getUserApiKey, setUserApiKey } from '../../utils/userApiKey'

export type TranscriptViewMode = 'bilingual' | 'original' | 'translation'

/**
 * ai — context-aware LLM translation through the backend /ws/translate
 * pipeline (rolling conversation context, custom prompt, sentence batching).
 * speechmatics — the provider's built-in per-fragment machine translation.
 */
export type TranslationEngine = 'ai' | 'speechmatics'

export interface UnifiedSettings {
  viewMode: TranscriptViewMode
  autoScroll: boolean
  sourceLanguage: string
  targetLanguage: string
  translationEnabled: boolean
  translationEngine: TranslationEngine
  translatePrompt: string
  reducedEffects: boolean
  keepLocalAudio: boolean
  automaticAiIngest: boolean
  aiApiKey: string
  aiApiBase: string
  aiModel: string
  aiPrompt: string
}

const SETTINGS_KEY = 'dt_unified_settings_v1'

const defaults: UnifiedSettings = {
  viewMode: 'bilingual',
  autoScroll: true,
  sourceLanguage: 'en',
  targetLanguage: 'cmn',
  translationEnabled: true,
  translationEngine: 'ai',
  translatePrompt: '',
  reducedEffects: false,
  keepLocalAudio: true,
  automaticAiIngest: false,
  aiApiKey: '',
  aiApiBase: '',
  aiModel: '',
  aiPrompt: '请基于当前会话，用简洁、准确的中文回答；不确定时明确说明。',
}

function readSettings(): UnifiedSettings {
  // getUserApiKey also scrubs credentials accidentally persisted by an older
  // settings implementation before this hook reads the remaining preferences.
  const aiApiKey = getUserApiKey()
  try {
    const stored = localStorage.getItem(SETTINGS_KEY)
    if (stored) {
      return {
        ...defaults,
        ...JSON.parse(stored) as Partial<UnifiedSettings>,
        aiApiKey,
      }
    }

    const pro = localStorage.getItem('dt_pro_settings')
    if (pro) {
      const legacy = JSON.parse(pro) as { autoScroll?: boolean }
      return {
        ...defaults,
        autoScroll: legacy.autoScroll ?? defaults.autoScroll,
        aiApiKey,
      }
    }
    const classic = localStorage.getItem('dt_settings_v1')
    if (classic) {
      const legacy = JSON.parse(classic) as { experimental_bilingual?: boolean }
      return {
        ...defaults,
        viewMode: legacy.experimental_bilingual === false ? 'translation' : 'bilingual',
        aiApiKey,
      }
    }
  } catch {
    // Use defaults when browser storage is unavailable or malformed.
  }
  return { ...defaults, aiApiKey }
}

export function persistUnifiedSettings(settings: UnifiedSettings): void {
  const { aiApiKey, ...nonSecretSettings } = settings
  setUserApiKey(aiApiKey)
  try {
    localStorage.setItem(SETTINGS_KEY, JSON.stringify(nonSecretSettings))
  } catch {
    // Settings still apply to the current tab.
  }
}

export function useUnifiedSettings() {
  const [settings, setSettingsState] = useState<UnifiedSettings>(readSettings)

  useEffect(() => {
    const clearCredentialState = () => {
      setSettingsState((current) => (
        current.aiApiKey ? { ...current, aiApiKey: '' } : current
      ))
    }
    const handleAuthChanged = (event: Event) => {
      if (
        event instanceof CustomEvent
        && (event as CustomEvent<AuthStateChangedDetail>).detail.identityChanged
      ) {
        clearCredentialState()
      }
    }
    window.addEventListener('dt-auth-cleared', clearCredentialState)
    window.addEventListener(AUTH_STATE_CHANGED_EVENT, handleAuthChanged)
    return () => {
      window.removeEventListener('dt-auth-cleared', clearCredentialState)
      window.removeEventListener(AUTH_STATE_CHANGED_EVENT, handleAuthChanged)
    }
  }, [])

  const setSettings = useCallback((next: UnifiedSettings) => {
    setSettingsState(next)
    persistUnifiedSettings(next)
  }, [])

  const patchSettings = useCallback((patch: Partial<UnifiedSettings>) => {
    setSettingsState((current) => {
      const next = { ...current, ...patch }
      persistUnifiedSettings(next)
      return next
    })
  }, [])

  return { settings, setSettings, patchSettings }
}
