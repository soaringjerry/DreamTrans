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
} from 'lucide-vue-next'
import { useAuth } from './composables/useAuth'
import { useCloudSession } from './composables/useCloudSession'
import { useSystemSettings } from './composables/useSystemSettings'
import { useSpeechmaticsProxy, type TranscriptSegment, type TranslationSegment } from './composables/useSpeechmaticsProxy'
import { askRag } from '../api'
import type { RagAskResponse, RagConfig } from '../api'

// Types
type Panel = 'none' | 'chat' | 'lexicon' | 'metrics'
type SettingsTab = 'model' | 'prompts' | 'general' | 'account'
type TextState = 'streaming' | 'confirmed' | 'translated'

// Auth
const { user, isAuthenticated, isAdmin, loading: authLoading, logout, init: initAuth } = useAuth()

// Cloud session
const {
  currentSession,
  hasSession,
  loading: sessionLoading,
  createSession,
  queueTranscript,
  flushTranscripts,
  endSession,
} = useCloudSession()

// System settings
const { allowUserApiKey, loadSettings: loadSystemSettings } = useSystemSettings()

// Speechmatics proxy
const {
  state: smState,
  isConnected,
  isRecording,
  connect: smConnect,
  sendAudio,
  endSession: smEndSession,
  disconnect: smDisconnect,
  onTranscript,
  onTranslation,
  onError,
} = useSpeechmaticsProxy()

// Reactive state
const rightPanel = ref<Panel>('none')
const showSettings = ref(false)
const showLoginModal = ref(false)
const settingsTab = ref<SettingsTab>('model')
const streamRef = ref<HTMLElement | null>(null)
const isInitializing = ref(false)
const isPaused = ref(false)
const elapsedTime = ref(0)
const error = ref<string | null>(null)
const timerInterval = ref<number | null>(null)

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

// Audio recording
let audioContext: AudioContext | null = null
let mediaStream: MediaStream | null = null
let audioWorklet: AudioWorkletNode | null = null
let mediaRecorder: MediaRecorder | null = null
const audioChunks: Blob[] = []

// Default prompts (same as Classic version)
const DEFAULT_TRANSLATE_PROMPT = (
  '您是一位专业的同声传译翻译，你正在把英文的口语内容翻译成中文易于理解的话，' +
  '请使用 <context> 来帮助你理解上下文和当前场景并作出适当的纠错和润色。' +
  '请仅翻译 <text>...</text> 里的文本变成中文，然后对中文进行润色，使其流畅、自然、易读，同时保留原文含义和语气。' +
  '请尽量使用简洁、地道的措辞；根据需要合并不完整的句子；修改不合适的词序；删除填充词。' +
  '请保持专业术语的准确性；保留数字/单位；并在适当的情况下将标点符号标准化为中文格式。' +
  '请勿在输出中包含 <context> 中的任何内容。请勿添加解释、引述、说话者标签、时间戳或语言标签。' +
  '仅返回最终润色后的中文句子，其他内容请勿返回。'
)
const DEFAULT_CHAT_PROMPT = '请用简洁的中文、分点列出要点。'
const DEFAULT_SUMMARY_PROMPT = 'You are a precise context compressor. Summarize English conversation text for downstream translation. Keep names, entities, topics, and unresolved references. Keep it concise and information-dense. Output in English.'

// Settings
const settings = reactive({
  apiKey: '',
  apiBase: 'https://api.openai.com/v1',
  modelChat: 'gpt-4o-mini',
  modelTranslate: 'gpt-4o-mini',
  promptChat: DEFAULT_CHAT_PROMPT,
  promptTranslate: DEFAULT_TRANSLATE_PROMPT,
  promptSummary: DEFAULT_SUMMARY_PROMPT,
  autoScroll: true,
})

const SETTINGS_KEY = 'dt_pro_settings'

function loadSettings() {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY)
    if (!raw) return
    const s = JSON.parse(raw)
    if (s.apiKey) settings.apiKey = s.apiKey
    if (s.apiBase) settings.apiBase = s.apiBase
    if (s.modelChat) settings.modelChat = s.modelChat
    if (s.modelTranslate) settings.modelTranslate = s.modelTranslate
    if (s.promptChat) settings.promptChat = s.promptChat
    if (s.promptTranslate) settings.promptTranslate = s.promptTranslate
    if (s.promptSummary) settings.promptSummary = s.promptSummary
    if (s.autoScroll !== undefined) settings.autoScroll = s.autoScroll
  } catch { /* ignore */ }
}

function saveSettings() {
  localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings))
  showSettings.value = false
}

// Login form
const loginForm = reactive({ email: '', password: '', name: '', isRegister: false, loading: false, error: '' })

async function handleLogin() {
  loginForm.loading = true
  loginForm.error = ''
  try {
    const { login: authLogin, register: authRegister } = useAuth()
    if (loginForm.isRegister) {
      await authRegister(loginForm.email, loginForm.password, loginForm.name)
    } else {
      await authLogin(loginForm.email, loginForm.password)
    }
    showLoginModal.value = false
    loginForm.email = ''
    loginForm.password = ''
    loginForm.name = ''
  } catch (e) {
    loginForm.error = e instanceof Error ? e.message : 'Login failed'
  } finally {
    loginForm.loading = false
  }
}

async function handleLogout() {
  await logout()
}

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
    const cfg: RagConfig = {
      api_key: settings.apiKey || undefined,
      api_base: settings.apiBase || undefined,
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
      const lineTranslations: Array<{ content: string; startTime: number; isPartial: boolean }> = []
      for (const seg of line.segments) {
        const match = translations.value.find(
          (t) => t.speaker === line.speaker && Math.abs(t.startTime - seg.startTime) < 1.5
        )
        if (match && !lineTranslations.some((lt) => lt.startTime === match.startTime)) {
          lineTranslations.push({
            content: match.content,
            startTime: match.startTime,
            isPartial: match.isPartial,
          })
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
    if (hasSession.value) {
      queueTranscript({
        speaker,
        text,
        start_time: startTime,
        end_time: endTime,
        status: 'confirmed',
      })
    }
  }
}

// Handle translation from proxy
function handleTranslation(seg: TranslationSegment) {
  const { text, startTime, speaker, isPartial } = seg
  const id = `${speaker}-${startTime}`
  const existing = translations.value.findIndex((t) => t.id === id)
  if (existing >= 0) {
    translations.value[existing] = { id, speaker, startTime, content: text, isPartial }
  } else {
    translations.value.push({ id, speaker, startTime, content: text, isPartial })
  }
}

// Start recording
async function startRecording() {
  if (!isAuthenticated.value) {
    showLoginModal.value = true
    return
  }

  try {
    error.value = null
    isInitializing.value = true

    // Create cloud session
    await createSession({
      title: `Session ${new Date().toLocaleString()}`,
      source_language: 'en',
      target_language: 'zh',
    })

    // Connect to Speechmatics proxy
    onTranscript.value = handleTranscript
    onTranslation.value = handleTranslation
    onError.value = (err) => { error.value = err }

    await smConnect({
      language: 'en',
      enable_partials: true,
      diarization: 'speaker',
      operating_point: 'enhanced',
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
      if (e.data && !isPaused.value && isConnected.value) {
        sendAudio(e.data)
      }
    }
    source.connect(audioWorklet)
    audioWorklet.connect(audioContext.destination)

    // Setup MediaRecorder for audio download
    mediaRecorder = new MediaRecorder(mediaStream, { mimeType: 'audio/webm;codecs=opus' })
    mediaRecorder.ondataavailable = (e) => {
      if (e.data.size > 0) audioChunks.push(e.data)
    }
    mediaRecorder.start(1000)

    // Start timer
    elapsedTime.value = 0
    timerInterval.value = window.setInterval(() => {
      if (!isPaused.value) elapsedTime.value++
    }, 1000)

    isInitializing.value = false
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to start recording'
    isInitializing.value = false
  }
}

// Stop recording
async function stopRecording() {
  // Stop timer
  if (timerInterval.value) {
    clearInterval(timerInterval.value)
    timerInterval.value = null
  }

  // End Speechmatics session
  smEndSession()
  smDisconnect()

  // Stop audio
  if (audioWorklet) {
    audioWorklet.disconnect()
    audioWorklet = null
  }
  if (audioContext) {
    await audioContext.close()
    audioContext = null
  }
  if (mediaStream) {
    mediaStream.getTracks().forEach((t) => t.stop())
    mediaStream = null
  }
  if (mediaRecorder && mediaRecorder.state !== 'inactive') {
    mediaRecorder.stop()
  }

  // Flush transcripts to cloud
  await flushTranscripts()
  await endSession()

  isPaused.value = false
  elapsedTime.value = 0
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
function downloadAudio() {
  if (audioChunks.length === 0) return
  const blob = new Blob(audioChunks, { type: 'audio/webm' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `recording-${Date.now()}.webm`
  a.click()
  URL.revokeObjectURL(url)
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
})

onUnmounted(() => {
  if (isRecording.value) {
    stopRecording()
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
        <!-- Recording status -->
        <div v-if="isRecording" class="status-pill recording">
          <span class="status-dot" />
          <span>{{ elapsedLabel }}</span>
        </div>

        <!-- Cloud status -->
        <div class="status-pill" :class="isAuthenticated ? 'cloud-on' : 'cloud-off'">
          <Cloud v-if="isAuthenticated" :size="14" />
          <CloudOff v-else :size="14" />
          <span>{{ isAuthenticated ? '云端' : '离线' }}</span>
        </div>

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

        <!-- Stream items -->
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
          >
            <MessageSquare :size="20" />
          </button>
        </div>

        <!-- Center: Record -->
        <div class="record-wrap">
          <button
            class="record-btn"
            :class="isRecording ? 'on' : 'off'"
            :disabled="isInitializing"
            @click="isRecording ? stopRecording() : startRecording()"
          >
            <span v-if="isRecording" class="ping" />
            <Loader2 v-if="isInitializing" :size="24" class="spin" />
            <Square v-else-if="isRecording" :size="20" />
            <Mic v-else :size="28" />
          </button>
        </div>

        <!-- Right: Actions -->
        <div class="cmd-group">
          <button class="cmd-btn" :disabled="!isRecording" @click="togglePause">
            <Pause v-if="!isPaused" :size="20" />
            <Play v-else :size="20" />
          </button>
          <button class="cmd-btn" @click="downloadAudio">
            <Download :size="20" />
          </button>
          <button class="cmd-btn" @click="downloadTranscript">
            <FileText :size="20" />
          </button>
          <button class="cmd-btn" @click="downloadTranslation">
            <Languages :size="20" />
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

                <label class="label mt-3">API Key</label>
                <input v-model="settings.apiKey" type="password" class="input" placeholder="sk-..." />
              </template>

              <template v-else>
                <div class="managed-notice">
                  <Lock :size="24" />
                  <p>API 由服务器管理</p>
                </div>
              </template>

              <label class="label mt-3">Chat 模型</label>
              <input v-model="settings.modelChat" type="text" class="input" />

              <label class="label mt-3">翻译模型</label>
              <input v-model="settings.modelTranslate" type="text" class="input" />
            </div>

            <!-- Prompts Tab -->
            <div v-else-if="settingsTab === 'prompts'" class="settings-section">
              <label class="label">翻译提示词</label>
              <textarea v-model="settings.promptTranslate" rows="5" class="textarea" />
              <button class="reset-btn" @click="settings.promptTranslate = DEFAULT_TRANSLATE_PROMPT">重置默认</button>

              <label class="label mt-3">Chat 提示词</label>
              <textarea v-model="settings.promptChat" rows="3" class="textarea" />
              <button class="reset-btn" @click="settings.promptChat = DEFAULT_CHAT_PROMPT">重置默认</button>

              <label class="label mt-3">摘要提示词</label>
              <textarea v-model="settings.promptSummary" rows="3" class="textarea" />
              <button class="reset-btn" @click="settings.promptSummary = DEFAULT_SUMMARY_PROMPT">重置默认</button>
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
  </div>
</template>

<style scoped>
/* Include the core styles */
@import './pro-standalone.css';
</style>
