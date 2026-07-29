<script setup lang="ts">
/**
 * DreamTrans Pro - Standalone Version
 *
 * This is a completely independent Pro UI that:
 * - Connects to Speechmatics via backend proxy (not directly)
 * - Uses cloud session storage (not IndexedDB)
 * - Has its own audio recording pipeline
 */
import { computed, nextTick, onMounted, onUnmounted, ref, watch, reactive } from 'vue'
import {
  History,
  Settings,
  MessageSquare,
  BookOpen,
  BarChart3,
  Mic,
  Square,
  Play,
  Pause,
  Download,
  FileText,
  Languages,
  X,
  Sparkles,
  Search,
  ArrowUpRight,
  Loader2,
  Save,
  Lock,
  LogIn,
  LogOut,
  User,
  Cloud,
  CloudOff,
  ChevronLeft,
  Shield,
  Wallet,
  RefreshCw,
} from 'lucide-vue-next'
import { useAuth } from './composables/useAuth'
import { useBalance } from './composables/useBalance'
import { useCloudSession } from './composables/useCloudSession'
import { useSystemSettings } from './composables/useSystemSettings'
import { getUserApiKey, setUserApiKey } from '../utils/userApiKey'
import { useSpeechmaticsProxy, type TranscriptSegment, type TranslationSegment } from './composables/useSpeechmaticsProxy'
import { ensureValidAccessToken } from './api/auth'
import { askRag, ingestRag } from '../api'
import type { RagAskResponse, RagConfig, UserBalance } from '../api'
import { lexIngest, lexSnapshot, lexReset, type LexSnapshot } from '../utils/lexicon'
// @ts-ignore - lamejs has no type definitions
import lamejs from 'lamejs'

// Types
type Panel = 'none' | 'chat' | 'lexicon' | 'metrics'
type SettingsTab = 'model' | 'prompts' | 'general' | 'account'
type TextState = 'streaming' | 'confirmed' | 'translated'

// Auth
const {
  user,
  isAuthenticated,
  isAdmin,
  loading: authLoading,
  login: authLogin,
  register: authRegister,
  logout,
  init: initAuth,
} = useAuth()

// User balance
const { balance, fetchBalance, formatBalance } = useBalance()

// Cloud session
const {
  currentSession,
  sessions,
  hasSession,
  loading: sessionLoading,
  error: cloudSessionError,
  createSession,
  loadSessions,
  loadSession,
  deleteSession,
  queueTranscript,
  flushTranscripts,
  endSession,
} = useCloudSession()

// History panel
const showHistory = ref(false)

// Load a historical session and restore its transcripts
async function loadHistoricalSession(sessionId: string) {
  try {
    await loadSession(sessionId)

    // Convert loaded transcripts to UI format
    if (currentSession.value?.transcripts) {
      // Clear current data
      lines.value = []
      translations.value = []
      resetCloudSegmentTracking()
      let lineId = 1

      // Group transcripts by speaker and time gaps
      const transcripts = currentSession.value.transcripts
      for (const t of transcripts) {
        const clientSegmentId = t.client_segment_id || createClientSegmentId()
        cloudSegments.set(clientSegmentId, {
          clientSegmentId,
          speaker: t.speaker || 'Speaker',
          text: t.text,
          translation: t.translation,
          startTime: t.start_time,
          endTime: t.end_time || t.start_time,
        })
        const lastLine = lines.value[lines.value.length - 1]
        const shouldNewLine = !lastLine ||
          lastLine.speaker !== (t.speaker || 'Speaker') ||
          (lastLine.segments.length > 0 && t.start_time - lastLine.segments[lastLine.segments.length - 1].endTime > 2.0)

        if (shouldNewLine) {
          lines.value.push({
            id: lineId++,
            speaker: t.speaker || 'Speaker',
            segments: [{
              text: t.text,
              startTime: t.start_time,
              endTime: t.end_time || t.start_time + 1
            }],
            partialText: ''
          })
        } else {
          lastLine.segments.push({
            text: t.text,
            startTime: t.start_time,
            endTime: t.end_time || t.start_time + 1
          })
        }

        // Add translation if exists
        if (t.translation) {
          const transId = `${t.speaker}-${t.start_time}`
          translations.value.push({
            id: transId,
            speaker: t.speaker || 'Speaker',
            startTime: t.start_time,
            content: t.translation,
            isPartial: false
          })
        }
      }

      // Update lexicon with loaded transcripts
      const sid = currentSession.value.id
      lexReset(sid)
      for (const t of transcripts) {
        lexIngest(sid, t.text)
      }
    }

    showHistory.value = false
  } catch (e) {
    console.error('Failed to load session:', e)
  }
}

// System settings
const { allowUserApiKey, loadSettings: loadSystemSettings } = useSystemSettings()

// Speechmatics proxy
const {
  state: smState,
  isConnected,
  isRecording,
  isReconnecting,
  reconnectAttempts,
  connect: smConnect,
  sendAudio,
  endSession: smEndSession,
  disconnect: smDisconnect,
  cancelReconnect: smCancelReconnect,
  onTranscript,
  onTranslation,
  onError,
  onBalanceUpdate,
  onReconnecting,
  onReconnected,
} = useSpeechmaticsProxy()

// Reactive state
const rightPanel = ref<Panel>('none')
const showSettings = ref(false)
const showLoginModal = ref(false)
const settingsTab = ref<SettingsTab>('model')
const streamRef = ref<HTMLElement | null>(null)
const isInitializing = ref(false)
const isStopping = ref(false)
const isPaused = ref(false)
const elapsedTime = ref(0)
const error = ref<string | null>(null)
const timerInterval = ref<number | null>(null)

watch(cloudSessionError, (message) => {
  if (message) error.value = message
})

// Transcript data
interface TranscriptLine {
  id: number
  speaker: string
  segments: Array<{ text: string; startTime: number; endTime: number }>
  partialText: string
}

interface TranslationLine {
  id: string
  speaker: string
  startTime: number
  content: string
  isPartial: boolean
}

const lines = ref<TranscriptLine[]>([])
const translations = ref<TranslationLine[]>([])
let nextLineId = 1
const PARAGRAPH_GAP = 2.0 // seconds

interface CloudSegment {
  clientSegmentId: string
  speaker: string
  text: string
  translation?: string
  startTime: number
  endTime: number
}

const cloudSegments = new Map<string, CloudSegment>()
const pendingCloudTranslations: TranslationSegment[] = []
const CLOUD_TRANSLATION_MATCH_TOLERANCE = 3
const MAX_PENDING_CLOUD_TRANSLATIONS = 100

function createClientSegmentId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  const bytes = new Uint8Array(16)
  if (typeof crypto !== 'undefined' && typeof crypto.getRandomValues === 'function') {
    crypto.getRandomValues(bytes)
  } else {
    for (let index = 0; index < bytes.length; index++) {
      bytes[index] = Math.floor(Math.random() * 256)
    }
  }
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = [...bytes].map((byte) => byte.toString(16).padStart(2, '0'))
  return [
    hex.slice(0, 4).join(''),
    hex.slice(4, 6).join(''),
    hex.slice(6, 8).join(''),
    hex.slice(8, 10).join(''),
    hex.slice(10).join(''),
  ].join('-')
}

function resetCloudSegmentTracking(): void {
  cloudSegments.clear()
  pendingCloudTranslations.length = 0
}

function cloudSegmentMatchScore(
  segment: Pick<CloudSegment, 'speaker' | 'startTime' | 'endTime'>,
  speaker: string,
  startTime: number,
  endTime: number,
): number {
  if (segment.speaker !== speaker) return Number.POSITIVE_INFINITY
  const candidateEnd = Math.max(startTime, endTime)
  const overlaps = startTime <= segment.endTime + 1 && candidateEnd >= segment.startTime - 1
  const startDelta = Math.abs(segment.startTime - startTime)
  if (!overlaps && startDelta > CLOUD_TRANSLATION_MATCH_TOLERANCE) {
    return Number.POSITIVE_INFINITY
  }
  return startDelta + Math.abs(segment.endTime - candidateEnd) * 0.1
}

function findCloudSegmentForTranscript(
  speaker: string,
  startTime: number,
  endTime: number,
): CloudSegment | undefined {
  return [...cloudSegments.values()].find((segment) =>
    segment.speaker === speaker &&
    Math.abs(segment.startTime - startTime) < 0.02 &&
    Math.abs(segment.endTime - endTime) < 0.25
  )
}

function findCloudSegmentForTranslation(
  speaker: string,
  startTime: number,
  endTime: number,
): CloudSegment | undefined {
  let best: CloudSegment | undefined
  let bestScore = Number.POSITIVE_INFINITY
  for (const segment of cloudSegments.values()) {
    const score = cloudSegmentMatchScore(segment, speaker, startTime, endTime)
    if (score < bestScore) {
      best = segment
      bestScore = score
    }
  }
  return best
}

function takePendingCloudTranslation(segment: CloudSegment): TranslationSegment | undefined {
  let bestIndex = -1
  let bestScore = Number.POSITIVE_INFINITY
  pendingCloudTranslations.forEach((translation, index) => {
    const score = cloudSegmentMatchScore(
      segment,
      translation.speaker,
      translation.startTime,
      translation.endTime,
    )
    if (score < bestScore) {
      bestIndex = index
      bestScore = score
    }
  })
  return bestIndex >= 0 ? pendingCloudTranslations.splice(bestIndex, 1)[0] : undefined
}

function queueCloudSegment(segment: CloudSegment): void {
  if (!hasSession.value) return
  queueTranscript({
    client_segment_id: segment.clientSegmentId,
    speaker: segment.speaker,
    text: segment.text,
    translation: segment.translation,
    start_time: segment.startTime,
    end_time: segment.endTime,
    status: segment.translation ? 'translated' : 'confirmed',
    is_partial: false,
  })
}

// Audio recording
let audioContext: AudioContext | null = null
let mediaStream: MediaStream | null = null
let audioWorklet: AudioWorkletNode | null = null
let mediaRecorder: MediaRecorder | null = null
const audioChunks: Blob[] = []
let audioMimeType = 'audio/webm;codecs=opus'

const DEFAULT_CHAT_PROMPT = '请用简洁的中文、分点列出要点。'

// Settings
const settings = reactive({
  apiKey: getUserApiKey(),
  apiBase: 'https://api.openai.com/v1',
  modelChat: 'gpt-4o-mini',
  promptChat: DEFAULT_CHAT_PROMPT,
  autoScroll: true,
})

const SETTINGS_KEY = 'dt_pro_settings'

function loadSettings() {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY)
    if (!raw) return
    const s = JSON.parse(raw)
    if (s.apiBase) settings.apiBase = s.apiBase
    if (s.modelChat) settings.modelChat = s.modelChat
    if (s.promptChat) settings.promptChat = s.promptChat
    if (s.autoScroll !== undefined) settings.autoScroll = s.autoScroll
  } catch { /* ignore */ }
}

function saveSettings() {
  const { apiKey: _apiKey, ...persistentSettings } = settings
  setUserApiKey(allowUserApiKey() ? settings.apiKey : '')
  localStorage.setItem(SETTINGS_KEY, JSON.stringify(persistentSettings))
  showSettings.value = false
}

// Login form
const loginForm = reactive({
  email: '',
  password: '',
  name: '',
  inviteCode: '',
  isRegister: false,
  loading: false,
  error: '',
})

async function handleLogin() {
  loginForm.loading = true
  loginForm.error = ''
  try {
    if (loginForm.isRegister) {
      await authRegister(
        loginForm.email,
        loginForm.password,
        loginForm.name,
        loginForm.inviteCode.trim() || undefined,
      )
    } else {
      await authLogin(loginForm.email, loginForm.password)
    }
    showLoginModal.value = false
    loginForm.email = ''
    loginForm.password = ''
    loginForm.name = ''
    loginForm.inviteCode = ''
  } catch (e) {
    const message = e instanceof Error ? e.message : 'Login failed'
    if (/self-registration is disabled/i.test(message)) {
      loginForm.error = '此服务器默认关闭自主注册，请联系管理员启用注册或创建账号。'
    } else if (/invalid registration invite code/i.test(message)) {
      loginForm.error = '邀请码缺失或无效，请向管理员获取有效邀请码。'
    } else {
      loginForm.error = message
    }
  } finally {
    loginForm.loading = false
  }
}

async function handleLogout() {
  if (isStopping.value) return
  if (isRecording.value || isInitializing.value || currentSession.value?.status === 'active') {
    if (!(await stopRecording())) return
  }
  await logout()
}

// Lexicon
const lexData = ref<LexSnapshot>({ words: [], bigrams: [], total: 0 })
const lexSessionId = computed(() => currentSession.value?.id || 'pro_session')

function updateLexicon() {
  lexData.value = lexSnapshot(lexSessionId.value)
}

// Top words and bigrams sorted by frequency
const topWords = computed(() =>
  [...lexData.value.words].sort((a, b) => b[1] - a[1]).slice(0, 30)
)
const topBigrams = computed(() =>
  [...lexData.value.bigrams].sort((a, b) => b[1] - a[1]).slice(0, 20)
)

// Listen for lexicon updates
onMounted(() => {
  window.addEventListener('dt-lex-updated', updateLexicon)
})
onUnmounted(() => {
  window.removeEventListener('dt-lex-updated', updateLexicon)
})

// Metrics tracking
const metricsData = reactive({
  transcriptCount: 0,
  translationCount: 0,
  wordsCount: 0,
})

// Update metrics based on lines and translations
watch([lines, translations], () => {
  metricsData.transcriptCount = lines.value.reduce(
    (acc, line) => acc + line.segments.length,
    0
  )
  metricsData.translationCount = translations.value.filter((t) => !t.isPartial).length
  metricsData.wordsCount = lexData.value.total
}, { deep: true })

// Summary
const summaryText = ref('')
const summaryLoading = ref(false)
const apiBaseUrl = (import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080') === '/'
  ? ''
  : (import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080')

async function fetchSummary(sessionId = currentSession.value?.id) {
  if (!sessionId || summaryLoading.value) return
  summaryLoading.value = true
  try {
    const token = await ensureValidAccessToken()
    const res = await fetch(`${apiBaseUrl}/api/rag/summary?session_id=${encodeURIComponent(sessionId)}`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    if (res.ok) {
      const data = await res.json()
      summaryText.value = data.summary || ''
    } else {
      throw new Error((await res.text()) || 'Failed to fetch summary')
    }
  } catch (e) {
    console.warn('Failed to fetch summary:', e)
  } finally {
    summaryLoading.value = false
  }
}

// Keep the summary fresh while its panel is visible. The watcher cleanup makes
// sure navigation and session switches cannot leave background timers behind.
watch(
  [rightPanel, () => currentSession.value?.id],
  ([panel, sessionId], _previous, onCleanup) => {
    if (panel !== 'metrics' || !sessionId) return
    void fetchSummary(sessionId)
    const id = window.setInterval(() => {
      void fetchSummary(sessionId)
    }, 15000)
    onCleanup(() => window.clearInterval(id))
  },
  { immediate: true },
)

// Bilingual mode
const bilingualEnabled = ref(false)

// Bilingual pairs - combine transcript with translation
const bilingualItems = computed(() => {
  return streamItems.value
    .filter((item) => item.translation && item.text)
    .map((item) => ({
      speaker: item.speaker,
      english: item.text,
      chinese: item.translation,
    }))
})

// Chat
interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
}
const chatMessages = ref<ChatMessage[]>([])
const chatInput = ref('')
const chatLoading = ref(false)

// Get current transcript as context for RAG
function getCurrentTranscriptContext(): string {
  const items = streamItems.value.slice(-20) // Last 20 items for context
  return items
    .map((item) => {
      const text = item.text || item.partial || ''
      const translation = item.translation || ''
      return `[${item.speaker}] ${text}${translation ? ` -> ${translation}` : ''}`
    })
    .join('\n')
}

async function sendChat() {
  const q = chatInput.value.trim()
  if (!q || chatLoading.value) return
  chatInput.value = ''
  chatMessages.value.push({ role: 'user', content: q })
  chatMessages.value.push({ role: 'assistant', content: '思考中...' })
  chatLoading.value = true
  scrollChatToBottom()
  try {
    const userApiAllowed = allowUserApiKey()
    const cfg: RagConfig = {
      api_key: userApiAllowed ? settings.apiKey || undefined : undefined,
      api_base: userApiAllowed ? settings.apiBase || undefined : undefined,
      model: settings.modelChat || undefined,
      prompt: settings.promptChat || undefined,
    }
    const sessionId = currentSession.value?.id || 'pro_session'

    // Include current context in the query
    const context = getCurrentTranscriptContext()
    const enrichedQuery = context
      ? `当前对话上下文:\n${context}\n\n用户问题: ${q}`
      : q

    const res: RagAskResponse = await askRag(sessionId, enrichedQuery, 5, cfg, 30000)
    chatMessages.value[chatMessages.value.length - 1] = { role: 'assistant', content: res.answer }
  } catch (e) {
    chatMessages.value[chatMessages.value.length - 1] = {
      role: 'assistant',
      content: `错误: ${e instanceof Error ? e.message : '未知错误'}`,
    }
  } finally {
    chatLoading.value = false
    scrollChatToBottom()
  }
}

// Chat panel scroll
const chatPanelRef = ref<HTMLElement | null>(null)
function scrollChatToBottom() {
  nextTick(() => {
    if (chatPanelRef.value) {
      const container = chatPanelRef.value.querySelector('.chat-messages')
      if (container) container.scrollTop = container.scrollHeight
    }
  })
}

// Stream items computation
const streamItems = computed(() =>
  lines.value
    .filter((line) => {
      // Filter out lines with no content (empty speaker or no text at all)
      const hasConfirmedText = line.segments.some((s) => s.text?.trim())
      const hasPartialText = !!line.partialText?.trim()
      return hasConfirmedText || hasPartialText
    })
    .map((line) => {
      const start = line.segments[0]?.startTime ?? 0

      // Collect translations for this line
      // Use wider time tolerance (3s) and also match by line time range
      const lineTranslations: Array<{ content: string; startTime: number; isPartial: boolean }> = []
      const lineStart = line.segments[0]?.startTime ?? 0
      const lineEnd = line.segments[line.segments.length - 1]?.endTime ?? lineStart + 5

      // First, try to match each segment with translations
      for (const seg of line.segments) {
        const match = translations.value.find(
          (t) => t.speaker === line.speaker && Math.abs(t.startTime - seg.startTime) < 3.0
        )
        if (match && !lineTranslations.some((lt) => lt.startTime === match.startTime)) {
          lineTranslations.push({
            content: match.content,
            startTime: match.startTime,
            isPartial: match.isPartial,
          })
        }
      }

      // Fallback: find any translation within the line's time range
      if (lineTranslations.length === 0) {
        const rangeMatches = translations.value.filter(
          (t) => t.speaker === line.speaker &&
                 t.startTime >= lineStart - 1.0 &&
                 t.startTime <= lineEnd + 2.0
        )
        for (const match of rangeMatches) {
          if (!lineTranslations.some((lt) => lt.startTime === match.startTime)) {
            lineTranslations.push({
              content: match.content,
              startTime: match.startTime,
              isPartial: match.isPartial,
            })
          }
        }
      }

      lineTranslations.sort((a, b) => a.startTime - b.startTime)
      const translationText = lineTranslations.map((t) => t.content).join(' ')
      const hasPartial = lineTranslations.some((t) => t.isPartial)
      const hasTranslation = lineTranslations.length > 0
      const confirmedText = line.segments.map((s) => s.text).join(' ')

      const state: TextState = line.partialText
        ? 'streaming'
        : hasTranslation && !hasPartial
          ? 'translated'
          : 'confirmed'

      return {
        id: line.id,
        speaker: line.speaker || 'Speaker',
        text: confirmedText,
        partial: line.partialText,
        start,
        translation: translationText,
        translationPartial: hasPartial,
        state,
      }
    })
)

// Elapsed time label
const elapsedLabel = computed(() => {
  const s = elapsedTime.value
  const m = Math.floor(s / 60)
  const ss = s % 60
  return `${String(m).padStart(2, '0')}:${String(ss).padStart(2, '0')}`
})

// Auto scroll - improved with requestAnimationFrame to prevent flickering
let scrollRAF: number | null = null
function scrollToBottom() {
  if (!settings.autoScroll) return
  if (scrollRAF) cancelAnimationFrame(scrollRAF)
  scrollRAF = requestAnimationFrame(() => {
    if (streamRef.value) {
      streamRef.value.scrollTop = streamRef.value.scrollHeight
    }
    scrollRAF = null
  })
}

// Scroll on new lines or content updates
watch(() => lines.value.length, scrollToBottom)
watch(
  () => lines.value[lines.value.length - 1]?.partialText,
  () => scrollToBottom(),
  { flush: 'post' }
)

// Handle transcript from proxy
function handleTranscript(seg: TranscriptSegment) {
  const { text, startTime, endTime, speaker, isPartial } = seg

  if (isPartial) {
    // Update partial text
    const lastLine = lines.value[lines.value.length - 1]
    if (lastLine && lastLine.speaker === speaker) {
      lastLine.partialText = text
    } else {
      lines.value.push({
        id: nextLineId++,
        speaker,
        segments: [],
        partialText: text,
      })
    }
  } else {
    const existingCloudSegment = findCloudSegmentForTranscript(speaker, startTime, endTime)
    if (existingCloudSegment) {
      // A provider retry can repeat a final event. Reuse the client ID so the
      // server updates the same row, and avoid duplicating it in the live UI.
      if (existingCloudSegment.text !== text) {
        existingCloudSegment.text = text
        const line = lines.value.find((item) =>
          item.speaker === speaker &&
          item.segments.some((itemSegment) => Math.abs(itemSegment.startTime - startTime) < 0.02)
        )
        const segmentIndex = line?.segments.findIndex(
          (itemSegment) => Math.abs(itemSegment.startTime - startTime) < 0.02
        ) ?? -1
        if (line && segmentIndex >= 0) {
          line.segments[segmentIndex] = { text, startTime, endTime }
        }
        queueCloudSegment(existingCloudSegment)
      }
      return
    }

    const cloudSegment: CloudSegment = {
      clientSegmentId: createClientSegmentId(),
      speaker,
      text,
      startTime,
      endTime,
    }
    const pendingTranslation = takePendingCloudTranslation(cloudSegment)
    if (pendingTranslation) cloudSegment.translation = pendingTranslation.text
    cloudSegments.set(cloudSegment.clientSegmentId, cloudSegment)

    // Confirmed segment
    const lastLine = lines.value[lines.value.length - 1]
    const shouldNewParagraph =
      !lastLine ||
      lastLine.speaker !== speaker ||
      (lastLine.segments.length > 0 &&
        startTime - (lastLine.segments[lastLine.segments.length - 1]?.endTime ?? 0) > PARAGRAPH_GAP)

    if (shouldNewParagraph) {
      lines.value.push({
        id: nextLineId++,
        speaker,
        segments: [{ text, startTime, endTime }],
        partialText: '',
      })
    } else {
      lastLine.segments.push({ text, startTime, endTime })
      lastLine.partialText = ''
    }

    // Queue for cloud save
    queueCloudSegment(cloudSegment)

    // Ingest into RAG for vector memory (background, non-blocking)
    const sessionId = currentSession.value?.id || 'pro_session'
    ingestRag(sessionId, speaker, text, startTime, endTime).catch((err) => {
      console.warn('RAG ingest error:', err)
    })

    // Ingest into lexicon for word frequency tracking
    lexIngest(sessionId, text)
  }
}

// Handle translation from proxy
function handleTranslation(seg: TranslationSegment) {
  const { text, startTime, endTime, speaker, isPartial } = seg
  const id = `${speaker}-${startTime}`
  const existing = translations.value.findIndex((t) => t.id === id)
  if (existing >= 0) {
    translations.value[existing] = { id, speaker, startTime, content: text, isPartial }
  } else {
    translations.value.push({ id, speaker, startTime, content: text, isPartial })
  }

  if (isPartial) return
  const cloudSegment = findCloudSegmentForTranslation(speaker, startTime, endTime)
  if (cloudSegment) {
    cloudSegment.translation = text
    queueCloudSegment(cloudSegment)
    return
  }

  const pendingIndex = pendingCloudTranslations.findIndex((translation) =>
    translation.speaker === speaker &&
    Math.abs(translation.startTime - startTime) < 0.02
  )
  if (pendingIndex >= 0) pendingCloudTranslations[pendingIndex] = seg
  else pendingCloudTranslations.push(seg)
  if (pendingCloudTranslations.length > MAX_PENDING_CLOUD_TRANSLATIONS) {
    pendingCloudTranslations.splice(
      0,
      pendingCloudTranslations.length - MAX_PENDING_CLOUD_TRANSLATIONS,
    )
  }
}

// Handle balance updates pushed from backend
function handleBalanceUpdate(payload: Record<string, unknown>) {
  const raw = (payload?.balance ?? null) as Partial<UserBalance> | null
  if (raw && typeof raw.dreampoints === 'number' && typeof raw.dreampoints_used === 'number') {
    balance.value = {
      user_id: raw.user_id || balance.value?.user_id || '',
      email: raw.email || balance.value?.email || '',
      name: raw.name || balance.value?.name || '',
      dreampoints: raw.dreampoints,
      dreampoints_used: raw.dreampoints_used,
    }
    return
  }
  if (isAuthenticated.value) {
    fetchBalance()
  }
}

function stopTimer() {
  if (timerInterval.value !== null) {
    window.clearInterval(timerInterval.value)
    timerInterval.value = null
  }
}

function stopMediaRecorder(): Promise<void> {
  const recorder = mediaRecorder
  if (!recorder || recorder.state === 'inactive') {
    mediaRecorder = null
    return Promise.resolve()
  }

  return new Promise((resolve) => {
    let settled = false
    const finish = () => {
      if (settled) return
      settled = true
      window.clearTimeout(timeoutId)
      recorder.removeEventListener('stop', finish)
      mediaRecorder = null
      resolve()
    }
    const timeoutId = window.setTimeout(finish, 3000)
    recorder.addEventListener('stop', finish, { once: true })
    try {
      recorder.requestData()
      recorder.stop()
    } catch {
      finish()
    }
  })
}

async function cleanupAudioCapture() {
  await stopMediaRecorder()
  if (audioWorklet) {
    audioWorklet.port.onmessage = null
    audioWorklet.disconnect()
    audioWorklet = null
  }
  if (audioContext) {
    try {
      await audioContext.close()
    } catch {
      // Already closed.
    }
    audioContext = null
  }
  if (mediaStream) {
    mediaStream.getTracks().forEach((track) => track.stop())
    mediaStream = null
  }
}

// Start recording
async function startRecording() {
  if (!isAuthenticated.value) {
    showLoginModal.value = true
    return
  }
  if (isInitializing.value || isStopping.value) return

  let createdSessionId: string | null = null
  try {
    error.value = null
    isInitializing.value = true
    stopTimer()
    audioChunks.length = 0
    lines.value = []
    translations.value = []
    resetCloudSegmentTracking()
    nextLineId = 1
    summaryText.value = ''
    elapsedTime.value = 0
    isPaused.value = false

    // Create cloud session
    const session = await createSession({
      title: `Session ${new Date().toLocaleString()}`,
      source_language: 'en',
      target_language: 'zh',
    })
    createdSessionId = session.id
    lexReset(session.id)

    // Connect to Speechmatics proxy
    onTranscript.value = handleTranscript
    onTranslation.value = handleTranslation
    onError.value = (err) => { error.value = err }
    onBalanceUpdate.value = handleBalanceUpdate

    // Reconnection handlers
    onReconnecting.value = (attempt, maxAttempts) => {
      error.value = `连接断开，正在重连... (${attempt}/${maxAttempts})`
    }
    onReconnected.value = () => {
      error.value = null
      console.log('[Pro] Speechmatics reconnected successfully')
    }

    await smConnect({
      language: 'en',
      enable_partials: true,
      diarization: 'speaker',
      operating_point: 'enhanced',
      max_delay: 2.0, // Reduce transcription latency (default is ~5s)
      translation_config: {
        target_languages: ['cmn'],
        enable_partials: true,
      },
    })

    // Setup audio recording
    mediaStream = await navigator.mediaDevices.getUserMedia({ audio: true })

    // Setup AudioWorklet for PCM data
    audioContext = new AudioContext({ sampleRate: 48000 })
    await audioContext.audioWorklet.addModule('/pcm-audio-worklet.min.js')

    const source = audioContext.createMediaStreamSource(mediaStream)
    audioWorklet = new AudioWorkletNode(audioContext, 'pcm-audio-processor')
    audioWorklet.port.onmessage = (e) => {
      if (e.data && !isPaused.value) {
        sendAudio(e.data)
      }
    }
    source.connect(audioWorklet)
    audioWorklet.connect(audioContext.destination)

    // Setup MediaRecorder for audio download
    audioMimeType = MediaRecorder.isTypeSupported('audio/mpeg') ? 'audio/mpeg' : 'audio/webm;codecs=opus'
    mediaRecorder = new MediaRecorder(mediaStream, { mimeType: audioMimeType })
    mediaRecorder.ondataavailable = (e) => {
      if (e.data.size > 0) audioChunks.push(e.data)
    }
    mediaRecorder.start(1000)

    // Start timer
    elapsedTime.value = 0
    timerInterval.value = window.setInterval(() => {
      if (!isPaused.value) elapsedTime.value++
    }, 1000)
  } catch (e) {
    stopTimer()
    smCancelReconnect()
    smDisconnect()
    await cleanupAudioCapture()

    let cleanupMessage = ''
    if (createdSessionId) {
      try {
        await deleteSession(createdSessionId)
      } catch (cleanupError) {
        cleanupMessage = `; failed to remove incomplete cloud session: ${
          cleanupError instanceof Error ? cleanupError.message : String(cleanupError)
        }`
      }
    }
    error.value = `${e instanceof Error ? e.message : 'Failed to start recording'}${cleanupMessage}`
  } finally {
    isInitializing.value = false
  }
}

// Stop recording
async function stopRecording(): Promise<boolean> {
  if (isStopping.value) return false
  isStopping.value = true
  stopTimer()
  const endingSessionId = currentSession.value?.id
  let stopError: unknown = null

  try {
    // Stop producing audio first, then tell Speechmatics no more frames are
    // coming and wait for its final transcript before closing the socket.
    await cleanupAudioCapture()
    try {
      await smEndSession()
    } catch (e) {
      stopError = e
    } finally {
      smDisconnect()
    }

    try {
      await flushTranscripts(endingSessionId)
      await endSession()
    } catch (e) {
      stopError ??= e
    }

    if (endingSessionId) {
      void fetchSummary(endingSessionId)
    }

    if (isAuthenticated.value) {
      try {
        await fetchBalance()
      } catch {
        // Balance refresh is non-critical after a completed recording.
      }
    }
  } catch (e) {
    stopError ??= e
    smDisconnect()
    await cleanupAudioCapture()
  } finally {
    isPaused.value = false
    elapsedTime.value = 0
    isStopping.value = false
  }

  if (stopError) {
    error.value = stopError instanceof Error ? stopError.message : 'Failed to stop recording cleanly'
    return false
  }
  return true
}

// Toggle pause
function togglePause() {
  isPaused.value = !isPaused.value
  if (mediaRecorder) {
    if (isPaused.value) mediaRecorder.pause()
    else mediaRecorder.resume()
  }
}

// Downloads
const isConverting = ref(false)

async function convertToMp3(audioBlob: Blob): Promise<Blob> {
  const audioContext = new AudioContext()
  const arrayBuffer = await audioBlob.arrayBuffer()
  const audioBuffer = await audioContext.decodeAudioData(arrayBuffer)

  const numChannels = audioBuffer.numberOfChannels
  const sampleRate = audioBuffer.sampleRate
  const mp3encoder = new lamejs.Mp3Encoder(numChannels, sampleRate, 128)

  const samples = audioBuffer.getChannelData(0)
  const sampleBlockSize = 1152
  const mp3Data: Int8Array[] = []

  // Convert Float32 samples to Int16
  const samples16 = new Int16Array(samples.length)
  for (let i = 0; i < samples.length; i++) {
    const s = Math.max(-1, Math.min(1, samples[i]))
    samples16[i] = s < 0 ? s * 0x8000 : s * 0x7FFF
  }

  // Encode in blocks
  for (let i = 0; i < samples16.length; i += sampleBlockSize) {
    const block = samples16.subarray(i, i + sampleBlockSize)
    const mp3buf = mp3encoder.encodeBuffer(block)
    if (mp3buf.length > 0) mp3Data.push(mp3buf)
  }

  // Flush remaining
  const end = mp3encoder.flush()
  if (end.length > 0) mp3Data.push(end)

  await audioContext.close()
  return new Blob(mp3Data, { type: 'audio/mpeg' })
}

async function downloadAudio() {
  if (audioChunks.length === 0) return

  isConverting.value = true
  try {
    const webmBlob = new Blob(audioChunks, { type: audioMimeType })
    const mp3Blob = await convertToMp3(webmBlob)

    const url = URL.createObjectURL(mp3Blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `recording-${Date.now()}.mp3`
    a.click()
    URL.revokeObjectURL(url)
  } catch (err) {
    console.error('MP3 conversion failed, falling back to webm:', err)
    // Fallback to webm if conversion fails
    const blob = new Blob(audioChunks, { type: audioMimeType })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `recording-${Date.now()}.webm`
    a.click()
    URL.revokeObjectURL(url)
  } finally {
    isConverting.value = false
  }
}

function downloadTranscript() {
  const text = lines.value
    .map((l) => `${l.speaker}: ${l.segments.map((s) => s.text).join(' ')}`)
    .join('\n\n')
  const blob = new Blob([text], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `transcript-${Date.now()}.txt`
  a.click()
  URL.revokeObjectURL(url)
}

function downloadTranslation() {
  const text = translations.value
    .filter((t) => !t.isPartial)
    .map((t) => `${t.speaker}: ${t.content}`)
    .join('\n\n')
  const blob = new Blob([text], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `translation-${Date.now()}.txt`
  a.click()
  URL.revokeObjectURL(url)
}

// Timestamp formatter
function formatTimestamp(seconds: number) {
  const m = Math.floor(seconds / 60)
  const s = Math.floor(seconds % 60)
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

// Navigation
function goToClassic() {
  window.location.href = '/'
}

function goToAdmin() {
  window.location.href = '/pro/admin'
}

// Lifecycle
onMounted(async () => {
  loadSettings()
  loadSystemSettings()
  await initAuth()
  // Fetch balance if authenticated
  if (isAuthenticated.value) {
    fetchBalance()
  }
})

// Refetch balance when auth state changes
watch(isAuthenticated, (auth) => {
  if (auth) {
    fetchBalance()
    return
  }
  resetCloudSegmentTracking()
  lines.value = []
  translations.value = []
  nextLineId = 1
  summaryText.value = ''
})

onUnmounted(() => {
  if (scrollRAF) cancelAnimationFrame(scrollRAF)
  stopTimer()
  if (isRecording.value || isInitializing.value || hasSession.value) {
    void stopRecording()
  } else {
    smCancelReconnect()
    smDisconnect()
    void cleanupAudioCapture()
  }
})
</script>

<template>
  <div class="pro-standalone">
    <!-- Background -->
    <div class="ambient ambient-1" />
    <div class="ambient ambient-2" />

    <!-- Header -->
    <header class="header">
      <div class="brand">
        <span class="dot" :class="isRecording ? 'recording' : ''" />
        <span class="brand-text">DreamTrans <span class="pro-badge">PRO</span></span>
      </div>

      <div class="header-actions">
        <!-- Reconnecting status -->
        <div v-if="isReconnecting" class="status-pill reconnecting">
          <RefreshCw :size="14" class="spin" />
          <span>重连中 ({{ reconnectAttempts }}/5)</span>
        </div>

        <!-- Recording status -->
        <div v-else-if="isRecording" class="status-pill recording">
          <span class="status-dot" />
          <span>{{ elapsedLabel }}</span>
        </div>

        <!-- Cloud status -->
        <div class="status-pill" :class="isAuthenticated ? 'cloud-on' : 'cloud-off'">
          <Cloud v-if="isAuthenticated" :size="14" />
          <CloudOff v-else :size="14" />
          <span>{{ isAuthenticated ? '云端' : '离线' }}</span>
        </div>

        <!-- Balance (Dreampoints) -->
        <div v-if="isAuthenticated && balance" class="status-pill balance-pill" :title="`已用: ${formatBalance(balance.dreampoints_used)}`">
          <Wallet :size="14" />
          <span>{{ formatBalance(balance.dreampoints) }}</span>
        </div>

        <!-- History -->
        <button v-if="isAuthenticated" class="icon-btn" @click="showHistory = true; loadSessions()" title="历史记录">
          <History :size="18" />
        </button>

        <!-- User menu -->
        <button v-if="isAuthenticated" class="icon-btn" @click="showSettings = true">
          <User :size="18" />
        </button>
        <button v-else class="login-btn" @click="showLoginModal = true">
          <LogIn :size="16" />
          <span>登录</span>
        </button>

        <button class="icon-btn" @click="showSettings = true">
          <Settings :size="18" />
        </button>

        <!-- Admin Panel -->
        <button v-if="isAdmin" class="icon-btn admin-btn" @click="goToAdmin" title="管理面板">
          <Shield :size="18" />
        </button>

        <button class="back-btn" @click="goToClassic">
          <ChevronLeft :size="16" />
          <span>经典版</span>
        </button>
      </div>
    </header>

    <!-- Main Content -->
    <main ref="streamRef" class="stream" :class="{ 'stream--offset': rightPanel !== 'none' }">
      <div class="stream-inner">
        <!-- Empty state -->
        <div v-if="streamItems.length === 0 && !isRecording" class="empty-state">
          <div class="empty-icon">
            <Mic :size="48" :stroke-width="1" />
          </div>
          <h3>准备开始转录</h3>
          <p v-if="!isAuthenticated">请先登录以使用云端功能</p>
          <p v-else>点击下方麦克风按钮开始实时语音转录和翻译</p>
        </div>

        <!-- Bilingual Mode -->
        <template v-if="bilingualEnabled && bilingualItems.length > 0">
          <article v-for="(item, idx) in bilingualItems" :key="`bi-${idx}`" class="line bilingual-line">
            <div class="meta">
              <span class="speaker" :class="item.speaker === 'Speaker A' ? 'speaker--a' : 'speaker--b'">
                {{ item.speaker }}
              </span>
            </div>
            <div class="card bilingual-card">
              <div class="bilingual-row">
                <div class="bilingual-en">{{ item.english }}</div>
                <div class="bilingual-zh">{{ item.chinese }}</div>
              </div>
            </div>
          </article>
        </template>

        <!-- Stream items (normal mode) -->
        <template v-else>
        <article
          v-for="item in streamItems"
          :key="item.id"
          class="line"
          :class="{
            'line--live': item.state === 'streaming',
            'line--translated': item.state === 'translated',
          }"
        >
          <div class="meta">
            <span class="speaker" :class="item.speaker === 'Speaker A' ? 'speaker--a' : 'speaker--b'">
              {{ item.speaker }}
            </span>
            <span class="timestamp">{{ formatTimestamp(item.start) }}</span>
            <span
              class="state-badge"
              :class="{
                'state--streaming': item.state === 'streaming',
                'state--confirmed': item.state === 'confirmed',
                'state--translated': item.state === 'translated',
              }"
            >
              {{ item.state === 'streaming' ? '识别中' : item.state === 'translated' ? '已翻译' : '待翻译' }}
            </span>
          </div>

          <div class="card" :class="{ 'card--live': item.state === 'streaming' }">
            <h3 class="text">
              {{ item.text }}
              <span v-if="item.partial" class="partial">{{ item.partial }}</span>
              <span v-if="item.state === 'streaming'" class="cursor" />
            </h3>

            <div class="translation" :class="{ 'translation--live': item.state === 'streaming' }">
              <p v-if="item.translation" class="translation-text">{{ item.translation }}</p>
              <div v-else class="translation-placeholder">
                <Loader2 v-if="item.state !== 'streaming'" :size="14" class="spin" />
                <span>{{ item.state === 'streaming' ? '等待确认...' : '翻译中...' }}</span>
              </div>
            </div>
          </div>
        </article>
        </template>

        <div class="stream-spacer" />
      </div>
    </main>

    <!-- Command Bar -->
    <div class="command" :class="{ 'command--offset': rightPanel !== 'none' }">
      <div class="command-inner">
        <!-- Left: Panels -->
        <div class="cmd-group">
          <button
            class="cmd-btn"
            :class="{ active: rightPanel === 'chat' }"
            @click="rightPanel = rightPanel === 'chat' ? 'none' : 'chat'"
            title="AI 助手"
          >
            <MessageSquare :size="20" />
          </button>
          <button
            class="cmd-btn"
            :class="{ active: rightPanel === 'lexicon' }"
            @click="rightPanel = rightPanel === 'lexicon' ? 'none' : 'lexicon'"
            title="词频统计"
          >
            <BookOpen :size="20" />
          </button>
          <button
            class="cmd-btn"
            :class="{ active: bilingualEnabled }"
            @click="bilingualEnabled = !bilingualEnabled"
            title="双语对照"
          >
            <Languages :size="20" />
          </button>
          <button
            class="cmd-btn"
            :class="{ active: rightPanel === 'metrics' }"
            @click="rightPanel = rightPanel === 'metrics' ? 'none' : 'metrics'"
            title="统计面板"
          >
            <BarChart3 :size="20" />
          </button>
        </div>

        <!-- Center: Record -->
        <div class="record-wrap">
          <button
            class="record-btn"
            :class="isRecording || currentSession?.status === 'active' ? 'on' : 'off'"
            :disabled="isInitializing || isStopping"
            @click="isRecording || currentSession?.status === 'active' ? stopRecording() : startRecording()"
          >
            <span v-if="isRecording" class="ping" />
            <Loader2 v-if="isInitializing || isStopping" :size="24" class="spin" />
            <Square v-else-if="isRecording || currentSession?.status === 'active'" :size="20" />
            <Mic v-else :size="28" />
          </button>
        </div>

        <!-- Right: Actions -->
        <div class="cmd-group">
          <button class="cmd-btn" :disabled="!isRecording" @click="togglePause" title="暂停/继续">
            <Pause v-if="!isPaused" :size="20" />
            <Play v-else :size="20" />
          </button>
          <button class="cmd-btn" @click="downloadAudio" :disabled="isConverting" :title="isConverting ? '转换中...' : '下载音频 (MP3)'">
            <Loader2 v-if="isConverting" :size="20" class="spin" />
            <Download v-else :size="20" />
          </button>
          <button class="cmd-btn" @click="downloadTranscript" title="下载原文">
            <FileText :size="20" />
          </button>
          <button class="cmd-btn" @click="downloadTranslation" title="下载译文">
            <Save :size="20" />
          </button>
        </div>
      </div>
    </div>

    <!-- Error Toast -->
    <div v-if="error" class="error-toast">
      <span>{{ error }}</span>
      <button @click="error = null"><X :size="16" /></button>
    </div>

    <!-- Chat Panel -->
    <aside v-if="rightPanel === 'chat'" ref="chatPanelRef" class="drawer">
      <header class="drawer-header">
        <div class="drawer-title">
          <Sparkles :size="16" />
          <span>AI 助手</span>
        </div>
        <button class="ghost-btn" @click="rightPanel = 'none'"><X :size="20" /></button>
      </header>

      <div class="drawer-body">
        <div class="chat-list chat-messages">
          <div v-for="(msg, i) in chatMessages" :key="i" class="chat-row" :class="msg.role">
            <div class="bubble" :class="msg.role">{{ msg.content }}</div>
          </div>
          <div v-if="chatMessages.length === 0" class="empty-chat">
            <Sparkles :size="32" :stroke-width="1" />
            <p>输入问题开始对话</p>
          </div>
        </div>

        <div class="chat-input">
          <input
            v-model="chatInput"
            type="text"
            placeholder="Ask a question..."
            :disabled="chatLoading"
            @keyup.enter="sendChat"
          />
          <button class="send-btn" :disabled="chatLoading" @click="sendChat">
            <ArrowUpRight :size="16" />
          </button>
        </div>
      </div>
    </aside>

    <!-- Lexicon Panel -->
    <aside v-if="rightPanel === 'lexicon'" class="drawer">
      <header class="drawer-header">
        <div class="drawer-title">
          <BookOpen :size="16" />
          <span>词频统计</span>
        </div>
        <button class="ghost-btn" @click="rightPanel = 'none'"><X :size="20" /></button>
      </header>

      <div class="drawer-body">
        <div class="lex-stats">
          <div class="lex-stat">
            <span class="lex-value">{{ lexData.total }}</span>
            <span class="lex-label">总词数</span>
          </div>
          <div class="lex-stat">
            <span class="lex-value">{{ lexData.words.length }}</span>
            <span class="lex-label">不同词</span>
          </div>
        </div>

        <div class="lex-section">
          <h4>高频单词</h4>
          <div class="lex-list">
            <div v-for="[word, count] in topWords" :key="word" class="lex-item">
              <span class="lex-word">{{ word }}</span>
              <span class="lex-count">{{ count }}</span>
            </div>
            <div v-if="topWords.length === 0" class="empty-lex">
              暂无数据，开始转录后将自动统计
            </div>
          </div>
        </div>

        <div class="lex-section">
          <h4>高频词组</h4>
          <div class="lex-list">
            <div v-for="[bigram, count] in topBigrams" :key="bigram" class="lex-item">
              <span class="lex-word">{{ bigram }}</span>
              <span class="lex-count">{{ count }}</span>
            </div>
            <div v-if="topBigrams.length === 0" class="empty-lex">
              暂无数据
            </div>
          </div>
        </div>
      </div>
    </aside>

    <!-- Metrics Panel -->
    <aside v-if="rightPanel === 'metrics'" class="drawer">
      <header class="drawer-header">
        <div class="drawer-title">
          <BarChart3 :size="16" />
          <span>统计面板</span>
        </div>
        <button class="ghost-btn" @click="rightPanel = 'none'"><X :size="20" /></button>
      </header>

      <div class="drawer-body">
        <div class="metrics-grid">
          <div class="metric-card">
            <div class="metric-icon recording">
              <Mic :size="18" />
            </div>
            <div class="metric-info">
              <span class="metric-value">{{ elapsedLabel }}</span>
              <span class="metric-label">录制时长</span>
            </div>
          </div>

          <div class="metric-card">
            <div class="metric-icon transcript">
              <FileText :size="18" />
            </div>
            <div class="metric-info">
              <span class="metric-value">{{ metricsData.transcriptCount }}</span>
              <span class="metric-label">转录段落</span>
            </div>
          </div>

          <div class="metric-card">
            <div class="metric-icon translation">
              <Languages :size="18" />
            </div>
            <div class="metric-info">
              <span class="metric-value">{{ metricsData.translationCount }}</span>
              <span class="metric-label">翻译段落</span>
            </div>
          </div>

          <div class="metric-card">
            <div class="metric-icon words">
              <BookOpen :size="18" />
            </div>
            <div class="metric-info">
              <span class="metric-value">{{ lexData.total }}</span>
              <span class="metric-label">总词数</span>
            </div>
          </div>
        </div>

        <div class="metrics-section">
          <h4>会话信息</h4>
          <div class="metrics-list">
            <div class="metrics-row">
              <span>会话 ID</span>
              <span class="mono">{{ currentSession?.id?.slice(0, 8) || '-' }}</span>
            </div>
            <div class="metrics-row">
              <span>说话人数</span>
              <span>{{ new Set(lines.map((l) => l.speaker)).size }}</span>
            </div>
            <div class="metrics-row">
              <span>云端同步</span>
              <span :class="hasSession ? 'status-on' : 'status-off'">
                {{ hasSession ? '已启用' : '未启用' }}
              </span>
            </div>
          </div>
        </div>

        <div v-if="topWords.length > 0" class="metrics-section">
          <h4>热门词汇</h4>
          <div class="hot-words">
            <span v-for="[word, count] in topWords.slice(0, 10)" :key="word" class="hot-word">
              {{ word }}
              <small>{{ count }}</small>
            </span>
          </div>
        </div>

        <div class="metrics-section">
          <div class="section-header-row">
            <h4>会话摘要</h4>
            <button class="refresh-btn" @click="fetchSummary()" :disabled="summaryLoading">
              <Loader2 v-if="summaryLoading" :size="14" class="spin" />
              <span v-else>刷新</span>
            </button>
          </div>
          <div v-if="summaryText" class="summary-box">
            <p v-for="(line, idx) in summaryText.split('\n').filter((l) => l.trim())" :key="idx">
              {{ line }}
            </p>
          </div>
          <div v-else class="empty-summary">
            <span>暂无摘要，转录更多内容后将自动生成</span>
          </div>
        </div>
      </div>
    </aside>

    <!-- Login Modal -->
    <div v-if="showLoginModal" class="overlay" @click.self="showLoginModal = false">
      <div class="modal login-modal">
        <div class="modal-header">
          <div class="modal-title">
            <LogIn :size="20" />
            <span>{{ loginForm.isRegister ? '注册' : '登录' }}</span>
          </div>
          <button class="ghost-btn" @click="showLoginModal = false"><X :size="24" /></button>
        </div>

        <div class="modal-body">
          <div v-if="loginForm.error" class="form-error">{{ loginForm.error }}</div>

          <label class="label">邮箱</label>
          <input v-model="loginForm.email" type="email" class="input" placeholder="email@example.com" />

          <label v-if="loginForm.isRegister" class="label mt-3">姓名</label>
          <input v-if="loginForm.isRegister" v-model="loginForm.name" type="text" class="input" placeholder="Your name" />

          <label v-if="loginForm.isRegister" class="label mt-3">邀请码（可选）</label>
          <input
            v-if="loginForm.isRegister"
            v-model="loginForm.inviteCode"
            type="text"
            class="input"
            placeholder="部分服务器要求邀请码"
            autocomplete="off"
          />
          <p v-if="loginForm.isRegister" class="registration-note">
            自主注册默认关闭；服务器启用注册后，也可能要求管理员提供的邀请码。
          </p>

          <label class="label mt-3">密码</label>
          <input v-model="loginForm.password" type="password" class="input" placeholder="********" />

          <button class="primary-btn mt-4" :disabled="loginForm.loading" @click="handleLogin">
            <Loader2 v-if="loginForm.loading" :size="16" class="spin" />
            <span>{{ loginForm.isRegister ? '注册' : '登录' }}</span>
          </button>

          <p class="switch-form">
            {{ loginForm.isRegister ? '已有账号？' : '没有账号？' }}
            <a href="#" @click.prevent="loginForm.isRegister = !loginForm.isRegister">
              {{ loginForm.isRegister ? '登录' : '注册' }}
            </a>
          </p>
        </div>
      </div>
    </div>

    <!-- Settings Modal -->
    <div v-if="showSettings" class="overlay" @click.self="showSettings = false">
      <div class="modal settings-modal">
        <div class="modal-header">
          <div class="modal-title">
            <Settings :size="20" />
            <span>设置</span>
          </div>
          <button class="ghost-btn" @click="showSettings = false"><X :size="24" /></button>
        </div>

        <div class="settings-body">
          <div class="settings-tabs">
            <button
              v-for="tab in ['model', 'prompts', 'general', 'account']"
              :key="tab"
              class="tab"
              :class="{ active: settingsTab === tab }"
              @click="settingsTab = tab as SettingsTab"
            >
              {{ tab === 'model' ? '模型' : tab === 'prompts' ? '提示词' : tab === 'general' ? '通用' : '账户' }}
            </button>
          </div>

          <div class="settings-content">
            <!-- Model Tab -->
            <div v-if="settingsTab === 'model'" class="settings-section">
              <template v-if="allowUserApiKey()">
                <label class="label">API Base</label>
                <input v-model="settings.apiBase" type="text" class="input" />

                <label class="label mt-3">API Key（仅在当前标签页内存储）</label>
                <input v-model="settings.apiKey" type="password" autocomplete="off" class="input" placeholder="sk-..." />
              </template>

              <template v-else>
                <div class="managed-notice">
                  <Lock :size="24" />
                  <p>API 由服务器管理</p>
                </div>
              </template>

              <label class="label mt-3">Chat 模型</label>
              <input v-model="settings.modelChat" type="text" class="input" />

              <div class="managed-notice mt-3">
                <Languages :size="24" />
                <p>实时翻译由 Speechmatics 提供；此处的 OpenAI 配置仅用于 AI 助手。</p>
              </div>
            </div>

            <!-- Prompts Tab -->
            <div v-else-if="settingsTab === 'prompts'" class="settings-section">
              <label class="label">Chat 提示词</label>
              <textarea v-model="settings.promptChat" rows="3" class="textarea" />
              <button class="reset-btn" @click="settings.promptChat = DEFAULT_CHAT_PROMPT">重置默认</button>
            </div>

            <!-- General Tab -->
            <div v-else-if="settingsTab === 'general'" class="settings-section">
              <label class="checkbox-label">
                <input type="checkbox" v-model="settings.autoScroll" />
                <span>自动滚动到底部</span>
              </label>
            </div>

            <!-- Account Tab -->
            <div v-else class="settings-section">
              <div v-if="isAuthenticated" class="account-info">
                <div class="avatar-large">
                  <User :size="32" />
                </div>
                <h4>{{ user?.name || user?.email }}</h4>
                <p>{{ user?.email }}</p>

                <!-- Dreampoints Balance -->
                <div v-if="balance" class="balance-card">
                  <div class="balance-header">
                    <Wallet :size="18" />
                    <span>Dreampoints 余额</span>
                  </div>
                  <div class="balance-amount">{{ formatBalance(balance.dreampoints) }}</div>
                  <div class="balance-used">已使用: {{ formatBalance(balance.dreampoints_used) }}</div>
                </div>

                <button class="logout-btn" @click="handleLogout">
                  <LogOut :size="16" />
                  <span>退出登录</span>
                </button>
              </div>
              <div v-else class="not-logged-in">
                <p>未登录</p>
                <button class="primary-btn" @click="showLoginModal = true; showSettings = false">
                  <LogIn :size="16" />
                  <span>登录</span>
                </button>
              </div>
            </div>
          </div>
        </div>

        <div class="modal-footer">
          <button class="primary-btn" @click="saveSettings">
            <Save :size="16" />
            <span>保存</span>
          </button>
        </div>
      </div>
    </div>

    <!-- History Modal -->
    <div v-if="showHistory" class="overlay" @click.self="showHistory = false">
      <div class="modal history-modal">
        <div class="modal-header">
          <div class="modal-title">
            <History :size="20" />
            <span>历史记录</span>
          </div>
          <button class="ghost-btn" @click="showHistory = false"><X :size="24" /></button>
        </div>

        <div class="modal-body">
          <div v-if="sessionLoading" class="loading-state">
            <Loader2 :size="24" class="spin" />
            <span>加载中...</span>
          </div>

          <div v-else-if="sessions.length === 0" class="empty-history">
            <History :size="48" :stroke-width="1" />
            <p>暂无历史记录</p>
          </div>

          <div v-else class="session-list">
            <div
              v-for="session in sessions"
              :key="session.id"
              class="session-item"
              :class="{ active: currentSession?.id === session.id }"
            >
              <div class="session-info" @click="loadHistoricalSession(session.id)">
                <h4>{{ session.title || '未命名会话' }}</h4>
                <div class="session-meta">
                  <span>{{ new Date(session.created_at).toLocaleString() }}</span>
                  <span class="session-status" :class="session.status">
                    {{ session.status === 'active' ? '进行中' : session.status === 'completed' ? '已完成' : '已归档' }}
                  </span>
                </div>
              </div>
              <button
                class="delete-session-btn"
                @click.stop="deleteSession(session.id)"
                title="删除"
              >
                <X :size="14" />
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Include the core styles */
@import './pro-standalone.css';
</style>
