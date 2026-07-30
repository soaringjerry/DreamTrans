import { Icon } from './Icon'

export interface HistorySession {
  id: string
  title: string
  createdAt: number
  durationSeconds: number
  status: string
  location: 'cloud' | 'local'
}

interface HistoryPanelProps {
  activeSessionId: string
  loading: boolean
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
  sessions,
  onDelete,
  onLoad,
}: HistoryPanelProps) {
  if (loading) {
    return <div className="dt-empty"><span className="dt-spinner" />正在读取会话…</div>
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
      {sessions.map((session) => (
        <article
          className={`dt-history-item${session.id === activeSessionId ? ' is-active' : ''}`}
          key={`${session.location}:${session.id}`}
        >
          <button
            className="dt-history-item__main"
            onClick={() => { void onLoad(session) }}
            type="button"
          >
            <span className="dt-history-item__icon">
              <Icon name={session.location === 'cloud' ? 'cloud' : 'archive'} size={18} />
            </span>
            <span>
              <strong>{session.title || '未命名会话'}</strong>
              <small>
                {formatDate(session.createdAt)} · {formatDuration(session.durationSeconds)}
              </small>
            </span>
            <span className="dt-history-item__status">
              {session.location === 'cloud' ? '云端' : '本地'}
            </span>
          </button>
          <button
            aria-label={`删除 ${session.title}`}
            className="dt-icon-button dt-icon-button--danger"
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
      ))}
    </div>
  )
}
