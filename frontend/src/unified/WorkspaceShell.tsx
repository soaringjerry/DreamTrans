import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  formatHours,
  formatUSD,
  formatUsageUSD,
  getSessionCostSummaries,
  type AccountBalance,
  type AccountSummary,
  type RagConfig,
  type SessionCostSummary,
} from '../api'
import type { User } from '../pro/api/auth'
import {
  TranscriptFeed,
  TranscriptFeedModeSwitch,
  type TranscriptChromeMode,
  type TranscriptFeedItem,
} from './feed'
import type { UnifiedSettings } from './hooks/useUnifiedSettings'
import type { SessionCostView, TransportDiagnostics } from './hooks/useUnifiedWorkspace'
import { AccountPanel } from './components/AccountPanel'
import { AssistantPanel } from './components/AssistantPanel'
import { GuideTour, type TourStep } from './components/GuideTour'
import {
  HistoryPanel,
  type HistoryOpenProgress,
  type HistorySession,
} from './components/HistoryPanel'
import { Icon } from './components/Icon'
import { InsightsPanel } from './components/InsightsPanel'
import { OnboardingDialog } from './components/OnboardingDialog'
import { RecorderBar, type RecorderStatus } from './components/RecorderBar'
import { SettingsPanel } from './components/SettingsPanel'
import { Sheet } from './components/Sheet'
import { useOnboarding } from './hooks/useOnboarding'
import { adminNavigationState } from './workspace/adminNavigation'
import { isInsufficientBalanceMessage } from './workspace/billingErrors'
import { AUDIO_SOURCE_LABELS, languageLabel } from './workspace/languageOptions'

export interface WorkspaceStats {
  finalSegments: number
  translatedSegments: number
  speakers: number
  topWords: Array<{ word: string; count: number }>
}

export interface WorkspaceShellProps {
  account: AccountSummary | null
  allowUserApiKey: boolean
  balance: AccountBalance | null
  connectionLabel: string
  durationLabel: string
  error: string | null
  feedGeneration: number
  feedItems: readonly TranscriptFeedItem[]
  historyLoading: boolean
  historyOpening: HistoryOpenProgress | null
  historySessions: HistorySession[]
  legacyHistoryCount: number
  paymentsEnabled: boolean
  pendingWrites: number
  ragEnabled: boolean
  recorderStatus: RecorderStatus
  sessionCost: SessionCostView | null
  sessionId: string
  sessionSourceLanguage: string
  settings: UnifiedSettings
  stats: WorkspaceStats
  title: string
  titleGenerating: boolean
  transportDiagnostics: TransportDiagnostics | null
  transcriptContext: string
  user: User | null
  onClearError: () => void
  onContinue: () => Promise<void>
  onDeleteHistory: (session: HistorySession) => Promise<void>
  onEndHistorySession: (session: HistorySession) => Promise<void>
  onUploadHistorySessionToCloud: (session: HistorySession) => Promise<void>
  onDownloadAudio: () => Promise<void>
  onDownloadText: (mode: 'original' | 'translation' | 'bilingual') => Promise<void>
  onLoadHistory: (session: HistorySession) => Promise<void>
  onMigrateLegacyHistory: () => Promise<void>
  onLogout: () => Promise<void>
  onPauseToggle: () => void
  onRefreshAccount: () => Promise<void>
  onRefreshHistory: () => Promise<void>
  onRequestLogin: () => void
  onSettingsChange: (patch: Partial<UnifiedSettings>) => void
  onStart: () => Promise<void>
  onStop: () => Promise<void>
  onTitleChange: (title: string) => Promise<void>
  onGenerateTitle: () => Promise<void>
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

/** `$12.50 · ≈ 16 小时`; hours follow the live balance using the account's hourly price. */
function balanceLabel(balance: AccountBalance | null, account: AccountSummary | null): string {
  if (!balance) return '本地模式'
  const money = formatUSD(balance.available_usd)
  if (!account) return money
  const hours = account.realtime_hour_usd > 0
    ? balance.available_usd / account.realtime_hour_usd
    : account.estimated_realtime_hours
  return `${money} · ${formatHours(hours)}`
}

/**
 * Interface tour shown after the first-run wizard. Each step lists desktop
 * and mobile targets; the tour skips whichever is not rendered.
 */
const WORKSPACE_TOUR_STEPS: readonly TourStep[] = [
  {
    id: 'record',
    selectors: ['[data-tour="record"]'],
    title: '开始与停止',
    body: '点这里开始一段新会话。首次使用时浏览器会请求麦克风或屏幕分享权限，请选择允许。录音中再点一次即停止并保存。',
  },
  {
    id: 'mode-switch',
    selectors: ['.dt-feed-toolbar .dt-transcript-feed-mode-switch'],
    title: '阅读视图',
    body: '随时在「原文 / 双语 / 译文」之间切换。「学习」会把难词旁注出来，不消耗翻译额度。',
  },
  {
    id: 'assistant',
    selectors: ['[data-tour="assistant"]'],
    title: 'AI 助手',
    body: '基于当前转录提问、生成摘要或解释术语。也可以直接选中转录里的文字，点「释义」。',
  },
  {
    id: 'history',
    selectors: ['[data-tour="history"]', '[data-tour="history-mobile"]'],
    title: '历史会话',
    body: '结束的会话会自动保存到这里，随时回看、继续录制或导出文本与录音。',
  },
  {
    id: 'settings',
    selectors: ['[data-tour="settings"]'],
    title: '设置与导出',
    body: '在这里更改音源、语言和翻译引擎；旁边的下载按钮可导出原文、译文和完整录音。',
  },
  {
    id: 'account',
    selectors: ['[data-tour="account"]', '[data-tour="account-mobile"]'],
    title: '账户与余额',
    body: '查看余额、用量与充值。转录按小时计费，余额旁会显示大约还能转录多久。',
  },
]

type BillingReturn = 'success' | 'cancel' | null

/** Reads and strips `?billing=success|cancel` left by the Stripe redirect. */
function consumeBillingReturn(): BillingReturn {
  if (typeof window === 'undefined') return null
  const url = new URL(window.location.href)
  const value = url.searchParams.get('billing')
  if (value !== 'success' && value !== 'cancel') return null
  url.searchParams.delete('billing')
  window.history.replaceState(window.history.state, '', `${url.pathname}${url.search}${url.hash}`)
  return value
}

const BILLING_RETURN_REFRESH_DELAYS_MS = [0, 3_000, 6_000]

/** Reads and strips `?session=<id>` left by 学习空间 deep links. */
function consumeSessionDeepLink(): string | null {
  if (typeof window === 'undefined') return null
  const url = new URL(window.location.href)
  const value = url.searchParams.get('session')
  if (!value) return null
  url.searchParams.delete('session')
  window.history.replaceState(window.history.state, '', `${url.pathname}${url.search}${url.hash}`)
  return value
}

export function WorkspaceShell(props: WorkspaceShellProps) {
  const {
    account,
    allowUserApiKey,
    balance,
    connectionLabel,
    durationLabel,
    error,
    feedGeneration,
    feedItems,
    historyLoading,
    historyOpening,
    historySessions,
    legacyHistoryCount,
    transportDiagnostics,
    paymentsEnabled,
    pendingWrites,
    ragEnabled,
    recorderStatus,
    sessionCost,
    sessionId,
    sessionSourceLanguage,
    settings,
    stats,
    title,
    titleGenerating,
    transcriptContext,
    user,
    onClearError,
    onContinue,
    onDeleteHistory,
    onEndHistorySession,
    onUploadHistorySessionToCloud,
    onDownloadAudio,
    onDownloadText,
    onLoadHistory,
    onMigrateLegacyHistory,
    onLogout,
    onPauseToggle,
    onRefreshAccount,
    onRefreshHistory,
    onRequestLogin,
    onSettingsChange,
    onStart,
    onStop,
    onTitleChange,
    onGenerateTitle,
  } = props
  const [panel, setPanel] = useState<PanelName | null>(null)
  const [assistantDraft, setAssistantDraft] = useState('')
  // A /pro?session=<id> deep link (e.g. from the 学习空间) waiting for history.
  const [pendingSession, setPendingSession] = useState(consumeSessionDeepLink)
  const [notice, setNotice] = useState<string | null>(null)
  const status = statusCopy[recorderStatus]
  const balanceError = isInsufficientBalanceMessage(error)
  const memberActive = balance?.member_active ?? account?.member_active ?? false

  const generateTitleHint = !ragEnabled
    ? '服务端未配置 AI 能力'
    : !user
      ? '登录后可使用 AI 标题'
      : !sessionId || !transcriptContext
        ? '有转录内容后可生成标题'
        : titleGenerating
          ? 'AI 正在生成标题…'
          : 'AI 生成标题（可重复生成）'
  const canGenerateTitle = Boolean(
    ragEnabled && user && sessionId && transcriptContext && !titleGenerating
      && recorderStatus !== 'starting' && recorderStatus !== 'stopping',
  )
  const learningMode = settings.assistMode === 'learn'
  const effectiveViewMode = learningMode
    ? 'original'
    : settings.translationEnabled
      ? settings.viewMode
      : 'original'
  const chromeMode: TranscriptChromeMode = learningMode
    ? 'learn'
    : effectiveViewMode

  const setChromeMode = (mode: TranscriptChromeMode) => {
    if (mode === 'learn') {
      onSettingsChange({ assistMode: 'learn' })
      return
    }
    onSettingsChange({
      assistMode: 'interpret',
      viewMode: mode,
      // Leaving learning should not strand the user with translation off.
      ...(mode !== 'original' ? { translationEnabled: true } : {}),
    })
  }
  const active = recorderStatus === 'recording'
    || recorderStatus === 'paused'
    || recorderStatus === 'reconnecting'
    || recorderStatus === 'error'
  const studyEnabled = ragEnabled && Boolean(user)
  const studyNavigationDisabled = !studyEnabled || recorderStatus !== 'idle'
  const studyNavigationTitle = !ragEnabled
    ? '服务端未配置 AI 能力'
    : !user
      ? '登录后可使用学习空间'
      : recorderStatus !== 'idle'
        ? '请先结束当前录音，再打开学习空间'
        : '课程、技能地图与课前课后练习'

  // Deep-linked session opens once the history list can resolve it.
  useEffect(() => {
    if (!pendingSession) return
    const match = historySessions.find(({ id }) => id === pendingSession)
    if (match) {
      setPendingSession(null)
      void onLoadHistory(match)
      return
    }
    if (!historyLoading && historySessions.length > 0) {
      setPendingSession(null)
      setNotice('没有找到要打开的会话，它可能已被删除。')
    }
  }, [pendingSession, historySessions, historyLoading, onLoadHistory])

  // Realtime costs for the history list. Keyed off the joined id string so a
  // refresh that returns the same sessions does not refetch.
  const [historyCosts, setHistoryCosts] = useState<Record<string, SessionCostSummary>>({})
  const recorderIdle = recorderStatus === 'idle'
  const historyCostIds = historySessions
    .filter((session) => session.location === 'cloud')
    .slice(0, 100)
    .map((session) => session.id)
    .join(',')
  useEffect(() => {
    if (!user || !historyCostIds) {
      setHistoryCosts({})
      return
    }
    let effectActive = true
    // recorderIdle retriggers this after a recording ends, once its
    // reservation tail has settled.
    void getSessionCostSummaries(historyCostIds.split(','))
      .then((summaries) => {
        if (!effectActive) return
        const next: Record<string, SessionCostSummary> = {}
        for (const summary of summaries) next[summary.session_id] = summary
        setHistoryCosts(next)
      })
      .catch(() => {
        // The list renders fine without cost figures.
      })
    return () => { effectActive = false }
  }, [user, historyCostIds, recorderIdle])
  const transitionBusy = recorderStatus === 'starting' || recorderStatus === 'stopping'
  const adminNavigation = adminNavigationState(user?.role, recorderStatus)
  const adminNavigationDisabled = adminNavigation === 'disabled'
  const adminNavigationTitle = adminNavigationDisabled
    ? '请先结束当前录音，再打开管理后台'
    : '打开管理后台'
  const today = useMemo(currentDateLabel, [])
  // Stripe sends the browser back to /pro?billing=success|cancel. The webhook
  // that credits the wallet may lag the redirect, so re-read the account a
  // few times instead of trusting the first response.
  const refreshAccountRef = useRef(onRefreshAccount)
  refreshAccountRef.current = onRefreshAccount
  useEffect(() => {
    if (!user) return
    const outcome = consumeBillingReturn()
    if (!outcome) return
    if (outcome === 'cancel') {
      setNotice('已取消支付。')
      return
    }
    setNotice('支付已完成，余额稍后更新。')
    const timers = BILLING_RETURN_REFRESH_DELAYS_MS.map((delay) => globalThis.setTimeout(() => {
      void refreshAccountRef.current()
    }, delay))
    return () => {
      for (const timer of timers) globalThis.clearTimeout(timer)
    }
  }, [user])
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
  const onboarding = useOnboarding({
    ownerId: user?.id ?? null,
    historyLoading,
    historyCount: historySessions.length,
    recorderStatus,
  })
  const tourSteps = useMemo(() => (
    ragEnabled
      ? WORKSPACE_TOUR_STEPS
      : WORKSPACE_TOUR_STEPS.filter((step) => step.id !== 'assistant')
  ), [ragEnabled])
  const replayOnboarding = useCallback(() => {
    closePanel()
    onboarding.openWizard()
  }, [closePanel, onboarding])
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
            disabled={studyNavigationDisabled}
            onClick={() => { window.location.assign('/pro/study') }}
            title={studyNavigationTitle}
            type="button"
          >
            <Icon name="map" size={18} />
            <span>学习空间</span>
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

        <div className="dt-sidebar__history-heading" data-tour="history">
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
            costs={historyCosts}
            loading={historyLoading}
            opening={historyOpening}
            sessions={historySessions}
            onDelete={onDeleteHistory}
            onLoad={onLoadHistory}
            onEndSession={onEndHistorySession}
            {...(user ? { onUploadToCloud: onUploadHistorySessionToCloud } : {})}
          />
        </div>

        <div className="dt-sidebar__footer">
          <button
            className="dt-account-chip"
            data-tour="account"
            onClick={() => setPanel('account')}
            type="button"
          >
            <span className="dt-account-chip__avatar">
              {user?.name?.trim().slice(0, 1).toUpperCase() || '访'}
            </span>
            <span>
              <strong>
                {user?.name || '访客'}
                {memberActive && <em className="dt-pro-badge">Pro</em>}
              </strong>
              <small>{balanceLabel(balance, account)}</small>
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
            data-tour="history-mobile"
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
              {sessionCost && sessionCost.realtimeUsd > 0 && (
                <>
                  <span aria-hidden="true">/</span>
                  <span
                    className="dt-session-cost"
                    title={`本场会话的转录与翻译费用${
                      sessionCost.approximate ? '（录音中为预估，结束后校准）' : ''
                    }${sessionCost.aiUsd > 0 ? `；AI 功能另计 ${formatUsageUSD(sessionCost.aiUsd)}` : ''}`}
                  >
                    {sessionCost.approximate ? '≈ ' : ''}
                    {formatUsageUSD(sessionCost.realtimeUsd)}
                  </span>
                </>
              )}
            </div>
            <div className="dt-session-title-row">
              <input
                aria-label="会话标题"
                className="dt-session-title"
                defaultValue={title}
                key={`${sessionId || 'empty'}:${title}`}
                onBlur={(event) => {
                  const nextTitle = event.currentTarget.value.trim()
                  if (nextTitle && nextTitle !== title) void onTitleChange(nextTitle)
                }}
                onKeyDown={(event) => {
                  if (event.key === 'Enter') event.currentTarget.blur()
                }}
              />
              <button
                aria-label={titleGenerating ? 'AI 正在生成标题' : 'AI 生成标题'}
                className={`dt-icon-button dt-session-title__ai${titleGenerating ? ' is-busy' : ''}`}
                disabled={!canGenerateTitle}
                onClick={() => void onGenerateTitle()}
                title={generateTitleHint}
                type="button"
              >
                <Icon name="sparkles" size={16} />
              </button>
            </div>
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
              data-tour="settings"
              onClick={() => setPanel('settings')}
              type="button"
            >
              <Icon name="settings" />
            </button>
            <button
              aria-label="账户"
              className="dt-topbar__avatar"
              data-tour="account-mobile"
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
            {balanceError && (
              <button
                className="dt-alert__action"
                onClick={() => setPanel('account')}
                type="button"
              >
                去充值
              </button>
            )}
            <button aria-label="关闭错误" onClick={onClearError} type="button">
              <Icon name="close" size={17} />
            </button>
          </div>
        )}

        {notice && !error && (
          <div className="dt-alert dt-alert--info" role="status">
            <span>{notice}</span>
            <button
              className="dt-alert__action"
              onClick={() => { setNotice(null); setPanel('account') }}
              type="button"
            >
              查看账户
            </button>
            <button aria-label="关闭提示" onClick={() => setNotice(null)} type="button">
              <Icon name="close" size={17} />
            </button>
          </div>
        )}

        {transportDiagnostics && (
          <div
            className={`dt-transport-diag dt-transport-diag--${transportDiagnostics.tone}`}
            role="status"
            title={transportDiagnostics.detail}
          >
            <div className="dt-transport-diag__head">
              <span className="dt-transport-diag__label">链路调试</span>
              <span className="dt-transport-diag__summary">{transportDiagnostics.summary}</span>
            </div>
            <dl className="dt-transport-diag__rows">
              {transportDiagnostics.rows.map((row) => (
                <div className="dt-transport-diag__row" key={row.label}>
                  <dt>{row.label}</dt>
                  <dd>
                    <strong>{row.value}</strong>
                    <small>{row.note}</small>
                  </dd>
                </div>
              ))}
            </dl>
            <span className="dt-transport-diag__hint">{transportDiagnostics.detail}</span>
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
              translationDisabled={!settings.translationEnabled && !learningMode}
              labels={learningMode
                ? { learn: `学习 · ${settings.learningLevel}` }
                : undefined}
              onChange={setChromeMode}
              value={chromeMode}
            />
          </div>

          <TranscriptFeed
            emptyState={(
              <div className="dt-feed-empty">
                <span className="dt-feed-empty__icon"><Icon name="mic" size={28} /></span>
                <strong>{active ? '正在聆听…' : '准备记录下一段对话'}</strong>
                <p>
                  {active
                    ? (learningMode
                      ? '原文会实时出现；确认句子后自动标注难词短义。'
                      : '说话内容会实时出现在这里。')
                    : '点击下方麦克风开始，长时间会话也会保持流畅。'}
                </p>
                {!active && (
                  <>
                    <button
                      className="dt-button dt-button--primary"
                      onClick={() => { void onStart() }}
                      type="button"
                    >
                      开始转录
                    </button>
                    <dl className="dt-feed-empty__setup" aria-label="下一次会话的设置">
                      <div>
                        <dt>音源</dt>
                        <dd>{AUDIO_SOURCE_LABELS[settings.audioSource]}</dd>
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
                    <div className="dt-feed-empty__links">
                      <button
                        className="dt-button dt-button--text"
                        onClick={() => setPanel('settings')}
                        type="button"
                      >
                        更改设置
                      </button>
                      <button
                        className="dt-button dt-button--text"
                        onClick={onboarding.openWizard}
                        type="button"
                      >
                        新手引导
                      </button>
                    </div>
                  </>
                )}
              </div>
            )}
            initialFollow={settings.autoScroll}
            items={feedItems}
            layoutRevision={feedGeneration}
            learningDomains={settings.learningDomains}
            learningLevel={settings.learningLevel}
            learningMode={learningMode}
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
          <button
            disabled={studyNavigationDisabled}
            onClick={() => { window.location.assign('/pro/study') }}
            title={studyNavigationTitle}
            type="button"
          >
            <Icon name="map" size={18} />
            <span>学习空间</span>
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
          costs={historyCosts}
          loading={historyLoading}
          opening={historyOpening}
          sessions={historySessions}
          onDelete={onDeleteHistory}
          onLoad={loadHistory}
          onEndSession={onEndHistorySession}
          {...(user ? { onUploadToCloud: onUploadHistorySessionToCloud } : {})}
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
            onTopUp={() => setPanel('account')}
            ownerId={user?.id ?? null}
            sessionId={sessionId}
            sourceLanguage={sessionSourceLanguage}
            suggestedQuestion={assistantDraft}
            transcriptContext={transcriptContext}
          />
        ) : (
          <div className="dt-empty dt-empty--compact">
            <Icon name="sparkles" size={24} />
            <strong>AI 助手尚未启用</strong>
            <span>请联系管理员开启 AI 能力。</span>
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
          onReplayOnboarding={replayOnboarding}
          ragEnabled={ragEnabled}
          recorderStatus={recorderStatus}
          settings={settings}
        />
      </Sheet>

      <Sheet
        description="会话洞察与导出工具；导出按需读取当前会话，录制和自动保存过程不会重组完整音频。"
        eyebrow="Tools"
        onClose={closePanel}
        open={panel === 'tools'}
        title="更多工具"
      >
        <div className="dt-export-list">
          <ExportButton
            description="增量统计、词汇分析和 API 用量"
            icon="wave"
            label="会话洞察"
            onClick={() => setPanel('insights')}
          />
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
        description={user ? '余额、会员与充值。' : undefined}
        eyebrow={user ? 'Account' : 'Local mode'}
        onClose={closePanel}
        open={panel === 'account'}
        title={user?.name || '访客模式'}
        wide={Boolean(user)}
      >
        <div className="dt-account-panel">
          <div className="dt-account-panel__identity">
            <span>{user?.name?.trim().slice(0, 1).toUpperCase() || '访'}</span>
            <div>
              <strong>
                {user?.email || '数据仅保存在此浏览器'}
                {memberActive && <em className="dt-pro-badge">Pro</em>}
              </strong>
              <small>{balanceLabel(balance, account)}</small>
            </div>
          </div>
          {user ? (
            <>
              <AccountPanel
                account={account}
                balance={balance}
                open={panel === 'account'}
                paymentsEnabled={paymentsEnabled}
                sessionId={sessionId}
                onRefreshAccount={onRefreshAccount}
              />
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

      {onboarding.phase === 'wizard' && recorderStatus === 'idle' && (
        <OnboardingDialog
          account={account}
          balance={balance}
          paymentsEnabled={paymentsEnabled}
          settings={settings}
          signedIn={Boolean(user)}
          onFinish={onboarding.finishWizard}
          onOpenAccount={() => {
            onboarding.finishWizard('close')
            setPanel('account')
          }}
          onSettingsChange={onSettingsChange}
        />
      )}
      {onboarding.phase === 'tour' && (
        <GuideTour onFinish={onboarding.finishTour} steps={tourSteps} />
      )}
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
  icon: 'archive' | 'download' | 'language' | 'message' | 'wave'
  label: string
  onClick: () => Promise<void> | void
}

function ExportButton({ description, icon, label, onClick }: ExportButtonProps) {
  const [busy, setBusy] = useState(false)
  return (
    <button
      className="dt-export-item"
      disabled={busy}
      onClick={() => {
        setBusy(true)
        void Promise.resolve(onClick()).finally(() => setBusy(false))
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
