import type { AudioCaptureSource } from '../../core/audio/BrowserAudioCapture'

export interface LanguageOption {
  value: string
  label: string
}

/** Languages offered for both the source and the translation target. */
export const LANGUAGE_OPTIONS: readonly LanguageOption[] = [
  { value: 'en', label: 'English' },
  { value: 'cmn', label: '简体中文' },
  { value: 'ja', label: '日本語' },
  { value: 'ko', label: '한국어' },
  { value: 'es', label: 'Español' },
  { value: 'fr', label: 'Français' },
  { value: 'de', label: 'Deutsch' },
]

export function languageLabel(code: string): string {
  return LANGUAGE_OPTIONS.find((option) => option.value === code)?.label ?? code
}

export const AUDIO_SOURCE_LABELS: Record<AudioCaptureSource, string> = {
  microphone: '麦克风',
  system: '电脑声音',
  mixed: '麦克风 + 电脑声音',
}
