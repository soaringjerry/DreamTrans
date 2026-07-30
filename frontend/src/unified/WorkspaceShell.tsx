import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  getUserUsage,
  type RagConfig,
  type UserBalance,
  type UserBillingSummary,
  type UserUsageItem,
} from '../api'
import type { User } from '../pro/api/auth'
import {
  TranscriptFeed,
  TranscriptFeedModeSwitch,
  type TranscriptFeedItem,
} from './feed'
import type { UnifiedSettings } from './hooks/useUnifiedSettings'
import { AssistantPanel } from './components/AssistantPanel'
import { HistoryPanel, type HistorySession } from './components/HistoryPanel'
import { Icon } from './components/Icon'
import { InsightsPanel } from './components/InsightsPanel'
import { RecorderBar, type RecorderStatus } from './components/RecorderBar'
import { SettingsPanel } from './components/SettingsPanel'
import { Sheet } from './components/Sheet'
import { adminNavigationState } from './workspace/adminNavigation'

export interface WorkspaceStats {
  finalSegments: number
  translatedSegments: number
  speakers: number
  topWords: Array<{ word: string; count: number }>
}

export interface WorkspaceShellProps {
  allowUserApiKey: boolean
  balance: UserBalance | null
  billingSummary: UserBillingSummary | null
  connectionLabel: string
  durationLabel: string
  error: string | null
  feedGeneration: number
  feedItems: readonly TranscriptFeedItem[]
  historyLoading: boolean
  historySessions: HistorySession[]
  legacyHistoryCount: number
  pendingWrites: number
  ragEnabled: boolean
  recorderStatus: RecorderStatus
  sessionId: string
  settings: UnifiedSettings
  stats: WorkspaceStats
  title: string
  transcriptContext: string
  user: User | null
  onClearError: () => void
  onContinue: () => Promise<void>
  onDeleteHistory: (session: HistorySession) => Promise<void>
  onDownloadAudio: () => Promise<void>
  onDownloadText: (mode: 'original' | 'translation' | 'bilingual') => Promise<void>
  onLoadHistory: (session: HistorySession) => Promise<void>
  onMigrateLegacyHistory: () => Promise<void>
  onLogout: () => Promise<void>
  onPauseToggle: () => void
  onRefreshHistory: () => Promise<void>
  onRequestLogin: () => void
  onSettingsChange: (patch: Partial<UnifiedSettings>) => void
  onStart: () => Promise<void>
  onStop: () => Promise<void>
  onTitleChange: (title: string) => Promise<void>
}

type PanelName = 'assistant' | 'history' | 'insights' | 'settings' | 'tools' | 'account'

const statusCopy: Record<RecorderStatus, { label: string; tone: string }> = {
  idle: { label: '准备就绪', tone: 'neutral' },
  starting: { label: '正在启动', tone: 'working' },
  recording: { label: '实时转录中', tone: 'live' },
  paused: { label: '已暂停', tone: 'warning' },
  stopping: { label: '正在收尾', tone: 'working' },
  reconnecting: { label: '正在重连', tone: 'warning' },
  error: { label: '连接已中断', tone: 'danger' },
}

function currentDateLabel(): string {
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'long',
    day: 'numeric',
    weekday: 'short',
  }).format(Date.now())
}

function pointsLabel(balance: UserBalance | null, summary: UserBillingSummary | null): string {
  if (!balance) return '本地模式'
  const dp = `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(balance.dreampoints)} DP`
  if (!summary || summary.realtime_rate_dp_per_hour <= 0) return dp
  const hours = Math.max(0, summary.estimated_realtime_hours)
  const time = hours >= 1
    ? `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(hours)} 小时`
    : `${Math.max(0, Math.floor(hours * 60))} 分钟`
  return `约可转写 ${time} · ${dp}`
}

export function WorkspaceShell(props: WorkspaceShellProps) {
  const {
    allowUserApiKey,
    balance,
    billingSummary,
    connectionLabel,
    durationLabel,
    error,
    feedGeneration,
    feedItems,
    historyLoading,
    historySessions,
    legacyHistoryCount,
    pendingWrites,
    ragEnabled,
    recorderStatus,
    sessionId,
    settings,
    stats,
    title,
    transcriptContext,
    user,
    onClearError,
    onContinue,
    onDeleteHistory,
    onDownloadAudio,
    onDownloadText,
    onLoadHistory,
    onMigrateLegacyHistory,
    onLogout,
    onPauseToggle,
    onRefreshHistory,
    onRequestLogin,
    onSettingsChange,
    onStart,
    onStop,
    onTitleChange,
  } = props
  const [panel, setPanel] = useState<PanelName | null>(null)
  const [assistantDraft, setAssistantDraft] = useState('')
  const [recentUsage, setRecentUsage] = useState<UserUsageItem[]>([])
  const status = statusCopy[recorderStatus]
  const effectiveViewMode = settings.translationEnabled ? settings.viewMode : 'original'
  const active = recorderStatus === 'recording'
    || recorderStatus === 'paused'
    || recorderStatus === 'reconnecting'
    || recorderStatus === 'error'
  const transitionBusy = recorderStatus === 'starting' || recorderStatus === 'stopping'
  const adminNavigation = adminNavigationState(user?.role, recorderStatus)
  const adminNavigationDisabled = adminNavigation === 'disabled'
  const adminNavigationTitle = adminNavigationDisabled
    ? '请先结束当前录音，再打开管理后台'
    : '打开管理后台'
  const today = useMemo(currentDateLabel, [])
  useEffect(() => {
    if (panel !== 'account' || !user) {
      setRecentUsage([])
      return
    }
    let active = true
    void getUserUsage(sessionId || undefined)
      .then((items) => {
        if (active) setRecentUsage(items.slice(0, 6))
      })
      .catch(() => {
        if (active) setRecentUsage([])
      })
    return () => { active = false }
  }, [panel, sessionId, user])
  const aiConfig = useMemo<RagConfig>(() => ({
    ...(allowUserApiKey && settings.aiApiKey.trim()
      ? {
          api_key: settings.aiApiKey.trim(),
          ...(settings.aiApiBase.trim() ? { api_base: settings.aiApiBase.trim() } : {}),
          ...(settings.aiModel.trim() ? { model: settings.aiModel.trim() } : {}),
        }
      : {}),
    ...(settings.aiPrompt.trim() ? { prompt: settings.aiPrompt.trim() } : {}),
  }), [
    allowUserApiKey,
    settings.aiApiBase,
    settings.aiApiKey,
    settings.aiModel,
    settings.aiPrompt,
  ])

  const closePanel = useCallback(() => setPanel(null), [])
  const explainTerm = useCallback((term: string) => {
    setAssistantDraft(
      `请解释英语单词或短语“${term}”：给出词性、准确中文含义、常见搭配，以及两个英文例句和对应中文翻译。`,
    )
    setPanel('assistant')
  }, [])

  // Select any text inside the live transcript to get a one-tap AI
  // explanation (parity with the old classic UI's selection lookup).
  const stageRef = useRef<HTMLElement | null>(null)
  const [selectionLookup, setSelectionLookup] = useState<
    { text: string; x: number; y: number } | null
  >(null)
  useEffect(() => {
    if (!ragEnabled) return
    const handleSelection = () => {
      const stage = stageRef.current
      const selection = window.getSelection()
      if (!stage || !selection || selection.isCollapsed) {
        setSelectionLookup(null)
        return
      }
      const text = selection.toString().trim().replace(/\s+/g, ' ')
      if (!text || text.length > 120) {
        setSelectionLookup(null)
        return
      }
      const anchor = selection.anchorNode
      if (!anchor || !stage.contains(anchor)) {
        setSelectionLookup(null)
        return
      }
      const rect = selection.getRangeAt(0).getBoundingClientRect()
      const stageRect = stage.getBoundingClientRect()
      setSelectionLookup({
        text,
        x: Math.min(
          Math.max(rect.left + rect.width / 2 - stageRect.left, 40),
          stageRect.width - 40,
        ),
        y: Math.max(rect.top - stageRect.top, 8),
      })
    }
    document.addEventListener('selectionchange', handleSelection)
    return () => document.removeEventListener('selectionchange', handleSelection)
  }, [ragEnabled])
  const loadHistory = async (session: HistorySession) => {
    await onLoadHistory(session)
    closePanel()
  }

  return (
    <div
      className={`dt-app${settings.reducedEffects ? ' dt-app--reduced-effects' : ''}`}
      data-recorder-status={recorderStatus}
    >
      <aside className="dt-sidebar" aria-label="主导航">
        <div className="dt-brand">
          <span className="dt-brand__mark"><Icon name="wave" size={22} /></span>
          <span>
            <strong>DreamTrans</strong>
            <small>{user ? 'Cloud workspace' : 'Local workspace'}</small>
          </span>
        </div>

        <nav className="dt-nav">
          <button className="is-active" type="button">
            <Icon name="mic" size={18} />
            <span>实时转录</span>
            {active && <i className="dt-nav__live" aria-label="正在录音" />}
          </button>
          <button
            disabled={!ragEnabled}
            onClick={() => setPanel('assistant')}
            title={ragEnabled ? undefined : '服务端未配置 AI 能力'}
            type="button"
          >
            <Icon name="sparkles" size={18} />
            <span>AI 助手</span>
          </button>
          <button onClick={() => setPanel('insights')} type="button">
            <Icon name="wave" size={18} />
            <span>会话洞察</span>
          </button>
          {adminNavigation !== 'hidden' && (
            <button
              disabled={adminNavigationDisabled}
              onClick={() => { window.location.assign('/pro/admin') }}
              title={adminNavigationTitle}
              type="button"
            >
              <Icon name="shield" size={18} />
              <span>管理后台</span>
            </button>
          )}
        </nav>

        <div className="dt-sidebar__history-heading">
          <span>最近会话</span>
          <button
            aria-label="刷新历史"
            className="dt-icon-button"
            onClick={() => { void onRefreshHistory() }}
            type="button"
          >
            <Icon name="history" size={16} />
          </button>
        </div>
        <div className="dt-sidebar__history">
          <LegacyHistoryNotice
            busy={historyLoading}
            count={legacyHistoryCount}
            disabled={recorderStatus !== 'idle'}
            onMigrate={onMigrateLegacyHistory}
          />
          <HistoryPanel
            activeSessionId={sessionId}
            loading={historyLoading}
            sessions={historySessions}
            onDelete={onDeleteHistory}
            onLoad={onLoadHistory}
          />
        </div>

        <div className="dt-sidebar__footer">
          <button className="dt-account-chip" onClick={() => setPanel('account')} type="button">
            <span className="dt-account-chip__avatar">
              {user?.name?.trim().slice(0, 1).toUpperCase() || '访'}
            </span>
            <span>
              <strong>{user?.name || '访客'}</strong>
              <small>{pointsLabel(balance, billingSummary)}</small>
            </span>
            <Icon name="more" size={17} />
          </button>
        </div>
      </aside>

      <section className="dt-workspace">
        <header className="dt-topbar">
          <button
            aria-label="打开历史会话"
            className="dt-icon-button dt-mobile-only"
            onClick={() => setPanel('history')}
            type="button"
          >
            <Icon name="menu" />
          </button>

          <div className="dt-session-heading">
            <div className="dt-session-heading__meta">
              <span>{today}</span>
              <span aria-hidden="true">/</span>
              <span className={`dt-status dt-status--${status.tone}`}>
                <i />
                {status.label}
              </span>
            </div>
            <input
              aria-label="会话标题"
              className="dt-session-title"
              defaultValue={title}
              key={sessionId || 'empty'}
              onBlur={(event) => {
                const nextTitle = event.currentTarget.value.trim()
                if (nextTitle && nextTitle !== title) void onTitleChange(nextTitle)
              }}
              onKeyDown={(event) => {
                if (event.key === 'Enter') event.currentTarget.blur()
              }}
            />
          </div>

          <div className="dt-topbar__actions">
            <span className="dt-connection" title="转录连接状态">
              <Icon name="cloud" size={16} />
              {connectionLabel}
            </span>
            <button
              aria-label="下载与导出"
              className="dt-icon-button"
              onClick={() => setPanel('tools')}
              type="button"
            >
              <Icon name="download" />
            </button>
            <button
              aria-label="设置"
              className="dt-icon-button"
              onClick={() => setPanel('settings')}
              type="button"
            >
              <Icon name="settings" />
            </button>
            <button
              aria-label="账户"
              className="dt-topbar__avatar"
              onClick={() => setPanel('account')}
              type="button"
            >
              {user?.name?.trim().slice(0, 1).toUpperCase() || '访'}
            </button>
          </div>
        </header>

        {error && (
          <div className="dt-alert" role="alert">
            <span><strong>出现问题</strong>{error}</span>
            <button aria-label="关闭错误" onClick={onClearError} type="button">
              <Icon name="close" size={17} />
            </button>
          </div>
        )}

        <main className="dt-stage" ref={stageRef}>
          <div className="dt-feed-toolbar">
            <div>
              <p className="dt-eyebrow">Live transcript</p>
              <span className="dt-feed-toolbar__count">
                {stats.finalSegments > 0
                  ? `${stats.finalSegments} 个片段`
                  : '等待声音输入'}
              </span>
            </div>
            <TranscriptFeedModeSwitch
              disabled={!settings.translationEnabled}
              onChange={(viewMode) => onSettingsChange({ viewMode })}
              value={effectiveViewMode}
            />
          </div>

          <TranscriptFeed
            emptyState={(
              <div className="dt-feed-empty">
                <span className="dt-feed-empty__icon"><Icon name="mic" size={28} /></span>
                <strong>{active ? '正在聆听…' : '准备记录下一段对话'}</strong>
                <p>
                  {active
                    ? '说话内容会实时出现在这里。'
                    : '点击下方麦克风开始，长时间会话也会保持流畅。'}
                </p>
                {!active && (
                  <button
                    className="dt-button dt-button--primary"
                    onClick={() => { void onStart() }}
                    type="button"
                  >
                    开始转录
                  </button>
                )}
              </div>
            )}
            initialFollow={settings.autoScroll}
            items={feedItems}
            layoutRevision={feedGeneration}
            mode={effectiveViewMode}
          />

          {selectionLookup && (
            <div
              className="dt-selection-lookup"
              style={{ left: selectionLookup.x, top: selectionLookup.y }}
            >
              <span className="dt-selection-lookup__text">{selectionLookup.text}</span>
              <button
                onClick={() => {
                  explainTerm(selectionLookup.text)
                  setSelectionLookup(null)
                  window.getSelection()?.removeAllRanges()
                }}
                type="button"
              >
                释义
              </button>
            </div>
          )}
        </main>

        <RecorderBar
          assistantEnabled={ragEnabled}
          canContinue={Boolean(sessionId)}
          durationLabel={durationLabel}
          onAssistant={() => setPanel('assistant')}
          onContinue={() => { void onContinue() }}
          onMore={() => setPanel('tools')}
          onPauseToggle={onPauseToggle}
          onStart={() => { void onStart() }}
          onStop={() => { void onStop() }}
          status={recorderStatus}
        />
      </section>

      <Sheet
        description="浏览并恢复本机或云端会话。列表不会读取录音文件。"
        eyebrow="Library"
        onClose={closePanel}
        open={panel === 'history'}
        title="历史会话"
        wide
      >
        <div className="dt-mobile-panel-nav" aria-label="移动端工具">
          <button
            disabled={!ragEnabled}
            onClick={() => setPanel('assistant')}
            title={ragEnabled ? undefined : '服务端未配置 AI 能力'}
            type="button"
          >
            <Icon name="sparkles" size={18} />
            <span>AI 助手</span>
          </button>
          <button onClick={() => setPanel('insights')} type="button">
            <Icon name="wave" size={18} />
            <span>会话洞察</span>
          </button>
          <button onClick={() => setPanel('account')} type="button">
            <Icon name="user" size={18} />
            <span>{user ? '账户' : '登录'}</span>
          </button>
          {adminNavigation !== 'hidden' && (
            <button
              disabled={adminNavigationDisabled}
              onClick={() => { window.location.assign('/pro/admin') }}
              title={adminNavigationTitle}
              type="button"
            >
              <Icon name="shield" size={18} />
              <span>管理后台</span>
            </button>
          )}
        </div>
        <LegacyHistoryNotice
          busy={historyLoading}
          count={legacyHistoryCount}
          disabled={recorderStatus !== 'idle'}
          onMigrate={onMigrateLegacyHistory}
        />
        <HistoryPanel
          activeSessionId={sessionId}
          loading={historyLoading}
          sessions={historySessions}
          onDelete={onDeleteHistory}
          onLoad={loadHistory}
        />
      </Sheet>

      <Sheet
        description="基于当前转录内容提问或生成摘要。"
        eyebrow="Copilot"
        onClose={closePanel}
        open={panel === 'assistant'}
        title="AI 助手"
        wide
      >
        {ragEnabled ? (
          <AssistantPanel
            key={`${user?.id ?? 'anonymous'}:${sessionId}`}
            config={aiConfig}
            ownerId={user?.id ?? null}
            sessionId={sessionId}
            suggestedQuestion={assistantDraft}
            transcriptContext={transcriptContext}
          />
        ) : (
          <div className="dt-empty dt-empty--compact">
            <Icon name="sparkles" size={24} />
            <strong>AI 助手尚未启用</strong>
            <span>请先在服务端配置 OpenAI 能力。</span>
          </div>
        )}
      </Sheet>

      <Sheet
        description="当前会话的增量统计、词汇分析和 API 用量。"
        eyebrow="Session"
        onClose={closePanel}
        open={panel === 'insights'}
        title="会话洞察"
        wide
      >
        <InsightsPanel
          assistantEnabled={ragEnabled}
          canViewApiMetrics={user?.role === 'super_admin'}
          durationLabel={durationLabel}
          finalSegments={stats.finalSegments}
          pendingWrites={pendingWrites}
          sessionId={sessionId}
          speakers={stats.speakers}
          topWords={stats.topWords}
          translatedSegments={stats.translatedSegments}
          onExplainTerm={explainTerm}
        />
      </Sheet>

      <Sheet
        description="调整语言、翻译和本地保存方式。"
        eyebrow="Preferences"
        onClose={closePanel}
        open={panel === 'settings'}
        title="设置"
        wide
      >
        <SettingsPanel
          allowUserApiKey={allowUserApiKey}
          authenticated={Boolean(user)}
          onChange={onSettingsChange}
          ragEnabled={ragEnabled}
          recorderStatus={recorderStatus}
          settings={settings}
        />
      </Sheet>

      <Sheet
        description="按需读取当前会话；录制和自动保存过程不会重组完整音频。"
        eyebrow="Export"
        onClose={closePanel}
        open={panel === 'tools'}
        title="下载与导出"
      >
        <div className="dt-export-list">
          <ExportButton
            description="按音频块顺序生成完整原始录音"
            icon="download"
            label="下载完整音频"
            onClick={onDownloadAudio}
          />
          <ExportButton
            description="包含说话人和时间戳"
            icon="message"
            label="下载双语文本"
            onClick={() => onDownloadText('bilingual')}
          />
          <ExportButton
            description="只导出识别原文"
            icon="archive"
            label="下载原文"
            onClick={() => onDownloadText('original')}
          />
          <ExportButton
            description="只导出已完成译文"
            icon="language"
            label="下载译文"
            onClick={() => onDownloadText('translation')}
          />
        </div>
      </Sheet>

      <Sheet
        eyebrow={user ? 'Account' : 'Local mode'}
        onClose={closePanel}
        open={panel === 'account'}
        title={user?.name || '访客模式'}
      >
        <div className="dt-account-panel">
          <div className="dt-account-panel__identity">
            <span>{user?.name?.trim().slice(0, 1).toUpperCase() || '访'}</span>
            <div>
              <strong>{user?.email || '数据仅保存在此浏览器'}</strong>
              <small>{pointsLabel(balance, billingSummary)}</small>
            </div>
          </div>
          {user ? (
            <>
              <div className="dt-account-usage">
                <div>
                  <strong>最近用量</strong>
                  <small>实际扣费按秒和 token 结算</small>
                </div>
                {recentUsage.length === 0 ? (
                  <p className="dt-muted">当前会话暂无计费用量。</p>
                ) : recentUsage.map((item) => (
                  <div className="dt-account-usage__row" key={item.id}>
                    <span>
                      <strong>{item.action}</strong>
                      <small>{item.model || '默认服务'} · {new Date(item.created_at).toLocaleTimeString()}</small>
                    </span>
                    <strong>{item.cost_dp.toFixed(4)} DP</strong>
                  </div>
                ))}
              </div>
              {adminNavigation === 'enabled' && (
                <a className="dt-button dt-button--primary dt-button--wide" href="/pro/admin">
                  打开管理后台
                </a>
              )}
              {adminNavigation === 'disabled' && (
                <button
                  className="dt-button dt-button--primary dt-button--wide"
                  disabled
                  title={adminNavigationTitle}
                  type="button"
                >
                  录音结束后打开管理后台
                </button>
              )}
              <button
                className="dt-button dt-button--secondary dt-button--wide"
                disabled={transitionBusy}
                onClick={() => { void onLogout().then(closePanel) }}
                type="button"
              >
                {transitionBusy ? '会话处理中…' : '退出登录'}
              </button>
            </>
          ) : (
            <button
              className="dt-button dt-button--primary dt-button--wide"
              disabled={transitionBusy}
              onClick={() => {
                closePanel()
                onRequestLogin()
              }}
              type="button"
            >
              登录并启用云端同步
            </button>
          )}
        </div>
      </Sheet>
    </div>
  )
}

interface LegacyHistoryNoticeProps {
  busy: boolean
  count: number
  disabled: boolean
  onMigrate: () => Promise<void>
}

function LegacyHistoryNotice({
  busy,
  count,
  disabled,
  onMigrate,
}: LegacyHistoryNoticeProps) {
  if (count === 0) return null
  return (
    <div className="dt-legacy-history" role="status">
      <span>
        发现 {count} 个旧版会话。仅在你确认迁移时读取旧数据；日常历史列表不会读取音频。
      </span>
      <button
        className="dt-button dt-button--secondary"
        disabled={busy || disabled}
        onClick={() => { void onMigrate() }}
        type="button"
      >
        {disabled ? '录音结束后迁移' : busy ? '正在迁移…' : '迁移旧版历史'}
      </button>
    </div>
  )
}

interface ExportButtonProps {
  description: string
  icon: 'archive' | 'download' | 'language' | 'message'
  label: string
  onClick: () => Promise<void>
}

function ExportButton({ description, icon, label, onClick }: ExportButtonProps) {
  const [busy, setBusy] = useState(false)
  return (
    <button
      className="dt-export-item"
      disabled={busy}
      onClick={() => {
        setBusy(true)
        void onClick().finally(() => setBusy(false))
      }}
      type="button"
    >
      <span className="dt-export-item__icon">
        {busy ? <span className="dt-spinner" /> : <Icon name={icon} size={20} />}
      </span>
      <span>
        <strong>{label}</strong>
        <small>{description}</small>
      </span>
      <Icon name="arrow-down" size={17} />
    </button>
  )
}
