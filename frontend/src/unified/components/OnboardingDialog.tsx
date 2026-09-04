import { useCallback, useId, useRef, useState } from 'react'
import { formatHours, formatUSD, type AccountBalance, type AccountSummary } from '../../api'
import type { AudioCaptureSource } from '../../core/audio/BrowserAudioCapture'
import { useDialogFocusTrap } from '../hooks/useDialogFocusTrap'
import type { UnifiedSettings } from '../hooks/useUnifiedSettings'
import { LANGUAGE_OPTIONS, languageLabel } from '../workspace/languageOptions'
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

const AUDIO_CHOICES: readonly AudioChoice[] = [
  {
    value: 'microphone',
    icon: 'mic',
    title: '麦克风',
    blurb: '线下课堂、面对面交流、自己练习口语。',
  },
  {
    value: 'system',
    icon: 'wave',
    title: '电脑里的声音',
    blurb: '网课、视频会议、正在播放的视频或播客。',
    note: '开始时浏览器会弹出分享窗口：选中对应标签页或窗口，并勾选「分享音频」。',
  },
  {
    value: 'mixed',
    icon: 'message',
    title: '两者一起',
    blurb: '线上会议里你也要发言，或想同时记录两边。',
  },
]

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
          <ol className="dt-onboarding__progress" aria-label="引导进度">
            {STEPS.map((name, position) => (
              <li
                aria-current={position === index ? 'step' : undefined}
                className={position <= index ? 'is-done' : undefined}
                key={name}
              />
            ))}
          </ol>
          <button
            aria-label="跳过引导"
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
              <span className="dt-onboarding__mark"><Icon name="wave" size={26} /></span>
              <p className="dt-eyebrow">欢迎</p>
              <h2 id={titleId}>把听到的内容，变成看得懂的文字</h2>
              <p className="dt-onboarding__lead">
                Yufolo 会实时转录你听到的声音，并同步翻译成你熟悉的语言。
                只需两步设置，就能开始第一次转录。
              </p>
              {signedIn ? (
                <div className={`dt-onboarding__credit${needsTopUp ? ' is-empty' : ''}`}>
                  <Icon name={needsTopUp ? 'shield' : 'check'} size={18} />
                  <span>
                    {needsTopUp ? (
                      <>
                        <strong>账户还没有余额</strong>
                        <small>转录按小时计费；开始前需要先充值。</small>
                      </>
                    ) : (
                      <>
                        <strong>可用额度 {formatUSD(available)}</strong>
                        <small>
                          {estimatedHours > 0
                            ? `约可实时转录 ${formatHours(estimatedHours)}，用完前不会中断。`
                            : '转录按小时计费，余额旁会显示大约还能用多久。'}
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
                      去充值
                    </button>
                  )}
                </div>
              ) : (
                <div className="dt-onboarding__credit">
                  <Icon name="archive" size={18} />
                  <span>
                    <strong>本地模式</strong>
                    <small>转录和录音只保存在这台设备的浏览器里；登录后可同步到云端。</small>
                  </span>
                </div>
              )}
            </>
          )}

          {step === 'audio' && (
            <>
              <p className="dt-eyebrow">第 1 步 · 音源</p>
              <h2 id={titleId}>你要转录的是什么声音？</h2>
              <p className="dt-onboarding__lead">这决定 Yufolo 从哪里听。以后可以在设置里随时更改。</p>
              <div aria-label="选择音源" className="dt-onboarding__choices" role="radiogroup">
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
              <p className="dt-eyebrow">第 2 步 · 语言</p>
              <h2 id={titleId}>说的是哪种语言？想翻译成什么？</h2>
              <p className="dt-onboarding__lead">
                原文会按原始语言识别；翻译会一句一句跟在后面出现。
              </p>
              <div className="dt-onboarding__languages">
                <label className="dt-field">
                  <span>原始语言</span>
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
                  <span>翻译成</span>
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
                    <option value={NO_TRANSLATION}>不翻译，只要原文</option>
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
              <p className="dt-eyebrow">准备就绪</p>
              <h2 id={titleId}>可以开始了</h2>
              <dl className="dt-onboarding__summary">
                <div>
                  <dt>音源</dt>
                  <dd>{AUDIO_CHOICES.find((choice) => choice.value === settings.audioSource)?.title}</dd>
                </div>
                <div>
                  <dt>语言</dt>
                  <dd>
                    {languageLabel(settings.sourceLanguage)}
                    {settings.translationEnabled
                      ? ` → ${languageLabel(settings.targetLanguage)}`
                      : ' · 仅原文'}
                  </dd>
                </div>
              </dl>
              <ul className="dt-onboarding__tips">
                <li>
                  <Icon name="mic" size={16} />
                  <span>点击底部的麦克风按钮开始。浏览器会先请求权限，请选择「允许」。</span>
                </li>
                <li>
                  <Icon name="language" size={16} />
                  <span>转录时可随时切换「原文 / 双语 / 译文 / 学习」视图。</span>
                </li>
                <li>
                  <Icon name="sparkles" size={16} />
                  <span>结束后会话自动保存；用 AI 助手提问、生成摘要或解释术语。</span>
                </li>
              </ul>
            </>
          )}
        </div>

        <footer className="dt-onboarding__footer">
          {step === 'welcome' && (
            <>
              <button className="dt-button dt-button--text" onClick={close} type="button">
                跳过
              </button>
              <button
                className="dt-button dt-button--primary"
                data-autofocus
                onClick={goNext}
                type="button"
              >
                开始设置
              </button>
            </>
          )}
          {(step === 'audio' || step === 'language') && (
            <>
              <button className="dt-button dt-button--secondary" onClick={goBack} type="button">
                上一步
              </button>
              <button className="dt-button dt-button--primary" onClick={goNext} type="button">
                下一步
              </button>
            </>
          )}
          {step === 'ready' && (
            <>
              <button className="dt-button dt-button--secondary" onClick={close} type="button">
                直接开始使用
              </button>
              <button
                className="dt-button dt-button--primary"
                onClick={() => onFinish('tour')}
                type="button"
              >
                带我看看界面
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
