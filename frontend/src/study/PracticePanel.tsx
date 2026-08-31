import { useCallback, useEffect, useRef, useState } from 'react'
import {
  nextStudyScenario,
  submitStudyAttempt,
  type StudyGradeResult,
  type StudyServe,
} from '../api'
import { Icon } from '../unified/components/Icon'

interface PracticePanelProps {
  projectId: string
  skillLabel: string
  onClose: () => void
}

/** Session-local combo multiplier (display only; stored XP is server-side). */
function comboMultiplier(combo: number): string | null {
  if (combo >= 12) return '×3'
  if (combo >= 8) return '×2'
  if (combo >= 5) return '×1.5'
  if (combo >= 3) return '×1.2'
  if (combo >= 2) return '×1.1'
  return null
}

const BONUS_LABELS: Record<string, string> = {
  no_hint: 'No Hint',
  first_try: 'First Try',
  self_correction: 'Self-Correction',
  precise_language: 'Precise Language',
  alternative_explanation: 'Alternative Explanation',
  hidden_insight: 'Hidden Insight',
  transfer: 'Transfer',
}

const LEVEL_LABELS: Record<string, string> = {
  learner: '入门',
  supervised: '辅助',
  hazard: '挑战',
  independent: '独立',
  mastered: '精通',
}

type Entry =
  | { kind: 'scenario'; serve: StudyServe }
  | { kind: 'answer'; text: string }
  | { kind: 'grade'; result: StudyGradeResult; combo: number }

/**
 * IM-style practice loop with a single fixed instructor voice:
 * situation → answer → grade with an exit → next situation.
 */
export function PracticePanel({ projectId, skillLabel, onClose }: PracticePanelProps) {
  const [entries, setEntries] = useState<Entry[]>([])
  const [serve, setServe] = useState<StudyServe | null>(null)
  const [draft, setDraft] = useState('')
  const [hintShown, setHintShown] = useState(false)
  const [zhShown, setZhShown] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [combo, setCombo] = useState(0)
  const logRef = useRef<HTMLDivElement | null>(null)

  const fetchNext = useCallback(async () => {
    setBusy(true)
    setError(null)
    try {
      const next = await nextStudyScenario(projectId, skillLabel, crypto.randomUUID())
      setServe(next)
      setHintShown(false)
      setZhShown(false)
      setDraft('')
      setEntries((current) => [...current, { kind: 'scenario', serve: next }])
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '题目加载失败')
    } finally {
      setBusy(false)
    }
  }, [projectId, skillLabel])

  // Guard StrictMode's double effect: one panel, one opening scenario.
  const startedRef = useRef(false)
  useEffect(() => {
    if (startedRef.current) return
    startedRef.current = true
    void fetchNext()
  }, [fetchNext])

  useEffect(() => {
    logRef.current?.scrollTo({ top: logRef.current.scrollHeight })
  }, [entries, busy])

  const submit = async () => {
    const answer = draft.trim()
    if (!serve || !answer || busy) return
    setBusy(true)
    setError(null)
    setEntries((current) => [...current, { kind: 'answer', text: answer }])
    try {
      const result = await submitStudyAttempt(
        projectId,
        { scenario_id: serve.scenario_id, answer, used_hint: hintShown },
        crypto.randomUUID(),
      )
      const passed = result.grade !== 'F' && result.grade !== 'P'
      const nextCombo = passed ? combo + 1 : 0
      setCombo(nextCombo)
      setServe(null)
      setDraft('')
      setEntries((current) => [...current, { kind: 'grade', result, combo: nextCombo }])
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '提交失败')
      // The unanswered scenario stays live so the learner can retry submitting.
      setEntries((current) => current.filter(
        (entry, index) => !(index === current.length - 1 && entry.kind === 'answer'),
      ))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div aria-label={`练习 ${skillLabel}`} className="dt-practice" role="dialog">
      <header className="dt-practice__head">
        <div>
          <p className="dt-eyebrow">Practice</p>
          <h3>{skillLabel}</h3>
        </div>
        <span className="dt-practice__head-right">
          {combo >= 2 && (
            <span className="dt-practice__combo">
              🔥 {combo} Combo {comboMultiplier(combo)}
            </span>
          )}
          <button
            aria-label="结束练习"
            className="dt-icon-button"
            onClick={onClose}
            type="button"
          >
            <Icon name="close" size={17} />
          </button>
        </span>
      </header>

      <div className="dt-practice__log" ref={logRef}>
        {entries.map((entry, index) => {
          if (entry.kind === 'scenario') {
            const { scenario } = entry.serve
            const isCurrent = serve !== null && index === entries.length - 1
            return (
              <div className="dt-practice__bubble dt-practice__bubble--instructor" key={index}>
                <strong className="dt-practice__speaker">导师</strong>
                <p>{scenario.situation}</p>
                <p className="dt-practice__question">{scenario.question}</p>
                {isCurrent && scenario.question_zh && (
                  zhShown
                    ? <p className="dt-practice__zh">{scenario.question_zh}</p>
                    : (
                      <button
                        className="dt-practice__aid"
                        onClick={() => setZhShown(true)}
                        type="button"
                      >
                        看中文
                      </button>
                    )
                )}
                {isCurrent && scenario.hint && (
                  hintShown
                    ? <p className="dt-practice__zh">提示：{scenario.hint}</p>
                    : (
                      <button
                        className="dt-practice__aid"
                        onClick={() => setHintShown(true)}
                        title="用了提示这题就不算完全独立完成"
                        type="button"
                      >
                        要提示
                      </button>
                    )
                )}
              </div>
            )
          }
          if (entry.kind === 'answer') {
            return (
              <div className="dt-practice__bubble dt-practice__bubble--me" key={index}>
                <p>{entry.text}</p>
              </div>
            )
          }
          const { result } = entry
          return (
            <div className="dt-practice__bubble dt-practice__bubble--grade" key={index}>
              <span className={`dt-practice__grade dt-practice__grade--${result.grade}`}>
                {result.grade}
              </span>
              <p>{result.feedback}</p>
              <p className="dt-practice__next-step">{result.next_step}</p>
              <p className="dt-practice__xp">
                +{result.xp} XP
                {result.bonuses.map((bonus) => (
                  <em key={bonus}>{BONUS_LABELS[bonus] ?? bonus}</em>
                ))}
              </p>
              {result.leveled_up && (
                <p className="dt-practice__levelup">
                  ⬆ 这项能力升到「{LEVEL_LABELS[result.state.level] ?? result.state.level}」
                </p>
              )}
            </div>
          )
        })}
        {busy && <p className="dt-practice__busy">导师正在{serve ? '批改' : '出题'}…</p>}
        {error && (
          <p className="dt-practice__error" role="alert">
            {error}
            <button
              onClick={() => {
                if (serve) {
                  setError(null)
                } else {
                  void fetchNext()
                }
              }}
              type="button"
            >
              {serve ? '知道了' : '重试'}
            </button>
          </p>
        )}
      </div>

      <footer className="dt-practice__composer">
        {serve ? (
          <>
            <textarea
              disabled={busy}
              maxLength={4000}
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
                  event.preventDefault()
                  void submit()
                }
              }}
              placeholder="写下你的判断和理由（Ctrl+Enter 提交）"
              value={draft}
            />
            <button
              className="dt-button dt-button--primary"
              disabled={busy || !draft.trim()}
              onClick={() => { void submit() }}
              type="button"
            >
              提交
            </button>
          </>
        ) : (
          <button
            className="dt-button dt-button--primary dt-button--wide"
            disabled={busy}
            onClick={() => { void fetchNext() }}
            type="button"
          >
            下一题
          </button>
        )}
      </footer>
    </div>
  )
}
