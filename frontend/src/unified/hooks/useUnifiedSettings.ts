import { useCallback, useEffect, useState } from 'react'
import {
  normalizeAudioCaptureSource,
  type AudioCaptureSource,
} from '../../core/audio/BrowserAudioCapture'
import {
  AUTH_STATE_CHANGED_EVENT,
  type AuthStateChangedDetail,
} from '../../pro/api/auth'
import {
  isAssistMode,
  isLearningLevel,
  type AssistMode,
  type LearningLevel,
} from '../../learning'
import { getUserApiKey, setUserApiKey } from '../../utils/userApiKey'

export type TranscriptViewMode = 'bilingual' | 'original' | 'translation'
export type { AudioCaptureSource }
export type { AssistMode, LearningLevel }

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
  /**
   * interpret — full context-aware AI translation (default product mode).
   * learn — original-first with local CEFR glosses; no automatic AI translate.
   */
  assistMode: AssistMode
  /** CEFR band used to decide which words count as hard in learning mode. */
  learningLevel: LearningLevel
  reducedEffects: boolean
  /** Live input for transcription: mic, system/tab audio, or both mixed. */
  audioSource: AudioCaptureSource
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
  assistMode: 'interpret',
  learningLevel: 'B1',
  reducedEffects: false,
  audioSource: 'microphone',
  keepLocalAudio: true,
  automaticAiIngest: false,
  aiApiKey: '',
  aiApiBase: '',
  aiModel: '',
  aiPrompt: '请基于当前会话，用简洁、准确的中文回答；不确定时明确说明。',
}

function coerceSettings(partial: Partial<UnifiedSettings>): UnifiedSettings {
  const assistMode = isAssistMode(partial.assistMode)
    ? partial.assistMode
    : defaults.assistMode
  const learningLevel = isLearningLevel(partial.learningLevel)
    ? partial.learningLevel
    : defaults.learningLevel
  return {
    ...defaults,
    ...partial,
    assistMode,
    learningLevel,
    audioSource: normalizeAudioCaptureSource(partial.audioSource),
  }
}

function readSettings(): UnifiedSettings {
  // getUserApiKey also scrubs credentials accidentally persisted by an older
  // settings implementation before this hook reads the remaining preferences.
  const aiApiKey = getUserApiKey()
  try {
    const stored = localStorage.getItem(SETTINGS_KEY)
    if (stored) {
      return coerceSettings({
        ...JSON.parse(stored) as Partial<UnifiedSettings>,
        aiApiKey,
      })
    }

    const pro = localStorage.getItem('dt_pro_settings')
    if (pro) {
      const legacy = JSON.parse(pro) as { autoScroll?: boolean }
      return coerceSettings({
        autoScroll: legacy.autoScroll ?? defaults.autoScroll,
        aiApiKey,
      })
    }
    const classic = localStorage.getItem('dt_settings_v1')
    if (classic) {
      const legacy = JSON.parse(classic) as { experimental_bilingual?: boolean }
      return coerceSettings({
        viewMode: legacy.experimental_bilingual === false ? 'translation' : 'bilingual',
        aiApiKey,
      })
    }
  } catch {
    // Use defaults when browser storage is unavailable or malformed.
  }
  return coerceSettings({ aiApiKey })
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
