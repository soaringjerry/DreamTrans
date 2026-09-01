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
import { PracticePanel, type PracticeMode } from './PracticePanel'
import { STUDY_BILLING_EVENT } from './StudyApp'
import { useStudySound } from './useStudySound'

/** Mirrors the server's skill_key normalization (lowercase, collapsed spaces). */
function skillKeyOf(label: string): string {
  return label.toLowerCase().split(/\s+/).filter(Boolean).join(' ')
}

const LEVEL_SHORT: Record<string, string> = {
  learner: '入门',
  supervised: '辅助',
  hazard: '挑战',
  independent: '独立',
  mastered: '精通',
}

const LEVEL_TITLES: Record<string, string> = {
  learner: '入门：中文框架 + 英文术语',
  supervised: '辅助：英文短句，中文提问',
  hazard: '挑战：英文短句',
  independent: '独立：英文考试语体',
  mastered: '精通：陌生情境也能完成',
}

const LEVEL_ORDER = ['learner', 'supervised', 'hazard', 'independent', 'mastered'] as const

/** Hint-free passes needed to leave each level (mirrors the server table). */
const LEVEL_UP_STREAK: Record<string, number> = {
  learner: 2,
  supervised: 3,
  hazard: 3,
  independent: 4,
}

function langTierForLevel(level: string): string {
  switch (level) {
    case 'hazard':
      return 'EN 短句 · 中文提问'
    case 'independent':
    case 'mastered':
      return 'EN 考试语体'
    default:
      return '中文框架 · EN 术语'
  }
}

interface StudyViewProps {
  /** Loads the session into the transcription workspace. */
  onOpenSession: (session: HistorySession) => void
}

interface PracticeTarget {
  skillLabel: string
  skillIndex: number
  mode: PracticeMode
  openLesson: boolean
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

function relativeDay(value?: string): string {
  if (!value) return ''
  const parsed = Date.parse(value)
  if (!parsed) return ''
  const days = Math.floor((Date.now() - parsed) / 86_400_000)
  if (days <= 0) return '今天'
  if (days === 1) return '昨天'
  return `${days} 天前`
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
  skill_map: '技能路线',
  study_bank: '出题',
  study_grade: '批改',
  study_lesson: '讲解',
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
 * 学习模式. Courses are the entry. A course opens on 今日行动: one mission
 * card (the next skill and why), one big button, and the route as a strip of
 * nodes. Everything administrative (sessions, materials, costs, regenerating
 * the route) lives on the 课程管理 tab.
 */
export function StudyView({ onOpenSession }: StudyViewProps) {
  const sound = useStudySound()
  const [courses, setCourses] = useState<AIProject[]>([])
  const [coursesLoading, setCoursesLoading] = useState(true)
  const [course, setCourse] = useState<AIProject | null>(null)
  const [tab, setTab] = useState<'home' | 'manage'>('home')
  const [sessions, setSessions] = useState<ProjectSession[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [draftName, setDraftName] = useState('')
  const [candidates, setCandidates] = useState<Session[] | null>(null)
  const [materials, setMaterials] = useState<KnowledgeSource[] | null>(null)
  const [uploading, setUploading] = useState(0)
  const [dragOver, setDragOver] = useState(false)
  const fileInputRef = useRef<HTMLInputElement | null>(null)
  const [costs, setCosts] = useState<StudyCosts | null>(null)
  const [costItemsShown, setCostItemsShown] = useState(false)
  // null = none stored yet; undefined = still loading.
  const [skillMap, setSkillMap] = useState<SkillMapDocument | null | undefined>(undefined)
  const [skillMapJob, setSkillMapJob] = useState<SkillMapJob | null>(null)
  const [skillMapBusy, setSkillMapBusy] = useState(false)
  const [expandedSkillId, setExpandedSkillId] = useState('')
  const [skillStates, setSkillStates] = useState<Record<string, StudySkillState>>({})
  const [continueSkill, setContinueSkill] = useState<StudyContinue | null>(null)
  const [practice, setPractice] = useState<PracticeTarget | null>(null)

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
        void refreshCosts(next.project_id)
        announceBilling()
      }
      return next
    })
    if (result.job?.status === 'error') {
      setError(result.job.error_message
        ? `技能路线生成失败：${result.job.error_message}`
        : '技能路线生成失败')
    }
  }, [refreshCosts])

  const refreshSkillMap = useCallback(async (courseId: string, keepExistingMap = false) => {
    if (!keepExistingMap) setSkillMap(undefined)
    try {
      applySkillMapResponse(await getProjectSkillMap(courseId), keepExistingMap)
    } catch (reason) {
      if (!keepExistingMap) setSkillMap(null)
      setError(errorMessage(reason, '技能路线加载失败'))
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
      // The route renders fine unlit; practice results refresh this again.
    }
  }, [])

  const openCourse = (next: AIProject) => {
    setError(null)
    setCandidates(null)
    setExpandedSkillId('')
    setSkillStates({})
    setCourse(next)
    setTab('home')
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
    setPractice(null)
    setError(null)
  }

  const jobRunning = skillMapJob?.status === 'queued' || skillMapJob?.status === 'processing'

  useEffect(() => {
    if (!course || !jobRunning) return
    void refreshSkillMap(course.id, true)
    const timer = window.setInterval(() => {
      void refreshSkillMap(course.id, true)
    }, 2500)
    return () => window.clearInterval(timer)
  }, [course, jobRunning, refreshSkillMap])

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
    sound.submit()
    try {
      applySkillMapResponse(
        await generateProjectSkillMap(course.id, crypto.randomUUID()),
        true,
      )
      setExpandedSkillId('')
    } catch (reason) {
      setError(errorMessage(reason, '技能路线生成失败'))
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

  const startPractice = (skillLabel: string, mode: PracticeMode, openLesson?: boolean) => {
    if (!skillMap) return
    const skillIndex = Math.max(0, skillMap.skills.findIndex(
      (skill) => skillKeyOf(skill.label) === skillKeyOf(skillLabel),
    ))
    const state = skillStates[skillKeyOf(skillLabel)]
    sound.submit()
    setPractice({
      skillLabel,
      skillIndex,
      mode,
      // A skill never practiced opens on its lesson; afterwards it is one tap away.
      openLesson: openLesson ?? !state,
    })
  }

  const courseNameById = new Map(courses.map(({ id, name }) => [id, name]))

  // Progress across the current route (only skills that are on it count).
  const mapStates = (skillMap?.skills ?? [])
    .map((skill) => skillStates[skillKeyOf(skill.label)])
    .filter((state): state is StudySkillState => Boolean(state))
  const masteredCount = mapStates.filter(({ level }) => level === 'mastered').length
  const xpTotal = mapStates.reduce((sum, { xp_total }) => sum + xp_total, 0)
  const sessionCount = sessions?.length ?? 0
  const readyMaterials = (materials ?? []).filter(({ status }) => status === 'ready').length
  const hasInput = sessionCount > 0 || readyMaterials > 0
  const generating = jobRunning || skillMapBusy
  const continueState = continueSkill ? skillStates[skillKeyOf(continueSkill.skill_label)] : undefined
  const continueLevel = continueState?.level ?? continueSkill?.level ?? 'learner'
  const continueIndex = continueSkill && skillMap
    ? skillMap.skills.findIndex((skill) => skillKeyOf(skill.label) === skillKeyOf(continueSkill.skill_label))
    : -1
  const passNeeded = LEVEL_UP_STREAK[continueLevel] ?? 0
  const passHave = Math.min(continueState?.clean_streak ?? 0, passNeeded)
  const recent = mapStates
    .filter((state) => state.last_grade && ['C', 'D', 'HD'].includes(state.last_grade))
    .sort((a, b) => Date.parse(b.updated_at ?? '') - Date.parse(a.updated_at ?? ''))
    .slice(0, 4)

  const renderProgress = () => (
    <div className="dt-study__progress">
      <span className="dt-study__progress-head">
        {skillMapJob && skillMapJob.chunk_count > 0
          ? `READING // 通读课堂 ${skillMapJob.processed_chunks}/${skillMapJob.chunk_count}`
          : 'QUEUED // 正在通读全部课堂转录和资料'}
        {jobRunning && (
          <button
            className="dt-study__progress-cancel"
            onClick={() => { void cancelSkillMap() }}
            title="停止这次生成；已有的路线不受影响"
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
  )

  const renderRoute = () => skillMap && (
    <div className="dt-route st-panel">
      <div className="dt-study__section-heading">
        <span className="st-label">
          <Icon name="map" size={14} />
          技能路线 // ROUTE
        </span>
        <span className="st-label st-label--mu">
          {masteredCount}/{skillMap.skills.length} 精通 · {xpTotal.toLocaleString('en-US')} XP · 进度只会往前走
        </span>
      </div>
      <div className="dt-route__strip">
        {skillMap.skills.map((skill, index) => {
          const state = skillStates[skillKeyOf(skill.label)]
          const level = state?.level ?? 'unlit'
          const isCurrent = index === continueIndex
          const nextState = skillMap.skills[index + 1]
            ? skillStates[skillKeyOf(skillMap.skills[index + 1].label)]
            : undefined
          return (
            <div className="dt-route__cell" key={skill.id}>
              <button
                className={`dt-route__node is-${level}${isCurrent ? ' is-cur' : ''}`}
                onClick={() => startPractice(skill.label, 'graded')}
                title={state ? `${LEVEL_TITLES[state.level] ?? state.level} · ${state.xp_total} XP` : '还没练过，从讲解卡开始'}
                type="button"
              >
                <span className="dt-route__hex">
                  <span>{level === 'mastered' ? <Icon name="check" size={16} /> : pad(index + 1)}</span>
                </span>
                <small>{skill.label}</small>
                <em>{isCurrent ? '▶ 下一站' : state ? LEVEL_SHORT[state.level] : '未开始'}</em>
              </button>
              {index < skillMap.skills.length - 1 && (
                <span className={`dt-route__link${nextState ? ' is-on' : ''}`} />
              )}
            </div>
          )
        })}
      </div>
      <div className="dt-route__legend">
        {LEVEL_ORDER.map((level) => (
          <span key={level}><i className={`is-${level}`} />{LEVEL_SHORT[level]}</span>
        ))}
        <span><i className="is-unlit" />未开始</span>
      </div>
    </div>
  )

  const renderHome = () => (
    <div className="dt-study__home">
      <section className="dt-study__main">
        {sessions !== null && !hasInput && (
          <div className="dt-study__mission st-panel is-empty">
            <span className="st-label st-label--or">第一步 // SETUP</span>
            <h3>这门课还是空的</h3>
            <p className="dt-study__mission-why">
              把课堂转录会话挂进来，或上传教材、课件、论文、截图。AI 通读它们之后，会提炼出这门课要求掌握的能力，排成一条路线。
            </p>
            <div className="dt-study__mission-go">
              <button className="st-btn st-btn--primary" onClick={() => setTab('manage')} type="button">
                去添加会话和资料
              </button>
            </div>
          </div>
        )}

        {hasInput && skillMap === null && (
          <div className="dt-study__mission st-panel is-empty">
            <span className="st-label st-label--or">第二步 // ROUTE</span>
            <h3>生成技能路线</h3>
            <p className="dt-study__mission-why">
              AI 通读 {sessionCount} 场课堂{readyMaterials > 0 ? `和 ${readyMaterials} 份资料` : ''}，提炼这门课要求掌握的能力，按从基础到进阶排成路线。按模型用量从余额扣费，完成后显示实际花费。
            </p>
            {generating ? renderProgress() : (
              <div className="dt-study__mission-go">
                <button className="st-btn st-btn--primary" onClick={() => { void generateSkillMap() }} type="button">
                  <Icon name="sparkles" size={14} />
                  生成技能路线
                </button>
              </div>
            )}
          </div>
        )}

        {skillMap === undefined && <p className="dt-study__empty">正在加载技能路线…</p>}

        {skillMap && continueSkill && (
          <div className="dt-study__mission st-panel">
            <div className="dt-study__mission-grid" aria-hidden="true" />
            <span className="dt-study__mission-wm" aria-hidden="true">{pad(continueIndex + 1)}</span>
            <span className="st-label st-label--or">今日行动 // RECOMMENDED</span>
            <div className="dt-study__mission-code">
              <b>OP-{pad(continueIndex + 1)}</b>
              <span className="st-label st-label--mu">
                {LEVEL_SHORT[continueLevel] ?? '入门'} · {langTierForLevel(continueLevel)}
              </span>
            </div>
            <h3>{continueSkill.skill_label}</h3>
            <p className="dt-study__mission-why">{continueSkill.reason ?? '路线上的下一站。'}</p>
            <div className="dt-study__mission-chips">
              {passNeeded > 0 && (
                <span className="st-chip">
                  过关
                  <span className="dt-study__pass">
                    {Array.from({ length: passNeeded }, (_, index) => (
                      <i className={index < passHave ? 'is-on' : ''} key={index} />
                    ))}
                  </span>
                  {passNeeded - passHave <= 1 ? '还差 1 题就升级' : `还差 ${passNeeded - passHave} 题`}
                </span>
              )}
              <span className="st-chip st-chip--cy">做到 C 就算过 · D 和 HD 是加分</span>
              {continueState?.last_error_pattern && (
                <span className="st-chip st-chip--or">上次卡在 {continueState.last_error_pattern}</span>
              )}
            </div>
            <div className="dt-study__mission-go">
              <button
                className="st-btn st-btn--orange st-btn--big"
                onClick={() => startPractice(continueSkill.skill_label, 'graded')}
                type="button"
              >
                <Icon name="play" size={14} />
                开始行动
              </button>
              <button
                className="st-btn"
                onClick={() => startPractice(continueSkill.skill_label, 'graded', true)}
                type="button"
              >
                先看讲解
              </button>
              <button
                className="st-btn st-btn--quiet"
                onClick={() => startPractice(continueSkill.skill_label, 'free', false)}
                title="不计等级、不计 XP，只有题和解析"
                type="button"
              >
                随便练练
              </button>
              <small className="st-label st-label--mu">无提示过关 +30 · 首答 +50 · 改对 +40</small>
            </div>
            <span className="dt-study__mission-bar" aria-hidden="true" />
          </div>
        )}

        {skillMap && !continueSkill && (
          <div className="dt-study__mission st-panel is-empty">
            <span className="st-label st-label--or">ALL CLEAR</span>
            <h3>这条路线上的能力都精通了</h3>
            <p className="dt-study__mission-why">换一门课，或者在课程管理里加入新的课堂再重新生成路线。</p>
          </div>
        )}

        {renderRoute()}
      </section>

      <aside className="dt-study__side">
        <div className="st-panel dt-study__tutor">
          <span className="st-label">终端 // TUTOR-01</span>
          <div className="dt-study__tutor-row">
            <Mascot mood={continueSkill ? 'happy' : skillMap ? 'proud' : 'idle'} size={56} />
            <p>
              {!hasInput && sessions !== null
                ? '先把课堂放进来，我来把它排成路线。'
                : !skillMap
                  ? '材料齐了。路线生成好之后，第一题会从一句中文开始。'
                  : continueSkill
                    ? recent.length > 0
                      ? '欢迎回来。上次收工时说好的下一站，题我已经准备好了。'
                      : '第一站从讲解卡开始，看完再做题。不会就直接看解析，不算错。'
                    : '都精通了。想保持手感的话，随便练练随时在。'}
            </p>
          </div>
        </div>

        {skillMap && (
          <div className="st-panel dt-study__recent">
            <span className="st-label">最近搞懂 // CLEARED</span>
            {recent.length === 0 ? (
              <p className="dt-study__empty">还没有。第一道过关的题会记在这里。</p>
            ) : (
              <ul>
                {recent.map((state) => (
                  <li key={state.skill_key}>
                    <span>{state.skill_label}</span>
                    <b>{LEVEL_SHORT[state.level]} · {relativeDay(state.updated_at)}</b>
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}

        <div className="st-panel dt-study__promise">
          <span className="st-label st-label--mu">这里的规则</span>
          <ul>
            <li>每题答完都有参考回答和解析，看解析不扣费。</li>
            <li>做错可以再试一次，改对了 XP 全额，还有奖励。</li>
            <li>不会就直接看解析，不算错。</li>
            <li>等级不降，XP 不减。</li>
          </ul>
        </div>
      </aside>
    </div>
  )

  const renderManage = () => (
    <div className="dt-study__manage">
      <div className="st-panel dt-study__map">
        <div className="dt-study__section-heading">
          <span className="st-label">
            <Icon name="map" size={14} />
            技能路线
            {skillMap && <small>// {pad(skillMap.skills.length)} NODES</small>}
          </span>
          <button
            className="st-btn"
            disabled={generating || !hasInput}
            onClick={() => { void generateSkillMap() }}
            title={!hasInput
              ? '先给课程添加会话或上传资料，再生成技能路线'
              : 'AI 通读课堂转录和课程资料，按模型用量从余额扣费；完成后这里和费用卡会显示实际花费'}
            type="button"
          >
            {generating ? '正在生成…' : skillMap ? '重新生成' : '生成技能路线'}
          </button>
        </div>
        {generating && renderProgress()}
        {skillMap === null && !generating && (
          <p className="dt-study__empty">还没有技能路线。加好会话和资料后生成，练习会把路线一个节点一个节点点亮。</p>
        )}
        {skillMap && (
          <>
            <div className="dt-study__skills">
              {skillMap.skills.map((skill, index) => {
                const expanded = expandedSkillId === skill.id
                const state = skillStates[skillKeyOf(skill.label)]
                const prerequisiteLabels = (skill.prerequisites ?? [])
                  .map((id) => skillMap.skills.find((item) => item.id === id)?.label)
                  .filter((label): label is string => Boolean(label))
                return (
                  <div className={`dt-study__skill${expanded ? ' is-expanded' : ''}`} key={skill.id}>
                    <button
                      className="dt-study__skill-head"
                      onClick={() => setExpandedSkillId(expanded ? '' : skill.id)}
                      type="button"
                    >
                      <span className={`dt-study__skill-state is-${state?.level ?? 'unlit'}`}>{pad(index + 1)}</span>
                      <span className="dt-study__skill-label">
                        {skill.label}
                        {skill.new && <em className="dt-study__skill-new">NEW</em>}
                      </span>
                      {skill.outcome && <small>{skill.outcome}</small>}
                    </button>
                    <button
                      className="st-btn st-btn--quiet dt-study__practice"
                      onClick={() => startPractice(skill.label, 'graded')}
                      type="button"
                    >
                      <Icon name="play" size={12} />
                      练习
                    </button>
                    {expanded && (
                      <div className="dt-study__skill-detail">
                        {skill.summary && <p>{skill.summary}</p>}
                        {prerequisiteLabels.length > 0 && (
                          <p className="dt-study__skill-prereq">REQUIRES // {prerequisiteLabels.join(' · ')}</p>
                        )}
                        {(skill.evidence ?? []).map((evidence, evidenceIndex) => {
                          if (evidence.source_id) {
                            return (
                              <div className="dt-study__skill-evidence is-source" key={evidenceIndex} title="来自上传的课程资料">
                                <span>“{evidence.quote}”</span>
                                <small>资料 · {evidence.source_title || '课程资料'}</small>
                              </div>
                            )
                          }
                          const evidenceSession = sessions?.find(({ id }) => id === evidence.session_id)
                          return (
                            <button
                              className="dt-study__skill-evidence"
                              disabled={!evidenceSession}
                              key={evidenceIndex}
                              onClick={() => { if (evidenceSession) onOpenSession(toHistorySession(evidenceSession)) }}
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
              {skillMap.truncated && '（旧版路线曾截断转录；请重新生成以覆盖全部课堂）'}
              {skillMap.generated_at && ` · ${formatDate(skillMap.generated_at)}`}
            </p>
          </>
        )}
      </div>

      <div className="dt-study__manage-side">
        <div className="st-panel dt-study__sessions-panel">
          <div className="dt-study__section-heading">
            <span className="st-label">
              <Icon name="history" size={14} />
              课程会话
              {sessions && <small>// {pad(sessions.length)}</small>}
            </span>
            <button
              className="st-btn"
              disabled={busy}
              onClick={() => { if (candidates) setCandidates(null); else void openPicker() }}
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
                      {candidate.project_id && ` · 现属于 ${courseNameById.get(candidate.project_id) ?? '其他课程'}`}
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
                  <Icon name="history" size={14} />
                  <span className="dt-study__row-title">{session.title || '未命名会话'}</span>
                  <small>{formatDate(session.started_at)} · {formatDuration(session.duration_seconds)}</small>
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

        <div
          className={`st-panel dt-study__materials${dragOver ? ' is-dragover' : ''}`}
          onDragLeave={() => setDragOver(false)}
          onDragOver={(event) => { event.preventDefault(); setDragOver(true) }}
          onDrop={onDropMaterials}
        >
          <div className="dt-study__section-heading">
            <span className="st-label">
              <Icon name="paperclip" size={14} />
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
            教材、课件（PPTX）、论文（PDF）、讲义、图片截图都可以，拖进来也行。抽取完成后会和课堂转录一起进入技能路线。
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

        <div className="st-panel dt-study__costs">
          <div className="dt-study__section-heading">
            <span className="st-label">
              <Icon name="shield" size={14} />
              费用
              <small>// USD</small>
            </span>
            {costs && costs.items.length > 0 && (
              <button className="st-btn st-btn--quiet" onClick={() => setCostItemsShown((value) => !value)} type="button">
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
                {(['skill_map', 'study_lesson', 'study_bank', 'study_grade'] as const).map((feature) => (
                  <span key={feature}>
                    <small>{FEATURE_LABELS[feature]}</small>
                    <b>{formatUsageUSD(costs.summary.by_feature[feature] ?? 0)}</b>
                  </span>
                ))}
              </div>
              <p className="dt-study__cost-hint">
                按模型用量实时扣费：路线按课程材料总量计，每项能力的讲解卡一次，每道新题和每次批改各记一笔。题库里已有的题、看解析、看讲解不收费。
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

        <div className="st-panel dt-study__settings">
          <div className="dt-study__section-heading">
            <span className="st-label st-label--mu"><Icon name="settings" size={14} />课程设置</span>
          </div>
          <div className="dt-study__settings-actions">
            <button className="st-btn st-btn--quiet" disabled={busy} onClick={() => { void renameCourse() }} type="button">
              重命名
            </button>
            <button className="st-btn st-btn--quiet is-danger" disabled={busy} onClick={() => { void removeCourse() }} type="button">
              删除课程
            </button>
          </div>
        </div>
      </div>
    </div>
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
            <span className="st-label st-label--or">COURSES // {pad(courses.length)}</span>
            <h2>选一门课，开始今天的行动</h2>
            <p className="dt-study__lead">
              每门课从课堂转录和资料里提炼出一条技能路线。先看讲解，再做题；每题答完都有解析，做错就再试，直到点亮节点。
            </p>
          </header>

          {coursesLoading && courses.length === 0 && (
            <p className="dt-study__empty">正在加载课程…</p>
          )}

          <div className="dt-study__grid">
            {courses.map((item, index) => (
              <button
                className="dt-study__card st-panel"
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
                <button className="st-btn st-btn--primary" disabled={busy || !draftName.trim()} type="submit">
                  创建
                </button>
                <button className="st-btn st-btn--quiet" onClick={() => { setCreating(false); setDraftName('') }} type="button">
                  取消
                </button>
              </form>
            ) : (
              <button className="dt-study__card dt-study__card--new st-panel" onClick={() => setCreating(true)} type="button">
                <Icon name="plus" size={22} />
                <strong>新建课程</strong>
              </button>
            )}
          </div>
        </>
      )}

      {course && (
        <>
          <header className="dt-study__course-head">
            <button className="dt-study__back" onClick={closeCourse} type="button">
              <Icon name="arrow-down" size={12} />
              全部课程
            </button>
            <h2>{course.name || '未命名课程'}</h2>
            <nav aria-label="课程页面" className="dt-study__tabs">
              <button className={tab === 'home' ? 'is-on' : ''} onClick={() => setTab('home')} type="button">
                今日行动
              </button>
              <button className={tab === 'manage' ? 'is-on' : ''} onClick={() => setTab('manage')} type="button">
                课程管理
              </button>
            </nav>
          </header>

          {tab === 'home' ? renderHome() : renderManage()}

          {practice && (
            <PracticePanel
              initialLevel={skillStates[skillKeyOf(practice.skillLabel)]?.level}
              initialStreak={skillStates[skillKeyOf(practice.skillLabel)]?.clean_streak}
              key={`${practice.skillLabel}-${practice.mode}`}
              mode={practice.mode}
              onClose={() => {
                setPractice(null)
                void refreshSkillStates(course.id)
                void refreshCosts(course.id)
                announceBilling()
              }}
              openLesson={practice.openLesson}
              projectId={course.id}
              skillIndex={practice.skillIndex}
              skillLabel={practice.skillLabel}
            />
          )}
        </>
      )}
    </div>
  )
}
