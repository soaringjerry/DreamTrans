import { Icon } from './Icon'

export type RecorderStatus =
  | 'idle'
  | 'starting'
  | 'recording'
  | 'paused'
  | 'stopping'
  | 'reconnecting'
  | 'error'

interface RecorderBarProps {
  assistantEnabled: boolean
  canContinue: boolean
  durationLabel: string
  onAssistant: () => void
  onContinue: () => void
  onMore: () => void
  onPauseToggle: () => void
  onStart: () => void
  onStop: () => void
  status: RecorderStatus
}

export function RecorderBar({
  assistantEnabled,
  canContinue,
  durationLabel,
  onAssistant,
  onContinue,
  onMore,
  onPauseToggle,
  onStart,
  onStop,
  status,
}: RecorderBarProps) {
  const active = status === 'recording'
    || status === 'paused'
    || status === 'reconnecting'
    || status === 'error'
  const busy = status === 'starting' || status === 'stopping'

  return (
    <div className="dt-recorder-shell">
      <div className="dt-recorder" role="toolbar" aria-label="录音控制">
        <button
          aria-label="打开 AI 助手"
          className="dt-recorder__utility"
          disabled={!assistantEnabled}
          onClick={onAssistant}
          title={assistantEnabled ? undefined : '服务端未配置 AI 能力'}
          type="button"
        >
          <Icon name="sparkles" />
          <span>AI</span>
        </button>

        <div className="dt-recorder__primary">
          {!active && !busy && canContinue && (
            <button
              aria-label="继续当前会话"
              className="dt-recorder__secondary-action"
              onClick={onContinue}
              title="继续当前会话"
              type="button"
            >
              <Icon name="play" />
            </button>
          )}
          {active && status !== 'error' && (
            <button
              aria-label={status === 'paused' ? '继续录音' : '暂停录音'}
              className="dt-recorder__secondary-action"
              disabled={status === 'reconnecting'}
              onClick={onPauseToggle}
              type="button"
            >
              <Icon name={status === 'paused' ? 'play' : 'pause'} />
            </button>
          )}

          <button
            aria-label={active ? '停止录音' : '开始新会话'}
            className={`dt-record-button${active ? ' is-recording' : ''}`}
            disabled={busy}
            onClick={active ? onStop : onStart}
            type="button"
          >
            {busy ? <span className="dt-spinner" /> : <Icon name={active ? 'stop' : 'mic'} size={25} />}
            {status === 'recording' && <span aria-hidden="true" className="dt-record-button__pulse" />}
          </button>

          {active && (
            <div className="dt-recorder__time" aria-live="off">
              <span className="dt-live-dot" />
              <strong>{durationLabel}</strong>
            </div>
          )}
        </div>

        <button
          aria-label="更多工具"
          className="dt-recorder__utility"
          onClick={onMore}
          type="button"
        >
          <Icon name="more" />
          <span>更多</span>
        </button>
      </div>
    </div>
  )
}
