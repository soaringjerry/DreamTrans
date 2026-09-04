import { useSyncExternalStore } from 'react'
import { zhCN } from './zh-CN'
import { en } from './en'

export type Locale = 'zh-CN' | 'en'
export type Messages = typeof zhCN

export const LOCALES: ReadonlyArray<{ code: Locale; label: string; intl: string }> = [
  { code: 'zh-CN', label: '中文', intl: 'zh-CN' },
  { code: 'en', label: 'English', intl: 'en' },
]

const STORAGE_KEY = 'dt_locale'
const DICTIONARIES: Record<Locale, Messages> = { 'zh-CN': zhCN, en }

function isLocale(value: unknown): value is Locale {
  return value === 'zh-CN' || value === 'en'
}

/** Browser preference → supported locale; Chinese variants map to zh-CN. */
export function detectLocale(): Locale {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (isLocale(stored)) return stored
  } catch {
    // Storage unavailable: fall through to the browser language.
  }
  // Only a browser's language preference counts. Node 21+ also exposes a
  // `navigator` (reporting en-US), which would flip the verification scripts
  // and any server-side tooling away from the zh-CN source dictionary.
  const languages = typeof window === 'undefined' || typeof navigator === 'undefined'
    ? []
    : [...(navigator.languages ?? []), navigator.language]
  for (const language of languages) {
    if (!language) continue
    if (language.toLowerCase().startsWith('zh')) return 'zh-CN'
    if (language.toLowerCase().startsWith('en')) return 'en'
  }
  return 'zh-CN'
}

let current: Locale = detectLocale()
const listeners = new Set<() => void>()

function applyDocumentLanguage(locale: Locale) {
  if (typeof document !== 'undefined') document.documentElement.lang = locale
}
applyDocumentLanguage(current)

export function getLocale(): Locale {
  return current
}

export function setLocale(locale: Locale): void {
  if (locale === current) return
  current = locale
  try {
    localStorage.setItem(STORAGE_KEY, locale)
  } catch {
    // Applies to this tab only.
  }
  applyDocumentLanguage(locale)
  for (const listener of listeners) listener()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => { listeners.delete(listener) }
}

/** Current dictionary for non-React code (error mappers, formatters). */
export function messages(): Messages {
  return DICTIONARIES[current]
}

/** BCP 47 tag for Intl formatters. */
export function intlLocale(locale: Locale = current): string {
  return LOCALES.find((item) => item.code === locale)?.intl ?? 'en'
}

export function useLocale(): [Locale, (locale: Locale) => void] {
  const locale = useSyncExternalStore(subscribe, getLocale, getLocale)
  return [locale, setLocale]
}

/** Re-renders the caller when the locale changes and returns its dictionary. */
export function useMessages(): Messages {
  const locale = useSyncExternalStore(subscribe, getLocale, getLocale)
  return DICTIONARIES[locale]
}
