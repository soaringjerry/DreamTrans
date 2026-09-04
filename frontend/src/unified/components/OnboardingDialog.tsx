import { useCallback, useId, useRef, useState } from 'react'
import { formatHours, formatUSD, type AccountBalance, type AccountSummary } from '../../api'
import type { AudioCaptureSource } from '../../core/audio/BrowserAudioCapture'
import { useMessages, type Messages } from '../../i18n'
import { useDialogFocusTrap } from '../hooks/useDialogFocusTrap'
import type { UnifiedSettings } from '../hooks/useUnifiedSettings'
import { languageLabel, languageOptions } from '../workspace/languageOptions'
import { BrandMark } from './BrandMark'
import { Icon, type IconName } from './Icon'

interface OnboardingDialogProps {
  account: AccountSummary | null
  balance: AccountBalance | null
  paymentsEnabled: boolean
  settings: UnifiedSettings
  signedIn: boolean
  onFinish: (next: 'tour' | 'close') => void
  onOpenAccount: () => void
  onSettingsChange: (patch: Partial<UnifiedSettings>) => void
}

type Step = 'welcome' | 'audio' | 'language' | 'ready'

const STEPS: readonly Step[] = ['welcome', 'audio', 'language', 'ready']

interface AudioChoice {
  value: AudioCaptureSource
  icon: IconName
  title: string
  blurb: string
  note?: string
}

function audioChoices(m: Messages): AudioChoice[] {
  const c = m.onboarding.audio.choices
  return [
    { value: 'microphone', icon: 'mic', title: c.microphone.title, blurb: c.microphone.blurb },
    { value: 'system', icon: 'wave', title: c.system.title, blurb: c.system.blurb, note: c.system.note },
    { value: 'mixed', icon: 'message', title: c.mixed.title, blurb: c.mixed.blurb },
  ]
}

const NO_TRANSLATION = '__none__'

export function OnboardingDialog({
  account,
  balance,
  paymentsEnabled,
  settings,
  signedIn,
  onFinish,
  onOpenAccount,
  onSettingsChange,
}: OnboardingDialogProps) {
  const m = useMessages()
  const o = m.onboarding
  const AUDIO_CHOICES = audioChoices(m)
  const LANGUAGE_OPTIONS = languageOptions()
  const [index, setIndex] = useState(0)
  const dialogRef = useRef<HTMLElement>(null)
  const titleId = useId()
  const step = STEPS[index] ?? 'welcome'
  const close = useCallback(() => onFinish('close'), [onFinish])
  useDialogFocusTrap(dialogRef, close)

  const available = balance?.available_usd ?? 0
  const hourlyPrice = account?.realtime_hour_usd ?? 0
  const estimatedHours = hourlyPrice > 0
    ? available / hourlyPrice
    : account?.estimated_realtime_hours ?? 0
  const needsTopUp = signedIn && paymentsEnabled && available <= 0

  const goNext = () => setIndex((value) => Math.min(value + 1, STEPS.length - 1))
  const goBack = () => setIndex((value) => Math.max(value - 1, 0))

  const translationValue = settings.translationEnabled
    ? settings.targetLanguage
    : NO_TRANSLATION

  return (
    <div className="dt-onboarding-backdrop">
      <section
        aria-labelledby={titleId}
        aria-modal="true"
        className="dt-onboarding"
        data-step={step}
        ref={dialogRef}
        role="dialog"
        tabIndex={-1}
      >
        <header className="dt-onboarding__header">
          <ol className="dt-onboarding__progress" aria-label={o.progressAria}>
            {STEPS.map((name, position) => (
              <li
                aria-current={position === index ? 'step' : undefined}
                className={position <= index ? 'is-done' : undefined}
                key={name}
              />
            ))}
          </ol>
          <button
            aria-label={o.skipAria}
            className="dt-icon-button"
            onClick={close}
            type="button"
          >
            <Icon name="close" />
          </button>
        </header>

        <div className="dt-onboarding__body">
          {step === 'welcome' && (
            <>
              <BrandMark className="dt-onboarding__mark" size={54} />
              <p className="dt-eyebrow">{o.welcome.eyebrow}</p>
              <h2 id={titleId}>{o.welcome.title}</h2>
              <p className="dt-onboarding__lead">{o.welcome.lead}</p>
              {signedIn ? (
                <div className={`dt-onboarding__credit${needsTopUp ? ' is-empty' : ''}`}>
                  <Icon name={needsTopUp ? 'shield' : 'check'} size={18} />
                  <span>
                    {needsTopUp ? (
                      <>
                        <strong>{o.welcome.noBalanceTitle}</strong>
                        <small>{o.welcome.noBalanceBody}</small>
                      </>
                    ) : (
                      <>
                        <strong>{o.welcome.creditTitle(formatUSD(available))}</strong>
                        <small>
                          {estimatedHours > 0
                            ? o.welcome.creditBody(formatHours(estimatedHours))
                            : o.welcome.creditBodyGeneric}
                        </small>
                      </>
                    )}
                  </span>
                  {needsTopUp && (
                    <button
                      className="dt-button dt-button--small"
                      onClick={onOpenAccount}
                      type="button"
                    >
                      {o.welcome.topUp}
                    </button>
                  )}
                </div>
              ) : (
                <div className="dt-onboarding__credit">
                  <Icon name="archive" size={18} />
                  <span>
                    <strong>{o.welcome.localTitle}</strong>
                    <small>{o.welcome.localBody}</small>
                  </span>
                </div>
              )}
            </>
          )}

          {step === 'audio' && (
            <>
              <p className="dt-eyebrow">{o.audio.eyebrow}</p>
              <h2 id={titleId}>{o.audio.title}</h2>
              <p className="dt-onboarding__lead">{o.audio.lead}</p>
              <div aria-label={o.audio.groupAria} className="dt-onboarding__choices" role="radiogroup">
                {AUDIO_CHOICES.map((choice) => {
                  const selected = settings.audioSource === choice.value
                  return (
                    <button
                      aria-checked={selected}
                      className={`dt-onboarding__choice${selected ? ' is-selected' : ''}`}
                      key={choice.value}
                      onClick={() => onSettingsChange({ audioSource: choice.value })}
                      role="radio"
                      type="button"
                    >
                      <span className="dt-onboarding__choice-icon"><Icon name={choice.icon} size={20} /></span>
                      <span className="dt-onboarding__choice-body">
                        <strong>{choice.title}</strong>
                        <small>{choice.blurb}</small>
                      </span>
                      <span aria-hidden="true" className="dt-onboarding__choice-check">
                        {selected && <Icon name="check" size={14} />}
                      </span>
                    </button>
                  )
                })}
              </div>
              {AUDIO_CHOICES.find((choice) => choice.value === settings.audioSource)?.note && (
                <p className="dt-onboarding__note">
                  {AUDIO_CHOICES.find((choice) => choice.value === settings.audioSource)?.note}
                </p>
              )}
            </>
          )}

          {step === 'language' && (
            <>
              <p className="dt-eyebrow">{o.language.eyebrow}</p>
              <h2 id={titleId}>{o.language.title}</h2>
              <p className="dt-onboarding__lead">{o.language.lead}</p>
              <div className="dt-onboarding__languages">
                <label className="dt-field">
                  <span>{o.language.source}</span>
                  <select
                    onChange={(event) => onSettingsChange({ sourceLanguage: event.target.value })}
                    value={settings.sourceLanguage}
                  >
                    {LANGUAGE_OPTIONS.map((language) => (
                      <option key={language.value} value={language.value}>{language.label}</option>
                    ))}
                  </select>
                </label>
                <span aria-hidden="true" className="dt-onboarding__arrow">→</span>
                <label className="dt-field">
                  <span>{o.language.target}</span>
                  <select
                    onChange={(event) => {
                      const value = event.target.value
                      if (value === NO_TRANSLATION) {
                        onSettingsChange({ translationEnabled: false })
                        return
                      }
                      onSettingsChange({
                        translationEnabled: true,
                        targetLanguage: value,
                        viewMode: 'bilingual',
                      })
                    }}
                    value={translationValue}
                  >
                    {LANGUAGE_OPTIONS
                      .filter((language) => language.value !== settings.sourceLanguage)
                      .map((language) => (
                        <option key={language.value} value={language.value}>{language.label}</option>
                      ))}
                    <option value={NO_TRANSLATION}>{o.language.noTranslation}</option>
                  </select>
                </label>
              </div>
              <div aria-hidden="true" className="dt-onboarding__preview">
                <span className="dt-onboarding__preview-speaker">S1</span>
                <p>{samplePhrase(settings.sourceLanguage)}</p>
                {settings.translationEnabled && (
                  <p className="dt-onboarding__preview-translation">
                    {samplePhrase(settings.targetLanguage)}
                  </p>
                )}
              </div>
            </>
          )}

          {step === 'ready' && (
            <>
              <span className="dt-onboarding__mark is-success"><Icon name="check" size={26} /></span>
              <p className="dt-eyebrow">{o.ready.eyebrow}</p>
              <h2 id={titleId}>{o.ready.title}</h2>
              <dl className="dt-onboarding__summary">
                <div>
                  <dt>{o.ready.audio}</dt>
                  <dd>{AUDIO_CHOICES.find((choice) => choice.value === settings.audioSource)?.title}</dd>
                </div>
                <div>
                  <dt>{o.ready.language}</dt>
                  <dd>
                    {languageLabel(settings.sourceLanguage)}
                    {settings.translationEnabled
                      ? ` → ${languageLabel(settings.targetLanguage)}`
                      : ` · ${o.ready.originalOnly}`}
                  </dd>
                </div>
              </dl>
              <ul className="dt-onboarding__tips">
                {(['mic', 'language', 'sparkles'] as const).map((icon, tipIndex) => (
                  <li key={icon}>
                    <Icon name={icon} size={16} />
                    <span>{o.ready.tips[tipIndex]}</span>
                  </li>
                ))}
              </ul>
            </>
          )}
        </div>

        <footer className="dt-onboarding__footer">
          {step === 'welcome' && (
            <>
              <button className="dt-button dt-button--text" onClick={close} type="button">
                {m.common.skip}
              </button>
              <button
                className="dt-button dt-button--primary"
                data-autofocus
                onClick={goNext}
                type="button"
              >
                {o.welcome.start}
              </button>
            </>
          )}
          {(step === 'audio' || step === 'language') && (
            <>
              <button className="dt-button dt-button--secondary" onClick={goBack} type="button">
                {m.common.prev}
              </button>
              <button className="dt-button dt-button--primary" onClick={goNext} type="button">
                {m.common.next}
              </button>
            </>
          )}
          {step === 'ready' && (
            <>
              <button className="dt-button dt-button--secondary" onClick={close} type="button">
                {o.ready.startNow}
              </button>
              <button
                className="dt-button dt-button--primary"
                onClick={() => onFinish('tour')}
                type="button"
              >
                {o.ready.showMe}
              </button>
            </>
          )}
        </footer>
      </section>
    </div>
  )
}

const SAMPLE_PHRASES: Record<string, string> = {
  en: 'Today we will look at how neural networks learn from examples.',
  cmn: '今天我们来看看神经网络是如何从样例中学习的。',
  ja: '今日はニューラルネットワークが例から学ぶ仕組みを見ていきます。',
  ko: '오늘은 신경망이 예시로부터 학습하는 방식을 살펴보겠습니다.',
  es: 'Hoy veremos cómo las redes neuronales aprenden a partir de ejemplos.',
  fr: 'Aujourd’hui, nous verrons comment les réseaux neuronaux apprennent à partir d’exemples.',
  de: 'Heute sehen wir uns an, wie neuronale Netze aus Beispielen lernen.',
}

function samplePhrase(language: string): string {
  return SAMPLE_PHRASES[language] ?? SAMPLE_PHRASES.en ?? ''
}
