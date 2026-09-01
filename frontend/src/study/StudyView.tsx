import {
  useCallback, useEffect, useRef, useState, type CSSProperties, type DragEvent, type FormEvent,
} from 'react'
import {
  cancelProjectSkillMap,
  createAIProject,
  deleteAIProject,
  deleteKnowledgeSource,
  formatUsageUSD,
  generateProjectSkillMap,
  getProjectSkillMap,
  getStudyCosts,
  linkProjectSession,
  listAIProjects,
  listKnowledgeSources,
  listProjectSessions,
  listStudyStates,
  retryKnowledgeSource,
  unlinkProjectSession,
  updateAIProject,
  uploadKnowledgeFile,
  type AIProject,
  type KnowledgeSource,
  type ProjectSession,
  type SkillMapDocument,
  type SkillMapJob,
  type SkillMapResponse,
  type StudyContinue,
  type StudyCosts,
  type StudySkillState,
} from '../api'
import { listSessions, type Session } from '../pro/api/auth'
import { Icon } from '../unified/components/Icon'
import type { HistorySession } from '../unified/components/HistoryPanel'
import { Mascot } from './Mascot'
import { PracticePanel } from './PracticePanel'
import { STUDY_BILLING_EVENT } from './StudyApp'

/** Mirrors the server's skill_key normalization (lowercase, collapsed spaces). */
function skillKeyOf(label: string): string {
  return label.toLowerCase().split(/\s+/).filter(Boolean).join(' ')
}

const LEVEL_TITLES: Record<string, string> = {
  learner: '入门：在明显提示下能做',
  supervised: '辅助：少量帮助下能做',
  hazard: '挑战：能自己发现问题',
  independent: '独立：无提示独立完成',
  mastered: '精通：陌生情境也能完成',
}

const LEVEL_ORDER = ['learner', 'supervised', 'hazard', 'independent', 'mastered'] as const

const LEVEL_SHORT: Record<string, string> = {
  learner: '入门',
  supervised: '辅助',
  hazard: '挑战',
  independent: '独立',
  mastered: '精通',
}

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

/** Stable hue per course so its cover colour survives reloads. */
function hueOf(text: string): number {
  let hash = 0
  for (const char of text) hash = (hash * 31 + char.charCodeAt(0)) >>> 0
  return hash % 360
}

function pad(value: number): string {
  return value < 10 ? `0${value}` : String(value)
}

const MATERIAL_ACCEPT = '.pdf,.pptx,.docx,.xlsx,.txt,.md,.csv,.tsv,.json,.png,.jpg,.jpeg,.webp'

const SOURCE_STATUS: Record<KnowledgeSource['status'], string> = {
  queued: '排队中',
  processing: '正在抽取',
  ready: '已就绪',
  error: '抽取失败',
}

const FEATURE_LABELS: Record<string, string> = {
  skill_map: '技能地图',
  study_bank: '出题',
  study_grade: '批改',
  chat: 'AI 助手',
  other: '其他 AI',
}

function featureLabel(feature?: string, action?: string): string {
  if (feature && FEATURE_LABELS[feature]) return FEATURE_LABELS[feature]
  if (action && FEATURE_LABELS[action]) return FEATURE_LABELS[action]
  return FEATURE_LABELS.other
}

function announceBilling(): void {
  window.dispatchEvent(new Event(STUDY_BILLING_EVENT))
}

function formatBytes(bytes?: number): string {
  if (!bytes) return ''
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

/**
 * 学习模式：课程是一等入口。课程主页的主角是技能路线；会话和管理操作
 * 放在侧栏。练习从路线上的节点或「继续」入口进入。
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
  // Uploaded course materials (textbooks, decks, papers…); null while loading.
  const [materials, setMaterials] = useState<KnowledgeSource[] | null>(null)
  const [uploading, setUploading] = useState(0)
  const [dragOver, setDragOver] = useState(false)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  // What this course has cost; null while loading.
  const [costs, setCosts] = useState<StudyCosts | null>(null)
  const [costItemsShown, setCostItemsShown] = useState(false)
  // null = none stored yet; undefined = still loading.
  const [skillMap, setSkillMap] = useState<SkillMapDocument | null | undefined>(undefined)
  const [skillMapJob, setSkillMapJob] = useState<SkillMapJob | null>(null)
  const [skillMapBusy, setSkillMapBusy] = useState(false)
  const [expandedSkillId, setExpandedSkillId] = useState('')
  const [skillStates, setSkillStates] = useState<Record<string, StudySkillState>>({})
  const [continueSkill, setContinueSkill] = useState<StudyContinue | null>(null)
  // Label of the skill currently being practiced (null = panel closed).
  const [practiceSkill, setPracticeSkill] = useState<string | null>(null)

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

  const refreshMaterials = useCallback(async (courseId: string) => {
    try {
      setMaterials(await listKnowledgeSources(courseId))
    } catch (reason) {
      setMaterials((current) => current ?? [])
      setError(errorMessage(reason, '课程资料加载失败'))
    }
  }, [])

  const refreshCosts = useCallback(async (courseId: string) => {
    try {
      setCosts(await getStudyCosts(courseId))
    } catch {
      // The cost card is informational; the page works without it.
    }
  }, [])

  const applySkillMapResponse = useCallback((
    result: SkillMapResponse,
    keepExistingMap = false,
  ) => {
    if (result.map || !keepExistingMap) {
      setSkillMap(result.map)
    }
    setSkillMapJob((previous) => {
      const next = result.job ?? null
      if (previous && next && previous.id === next.id
        && previous.status !== 'ready' && next.status === 'ready') {
        // Generation just finished: it was charged, refresh what we show.
        void refreshCosts(next.project_id)
        announceBilling()
      }
      return next
    })
    if (result.job?.status === 'error') {
      setError(result.job.error_message
        ? `技能地图生成失败：${result.job.error_message}`
        : '技能地图生成失败')
    }
  }, [refreshCosts])

  const refreshSkillMap = useCallback(async (courseId: string, keepExistingMap = false) => {
    if (!keepExistingMap) setSkillMap(undefined)
    try {
      applySkillMapResponse(await getProjectSkillMap(courseId), keepExistingMap)
    } catch (reason) {
      if (!keepExistingMap) setSkillMap(null)
      setError(errorMessage(reason, '技能地图加载失败'))
    }
  }, [applySkillMapResponse])

  const refreshSkillStates = useCallback(async (courseId: string) => {
    try {
      const result = await listStudyStates(courseId)
      const byKey: Record<string, StudySkillState> = {}
      for (const state of result.states) byKey[state.skill_key] = state
      setSkillStates(byKey)
      setContinueSkill(result.continue)
    } catch {
      // The map renders fine unlit; practice results refresh this again.
    }
  }, [])

  const openCourse = (next: AIProject) => {
    setError(null)
    setCandidates(null)
    setExpandedSkillId('')
    setSkillStates({})
    setCourse(next)
    setMaterials(null)
    setCosts(null)
    setCostItemsShown(false)
    void refreshCosts(next.id)
    void refreshSessions(next.id)
    void refreshMaterials(next.id)
    void refreshSkillMap(next.id)
    void refreshSkillStates(next.id)
  }

  const closeCourse = () => {
    setCourse(null)
    setSessions(null)
    setCandidates(null)
    setMaterials(null)
    setCosts(null)
    setSkillMap(undefined)
    setSkillMapJob(null)
    setExpandedSkillId('')
    setSkillStates({})
    setContinueSkill(null)
    setPracticeSkill(null)
    setError(null)
  }

  const jobRunning = skillMapJob?.status === 'queued'
    || skillMapJob?.status === 'processing'

  useEffect(() => {
    if (!course || !jobRunning) return
    void refreshSkillMap(course.id, true)
    const timer = window.setInterval(() => {
      void refreshSkillMap(course.id, true)
    }, 2500)
    return () => window.clearInterval(timer)
  }, [course, jobRunning, refreshSkillMap])

  // Extraction runs in the background; poll while anything is still pending.
  const materialsPending = (materials ?? []).some(
    ({ status }) => status === 'queued' || status === 'processing',
  )
  useEffect(() => {
    if (!course || !materialsPending) return
    const timer = window.setInterval(() => { void refreshMaterials(course.id) }, 3000)
    return () => window.clearInterval(timer)
  }, [course, materialsPending, refreshMaterials])

  const uploadMaterials = async (files: FileList | File[]) => {
    if (!course) return
    const list = Array.from(files)
    if (list.length === 0) return
    setError(null)
    setUploading((count) => count + list.length)
    for (const file of list) {
      try {
        const source = await uploadKnowledgeFile(course.id, file)
        setMaterials((current) => [source, ...(current ?? []).filter(({ id }) => id !== source.id)])
      } catch (reason) {
        setError(errorMessage(reason, `上传 ${file.name} 失败`))
      } finally {
        setUploading((count) => count - 1)
      }
    }
    void refreshMaterials(course.id)
  }

  const retryMaterial = async (source: KnowledgeSource) => {
    if (!course) return
    try {
      const updated = await retryKnowledgeSource(course.id, source.id)
      setMaterials((current) => (current ?? []).map((item) => (item.id === updated.id ? updated : item)))
    } catch (reason) {
      setError(errorMessage(reason, '重试抽取失败'))
    }
  }

  const removeMaterial = async (source: KnowledgeSource) => {
    if (!course) return
    if (!window.confirm(`删除资料“${source.name}”？`)) return
    try {
      await deleteKnowledgeSource(course.id, source.id)
      setMaterials((current) => (current ?? []).filter(({ id }) => id !== source.id))
    } catch (reason) {
      setError(errorMessage(reason, '删除资料失败'))
    }
  }

  const onDropMaterials = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault()
    setDragOver(false)
    void uploadMaterials(event.dataTransfer.files)
  }

  const generateSkillMap = async () => {
    if (!course || skillMapBusy || jobRunning) return
    setSkillMapBusy(true)
    setError(null)
    try {
      applySkillMapResponse(
        await generateProjectSkillMap(course.id, crypto.randomUUID()),
        true,
      )
      setExpandedSkillId('')
    } catch (reason) {
      setError(errorMessage(reason, '技能地图生成失败'))
    } finally {
      setSkillMapBusy(false)
    }
  }

  const cancelSkillMap = async () => {
    if (!course) return
    try {
      applySkillMapResponse(await cancelProjectSkillMap(course.id), true)
    } catch (reason) {
      setError(errorMessage(reason, '取消生成失败'))
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

  // Progress across the current map (only skills that are on it count).
  const mapStates = (skillMap?.skills ?? [])
    .map((skill) => skillStates[skillKeyOf(skill.label)])
    .filter((state): state is StudySkillState => Boolean(state))
  const masteredCount = mapStates.filter(({ level }) => level === 'mastered').length
  const xpTotal = mapStates.reduce((sum, { xp_total }) => sum + xp_total, 0)
  const sessionCount = sessions?.length ?? 0
  const readyMaterials = (materials ?? []).filter(({ status }) => status === 'ready').length
  const hasInput = sessionCount > 0 || readyMaterials > 0
  const generating = jobRunning || skillMapBusy
  const showSteps = course && sessions !== null && skillMap !== undefined
    && (!hasInput || (!skillMap && !generating) || mapStates.length === 0)
  const stepState = (done: boolean, next: boolean) => (
    done ? ' is-done' : next ? ' is-next' : ''
  )

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
          <header className="dt-study__hero">
            <div>
              <span className="st-tag">Courses // {pad(courses.length)}</span>
              <h2>选一门课，开始今天的训练</h2>
              <p className="dt-study__lead">
                每门课汇集它的课堂会话，从转录里提炼出技能路线；练习把路线上的节点一个个点亮。
              </p>
            </div>
          </header>

          {coursesLoading && courses.length === 0 && (
            <p className="dt-study__empty">正在加载课程…</p>
          )}

          <div className="dt-study__grid">
            {courses.map((item, index) => (
              <button
                className="dt-study__card"
                key={item.id}
                onClick={() => openCourse(item)}
                style={{ '--hue': hueOf(item.id) } as CSSProperties}
                type="button"
              >
                <span className="dt-study__card-cover">
                  <span className="dt-study__card-code">COURSE {pad(index + 1)}</span>
                </span>
                <span className="dt-study__card-body">
                  <strong>{item.name || '未命名课程'}</strong>
                  {item.description && <span>{item.description}</span>}
                </span>
              </button>
            ))}
            {creating ? (
              <form
                className="dt-study__card dt-study__card--new dt-study__create st-panel"
                onSubmit={(event) => { void submitCreate(event) }}
              >
                <input
                  autoFocus
                  maxLength={160}
                  onChange={(event) => setDraftName(event.target.value)}
                  placeholder="课程名称，如 PSY2041"
                  value={draftName}
                />
                <button
                  className="st-btn st-btn--primary"
                  disabled={busy || !draftName.trim()}
                  type="submit"
                >
                  创建
                </button>
                <button
                  className="st-btn st-btn--quiet"
                  onClick={() => { setCreating(false); setDraftName('') }}
                  type="button"
                >
                  取消
                </button>
              </form>
            ) : (
              <button
                className="dt-study__card dt-study__card--new"
                onClick={() => setCreating(true)}
                type="button"
              >
                <Icon name="plus" size={22} />
                <strong>新建课程</strong>
              </button>
            )}
          </div>
        </>
      )}

      {course && (
        <>
          <header className="dt-study__header">
            <div>
              <button className="dt-study__back" onClick={closeCourse} type="button">
                <Icon name="close" size={12} />
                全部课程
              </button>
              <h2>{course.name || '未命名课程'}</h2>
              {course.description && <p className="dt-study__lead">{course.description}</p>}
            </div>
            <span className="dt-study__course-actions">
              <button
                aria-label="重命名课程"
                className="st-iconbtn"
                disabled={busy}
                onClick={() => { void renameCourse() }}
                title="重命名"
                type="button"
              >
                <Icon name="settings" size={16} />
              </button>
              <button
                aria-label="删除课程"
                className="st-iconbtn st-iconbtn--danger"
                disabled={busy}
                onClick={() => { void removeCourse() }}
                title="删除课程"
                type="button"
              >
                <Icon name="close" size={16} />
              </button>
            </span>
          </header>

          <div className="dt-study__course">
            <section className="dt-study__main">
              {showSteps && (
                <div className="dt-study__steps st-panel">
                  <div className={`dt-study__step${stepState(hasInput, !hasInput)}`}>
                    <b><i>1</i>放进课程材料</b>
                    <span>挂上课堂转录会话，或上传教材、课件、论文、图片，越全越好。</span>
                  </div>
                  <div className={`dt-study__step${stepState(Boolean(skillMap), hasInput && !skillMap)}`}>
                    <b><i>2</i>生成技能路线</b>
                    <span>AI 通读全部课堂和资料，提炼这门课要求掌握的能力。</span>
                  </div>
                  <div className={`dt-study__step${stepState(mapStates.length > 0, Boolean(skillMap) && mapStates.length === 0)}`}>
                    <b><i>3</i>开始练习</b>
                    <span>从第一个节点进入情境题，等级会随表现点亮。</span>
                  </div>
                </div>
              )}

              <div className="dt-study__map st-panel">
                <div className="dt-study__section-heading">
                  <span>
                    <Icon name="map" size={16} />
                    技能路线
                    {skillMap && <small>// {pad(skillMap.skills.length)} NODES</small>}
                  </span>
                  <button
                    className="st-btn"
                    disabled={generating || !hasInput}
                    onClick={() => { void generateSkillMap() }}
                    title={!hasInput
                      ? '先给课程添加会话或上传资料，再生成技能地图'
                      : 'AI 通读课堂转录和课程资料，按模型用量从余额扣费；完成后这里和费用卡会显示实际花费'}
                    type="button"
                  >
                    {generating ? '正在生成…' : skillMap ? '重新生成' : '生成技能地图'}
                  </button>
                </div>

                {generating && (
                  <div className="dt-study__progress">
                    <span className="dt-study__progress-head">
                      {skillMapJob && skillMapJob.chunk_count > 0
                        ? `READING TRANSCRIPTS ${skillMapJob.processed_chunks}/${skillMapJob.chunk_count}`
                        : 'QUEUED // 正在通读全部课堂转录'}
                      {jobRunning && (
                        <button
                          className="dt-study__progress-cancel"
                          onClick={() => { void cancelSkillMap() }}
                          title="停止这次生成；已有的地图不受影响"
                          type="button"
                        >
                          取消
                        </button>
                      )}
                    </span>
                    <div className="dt-study__progress-bar">
                      <i
                        className={skillMapJob && skillMapJob.chunk_count > 0 ? '' : 'is-indeterminate'}
                        style={skillMapJob && skillMapJob.chunk_count > 0
                          ? { '--pct': `${Math.max(4, Math.round(100 * skillMapJob.processed_chunks / skillMapJob.chunk_count))}%` } as CSSProperties
                          : undefined}
                      />
                    </div>
                  </div>
                )}

                {skillMap === undefined && <p className="dt-study__empty">正在加载技能地图…</p>}
                {skillMap === null && !generating && (
                  <p className="dt-study__empty">
                    还没有技能地图。生成后，这门课要求掌握的能力会按从基础到进阶排成一条路线，
                    练习会把它一个节点一个节点点亮。
                  </p>
                )}
                {skillMap && (
                  <>
                    <div className="dt-study__legend">
                      {LEVEL_ORDER.map((level) => (
                        <span key={level}>
                          <i style={{ '--c': `var(--st-${LEVEL_COLOR[level]})` } as CSSProperties} />
                          {LEVEL_SHORT[level]}
                        </span>
                      ))}
                    </div>
                    <div className="dt-study__skills">
                      {skillMap.skills.map((skill, index) => {
                        const expanded = expandedSkillId === skill.id
                        const state = skillStates[skillKeyOf(skill.label)]
                        const level = state?.level ?? 'unlit'
                        const stateTitle = state
                          ? `${LEVEL_TITLES[state.level] ?? state.level} · ${state.xp_total} XP`
                          : '还没练过'
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
                              <span
                                aria-label={stateTitle}
                                className={`dt-study__skill-state is-${level}`}
                                title={stateTitle}
                              >
                                {level === 'mastered'
                                  ? <Icon name="check" size={16} />
                                  : <span>{pad(index + 1)}</span>}
                              </span>
                              <span className="dt-study__skill-label">
                                {skill.label}
                                {skill.new && <em className="dt-study__skill-new">NEW</em>}
                              </span>
                              {skill.outcome && <small>{skill.outcome}</small>}
                            </button>
                            <button
                              className="st-btn dt-study__practice"
                              onClick={() => setPracticeSkill(skill.label)}
                              type="button"
                            >
                              <Icon name="play" size={12} />
                              练习
                            </button>
                            {expanded && (
                              <div className="dt-study__skill-detail">
                                {skill.summary && <p>{skill.summary}</p>}
                                {prerequisiteLabels.length > 0 && (
                                  <p className="dt-study__skill-prereq">
                                    REQUIRES // {prerequisiteLabels.join(' · ')}
                                  </p>
                                )}
                                {(skill.evidence ?? []).map((evidence, evidenceIndex) => {
                                  if (evidence.source_id) {
                                    return (
                                      <div
                                        className="dt-study__skill-evidence is-source"
                                        key={evidenceIndex}
                                        title="来自上传的课程资料"
                                      >
                                        <span>“{evidence.quote}”</span>
                                        <small>资料 · {evidence.source_title || '课程资料'}</small>
                                      </div>
                                    )
                                  }
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
                    </div>
                    <p className="dt-study__skill-meta">
                      {skillMapJob?.status === 'ready' && (skillMapJob.cost_usd ?? 0) > 0 && (
                        <>本次生成 {formatUsageUSD(skillMapJob.cost_usd ?? 0)} · </>
                      )}
                      基于 {skillMap.session_count} 场会话
                      {skillMap.source_count ? `、${skillMap.source_count} 份资料` : ''}生成
                      {skillMap.truncated && '（旧版地图曾截断转录；请重新生成以覆盖全部课堂）'}
                      {skillMap.generated_at && ` · ${formatDate(skillMap.generated_at)}`}
                    </p>
                  </>
                )}
              </div>
            </section>

            <aside className="dt-study__side">
              {skillMap && (
                <div className="dt-study__continue st-panel">
                  <Mascot mood={continueSkill ? 'happy' : 'proud'} size={64} />
                  <div className="dt-study__continue-text">
                    {continueSkill ? (
                      <>
                        <p>
                          下一站：<b>{continueSkill.skill_label}</b>
                          {' '}· {LEVEL_SHORT[continueSkill.level] ?? '入门'}
                        </p>
                        <button
                          className="st-btn st-btn--primary"
                          onClick={() => setPracticeSkill(continueSkill.skill_label)}
                          type="button"
                        >
                          <Icon name="play" size={12} />
                          <span>继续 — {continueSkill.skill_label}</span>
                        </button>
                      </>
                    ) : (
                      <p>这条路线上的能力都精通了。换一门课，或者重新生成看看有没有新内容。</p>
                    )}
                  </div>
                </div>
              )}

              {skillMap && (
                <div className="dt-study__stats st-panel">
                  <div className="dt-study__stat">
                    <b className="is-mastered">{masteredCount}/{skillMap.skills.length}</b>
                    <span>Mastered</span>
                  </div>
                  <div className="dt-study__stat">
                    <b>{mapStates.length}</b>
                    <span>Started</span>
                  </div>
                  <div className="dt-study__stat">
                    <b className="is-xp">{xpTotal}</b>
                    <span>Total XP</span>
                  </div>
                </div>
              )}

              <div className="dt-study__costs st-panel">
                <div className="dt-study__section-heading">
                  <span>
                    <Icon name="shield" size={15} />
                    费用
                    <small>// USD</small>
                  </span>
                  {costs && costs.items.length > 0 && (
                    <button
                      className="st-btn st-btn--quiet"
                      onClick={() => setCostItemsShown((value) => !value)}
                      type="button"
                    >
                      {costItemsShown ? '收起明细' : '明细'}
                    </button>
                  )}
                </div>
                {costs === null && <p className="dt-study__empty">正在统计…</p>}
                {costs && !costs.billing_enabled && (
                  <p className="dt-study__empty">这个部署没有开启计费，学习模式不扣费。</p>
                )}
                {costs?.billing_enabled && (
                  <>
                    <div className="dt-study__cost-total">
                      <b>{formatUsageUSD(costs.summary.total_usd)}</b>
                      <span>本课程累计 · {costs.summary.operations} 次调用</span>
                    </div>
                    <div className="dt-study__cost-split">
                      {(['skill_map', 'study_bank', 'study_grade'] as const).map((feature) => (
                        <span key={feature}>
                          <small>{FEATURE_LABELS[feature]}</small>
                          <b>{formatUsageUSD(costs.summary.by_feature[feature] ?? 0)}</b>
                        </span>
                      ))}
                    </div>
                    <p className="dt-study__cost-hint">
                      按模型用量实时扣费：生成技能地图按课程材料总量计，每道新题和每次批改各记一笔，从题库直接出的题不收费。
                    </p>
                    {costItemsShown && (
                      <ul className="dt-study__cost-items">
                        {costs.items.map((item) => (
                          <li key={item.id}>
                            <span>{featureLabel(item.feature, item.action)}</span>
                            <small>{item.model || '—'} · {formatDate(item.created_at)}</small>
                            <b>{item.refunded ? '已退' : formatUsageUSD(item.cost_usd)}</b>
                          </li>
                        ))}
                      </ul>
                    )}
                  </>
                )}
              </div>

              <div
                className={`dt-study__materials st-panel${dragOver ? ' is-dragover' : ''}`}
                onDragLeave={() => setDragOver(false)}
                onDragOver={(event) => { event.preventDefault(); setDragOver(true) }}
                onDrop={onDropMaterials}
              >
                <div className="dt-study__section-heading">
                  <span>
                    <Icon name="paperclip" size={15} />
                    课程资料
                    {materials && <small>// {pad(materials.length)}</small>}
                  </span>
                  <button
                    className="st-btn"
                    disabled={uploading > 0}
                    onClick={() => fileInputRef.current?.click()}
                    type="button"
                  >
                    {uploading > 0 ? `上传中 ${uploading}…` : '上传资料'}
                  </button>
                  <input
                    accept={MATERIAL_ACCEPT}
                    aria-label="上传课程资料"
                    hidden
                    multiple
                    onChange={(event) => {
                      if (event.target.files) void uploadMaterials(event.target.files)
                      event.target.value = ''
                    }}
                    ref={fileInputRef}
                    type="file"
                  />
                </div>
                <p className="dt-study__materials-hint">
                  教材、课件（PPTX）、论文（PDF）、讲义、图片截图都可以，拖进来也行。
                  抽取完成后会和课堂转录一起进入技能路线。
                </p>
                {materials === null && <p className="dt-study__empty">正在加载课程资料…</p>}
                <div className="dt-study__sources">
                  {materials?.map((source) => (
                    <div className={`dt-study__source is-${source.status}`} key={source.id}>
                      <span className="dt-study__source-main">
                        <span className="dt-study__source-name">{source.name}</span>
                        <small>
                          <i className="dt-study__source-status">{SOURCE_STATUS[source.status] ?? source.status}</i>
                          {source.size_bytes ? ` · ${formatBytes(source.size_bytes)}` : ''}
                          {source.status === 'ready' && source.chunk_count ? ` · ${source.chunk_count} 段` : ''}
                          {source.status === 'error' && source.error_message ? ` · ${source.error_message}` : ''}
                        </small>
                      </span>
                      {source.status === 'error' && (
                        <button
                          aria-label={`重试抽取 ${source.name}`}
                          className="st-iconbtn"
                          onClick={() => { void retryMaterial(source) }}
                          title="重试抽取"
                          type="button"
                        >
                          <Icon name="wave" size={14} />
                        </button>
                      )}
                      <button
                        aria-label={`删除资料 ${source.name}`}
                        className="st-iconbtn st-iconbtn--danger"
                        onClick={() => { void removeMaterial(source) }}
                        title="删除资料"
                        type="button"
                      >
                        <Icon name="close" size={14} />
                      </button>
                    </div>
                  ))}
                </div>
              </div>

              <div className="dt-study__sessions-panel st-panel">
                <div className="dt-study__section-heading">
                  <span>
                    <Icon name="history" size={15} />
                    课程会话
                    {sessions && <small>// {pad(sessions.length)}</small>}
                  </span>
                  <button
                    className="st-btn"
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
                  <p className="dt-study__empty">这门课程还没有会话。点「添加会话」把已有的云端会话挂进来。</p>
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
                        className="st-iconbtn"
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
              </div>
            </aside>
          </div>

          {practiceSkill && (
            <PracticePanel
              key={practiceSkill}
              initialLevel={skillStates[skillKeyOf(practiceSkill)]?.level}
              initialStreak={skillStates[skillKeyOf(practiceSkill)]?.clean_streak}
              onClose={() => {
                setPracticeSkill(null)
                void refreshSkillStates(course.id)
                void refreshCosts(course.id)
                announceBilling()
              }}
              projectId={course.id}
              skillLabel={practiceSkill}
            />
          )}
        </>
      )}
    </div>
  )
}

const LEVEL_COLOR: Record<string, string> = {
  learner: 'yellow',
  supervised: 'cyan',
  hazard: 'orange',
  independent: 'violet',
  mastered: 'green',
}
