import { Icon } from './Icon'

export interface HistorySession {
  id: string
  title: string
  createdAt: number
  durationSeconds: number
  status: string
  location: 'cloud' | 'local'
}

export interface HistoryOpenProgress {
  sessionId: string
  label: string
  /** 0–100 when known; null means indeterminate. */
  percent: number | null
}

interface HistoryPanelProps {
  activeSessionId: string
  loading: boolean
  opening?: HistoryOpenProgress | null
  sessions: HistorySession[]
  onDelete: (session: HistorySession) => Promise<void>
  onLoad: (session: HistorySession) => Promise<void>
}

function formatDate(value: number): string {
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(value)
}

function formatDuration(seconds: number): string {
  if (!seconds) return '少于 1 分钟'
  const hours = Math.floor(seconds / 3_600)
  const minutes = Math.floor(seconds % 3_600 / 60)
  if (hours > 0) {
    return minutes > 0 ? `${hours} 小时 ${minutes} 分钟` : `${hours} 小时`
  }
  return `${Math.max(1, minutes)} 分钟`
}

export function HistoryPanel({
  activeSessionId,
  loading,
  opening = null,
  sessions,
  onDelete,
  onLoad,
}: HistoryPanelProps) {
  // Only blank the list on the first fetch. Opening a session keeps the list
  // visible so users can see which item is loading and cancel by picking another.
  if (loading && sessions.length === 0) {
    return (
      <div className="dt-empty">
        <span className="dt-spinner" />
        正在读取会话列表…
      </div>
    )
  }

  if (sessions.length === 0) {
    return (
      <div className="dt-empty">
        <Icon name="history" size={28} />
        <strong>还没有历史会话</strong>
        <span>完成第一次转录后会出现在这里。</span>
      </div>
    )
  }

  return (
    <div className="dt-history-list">
      {loading && (
        <div className="dt-history-list__banner" aria-live="polite">
          <span className="dt-spinner" />
          正在刷新列表…
        </div>
      )}
      {sessions.map((session) => {
        const isOpening = opening?.sessionId === session.id
        const isActive = session.id === activeSessionId
        return (
          <article
            className={[
              'dt-history-item',
              isActive ? ' is-active' : '',
              isOpening ? ' is-opening' : '',
            ].join('')}
            key={`${session.location}:${session.id}`}
          >
            <button
              className="dt-history-item__main"
              aria-busy={isOpening || undefined}
              onClick={() => { void onLoad(session) }}
              type="button"
            >
              <span className="dt-history-item__icon">
                {isOpening
                  ? <span className="dt-spinner" aria-hidden />
                  : <Icon name={session.location === 'cloud' ? 'cloud' : 'archive'} size={18} />}
              </span>
              <span>
                <strong>{session.title || '未命名会话'}</strong>
                <small>
                  {isOpening
                    ? opening.label
                    : `${formatDate(session.createdAt)} · ${formatDuration(session.durationSeconds)}`}
                </small>
              </span>
              <span className="dt-history-item__status">
                {session.location === 'cloud' ? '云端' : '本地'}
              </span>
            </button>
            {isOpening && (
              <div
                className="dt-history-item__progress"
                role="progressbar"
                aria-label={opening.label}
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={opening.percent ?? undefined}
                aria-valuetext={
                  opening.percent === null
                    ? opening.label
                    : `${opening.label} ${Math.round(opening.percent)}%`
                }
              >
                <span
                  className={
                    opening.percent === null
                      ? 'dt-history-item__progress-bar is-indeterminate'
                      : 'dt-history-item__progress-bar'
                  }
                  style={
                    opening.percent === null
                      ? undefined
                      : { width: `${Math.max(4, Math.min(100, opening.percent))}%` }
                  }
                />
              </div>
            )}
            <button
              aria-label={`删除 ${session.title}`}
              className="dt-icon-button dt-icon-button--danger"
              disabled={isOpening}
              onClick={() => {
                if (window.confirm(`确定删除“${session.title || '未命名会话'}”吗？`)) {
                  void onDelete(session)
                }
              }}
              type="button"
            >
              <Icon name="close" size={17} />
            </button>
          </article>
        )
      })}
    </div>
  )
}
