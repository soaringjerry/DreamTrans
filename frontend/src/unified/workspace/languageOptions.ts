import type { AudioCaptureSource } from '../../core/audio/BrowserAudioCapture'
import { messages } from '../../i18n'

export interface LanguageOption {
  value: string
  label: string
}

export const LANGUAGE_CODES = ['en', 'cmn', 'ja', 'ko', 'es', 'fr', 'de'] as const

/** Languages offered for both the source and the translation target. */
export function languageOptions(): LanguageOption[] {
  const names = messages().languages
  return LANGUAGE_CODES.map((value) => ({ value, label: names[value] }))
}

export function languageLabel(code: string): string {
  const names = messages().languages as Record<string, string | undefined>
  return names[code] ?? code
}

export function audioSourceLabel(source: AudioCaptureSource): string {
  return messages().audioSources[source]
}
