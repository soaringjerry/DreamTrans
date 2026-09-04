import { LOCALES, useLocale, useMessages } from './index'

interface LocaleSwitchProps {
  className?: string
}

/** Compact 中文 / English toggle. */
export function LocaleSwitch({ className }: LocaleSwitchProps) {
  const [locale, setLocale] = useLocale()
  const m = useMessages()
  return (
    <div
      aria-label={m.locale.switchLabel}
      className={`dt-locale-switch${className ? ` ${className}` : ''}`}
      role="radiogroup"
    >
      {LOCALES.map((item) => (
        <button
          aria-checked={item.code === locale}
          className={item.code === locale ? 'is-active' : undefined}
          key={item.code}
          onClick={() => setLocale(item.code)}
          role="radio"
          type="button"
        >
          {item.label}
        </button>
      ))}
    </div>
  )
}
