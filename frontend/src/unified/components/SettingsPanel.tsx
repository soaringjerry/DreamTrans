import type { UnifiedSettings } from '../hooks/useUnifiedSettings'
import type { RecorderStatus } from './RecorderBar'

interface SettingsPanelProps {
  allowUserApiKey: boolean
  ragEnabled: boolean
  settings: UnifiedSettings
  onChange: (patch: Partial<UnifiedSettings>) => void
  recorderStatus: RecorderStatus
}

const languages = [
  { value: 'en', label: 'English' },
  { value: 'cmn', label: '简体中文' },
  { value: 'ja', label: '日本語' },
  { value: 'ko', label: '한국어' },
  { value: 'es', label: 'Español' },
  { value: 'fr', label: 'Français' },
  { value: 'de', label: 'Deutsch' },
]

export function SettingsPanel({
  allowUserApiKey,
  ragEnabled,
  settings,
  onChange,
  recorderStatus,
}: SettingsPanelProps) {
  const nextSessionLocked = recorderStatus !== 'idle'

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
              {languages.map((language) => (
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
              {languages.map((language) => (
                <option key={language.value} value={language.value}>{language.label}</option>
              ))}
            </select>
          </label>
        </div>
        <Toggle
          checked={settings.translationEnabled}
          description="实时显示与原文配对的翻译。"
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
            <option value="speechmatics">Speechmatics 机器翻译（逐句直译，延迟最低）</option>
          </select>
        </label>
        {!ragEnabled && settings.translationEngine === 'ai' && (
          <p className="dt-muted">
            当前无法确认服务端 AI 能力；不会自动改用 Speechmatics。
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
                  placeholder="https://api.openai.com/v1"
                  type="url"
                  value={settings.aiApiBase}
                />
              </label>
              <label className="dt-field">
                <span>Chat Model</span>
                <input
                  disabled={!settings.aiApiKey}
                  maxLength={200}
                  onChange={(event) => onChange({ aiModel: event.target.value })}
                  placeholder="使用服务端默认"
                  value={settings.aiModel}
                />
              </label>
            </div>
          </>
        )}
      </section>
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
