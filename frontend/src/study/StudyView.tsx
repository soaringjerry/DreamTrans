import { useCallback, useEffect, useState, type FormEvent } from 'react'
import {
  createAIProject,
  deleteAIProject,
  generateProjectSkillMap,
  getProjectSkillMap,
  linkProjectSession,
  listAIProjects,
  listProjectSessions,
  unlinkProjectSession,
  updateAIProject,
  type AIProject,
  type ProjectSession,
  type SkillMapDocument,
} from '../api'
import { listSessions, type Session } from '../pro/api/auth'
import { Icon } from '../unified/components/Icon'
import type { HistorySession } from '../unified/components/HistoryPanel'

interface StudyViewProps {
  /** Loads the session into the transcription workspace. */
  onOpenSession: (session: HistorySession) => void
}

function formatDate(value: string): string {
  const parsed = Date.parse(value)
  if (!parsed) return ''
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(parsed)
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

function toHistorySession(session: ProjectSession): HistorySession {
  return {
    id: session.id,
    title: session.title,
    createdAt: Date.parse(session.created_at) || Date.now(),
    durationSeconds: session.duration_seconds,
    status: session.status,
    location: 'cloud',
  }
}

function errorMessage(reason: unknown, fallback: string): string {
  const message = reason instanceof Error ? reason.message : String(reason ?? '')
  return message ? `${fallback}：${message}` : fallback
}

/**
 * 学习模式：课程（AI 项目）是一等入口。课程主页列出课程里的全部会话，
 * 会话的归属在这里管理，而不是从打开的会话反向挑项目。
 */
export function StudyView({ onOpenSession }: StudyViewProps) {
  const [courses, setCourses] = useState<AIProject[]>([])
  const [coursesLoading, setCoursesLoading] = useState(true)
  const [course, setCourse] = useState<AIProject | null>(null)
  const [sessions, setSessions] = useState<ProjectSession[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [draftName, setDraftName] = useState('')
  // Cloud sessions available to add; null while the picker is closed.
  const [candidates, setCandidates] = useState<Session[] | null>(null)
  // null = none stored yet; undefined = still loading.
  const [skillMap, setSkillMap] = useState<SkillMapDocument | null | undefined>(undefined)
  const [skillMapBusy, setSkillMapBusy] = useState(false)
  const [expandedSkillId, setExpandedSkillId] = useState('')

  const refreshCourses = useCallback(async () => {
    setCoursesLoading(true)
    try {
      const result = await listAIProjects()
      setCourses(result.projects)
    } catch (reason) {
      setError(errorMessage(reason, '课程列表加载失败'))
    } finally {
      setCoursesLoading(false)
    }
  }, [])

  useEffect(() => { void refreshCourses() }, [refreshCourses])

  const refreshSessions = useCallback(async (courseId: string) => {
    setSessions(null)
    try {
      setSessions(await listProjectSessions(courseId))
    } catch (reason) {
      setSessions([])
      setError(errorMessage(reason, '课程会话加载失败'))
    }
  }, [])

  const refreshSkillMap = useCallback(async (courseId: string) => {
    setSkillMap(undefined)
    try {
      const result = await getProjectSkillMap(courseId)
      setSkillMap(result.map)
    } catch (reason) {
      setSkillMap(null)
      setError(errorMessage(reason, '技能地图加载失败'))
    }
  }, [])

  const openCourse = (next: AIProject) => {
    setError(null)
    setCandidates(null)
    setExpandedSkillId('')
    setCourse(next)
    void refreshSessions(next.id)
    void refreshSkillMap(next.id)
  }

  const closeCourse = () => {
    setCourse(null)
    setSessions(null)
    setCandidates(null)
    setSkillMap(undefined)
    setExpandedSkillId('')
    setError(null)
  }

  const generateSkillMap = async () => {
    if (!course || skillMapBusy) return
    setSkillMapBusy(true)
    setError(null)
    try {
      const result = await generateProjectSkillMap(course.id, crypto.randomUUID())
      setSkillMap(result.map)
      setExpandedSkillId('')
    } catch (reason) {
      setError(errorMessage(reason, '技能地图生成失败'))
    } finally {
      setSkillMapBusy(false)
    }
  }

  const perform = useCallback(async (fallback: string, operation: () => Promise<void>) => {
    setBusy(true)
    setError(null)
    try {
      await operation()
    } catch (reason) {
      setError(errorMessage(reason, fallback))
    } finally {
      setBusy(false)
    }
  }, [])

  const submitCreate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const name = draftName.trim()
    if (!name || busy) return
    await perform('创建课程失败', async () => {
      const created = await createAIProject(name)
      setDraftName('')
      setCreating(false)
      await refreshCourses()
      openCourse(created)
    })
  }

  const renameCourse = async () => {
    if (!course) return
    const next = window.prompt('课程名称', course.name)?.trim()
    if (!next || next === course.name) return
    await perform('重命名课程失败', async () => {
      const updated = await updateAIProject(course.id, { name: next })
      setCourse(updated)
      await refreshCourses()
    })
  }

  const removeCourse = async () => {
    if (!course) return
    if (!window.confirm(
      `删除课程“${course.name || '未命名课程'}”？课程里的资料会一并删除，会话本身不受影响。`,
    )) return
    await perform('删除课程失败', async () => {
      await deleteAIProject(course.id)
      closeCourse()
      await refreshCourses()
    })
  }

  const openPicker = async () => {
    if (!course) return
    await perform('云端会话加载失败', async () => {
      const result = await listSessions(1, 100)
      setCandidates(result.sessions.filter(
        (session) => session.project_id !== course.id,
      ))
    })
  }

  const addSession = async (candidate: Session) => {
    if (!course) return
    await perform('添加会话失败', async () => {
      await linkProjectSession(course.id, candidate.id)
      setCandidates((current) => current?.filter(({ id }) => id !== candidate.id) ?? null)
      await refreshSessions(course.id)
    })
  }

  const removeSession = async (session: ProjectSession) => {
    if (!course) return
    await perform('移除会话失败', async () => {
      await unlinkProjectSession(course.id, session.id)
      await refreshSessions(course.id)
    })
  }

  const courseNameById = new Map(courses.map(({ id, name }) => [id, name]))

  return (
    <div className="dt-study">
      {error && (
        <p className="dt-study__error" role="alert">
          {error}
          <button aria-label="关闭错误提示" onClick={() => setError(null)} type="button">
            <Icon name="close" size={12} />
          </button>
        </p>
      )}

      {!course && (
        <>
          <header className="dt-study__header">
            <div>
              <p className="dt-eyebrow">Study</p>
              <h2>学习</h2>
              <p className="dt-study__lead">
                每门课程汇集它的全部会话和资料。点开课程查看内容，后续的技能地图和练习也会在这里。
              </p>
            </div>
            <button
              className="dt-button dt-button--primary"
              onClick={() => { setCreating((value) => !value); setDraftName('') }}
              type="button"
            >
              {creating ? '取消' : '新建课程'}
            </button>
          </header>

          {creating && (
            <form className="dt-study__create" onSubmit={(event) => { void submitCreate(event) }}>
              <input
                autoFocus
                maxLength={160}
                onChange={(event) => setDraftName(event.target.value)}
                placeholder="课程名称，如 PSY2041"
                value={draftName}
              />
              <button
                className="dt-button dt-button--primary"
                disabled={busy || !draftName.trim()}
                type="submit"
              >
                创建
              </button>
            </form>
          )}

          {coursesLoading && courses.length === 0 && (
            <p className="dt-study__empty">正在加载课程…</p>
          )}
          {!coursesLoading && courses.length === 0 && !creating && (
            <div className="dt-empty dt-empty--compact">
              <Icon name="map" size={24} />
              <strong>还没有课程</strong>
              <span>建一门课程，把相关的会话和资料挂在一起。</span>
            </div>
          )}

          <div className="dt-study__grid">
            {courses.map((item) => (
              <button
                className="dt-study__card"
                key={item.id}
                onClick={() => openCourse(item)}
                type="button"
              >
                <strong>{item.name || '未命名课程'}</strong>
                {item.description && <span>{item.description}</span>}
              </button>
            ))}
          </div>
        </>
      )}

      {course && (
        <>
          <header className="dt-study__header">
            <div>
              <button className="dt-study__back" onClick={closeCourse} type="button">
                <Icon name="close" size={14} />
                全部课程
              </button>
              <h2>{course.name || '未命名课程'}</h2>
              {course.description && <p className="dt-study__lead">{course.description}</p>}
            </div>
            <span className="dt-study__course-actions">
              <button
                aria-label="重命名课程"
                className="dt-icon-button"
                disabled={busy}
                onClick={() => { void renameCourse() }}
                title="重命名"
                type="button"
              >
                <Icon name="settings" size={16} />
              </button>
              <button
                aria-label="删除课程"
                className="dt-icon-button dt-icon-button--danger"
                disabled={busy}
                onClick={() => { void removeCourse() }}
                title="删除课程"
                type="button"
              >
                <Icon name="close" size={16} />
              </button>
            </span>
          </header>

          <div className="dt-study__section-heading">
            <span>技能地图</span>
            <button
              className="dt-button dt-button--secondary"
              disabled={skillMapBusy || (sessions?.length ?? 0) === 0}
              onClick={() => { void generateSkillMap() }}
              title={(sessions?.length ?? 0) === 0
                ? '先给课程添加会话，再生成技能地图'
                : 'AI 从课程会话的转录提炼技能地图，会产生少量费用'}
              type="button"
            >
              {skillMapBusy ? '正在生成…' : skillMap ? '重新生成' : '生成技能地图'}
            </button>
          </div>

          {skillMap === undefined && <p className="dt-study__empty">正在加载技能地图…</p>}
          {skillMap === null && !skillMapBusy && (
            <p className="dt-study__empty">
              还没有技能地图。生成后，这门课要求掌握的能力会按从基础到进阶排列在这里,
              后续的练习会把它一项一项点亮。
            </p>
          )}
          {skillMap && (
            <div className="dt-study__skills">
              {skillMap.skills.map((skill, index) => {
                const expanded = expandedSkillId === skill.id
                const prerequisiteLabels = (skill.prerequisites ?? [])
                  .map((id) => skillMap.skills.find((item) => item.id === id)?.label)
                  .filter((label): label is string => Boolean(label))
                return (
                  <div
                    className={`dt-study__skill${expanded ? ' is-expanded' : ''}`}
                    key={skill.id}
                  >
                    <button
                      className="dt-study__skill-head"
                      onClick={() => setExpandedSkillId(expanded ? '' : skill.id)}
                      type="button"
                    >
                      <span aria-label="未开始" className="dt-study__skill-state" title="未开始">
                        {index + 1}
                      </span>
                      <span className="dt-study__skill-label">
                        {skill.label}
                        {skill.new && <em className="dt-study__skill-new">新</em>}
                      </span>
                      {skill.outcome && <small>{skill.outcome}</small>}
                    </button>
                    {expanded && (
                      <div className="dt-study__skill-detail">
                        {skill.summary && <p>{skill.summary}</p>}
                        {prerequisiteLabels.length > 0 && (
                          <p className="dt-study__skill-prereq">
                            依赖：{prerequisiteLabels.join('、')}
                          </p>
                        )}
                        {(skill.evidence ?? []).map((evidence, evidenceIndex) => {
                          const evidenceSession = sessions?.find(
                            ({ id }) => id === evidence.session_id,
                          )
                          return (
                            <button
                              className="dt-study__skill-evidence"
                              disabled={!evidenceSession}
                              key={evidenceIndex}
                              onClick={() => {
                                if (evidenceSession) {
                                  onOpenSession(toHistorySession(evidenceSession))
                                }
                              }}
                              title={evidenceSession ? '在转录工作区打开这场会话' : '这场会话已不在课程里'}
                              type="button"
                            >
                              <span>“{evidence.quote}”</span>
                              <small>{evidence.session_title || '课程会话'}</small>
                            </button>
                          )
                        })}
                      </div>
                    )}
                  </div>
                )
              })}
              <p className="dt-study__skill-meta">
                基于 {skillMap.session_count} 场会话生成
                {skillMap.truncated && '（部分转录超出预算被截断）'}
                {skillMap.generated_at && ` · ${formatDate(skillMap.generated_at)}`}
              </p>
            </div>
          )}

          <div className="dt-study__section-heading">
            <span>课程会话</span>
            <button
              className="dt-button dt-button--secondary"
              disabled={busy}
              onClick={() => {
                if (candidates) {
                  setCandidates(null)
                } else {
                  void openPicker()
                }
              }}
              type="button"
            >
              {candidates ? '收起' : '添加会话'}
            </button>
          </div>

          {candidates && (
            <div className="dt-study__picker">
              {candidates.length === 0 && (
                <p className="dt-study__empty">没有可添加的云端会话。本地会话请先上传到云端。</p>
              )}
              {candidates.map((candidate) => (
                <div className="dt-study__row" key={candidate.id}>
                  <button
                    className="dt-study__row-main"
                    disabled={busy}
                    onClick={() => { void addSession(candidate) }}
                    type="button"
                  >
                    <Icon name="plus" size={14} />
                    <span className="dt-study__row-title">{candidate.title || '未命名会话'}</span>
                    <small>
                      {formatDate(candidate.created_at)}
                      {candidate.project_id && (
                        ` · 现属于 ${courseNameById.get(candidate.project_id) ?? '其他课程'}`
                      )}
                    </small>
                  </button>
                </div>
              ))}
            </div>
          )}

          {sessions === null && <p className="dt-study__empty">正在加载课程会话…</p>}
          {sessions?.length === 0 && !candidates && (
            <div className="dt-empty dt-empty--compact">
              <Icon name="history" size={24} />
              <strong>这门课程还没有会话</strong>
              <span>点击“添加会话”，把已有的云端会话挂进来。</span>
            </div>
          )}

          <div className="dt-study__sessions">
            {sessions?.map((session) => (
              <div className="dt-study__row" key={session.id}>
                <button
                  className="dt-study__row-main"
                  onClick={() => onOpenSession(toHistorySession(session))}
                  title="在转录工作区打开"
                  type="button"
                >
                  <Icon name="history" size={15} />
                  <span className="dt-study__row-title">{session.title || '未命名会话'}</span>
                  <small>
                    {formatDate(session.started_at)} · {formatDuration(session.duration_seconds)}
                  </small>
                </button>
                <button
                  aria-label={`把 ${session.title || '未命名会话'} 移出课程`}
                  className="dt-icon-button"
                  disabled={busy}
                  onClick={() => { void removeSession(session) }}
                  title="移出课程（会话保留）"
                  type="button"
                >
                  <Icon name="close" size={14} />
                </button>
              </div>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
