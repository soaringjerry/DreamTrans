import { useCallback, useEffect, useRef, useState } from 'react'
import {
  nextStudyScenario,
  submitStudyAttempt,
  type StudyGradeResult,
  type StudyServe,
} from '../api'
import { Icon } from '../unified/components/Icon'
import { Mascot, type MascotMood } from './Mascot'

interface PracticePanelProps {
  projectId: string
  skillLabel: string
  /** Last known level/streak so the header is right before the first serve. */
  initialLevel?: string
  initialStreak?: number
  onClose: () => void
}

export const INSTRUCTOR_NAME = 'TUTOR-01'

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
  no_hint: 'NO HINT',
  first_try: 'FIRST TRY',
  self_correction: 'SELF-CORRECTION',
  precise_language: 'PRECISE LANGUAGE',
  alternative_explanation: 'ALT. EXPLANATION',
  hidden_insight: 'HIDDEN INSIGHT',
  transfer: 'TRANSFER',
  language_independence: 'LANGUAGE INDEPENDENCE',
}

const EVENT_LABELS: Record<string, string> = {
  misconception_broken: 'Misconception Broken',
  transfer_success: 'Transfer Success',
  critical_insight: 'Critical Insight',
  self_correction: 'Self Correction',
  language_save: 'Language Save',
}

const LEVEL_LABELS: Record<string, string> = {
  learner: '入门',
  supervised: '辅助',
  hazard: '挑战',
  independent: '独立',
  mastered: '精通',
}

/** Hint-free passes needed to leave each level (mirrors the server table). */
const LEVEL_UP_STREAK: Record<string, number> = {
  learner: 2,
  supervised: 3,
  hazard: 3,
  independent: 4,
}

function moodForGrade(grade: string): MascotMood {
  switch (grade) {
    case 'HD':
    case 'D':
      return 'proud'
    case 'C':
      return 'happy'
    case 'P':
      return 'meh'
    default:
      return 'oops'
  }
}

type Entry =
  | { kind: 'scenario'; serve: StudyServe }
  | { kind: 'answer'; text: string }
  | { kind: 'grade'; result: StudyGradeResult; combo: number }

/**
 * The practice stage: one fixed instructor (a TV-headed unit whose screen
 * shows its mood), situation → answer → stamped grade with an exit → next.
 */
export function PracticePanel({
  projectId, skillLabel, initialLevel, initialStreak, onClose,
}: PracticePanelProps) {
  const [entries, setEntries] = useState<Entry[]>([])
  const [serve, setServe] = useState<StudyServe | null>(null)
  const [draft, setDraft] = useState('')
  const [hintShown, setHintShown] = useState(false)
  const [zhShown, setZhShown] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [combo, setCombo] = useState(0)
  const [level, setLevel] = useState(initialLevel ?? 'learner')
  const [streak, setStreak] = useState(initialStreak ?? 0)
  const [mood, setMood] = useState<MascotMood>('thinking')
  const [levelUpFlash, setLevelUpFlash] = useState(false)
  const logRef = useRef<HTMLDivElement | null>(null)
  const practiceSessionId = useRef(crypto.randomUUID())

  const fetchNext = useCallback(async () => {
    setBusy(true)
    setError(null)
    setMood('thinking')
    try {
      const next = await nextStudyScenario(
        projectId,
        skillLabel,
        crypto.randomUUID(),
        practiceSessionId.current,
      )
      setServe(next)
      setLevel(next.level)
      setHintShown(false)
      setZhShown(Boolean(next.scaffold?.show_zh))
      setDraft('')
      setMood('idle')
      setEntries((current) => [...current, { kind: 'scenario', serve: next }])
    } catch (reason) {
      setMood('glitch')
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

  useEffect(() => {
    if (!levelUpFlash) return
    const timer = window.setTimeout(() => setLevelUpFlash(false), 900)
    return () => window.clearTimeout(timer)
  }, [levelUpFlash])

  const submit = async () => {
    const answer = draft.trim()
    if (!serve || !answer || busy) return
    setBusy(true)
    setError(null)
    setMood('thinking')
    setEntries((current) => [...current, { kind: 'answer', text: answer }])
    try {
      const result = await submitStudyAttempt(
        projectId,
        {
          scenario_id: serve.scenario_id,
          answer,
          used_hint: hintShown,
          used_zh: zhShown,
          practice_session_id: practiceSessionId.current,
        },
        crypto.randomUUID(),
      )
      setCombo(result.combo ?? 0)
      setLevel(result.state.level)
      setStreak(result.state.clean_streak)
      setServe(null)
      setDraft('')
      setMood((result.events ?? []).includes('misconception_broken')
        ? 'surprised'
        : moodForGrade(result.grade))
      if (result.leveled_up) setLevelUpFlash(true)
      setEntries((current) => [...current, { kind: 'grade', result, combo: result.combo ?? 0 }])
    } catch (reason) {
      setMood('glitch')
      setError(reason instanceof Error ? reason.message : '提交失败')
      // The unanswered scenario stays live so the learner can retry submitting.
      setEntries((current) => current.filter(
        (entry, index) => !(index === current.length - 1 && entry.kind === 'answer'),
      ))
    } finally {
      setBusy(false)
    }
  }

  const streakTarget = LEVEL_UP_STREAK[level] ?? 0

  return (
    <div
      aria-label={`练习 ${skillLabel}`}
      className={`dt-practice${levelUpFlash ? ' is-levelup' : ''}`}
      role="dialog"
    >
      <header className="dt-practice__head">
        <Mascot mood={mood} size={52} />
        <div className="dt-practice__head-text">
          <h3>{skillLabel}</h3>
          <div className="dt-practice__meta">
            <span>{INSTRUCTOR_NAME}</span>
            <span className={`dt-practice__level is-${level}`}>
              {LEVEL_LABELS[level] ?? level}
            </span>
            {streakTarget > 0 && (
              <span
                className="dt-practice__streak"
                title={`无提示通过 ${streakTarget} 次即可升级，当前 ${Math.min(streak, streakTarget)}/${streakTarget}`}
              >
                {Array.from({ length: streakTarget }, (_, index) => (
                  <i className={index < streak ? 'is-on' : ''} key={index} />
                ))}
              </span>
            )}
          </div>
        </div>
        <span className="dt-practice__head-right">
          {combo >= 2 && (
            <span className="dt-practice__combo">
              {combo} COMBO {comboMultiplier(combo)}
            </span>
          )}
          <button
            aria-label="结束练习"
            className="st-iconbtn"
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
            const offerZH = isCurrent && Boolean(scenario.question_zh)
              && entry.serve.scaffold?.offer_zh !== false
            const offerHint = isCurrent && Boolean(scenario.hint)
              && entry.serve.scaffold?.offer_hint !== false
            return (
              <div className="dt-practice__bubble dt-practice__bubble--instructor" key={index}>
                <Mascot mood={isCurrent ? mood : 'idle'} size={40} />
                <div className="dt-practice__bubble-body">
                  <strong className="dt-practice__speaker">
                    {INSTRUCTOR_NAME} // LV.{entry.serve.difficulty}
                  </strong>
                  {entry.serve.coach_line && (
                    <p className="dt-practice__coach">{entry.serve.coach_line}</p>
                  )}
                  <p>{scenario.situation}</p>
                  <p className="dt-practice__question">{scenario.question}</p>
                  {offerZH && zhShown && (
                    <p className="dt-practice__zh">{scenario.question_zh}</p>
                  )}
                  {offerHint && hintShown && (
                    <p className="dt-practice__zh">提示：{scenario.hint}</p>
                  )}
                  {((offerZH && !zhShown) || (offerHint && !hintShown)) && (
                    <div className="dt-practice__aids">
                      {offerZH && !zhShown && (
                        <button
                          className="dt-practice__aid"
                          onClick={() => setZhShown(true)}
                          type="button"
                        >
                          看中文
                        </button>
                      )}
                      {offerHint && !hintShown && (
                        <button
                          className="dt-practice__aid"
                          onClick={() => { setHintShown(true); setMood('wink') }}
                          title="用了提示这题就不算完全独立完成"
                          type="button"
                        >
                          要提示
                        </button>
                      )}
                    </div>
                  )}
                </div>
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
            <div
              className={`dt-practice__bubble dt-practice__bubble--grade is-${result.grade}`}
              key={index}
            >
              <span className={`dt-practice__grade dt-practice__grade--${result.grade}`}>
                {result.grade}
              </span>
              <div className="dt-practice__grade-body">
                <p>{result.feedback}</p>
                <p className="dt-practice__next-step">{result.next_step}</p>
                <p className="dt-practice__xp">
                  +{result.xp} XP
                  {entry.combo >= 2 && comboMultiplier(entry.combo) && (
                    <em className="is-combo">{entry.combo} COMBO {comboMultiplier(entry.combo)}</em>
                  )}
                  {result.bonuses.map((bonus) => (
                    <em key={bonus}>{BONUS_LABELS[bonus] ?? bonus}</em>
                  ))}
                </p>
                {(result.events ?? []).map((event) => (
                  <p className="dt-practice__event" key={event}>
                    {EVENT_LABELS[event] ?? event}
                  </p>
                ))}
                {result.leveled_up && (
                  <p className="dt-practice__levelup">
                    LEVEL UP · 这项能力升到「{LEVEL_LABELS[result.state.level] ?? result.state.level}」
                  </p>
                )}
              </div>
            </div>
          )
        })}
        {busy && (
          <p className="dt-practice__busy">{serve ? 'GRADING // 导师正在批改' : 'LOADING // 导师正在出题'}</p>
        )}
        {error && (
          <p className="dt-practice__error" role="alert">
            {error}
            <button
              onClick={() => {
                if (serve) {
                  setError(null)
                  setMood('idle')
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
              className="st-btn st-btn--primary"
              disabled={busy || !draft.trim()}
              onClick={() => { void submit() }}
              type="button"
            >
              提交
            </button>
          </>
        ) : (
          <button
            className="st-btn st-btn--orange st-btn--wide"
            disabled={busy}
            onClick={() => { void fetchNext() }}
            type="button"
          >
            <Icon name="play" size={12} />
            下一题
          </button>
        )}
      </footer>
    </div>
  )
}
