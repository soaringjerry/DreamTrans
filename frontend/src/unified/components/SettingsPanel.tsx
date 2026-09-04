import { useEffect, useState } from 'react'
import {
  getAvailableModels,
  getUserModelPreferences,
  saveUserModelPreferences,
  type AvailableModel,
  type UserModelPreferences,
} from '../../api'
import { listTermDomains, type TermDomain } from '../../learning'
import type { UnifiedSettings } from '../hooks/useUnifiedSettings'
import { LANGUAGE_OPTIONS } from '../workspace/languageOptions'
import type { RecorderStatus } from './RecorderBar'

interface SettingsPanelProps {
  allowUserApiKey: boolean
  authenticated: boolean
  ragEnabled: boolean
  settings: UnifiedSettings
  onChange: (patch: Partial<UnifiedSettings>) => void
  /** Replays the first-run setup wizard and interface tour. */
  onReplayOnboarding?: () => void
  recorderStatus: RecorderStatus
}

const DOMAIN_UI: Record<TermDomain, { mark: string; blurb: string; tone: string }> = {
  ai: { mark: 'AI', blurb: '模型 · 算法 · 推理', tone: 'indigo' },
  internet: { mark: '网', blurb: '产品 · 云 · 工程', tone: 'sky' },
  psychology: { mark: '心', blurb: '认知 · 行为 · 临床', tone: 'violet' },
  geography: { mark: '地', blurb: '地质 · 气候 · 空间', tone: 'teal' },
  biology: { mark: '生', blurb: '细胞 · 基因 · 生态', tone: 'green' },
}

export function SettingsPanel({
  allowUserApiKey,
  authenticated,
  ragEnabled,
  settings,
  onChange,
  onReplayOnboarding,
  recorderStatus,
}: SettingsPanelProps) {
  const nextSessionLocked = recorderStatus !== 'idle'
  const [availableModels, setAvailableModels] = useState<AvailableModel[]>([])
  const [modelPreferences, setModelPreferences] = useState<UserModelPreferences | null>(null)
  const [modelStatus, setModelStatus] = useState('')

  useEffect(() => {
    if (!authenticated) {
      setAvailableModels([])
      setModelPreferences(null)
      return
    }
    let active = true
    void Promise.all([getAvailableModels(), getUserModelPreferences()])
      .then(([models, preferences]) => {
        if (!active) return
        setAvailableModels(models)
        setModelPreferences(preferences)
        setModelStatus('')
      })
      .catch(() => {
        if (active) setModelStatus('暂时无法读取管理员批准的模型清单。')
      })
    return () => { active = false }
  }, [authenticated])

  async function changeAccountModel(
    key: keyof UserModelPreferences,
    model: string,
  ) {
    if (!modelPreferences) return
    const next = { ...modelPreferences, [key]: model }
    setModelPreferences(next)
    setModelStatus('正在保存…')
    try {
      const saved = await saveUserModelPreferences(next)
      setModelPreferences(saved)
      setModelStatus('已保存到账号，将在下一次请求中生效。')
    } catch (reason) {
      setModelPreferences(modelPreferences)
      setModelStatus(reason instanceof Error ? reason.message : '模型偏好保存失败。')
    }
  }

  function modelsFor(purpose: AvailableModel['purpose']) {
    return availableModels.filter((model) => model.purpose === purpose)
  }

  return (
    <div className="dt-settings">
      <section className="dt-settings__section">
        <div>
          <h3>语言与翻译</h3>
          <p className="dt-muted">
            {nextSessionLocked
              ? '当前会话进行中；结束后可修改，并在下一次会话中应用。'
              : '开始新会话时应用。'}
          </p>
        </div>
        <div className="dt-settings__grid">
          <label className="dt-field">
            <span>原始语言</span>
            <select
              disabled={nextSessionLocked}
              onChange={(event) => onChange({ sourceLanguage: event.target.value })}
              value={settings.sourceLanguage}
            >
              {LANGUAGE_OPTIONS.map((language) => (
                <option key={language.value} value={language.value}>{language.label}</option>
              ))}
            </select>
          </label>
          <label className="dt-field">
            <span>翻译语言</span>
            <select
              disabled={nextSessionLocked || !settings.translationEnabled}
              onChange={(event) => onChange({ targetLanguage: event.target.value })}
              value={settings.targetLanguage}
            >
              {LANGUAGE_OPTIONS.map((language) => (
                <option key={language.value} value={language.value}>{language.label}</option>
              ))}
            </select>
          </label>
        </div>
        <p className="dt-muted">
          主界面工具栏可一键切换「原文 / 双语 / 译文 / 学习」。
          学习模式用本地 CEFR 与场景术语旁注，不请求翻译模型。
        </p>
        <Toggle
          checked={settings.translationEnabled}
          description="同传相关视图（双语 / 译文）需要开启。学习模式不依赖此项。"
          disabled={nextSessionLocked}
          label="启用实时翻译"
          onChange={(translationEnabled) => onChange({ translationEnabled })}
        />
        <label className="dt-field">
          <span>翻译引擎</span>
          <select
            disabled={nextSessionLocked || !settings.translationEnabled}
            onChange={(event) => onChange({
              translationEngine: event.target.value === 'speechmatics'
                ? 'speechmatics'
                : 'ai',
            })}
            value={settings.translationEngine}
          >
            <option value="ai">
              {ragEnabled
                ? 'AI 上下文翻译（推荐：整句润色，理解上下文）'
                : 'AI 上下文翻译（当前未确认服务能力）'}
            </option>
            <option value="speechmatics">快速机器翻译（逐句直译，延迟最低）</option>
          </select>
        </label>
        {!ragEnabled && settings.translationEngine === 'ai' && (
          <p className="dt-muted">
            当前无法确认服务端 AI 能力；不会自动改用快速机器翻译。
            AI 不可用时原文转录仍会保留。
          </p>
        )}
        {settings.translationEngine === 'ai' && (
          <label className="dt-field">
            <span>翻译提示词</span>
            <textarea
              disabled={nextSessionLocked || !settings.translationEnabled}
              maxLength={20_000}
              onChange={(event) => onChange({ translatePrompt: event.target.value })}
              placeholder="留空使用服务端默认提示词（英语→中文同传润色）。下次会话生效。"
              rows={4}
              value={settings.translatePrompt}
            />
          </label>
        )}
      </section>

      <section className="dt-settings__section">
        <div>
          <h3>学习模式</h3>
          <p className="dt-muted">在工具栏点「学习」启用；此处只调水平和术语表。</p>
        </div>
        <label className="dt-field">
          <span>学习水平（决定哪些词算难）</span>
          <select
            onChange={(event) => {
              const value = event.target.value
              onChange({
                learningLevel: value === 'A2' || value === 'B2' ? value : 'B1',
              })
            }}
            value={settings.learningLevel}
          >
            <option value="A2">初级 A2 · 假定已会 A1，旁注 A2 及以上</option>
            <option value="B1">中级 B1 · 假定已会 A2，旁注 B1 及以上（推荐）</option>
            <option value="B2">中高 B2 · 假定已会 B1，旁注 B2 及以上</option>
          </select>
        </label>

        <div className="dt-domain-picker">
          <div className="dt-domain-picker__head">
            <div>
              <span className="dt-domain-picker__title">场景术语表</span>
              <p className="dt-domain-picker__desc">
                命中的专业词优先旁注（本地词库，无 AI）。可多选。
              </p>
            </div>
            <div className="dt-domain-picker__actions">
              <button
                className="dt-domain-picker__link"
                type="button"
                onClick={() => onChange({
                  learningDomains: listTermDomains().map((item) => item.id),
                })}
              >
                全选
              </button>
              <button
                className="dt-domain-picker__link"
                type="button"
                onClick={() => onChange({ learningDomains: [] })}
              >
                清空
              </button>
            </div>
          </div>
          <div className="dt-domain-picker__grid" role="group" aria-label="场景术语表">
            {listTermDomains().map((domain) => {
              const checked = settings.learningDomains.includes(domain.id)
              const ui = DOMAIN_UI[domain.id]
              return (
                <label
                  key={domain.id}
                  className={
                    checked
                      ? 'dt-domain-card is-selected'
                      : 'dt-domain-card'
                  }
                  data-tone={ui.tone}
                >
                  <input
                    className="dt-domain-card__input"
                    checked={checked}
                    type="checkbox"
                    onChange={() => {
                      const next: TermDomain[] = checked
                        ? settings.learningDomains.filter((id) => id !== domain.id)
                        : [...settings.learningDomains, domain.id]
                      onChange({ learningDomains: next })
                    }}
                  />
                  <span className="dt-domain-card__mark" aria-hidden="true">
                    {ui.mark}
                  </span>
                  <span className="dt-domain-card__body">
                    <strong>{domain.label}</strong>
                    <small>{ui.blurb}</small>
                  </span>
                  <span className="dt-domain-card__meta">
                    <span className="dt-domain-card__count">
                      {domain.termCount.toLocaleString('zh-CN')}
                      <em>词</em>
                    </span>
                    <span
                      className={
                        checked
                          ? 'dt-domain-card__check is-on'
                          : 'dt-domain-card__check'
                      }
                      aria-hidden="true"
                    >
                      {checked ? '✓' : ''}
                    </span>
                  </span>
                </label>
              )
            })}
          </div>
          <p className="dt-domain-picker__footnote">
            已启用 {settings.learningDomains.length} / {listTermDomains().length} 类
            · 来自公开词库自动生成
          </p>
        </div>
      </section>

      <section className="dt-settings__section">
        <div>
          <h3>调试</h3>
          <p className="dt-muted">默认关闭；仅排查延迟或丢字时打开。</p>
        </div>
        <Toggle
          checked={settings.debugTransport}
          description="录音时显示发送积压、识别落后、丢包与 AI 翻译队列。立即生效。"
          label="显示链路诊断"
          onChange={(debugTransport) => onChange({ debugTransport })}
        />
      </section>

      <section className="dt-settings__section">
        <div>
          <h3>阅读体验</h3>
          <p className="dt-muted">所有设备立即生效。</p>
        </div>
        <Toggle
          checked={settings.autoScroll}
          description="仅当你停留在最新内容附近时自动跟随。"
          label="自动跟随实时内容"
          onChange={(autoScroll) => onChange({ autoScroll })}
        />
        <Toggle
          checked={settings.reducedEffects}
          description="减少阴影和过渡，适合低性能设备。"
          label="降低视觉效果"
          onChange={(reducedEffects) => onChange({ reducedEffects })}
        />
      </section>

      <section className="dt-settings__section">
        <div>
          <h3>音频输入</h3>
          <p className="dt-muted">
            {nextSessionLocked
              ? '当前会话的音源已锁定；结束后可为下一次会话修改。'
              : '选择转录监听的实时音源。系统音频需在浏览器分享弹窗中勾选“分享音频”。'}
          </p>
        </div>
        <label className="dt-field">
          <span>音源</span>
          <select
            disabled={nextSessionLocked}
            onChange={(event) => {
              const value = event.target.value
              onChange({
                audioSource:
                  value === 'system' || value === 'mixed' || value === 'microphone'
                    ? value
                    : 'microphone',
              })
            }}
            value={settings.audioSource}
          >
            <option value="microphone">麦克风</option>
            <option value="system">系统 / 标签页音频（电脑声音）</option>
            <option value="mixed">麦克风 + 系统音频</option>
          </select>
        </label>
        {settings.audioSource !== 'microphone' && (
          <p className="dt-muted">
            开始录音时会弹出屏幕分享对话框。请优先分享<strong>单个标签页</strong>并勾选
            「分享音频 / Share audio」（比分享整个屏幕延迟更低、更稳）。桌面端
            Chrome / Edge 支持最好；Safari 与多数移动浏览器可能无法捕获系统声音。
          </p>
        )}
      </section>

      <section className="dt-settings__section">
        <div>
          <h3>本地数据</h3>
          <p className="dt-muted">
            {nextSessionLocked
              ? '当前会话的保存方式已锁定；结束后可为下一次会话修改。'
              : '音频以分块方式保存，不会反复重写完整录音。'}
          </p>
        </div>
        <Toggle
          checked={settings.keepLocalAudio}
          description="用于离线恢复和下载完整原始录音。"
          disabled={nextSessionLocked}
          label="保存本地录音"
          onChange={(keepLocalAudio) => onChange({ keepLocalAudio })}
        />
      </section>

      <section className="dt-settings__section">
        <div>
          <h3>AI 与费用控制</h3>
          <p className="dt-muted">
            {ragEnabled
              ? '聊天和手动刷新摘要由你主动触发；自动入库默认关闭。'
              : '服务端当前未配置 AI 能力。'}
          </p>
        </div>
        <Toggle
          checked={settings.automaticAiIngest}
          description="开启后，最终转录会发送到服务端进行摘要/向量入库，可能产生模型费用。"
          disabled={!ragEnabled}
          label="自动 AI 入库"
          onChange={(automaticAiIngest) => onChange({ automaticAiIngest })}
        />
        {authenticated && modelPreferences && (
          <div className="dt-settings__section">
            <div>
              <h3>账号模型</h3>
              <p className="dt-muted">只显示管理员已批准且已配置成本的模型；选择会跨设备同步。</p>
            </div>
            <div className="dt-settings__grid">
              <label className="dt-field">
                <span>翻译模型</span>
                <select
                  disabled={nextSessionLocked}
                  onChange={(event) => void changeAccountModel('translation_model', event.target.value)}
                  value={modelPreferences.translation_model}
                >
                  {modelsFor('translation').map((model) => (
                    <option key={model.model_id} value={model.model_id}>
                      {model.model_id}{model.is_default ? '（默认）' : ''}
                    </option>
                  ))}
                </select>
              </label>
              <label className="dt-field">
                <span>摘要与标题模型</span>
                <select
                  disabled={nextSessionLocked}
                  onChange={(event) => void changeAccountModel('summary_model', event.target.value)}
                  value={modelPreferences.summary_model}
                >
                  {modelsFor('summary').map((model) => (
                    <option key={model.model_id} value={model.model_id}>
                      {model.model_id}{model.is_default ? '（默认）' : ''}
                    </option>
                  ))}
                </select>
              </label>
              <label className="dt-field">
                <span>聊天与问答模型</span>
                <select
                  onChange={(event) => void changeAccountModel('chat_model', event.target.value)}
                  value={modelPreferences.chat_model}
                >
                  {modelsFor('chat').map((model) => (
                    <option key={model.model_id} value={model.model_id}>
                      {model.model_id}{model.is_default ? '（默认）' : ''}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            {modelStatus && <p className="dt-muted">{modelStatus}</p>}
          </div>
        )}
        {authenticated && !modelPreferences && modelStatus && (
          <p className="dt-muted">{modelStatus}</p>
        )}
        <label className="dt-field">
          <span>AI 回答提示词</span>
          <textarea
            disabled={!ragEnabled}
            maxLength={20_000}
            onChange={(event) => onChange({ aiPrompt: event.target.value })}
            rows={4}
            value={settings.aiPrompt}
          />
        </label>
        {allowUserApiKey && (
          <>
            <p className="dt-muted">
              此服务器允许自带 AI Key。Key 仅保存在当前标签页，关闭标签页或退出登录即清除，
              并且只随主动 AI 请求发送。
            </p>
            <label className="dt-field">
              <span>API Key</span>
              <input
                autoComplete="off"
                maxLength={4_096}
                onChange={(event) => onChange({ aiApiKey: event.target.value })}
                placeholder="留空则使用服务端配置"
                type="password"
                value={settings.aiApiKey}
              />
            </label>
            <div className="dt-settings__grid">
              <label className="dt-field">
                <span>API Base</span>
                <input
                  disabled={!settings.aiApiKey}
                  maxLength={2_048}
                  onChange={(event) => onChange({ aiApiBase: event.target.value })}
                  placeholder="https://api.example.com/v1"
                  type="url"
                  value={settings.aiApiBase}
                />
              </label>
              <label className="dt-field">
                <span>Chat Model</span>
                <select
                  disabled={!settings.aiApiKey}
                  onChange={(event) => onChange({ aiModel: event.target.value })}
                  value={settings.aiModel || modelPreferences?.chat_model || ''}
                >
                  {modelsFor('chat').map((model) => (
                    <option key={model.model_id} value={model.model_id}>
                      {model.model_id}{model.is_default ? '（默认）' : ''}
                    </option>
                  ))}
                </select>
              </label>
            </div>
          </>
        )}
      </section>

      {onReplayOnboarding && (
        <section className="dt-settings__section">
          <div>
            <h3>帮助</h3>
            <p className="dt-muted">重新走一遍首次设置（音源、语言）和界面导览。</p>
          </div>
          <button
            className="dt-button dt-button--secondary"
            disabled={nextSessionLocked}
            onClick={onReplayOnboarding}
            title={nextSessionLocked ? '录音结束后可重新查看引导' : undefined}
            type="button"
          >
            重新查看新手引导
          </button>
        </section>
      )}
    </div>
  )
}

interface ToggleProps {
  checked: boolean
  description: string
  disabled?: boolean
  label: string
  onChange: (checked: boolean) => void
}

function Toggle({
  checked,
  description,
  disabled = false,
  label,
  onChange,
}: ToggleProps) {
  return (
    <label className={`dt-toggle${disabled ? ' is-disabled' : ''}`}>
      <span>
        <strong>{label}</strong>
        <small>{description}</small>
      </span>
      <input
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        type="checkbox"
      />
      <span aria-hidden="true" className="dt-toggle__track">
        <span />
      </span>
    </label>
  )
}
