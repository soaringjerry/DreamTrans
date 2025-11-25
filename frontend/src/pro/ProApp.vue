<script setup lang="ts">
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
  Download,
  FileText,
  Languages,
  X,
  Sparkles,
  Search,
  ArrowUpRight,
  Loader2,
  Save,
  ChevronLeft,
} from 'lucide-vue-next'
import { askRag } from '../api'
import type { RagAskResponse, RagConfig } from '../api'
import { formatDuration } from '../utils/format'
import { lexSnapshot, type LexSnapshot } from '../utils/lexicon'
import { getMetrics, getMetricsByKind, type MetricEvent } from '../utils/metrics'
import { listSessions, getSessionMeta } from '../db'
import { emitProCommand, onProState, type ProStateSnapshot } from './bridge'
import { useSystemSettings } from './composables/useSystemSettings'
import { Lock } from 'lucide-vue-next'

// Types
type Panel = 'none' | 'chat' | 'lexicon' | 'metrics'
type SettingsTab = 'model' | 'prompts' | 'experimental' | 'api'
type TextState = 'streaming' | 'confirmed' | 'translated'

// Props
const props = defineProps<{ onBackToClassic?: () => void }>()

// System settings (determines if user can use their own API key)
const { allowUserApiKey, loadSettings: loadSystemSettings } = useSystemSettings()

// Reactive state
const rightPanel = ref<Panel>('none')
const showSettings = ref(false)
const showHistory = ref(false)
const settingsTab = ref<SettingsTab>('model')
const streamRef = ref<HTMLElement | null>(null)

// State from React bridge
const snapshot = ref<ProStateSnapshot>({
  lines: [],
  translations: [],
  isTranscribing: false,
  isInitializing: false,
  isPaused: false,
  elapsedTime: 0,
  sessionId: '',
  hiddenCounts: { transcripts: 0, translations: 0 },
})

// Settings
const DEFAULT_TRANSLATE_PROMPT = (
  '您是一位专业的同声传译翻译，你正在把英文的口语内容翻译成中文易于理解的话，' +
  '请使用 <context> 来帮助你理解上下文和当前场景并作出适当的纠错和润色。' +
  '请仅翻译 <text>...</text> 里的文本变成中文，然后对中文进行润色，使其流畅、自然、易读，同时保留原文含义和语气。' +
  '请尽量使用简洁、地道的措辞；根据需要合并不完整的句子；修改不合适的词序；删除填充词。' +
  '请保持专业术语的准确性；保留数字/单位；并在适当的情况下将标点符号标准化为中文格式。' +
  '请勿在输出中包含 <context> 中的任何内容。请勿添加解释、引述、说话者标签、时间戳或语言标签。' +
  '仅返回最终润色后的中文句子，其他内容请勿返回。'
)
const DEFAULT_SUMMARY_PROMPT = 'You are a precise context compressor. Summarize English conversation text for downstream translation. Keep names, entities, topics, and unresolved references. Keep it concise and information-dense. Output in English.'

const settings = reactive({
  apiKey: '',
  apiBase: 'https://api.openai.com/v1',
  modelChat: 'gpt-5',
  modelTranslate: 'gpt-4.1-mini',
  modelSummary: 'gpt-5-chat-latest',
  promptChat: '请用简洁的中文、分点列出要点。',
  promptTranslate: DEFAULT_TRANSLATE_PROMPT,
  promptSummary: DEFAULT_SUMMARY_PROMPT,
  promptLookup: '请解释以下单词或短语的含义，并给出词性、常见搭配和 2 个例句（英文+中文）：\n{{text}}',
  transMode: 'ai_rolling',
  transModel: 'gpt-4.1-mini',
  expStreaming: false,
  expSmart: true,
  expTypewriter: false,
  expBilingual: true,
  expSummary: false,
  expEmbeddings: true,
})

const SETTINGS_KEY = 'dt_settings_v1'

function loadSettings() {
  try {
    const raw = localStorage.getItem(SETTINGS_KEY)
    if (!raw) return
    const s = JSON.parse(raw) as Record<string, unknown>
    settings.apiKey = (s.apiKey as string) || ''
    settings.apiBase = (s.apiBase as string) || settings.apiBase
    settings.modelChat = (s.model_chat as string) || (s.model as string) || settings.modelChat
    settings.modelTranslate = (s.model_translate as string) || settings.modelTranslate
    settings.modelSummary = (s.model_summary as string) || settings.modelSummary
    settings.promptChat = (s.prompt_chat as string) || (s.prompt as string) || settings.promptChat
    settings.promptTranslate = (s.prompt_translate as string) || settings.promptTranslate
    settings.promptSummary = (s.prompt_summary as string) || settings.promptSummary
    settings.promptLookup = (s.prompt_lookup as string) || settings.promptLookup
    if (s.transMode === 'speechmatics' || s.transMode === 'ai_rolling' || s.transMode === 'ai_compressed') {
      settings.transMode = s.transMode as string
    }
    settings.transModel = (s.transModel as string) || settings.transModel
    settings.expStreaming = !!s.experimental_streaming
    settings.expSmart = s.experimental_smart !== undefined ? !!s.experimental_smart : settings.expSmart
    settings.expTypewriter = !!s.experimental_typewriter
    settings.expBilingual = s.experimental_bilingual !== undefined ? !!s.experimental_bilingual : settings.expBilingual
    settings.expSummary = s.experimental_summary !== undefined ? !!s.experimental_summary : settings.expSummary
    settings.expEmbeddings = s.experimental_embeddings !== undefined ? !!s.experimental_embeddings : settings.expEmbeddings
  } catch { /* ignore */ }
}

function saveSettings() {
  const payload = {
    apiKey: settings.apiKey,
    apiBase: settings.apiBase,
    model: settings.modelChat,
    model_chat: settings.modelChat,
    model_translate: settings.modelTranslate,
    model_summary: settings.modelSummary,
    prompt: settings.promptChat,
    prompt_chat: settings.promptChat,
    prompt_translate: settings.promptTranslate,
    prompt_summary: settings.promptSummary,
    prompt_lookup: settings.promptLookup,
    transMode: settings.transMode,
    transModel: settings.transModel,
    experimental_streaming: settings.expStreaming,
    experimental_smart: settings.expSmart,
    experimental_typewriter: settings.expTypewriter,
    experimental_bilingual: settings.expBilingual,
    experimental_summary: settings.expSummary,
    experimental_embeddings: settings.expEmbeddings,
  }
  localStorage.setItem(SETTINGS_KEY, JSON.stringify(payload))
  window.dispatchEvent(new CustomEvent('dt-settings-updated'))
  showSettings.value = false
}

// Chat state
interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
  meta?: { tokens?: string; latency?: string; model?: string }
}
const chatMessages = ref<ChatMessage[]>([])
const chatInput = ref('')
const chatLoading = ref(false)
const chatHistoryLoaded = ref(false)

const chatHistoryKey = computed(() => snapshot.value.sessionId ? `dt_chat_history_${snapshot.value.sessionId}` : '')

function loadChatHistory() {
  if (!chatHistoryKey.value) return
  try {
    const raw = localStorage.getItem(chatHistoryKey.value)
    if (raw) {
      const arr = JSON.parse(raw) as ChatMessage[]
      if (Array.isArray(arr)) chatMessages.value = arr
    }
    chatHistoryLoaded.value = true
  } catch { /* ignore */ }
}

watch(chatHistoryKey, () => {
  chatMessages.value = []
  chatHistoryLoaded.value = false
  loadChatHistory()
})

watch(chatMessages, () => {
  if (!chatHistoryLoaded.value) return
  try {
    if (chatHistoryKey.value) {
      localStorage.setItem(chatHistoryKey.value, JSON.stringify(chatMessages.value))
    }
  } catch { /* noop */ }
}, { deep: true })

async function sendChat(text?: string) {
  const q = (text ?? chatInput.value).trim()
  if (!q || chatLoading.value) return
  chatInput.value = ''
  chatMessages.value = [...chatMessages.value, { role: 'user', content: q }, { role: 'assistant', content: '…' }]
  chatLoading.value = true
  try {
    const cfg: RagConfig = {
      api_key: settings.apiKey || undefined,
      api_base: settings.apiBase || undefined,
      model: settings.modelChat || undefined,
      prompt: settings.promptChat || undefined,
    }
    const res: RagAskResponse = await askRag(snapshot.value.sessionId || 'current_session', q, 5, cfg, 45000)
    const hasUsage = !!res.usage && ((res.usage.total_tokens ?? 0) > 0 || (res.usage.prompt_tokens ?? 0) > 0 || (res.usage.completion_tokens ?? 0) > 0)
    const tokens = hasUsage ? `${res.usage!.prompt_tokens}/${res.usage!.completion_tokens} (${res.usage!.total_tokens})` : undefined
    const latency = res.latency_ms !== undefined ? formatDuration(res.latency_ms) : undefined
    const apiModel = res.usage?.model
    chatMessages.value = chatMessages.value.map((m, idx, arr) => {
      if (idx === arr.length - 1 && m.role === 'assistant' && m.content === '…') {
        return { role: 'assistant', content: res.answer, meta: { tokens, latency, model: apiModel } }
      }
      return m
    })
  } catch (e: unknown) {
    const msg = e instanceof Error ? e.message : String(e)
    chatMessages.value = chatMessages.value.map((m, idx, arr) => {
      if (idx === arr.length - 1 && m.role === 'assistant' && m.content === '…') {
        return { role: 'assistant', content: `请求失败：${msg}` }
      }
      return m
    })
  } finally {
    chatLoading.value = false
  }
}

// Lexicon
const lex = ref<LexSnapshot | null>(null)
const lexUpdated = () => {
  if (!snapshot.value.sessionId) return
  lex.value = lexSnapshot(snapshot.value.sessionId)
}

// Metrics
const metrics = ref<MetricEvent[]>([])
const metricsTranslate = ref<MetricEvent[]>([])
const metricsChat = ref<MetricEvent[]>([])
const refreshMetrics = () => {
  metrics.value = getMetrics()
  metricsTranslate.value = getMetricsByKind('translation', 20)
  metricsChat.value = getMetricsByKind('chat', 20)
}

// History
const sessions = ref<Array<{ id: string; timestamp: number }>>([])
const sessionMeta = ref<Record<string, { title?: string; summary?: string }>>({})
const loadHistory = async () => {
  sessions.value = await listSessions()
  const metas: Record<string, { title?: string; summary?: string }> = {}
  for (const s of sessions.value.slice(0, 10)) {
    try {
      metas[s.id] = await getSessionMeta(s.id)
    } catch { /* ignore */ }
  }
  sessionMeta.value = metas
}

watch(showHistory, (v) => { if (v) loadHistory() })

// Computed
const elapsedLabel = computed(() => {
  const s = snapshot.value.elapsedTime || 0
  const m = Math.floor(s / 60)
  const ss = s % 60
  return `${String(m).padStart(2, '0')}:${String(ss).padStart(2, '0')}`
})

const isRecording = computed(() => snapshot.value.isTranscribing || snapshot.value.isInitializing)

// Stream items with state calculation
const streamItems = computed(() =>
  snapshot.value.lines.map((line) => {
    const start = line.confirmedSegments[0]?.startTime ?? 0
    const translation = snapshot.value.translations
      .filter((t) => t.speaker === line.speaker && Math.abs(t.startTime - start) < 1.2)
      .at(-1)
      ?? snapshot.value.translations.filter((t) => t.speaker === line.speaker).at(-1)

    // Join confirmed segments with line breaks for better readability
    const confirmedText = line.confirmedSegments.map((s) => s.text).join(' ')

    // Determine state: streaming > confirmed > translated
    const state: TextState = line.partialText
      ? 'streaming'
      : translation && !translation.isPartial
        ? 'translated'
        : 'confirmed'

    return {
      id: line.id,
      speaker: line.speaker,
      text: confirmedText,
      partial: line.partialText,
      start,
      translation: translation?.content ?? '',
      translationPartial: translation?.isPartial ?? false,
      state,
    }
  }),
)

const scrollToBottom = () => {
  nextTick(() => {
    if (streamRef.value) {
      streamRef.value.scrollTop = streamRef.value.scrollHeight
    }
  })
}

// Lifecycle
onMounted(() => {
  loadSettings()
  loadSystemSettings() // Load system-wide settings
  loadChatHistory()
  lexUpdated()
  refreshMetrics()

  const offProState = onProState((state) => {
    snapshot.value = state
  })

  const lexHandler = () => lexUpdated()
  const metricsHandler = () => refreshMetrics()

  window.addEventListener('dt-lex-updated', lexHandler as EventListener)
  window.addEventListener('dt-metrics', metricsHandler as EventListener)

  scrollToBottom()

  onUnmounted(() => {
    offProState()
    window.removeEventListener('dt-lex-updated', lexHandler as EventListener)
    window.removeEventListener('dt-metrics', metricsHandler as EventListener)
  })
})

watch(() => snapshot.value.lines, () => scrollToBottom())

// Helpers
const formatTimestamp = (seconds: number) => {
  const m = Math.floor(seconds / 60)
  const s = Math.floor(seconds % 60)
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

// Actions via bridge
const start = () => emitProCommand({ type: 'start' })
const stop = () => emitProCommand({ type: 'stop' })
const resume = () => emitProCommand({ type: 'continue' })
const pauseToggle = () => emitProCommand({ type: 'pause-toggle' })
const downloadAudio = () => emitProCommand({ type: 'download-audio' })
const downloadTranscript = () => emitProCommand({ type: 'download-transcript' })
const downloadTranslation = () => emitProCommand({ type: 'download-translation' })
</script>

<template>
  <div class="pro-root">
    <!-- Ambient Background -->
    <div class="ambient ambient-1" />
    <div class="ambient ambient-2" />

    <!-- Header -->
    <header class="pro-header">
      <div class="brand-pill" @click="props.onBackToClassic?.()">
        <span class="dot" :class="isRecording ? 'dot--recording' : 'dot--idle'" />
        <span class="brand-text">
          DreamTrans
          <span class="brand-sub">PRO</span>
        </span>
      </div>

      <div class="header-actions">
        <!-- Recording status -->
        <div v-if="isRecording" class="status-pill">
          <span class="status-dot" />
          <span class="status-text">{{ elapsedLabel }}</span>
        </div>

        <button class="icon-btn" title="历史记录" @click="showHistory = true">
          <History :size="18" />
        </button>
        <button class="icon-btn" title="设置" @click="showSettings = true">
          <Settings :size="18" />
        </button>
        <button v-if="props.onBackToClassic" class="back-btn" @click="props.onBackToClassic?.()">
          <ChevronLeft :size="16" />
          <span>经典版</span>
        </button>
      </div>
    </header>

    <!-- Main Stream -->
    <main
      ref="streamRef"
      class="stream"
      :class="{ 'stream--offset': rightPanel !== 'none' }"
    >
      <div class="stream-inner">
        <!-- Hidden count hint -->
        <div v-if="(snapshot.hiddenCounts?.transcripts || 0) > 0" class="hint">
          仅显示最新片段 · 已隐藏 {{ snapshot.hiddenCounts?.transcripts }} 行
        </div>

        <!-- Empty state -->
        <div v-if="streamItems.length === 0" class="empty-state">
          <div class="empty-icon">
            <Mic :size="48" :stroke-width="1" />
          </div>
          <h3>准备开始转录</h3>
          <p>点击下方麦克风按钮开始实时语音转录和翻译</p>
        </div>

        <!-- Stream items -->
        <article
          v-for="(item, idx) in streamItems"
          :key="item.id"
          class="line"
          :class="{
            'line--live': item.state === 'streaming',
            'line--confirmed': item.state === 'confirmed',
            'line--translated': item.state === 'translated',
          }"
        >
          <!-- Connector line -->
          <div v-if="idx !== 0" class="connector" />

          <!-- Meta info -->
          <div class="meta">
            <span
              class="speaker"
              :class="item.speaker === 'Speaker A' ? 'speaker--a' : 'speaker--b'"
            >
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
              {{ item.state === 'streaming' ? '流式' : item.state === 'translated' ? '已翻译' : '待翻译' }}
            </span>
          </div>

          <!-- Card -->
          <div class="card" :class="{ 'card--live': item.state === 'streaming' }">
            <!-- Original text -->
            <h3 class="text">
              {{ item.text }}
              <span v-if="item.partial" class="partial">{{ item.partial }}</span>
              <span v-if="item.state === 'streaming'" class="cursor" />
            </h3>

            <!-- Translation -->
            <div
              class="translation"
              :class="{ 'translation--live': item.state === 'streaming' }"
            >
              <p v-if="item.translation" class="translation-text">
                {{ item.translation }}
                <span v-if="item.translationPartial" class="tag-partial">partial</span>
              </p>
              <div v-else class="translation-placeholder">
                <span v-if="item.state === 'streaming'" class="pulse-dots">
                  <span /><span /><span />
                </span>
                <div v-else class="skeleton">
                  <div class="sk sk-1" />
                  <div class="sk sk-2" />
                  <div class="loader">
                    <Loader2 :size="12" class="spin" />
                    <span>AI Translating...</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </article>

        <!-- Spacer -->
        <div class="stream-spacer" />
      </div>
    </main>

    <!-- Command Bar -->
    <div class="command" :class="{ 'command--offset': rightPanel !== 'none' }">
      <div class="command-inner">
        <!-- Left: Panel toggles -->
        <div class="cmd-group">
          <button
            class="cmd-btn"
            :class="{ active: rightPanel === 'chat', chat: rightPanel === 'chat' }"
            title="AI Chat"
            @click="rightPanel = rightPanel === 'chat' ? 'none' : 'chat'"
          >
            <MessageSquare :size="20" />
          </button>
          <button
            class="cmd-btn"
            :class="{ active: rightPanel === 'lexicon', lexicon: rightPanel === 'lexicon' }"
            title="词汇统计"
            @click="rightPanel = rightPanel === 'lexicon' ? 'none' : 'lexicon'"
          >
            <BookOpen :size="20" />
          </button>
          <button
            class="cmd-btn"
            :class="{ active: rightPanel === 'metrics', metrics: rightPanel === 'metrics' }"
            title="性能监控"
            @click="rightPanel = rightPanel === 'metrics' ? 'none' : 'metrics'"
          >
            <BarChart3 :size="20" />
          </button>
        </div>

        <!-- Center: Record button -->
        <div class="record-wrap">
          <button
            class="record-btn"
            :class="isRecording ? 'on' : 'off'"
            :title="isRecording ? '停止' : '开始录制'"
            @click="isRecording ? stop() : start()"
          >
            <span v-if="isRecording" class="ping" />
            <Square v-if="isRecording" :size="20" class="stop-icon" />
            <Mic v-else :size="28" class="mic-icon" />
          </button>
        </div>

        <!-- Right: Actions -->
        <div class="cmd-group">
          <button
            class="cmd-btn"
            :disabled="!isRecording"
            title="暂停/继续"
            @click="pauseToggle"
          >
            <Play :size="20" />
          </button>
          <button class="cmd-btn" title="下载音频" @click="downloadAudio">
            <Download :size="20" />
          </button>
          <button class="cmd-btn" title="下载原文" @click="downloadTranscript">
            <FileText :size="20" />
          </button>
          <button class="cmd-btn" title="下载翻译" @click="downloadTranslation">
            <Languages :size="20" />
          </button>
        </div>
      </div>
    </div>

    <!-- Side Drawer -->
    <aside v-if="rightPanel !== 'none'" class="drawer">
      <header class="drawer-header">
        <div class="drawer-title">
          <span
            class="drawer-chip"
            :class="{
              chat: rightPanel === 'chat',
              lexicon: rightPanel === 'lexicon',
              metrics: rightPanel === 'metrics',
            }"
          >
            <MessageSquare v-if="rightPanel === 'chat'" :size="14" />
            <BookOpen v-else-if="rightPanel === 'lexicon'" :size="14" />
            <BarChart3 v-else :size="14" />
          </span>
          <span>
            {{ rightPanel === 'chat' ? 'AI Assistant' : rightPanel === 'lexicon' ? 'Lexicon' : 'Performance' }}
          </span>
        </div>
        <button class="ghost-btn" @click="rightPanel = 'none'">
          <X :size="20" />
        </button>
      </header>

      <!-- Chat Panel -->
      <div v-if="rightPanel === 'chat'" class="drawer-body drawer-chat">
        <div class="chat-list">
          <div
            v-for="(msg, i) in chatMessages"
            :key="i"
            class="chat-row"
            :class="msg.role === 'user' ? 'user' : 'ai'"
          >
            <div class="avatar" :class="msg.role === 'ai' ? 'ai' : 'user'">
              <Sparkles v-if="msg.role === 'ai'" :size="14" />
              <div v-else class="dot-small" />
            </div>
            <div class="bubble" :class="msg.role === 'ai' ? 'ai' : 'user'">
              <div>{{ msg.content }}</div>
              <div v-if="msg.meta" class="bubble-meta">
                <span v-if="msg.meta.model">model {{ msg.meta.model }}</span>
                <span v-if="msg.meta.tokens">tokens {{ msg.meta.tokens }}</span>
                <span v-if="msg.meta.latency">latency {{ msg.meta.latency }}</span>
              </div>
            </div>
          </div>
          <div v-if="!chatMessages.length" class="empty-placeholder">
            <Sparkles :size="32" :stroke-width="1" />
            <p>结合上下文的问答助手，输入问题开始对话</p>
          </div>
        </div>
        <div class="chat-input">
          <div class="input-wrap">
            <Search :size="16" class="input-icon" />
            <input
              v-model="chatInput"
              type="text"
              :placeholder="chatLoading ? '正在生成...' : 'Ask about the context...'"
              :disabled="chatLoading"
              @keyup.enter="sendChat()"
            />
          </div>
          <button class="send-btn" :disabled="chatLoading" @click="sendChat()">
            <ArrowUpRight :size="16" />
          </button>
        </div>
      </div>

      <!-- Lexicon Panel -->
      <div v-else-if="rightPanel === 'lexicon'" class="drawer-body drawer-lexicon">
        <div class="stats-grid">
          <div class="stat-card">
            <p class="stat-label">Tokens</p>
            <p class="stat-value">{{ lex?.total || 0 }}</p>
          </div>
          <div class="stat-card">
            <p class="stat-label">Words</p>
            <p class="stat-value">{{ lex?.words.length || 0 }}</p>
          </div>
          <div class="stat-card">
            <p class="stat-label">Bigrams</p>
            <p class="stat-value">{{ lex?.bigrams.length || 0 }}</p>
          </div>
        </div>

        <div class="search-row">
          <Search :size="16" class="input-icon" />
          <input type="text" placeholder="Search words..." disabled />
        </div>

        <div class="lex-list">
          <div
            v-for="(item, i) in (lex?.words || []).slice().sort((a, b) => b[1] - a[1]).slice(0, 20)"
            :key="i"
            class="lex-card"
          >
            <div class="lex-row">
              <div>
                <h4>{{ item[0] }}</h4>
                <span class="freq">freq: {{ item[1] }}</span>
              </div>
              <span class="status-dot ok" />
            </div>
            <div class="bar">
              <div
                class="bar-fill ok"
                :style="{ width: `${Math.min(item[1] * 2, 100)}%` }"
              />
            </div>
          </div>
          <div v-if="!lex || lex.words.length === 0" class="empty-placeholder">
            <BookOpen :size="32" :stroke-width="1" />
            <p>等待转写累积后展示词频</p>
          </div>
        </div>
      </div>

      <!-- Metrics Panel -->
      <div v-else class="drawer-body drawer-lexicon">
        <div class="stats-grid">
          <div class="stat-card">
            <p class="stat-label">Latest</p>
            <p class="stat-value">{{ metrics[0]?.latency_ms ? formatDuration(metrics[0].latency_ms) : '—' }}</p>
          </div>
          <div class="stat-card">
            <p class="stat-label">Translates</p>
            <p class="stat-value">{{ metricsTranslate.length }}</p>
          </div>
          <div class="stat-card">
            <p class="stat-label">Chat</p>
            <p class="stat-value">{{ metricsChat.length }}</p>
          </div>
        </div>

        <div class="lex-list">
          <div v-if="metricsTranslate.length" class="lex-card">
            <div class="lex-row">
              <h4>Translate Latency (ms)</h4>
            </div>
            <div class="mini-bars">
              <span
                v-for="(m, i) in metricsTranslate"
                :key="i"
                class="mini-bar"
                :style="{ height: `${Math.min((m.latency_ms || 0) / 20, 100)}%` }"
              />
            </div>
          </div>
          <div v-if="metricsChat.length" class="lex-card">
            <div class="lex-row">
              <h4>Chat Latency (ms)</h4>
            </div>
            <div class="mini-bars">
              <span
                v-for="(m, i) in metricsChat"
                :key="i"
                class="mini-bar chat"
                :style="{ height: `${Math.min((m.latency_ms || 0) / 20, 100)}%` }"
              />
            </div>
          </div>
          <div v-if="!metricsTranslate.length && !metricsChat.length" class="empty-placeholder">
            <BarChart3 :size="32" :stroke-width="1" />
            <p>暂无性能数据</p>
          </div>
        </div>
      </div>
    </aside>

    <!-- Settings Modal -->
    <div v-if="showSettings" class="overlay" @click="showSettings = false">
      <div class="modal settings-modal" @click.stop>
        <div class="modal-header">
          <div class="modal-title">
            <Settings :size="20" />
            <span>Settings</span>
          </div>
          <button class="ghost-btn" @click="showSettings = false">
            <X :size="24" />
          </button>
        </div>

        <div class="settings-body">
          <div class="settings-tabs">
            <button
              v-for="tab in ['model', 'prompts', 'experimental', 'api']"
              :key="tab"
              class="tab"
              :class="{ active: settingsTab === tab }"
              @click="settingsTab = tab as SettingsTab"
            >
              {{ tab.charAt(0).toUpperCase() + tab.slice(1) }}
            </button>
          </div>

          <div class="settings-content">
            <!-- Model Tab -->
            <div v-if="settingsTab === 'model'" class="settings-section">
              <label class="label">Translation Model</label>
              <div class="model-grid">
                <button
                  v-for="m in ['gpt-4o-2024', 'gpt-4.1-mini', 'claude-3.5', 'gemini-pro']"
                  :key="m"
                  class="model-btn"
                  :class="{ active: settings.modelTranslate === m }"
                  @click="settings.modelTranslate = m"
                >
                  <span>{{ m }}</span>
                  <span v-if="m === 'gpt-4.1-mini'" class="active-dot" />
                </button>
              </div>

              <label class="label mt-4">Chat Model</label>
              <input v-model="settings.modelChat" type="text" class="input" placeholder="gpt-5" />

              <label class="label mt-4">Summary Model</label>
              <input v-model="settings.modelSummary" type="text" class="input" placeholder="gpt-5-chat-latest" />
            </div>

            <!-- Prompts Tab -->
            <div v-else-if="settingsTab === 'prompts'" class="settings-section">
              <label class="label">Chat Prompt</label>
              <textarea v-model="settings.promptChat" rows="3" class="textarea" />

              <label class="label mt-4">Translation Prompt</label>
              <textarea v-model="settings.promptTranslate" rows="4" class="textarea" />

              <label class="label mt-4">Summary Prompt</label>
              <textarea v-model="settings.promptSummary" rows="3" class="textarea" />

              <label class="label mt-4">Lookup Prompt</label>
              <textarea v-model="settings.promptLookup" rows="2" class="textarea" />
            </div>

            <!-- Experimental Tab -->
            <div v-else-if="settingsTab === 'experimental'" class="settings-section switches">
              <label class="switch-label">
                <input type="checkbox" v-model="settings.expSmart" />
                <span>Smart Algorithm</span>
              </label>
              <label class="switch-label">
                <input type="checkbox" v-model="settings.expStreaming" />
                <span>Streaming Output</span>
              </label>
              <label class="switch-label">
                <input type="checkbox" v-model="settings.expTypewriter" />
                <span>Typewriter Effect</span>
              </label>
              <label class="switch-label">
                <input type="checkbox" v-model="settings.expBilingual" />
                <span>Bilingual Mode</span>
              </label>
              <label class="switch-label">
                <input type="checkbox" v-model="settings.expSummary" />
                <span>Summarization</span>
              </label>
              <label class="switch-label">
                <input type="checkbox" v-model="settings.expEmbeddings" />
                <span>Embeddings</span>
              </label>
            </div>

            <!-- API Tab -->
            <div v-else class="settings-section">
              <!-- Show API settings only if allowed by system settings -->
              <template v-if="allowUserApiKey()">
                <label class="label">API Base</label>
                <input v-model="settings.apiBase" type="text" class="input" placeholder="https://api.openai.com/v1" />

                <label class="label mt-4">API Key</label>
                <input v-model="settings.apiKey" type="password" class="input" placeholder="sk-..." />
              </template>

              <!-- Show managed API message when user API key is not allowed -->
              <template v-else>
                <div class="managed-api-notice">
                  <div class="notice-icon">
                    <Lock :size="32" />
                  </div>
                  <h4>API Managed by Server</h4>
                  <p>
                    All API calls are routed through the backend server.
                    Contact your administrator if you need to use a custom API key.
                  </p>
                </div>
              </template>
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
    <div v-if="showHistory" class="overlay" @click="showHistory = false">
      <div class="modal" @click.stop>
        <div class="modal-header">
          <div class="modal-title">
            <History :size="20" />
            <span>历史会话</span>
          </div>
          <button class="ghost-btn" @click="showHistory = false">
            <X :size="24" />
          </button>
        </div>

        <div class="modal-body">
          <div v-if="!sessions.length" class="empty-placeholder">
            <History :size="32" :stroke-width="1" />
            <p>暂无历史记录</p>
          </div>
          <div v-else class="history-list">
            <div v-for="s in sessions" :key="s.id" class="history-item">
              <div class="history-title">
                <strong>{{ sessionMeta[s.id]?.title || s.id }}</strong>
                <span>{{ new Date(s.timestamp).toLocaleString() }}</span>
              </div>
              <div class="history-summary">
                {{ sessionMeta[s.id]?.summary || '暂无摘要' }}
              </div>
              <div class="history-actions">
                <button class="pill-btn" @click="resume(); showHistory = false">
                  继续当前
                </button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* ==================== Variables ==================== */
.pro-root {
  --bg: #0a0a0a;
  --bg-elevated: #121212;
  --bg-card: rgba(255, 255, 255, 0.03);
  --bg-hover: rgba(255, 255, 255, 0.06);
  --border: rgba(255, 255, 255, 0.1);
  --border-strong: rgba(255, 255, 255, 0.2);
  --text: #e8ecf5;
  --text-muted: rgba(226, 232, 240, 0.68);
  --text-dim: rgba(255, 255, 255, 0.4);

  /* Accent colors */
  --purple: #8b5cf6;
  --purple-glow: rgba(139, 92, 246, 0.3);
  --blue: #3b82f6;
  --blue-glow: rgba(59, 130, 246, 0.3);
  --green: #22c55e;
  --green-glow: rgba(34, 197, 94, 0.5);
  --red: #ef4444;
  --cyan: #22d3ee;

  position: relative;
  min-height: 100vh;
  background: var(--bg);
  color: var(--text);
  font-family: 'Inter', system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  overflow: hidden;
}

/* ==================== Ambient Background ==================== */
.ambient {
  position: absolute;
  width: 50vw;
  height: 50vw;
  border-radius: 50%;
  filter: blur(120px);
  opacity: 0.3;
  pointer-events: none;
  z-index: 0;
}

.ambient-1 {
  top: -20%;
  left: -10%;
  background: #581c87;
  animation: blob 8s infinite;
}

.ambient-2 {
  bottom: -20%;
  right: -10%;
  background: #1e3a5f;
  animation: blob 8s infinite 2s;
}

@keyframes blob {
  0%, 100% { transform: translate(0, 0) scale(1); }
  33% { transform: translate(30px, -50px) scale(1.1); }
  66% { transform: translate(-20px, 20px) scale(0.9); }
}

/* ==================== Header ==================== */
.pro-header {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: 50;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 24px;
  background: rgba(10, 10, 10, 0.8);
  backdrop-filter: blur(12px);
  border-bottom: 1px solid var(--border);
}

.brand-pill {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  padding: 8px 16px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  cursor: pointer;
  transition: all 0.2s ease;
}

.brand-pill:hover {
  background: var(--bg-hover);
  border-color: var(--border-strong);
}

.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  transition: all 0.3s ease;
}

.dot--idle {
  background: #6b7280;
}

.dot--recording {
  background: var(--green);
  box-shadow: 0 0 12px var(--green-glow);
  animation: pulse 1.5s infinite;
}

@keyframes pulse {
  0%, 100% { opacity: 1; transform: scale(1); }
  50% { opacity: 0.7; transform: scale(1.1); }
}

.brand-text {
  font-weight: 600;
  font-size: 14px;
  letter-spacing: 0.2px;
}

.brand-sub {
  margin-left: 6px;
  font-size: 11px;
  color: var(--purple);
  font-weight: 700;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.status-pill {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 12px;
  border-radius: 999px;
  background: rgba(239, 68, 68, 0.15);
  border: 1px solid rgba(239, 68, 68, 0.3);
  margin-right: 8px;
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: var(--red);
  animation: pulse 1s infinite;
}

.status-text {
  font-size: 12px;
  font-family: ui-monospace, monospace;
  color: #fca5a5;
}

.icon-btn {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-muted);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s ease;
}

.icon-btn:hover {
  background: var(--bg-hover);
  color: var(--text);
  border-color: var(--border-strong);
}

.back-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 8px 12px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-muted);
  font-size: 13px;
  cursor: pointer;
  transition: all 0.2s ease;
}

.back-btn:hover {
  background: var(--bg-hover);
  color: var(--text);
}

/* ==================== Main Stream ==================== */
.stream {
  position: relative;
  z-index: 10;
  height: 100vh;
  overflow-y: auto;
  padding: 100px 0 200px;
  display: flex;
  justify-content: center;
  transition: margin-right 0.3s ease;
}

.stream--offset {
  margin-right: 400px;
}

.stream-inner {
  width: min(800px, 100%);
  padding: 0 24px;
  display: flex;
  flex-direction: column;
  gap: 32px;
}

.stream-spacer {
  height: 100px;
}

.hint {
  padding: 10px 16px;
  border-radius: 12px;
  border: 1px dashed var(--border);
  background: var(--bg-card);
  color: var(--text-muted);
  font-size: 13px;
  text-align: center;
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 80px 20px;
  text-align: center;
  color: var(--text-muted);
}

.empty-icon {
  padding: 24px;
  border-radius: 50%;
  background: var(--bg-card);
  border: 1px solid var(--border);
  margin-bottom: 24px;
}

.empty-state h3 {
  font-size: 20px;
  font-weight: 600;
  color: var(--text);
  margin: 0 0 8px;
}

.empty-state p {
  font-size: 14px;
  margin: 0;
}

/* ==================== Stream Item ==================== */
.line {
  position: relative;
}

.connector {
  position: absolute;
  left: 40px;
  top: -20px;
  width: 2px;
  height: 20px;
  background: linear-gradient(to bottom, transparent, var(--border));
}

.meta {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.speaker {
  padding: 4px 10px;
  border-radius: 8px;
  font-size: 11px;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.speaker--a {
  background: #e0e7ff;
  color: #3730a3;
}

.speaker--b {
  background: #cffafe;
  color: #155e75;
}

.timestamp {
  font-size: 11px;
  font-family: ui-monospace, monospace;
  color: var(--text-dim);
}

.state-badge {
  padding: 3px 8px;
  border-radius: 6px;
  font-size: 10px;
  font-weight: 600;
  border: 1px solid transparent;
}

.state--streaming {
  background: rgba(139, 92, 246, 0.2);
  color: #e9d5ff;
  border-color: rgba(139, 92, 246, 0.4);
}

.state--confirmed {
  background: rgba(59, 130, 246, 0.15);
  color: #bfdbfe;
  border-color: rgba(59, 130, 246, 0.3);
}

.state--translated {
  background: rgba(34, 197, 94, 0.15);
  color: #bbf7d0;
  border-color: rgba(34, 197, 94, 0.3);
}

.card {
  padding: 20px 24px;
  border-radius: 20px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  backdrop-filter: blur(8px);
  transition: all 0.3s ease;
}

.line--live .card {
  border-color: rgba(139, 92, 246, 0.5);
  box-shadow: 0 0 40px var(--purple-glow);
}

.line--confirmed .card:hover {
  border-color: var(--border-strong);
  background: var(--bg-hover);
}

.line--translated .card {
  border-color: rgba(34, 197, 94, 0.2);
}

.text {
  margin: 0;
  font-size: 18px;
  font-weight: 500;
  line-height: 1.6;
  color: var(--text);
}

.partial {
  color: var(--purple);
  opacity: 0.9;
}

.cursor {
  display: inline-block;
  width: 2px;
  height: 1.2em;
  background: var(--purple);
  margin-left: 2px;
  vertical-align: text-bottom;
  animation: blink 1s steps(1) infinite;
}

@keyframes blink {
  0%, 50% { opacity: 1; }
  51%, 100% { opacity: 0; }
}

.translation {
  margin-top: 16px;
  padding: 16px;
  border-radius: 12px;
  border: 1px dashed var(--border);
  background: rgba(255, 255, 255, 0.02);
}

.translation--live {
  border-color: rgba(139, 92, 246, 0.4);
  background: rgba(139, 92, 246, 0.05);
}

.translation-text {
  margin: 0;
  font-size: 16px;
  line-height: 1.7;
  color: #cbd5e1;
}

.tag-partial {
  margin-left: 8px;
  padding: 2px 8px;
  border-radius: 999px;
  border: 1px solid var(--border);
  font-size: 10px;
  color: var(--text-muted);
}

.translation-placeholder {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--text-dim);
}

.pulse-dots {
  display: flex;
  gap: 6px;
}

.pulse-dots span {
  width: 8px;
  height: 8px;
  background: var(--purple);
  border-radius: 50%;
  animation: pulse 1s infinite;
}

.pulse-dots span:nth-child(2) { animation-delay: 0.15s; }
.pulse-dots span:nth-child(3) { animation-delay: 0.3s; }

.skeleton {
  width: 100%;
}

.sk {
  height: 10px;
  background: linear-gradient(90deg, rgba(139, 92, 246, 0.15), transparent);
  border-radius: 4px;
  margin-bottom: 8px;
}

.sk-1 { width: 80%; }
.sk-2 { width: 50%; }

.loader {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--purple);
  font-size: 11px;
  font-family: ui-monospace, monospace;
}

.spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

/* ==================== Command Bar ==================== */
.command {
  position: fixed;
  bottom: 24px;
  left: 50%;
  transform: translateX(-50%);
  z-index: 40;
  width: min(600px, calc(100% - 48px));
  transition: all 0.3s ease;
}

.command--offset {
  transform: translateX(calc(-50% - 200px));
}

.command-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-radius: 20px;
  background: rgba(18, 18, 18, 0.95);
  border: 1px solid var(--border);
  backdrop-filter: blur(16px);
  box-shadow: 0 20px 50px rgba(0, 0, 0, 0.5);
}

.cmd-group {
  display: flex;
  gap: 6px;
}

.cmd-btn {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-muted);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s ease;
}

.cmd-btn:hover:not(:disabled) {
  background: var(--bg-hover);
  color: var(--text);
  transform: translateY(-1px);
}

.cmd-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.cmd-btn.active {
  border-color: var(--purple);
  color: white;
}

.cmd-btn.active.chat {
  background: var(--blue);
  border-color: var(--blue);
}

.cmd-btn.active.lexicon {
  background: var(--green);
  border-color: var(--green);
}

.cmd-btn.active.metrics {
  background: #f97316;
  border-color: #f97316;
}

.record-wrap {
  position: relative;
}

.record-btn {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  border: none;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s ease;
  position: relative;
}

.record-btn.off {
  background: white;
  color: #0a0a0a;
}

.record-btn.off:hover {
  transform: scale(1.05);
  box-shadow: 0 8px 30px rgba(255, 255, 255, 0.2);
}

.record-btn.on {
  background: var(--red);
  color: white;
}

.record-btn.on:hover {
  background: #dc2626;
}

.ping {
  position: absolute;
  width: 100%;
  height: 100%;
  border-radius: 50%;
  border: 2px solid var(--red);
  animation: ping 1.5s infinite;
}

@keyframes ping {
  0% { transform: scale(1); opacity: 0.5; }
  100% { transform: scale(1.3); opacity: 0; }
}

.mic-icon {
  color: #0a0a0a;
}

.stop-icon {
  color: white;
}

/* ==================== Drawer ==================== */
.drawer {
  position: fixed;
  top: 0;
  right: 0;
  width: 400px;
  height: 100vh;
  z-index: 45;
  background: rgba(12, 12, 12, 0.98);
  border-left: 1px solid var(--border);
  backdrop-filter: blur(16px);
  display: flex;
  flex-direction: column;
  animation: slideIn 0.3s ease;
}

@keyframes slideIn {
  from { transform: translateX(100%); }
  to { transform: translateX(0); }
}

.drawer-header {
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 20px;
  border-bottom: 1px solid var(--border);
}

.drawer-title {
  display: flex;
  align-items: center;
  gap: 12px;
  font-weight: 600;
}

.drawer-chip {
  width: 32px;
  height: 32px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
}

.drawer-chip.chat {
  background: rgba(59, 130, 246, 0.2);
  color: #93c5fd;
}

.drawer-chip.lexicon {
  background: rgba(34, 197, 94, 0.2);
  color: #86efac;
}

.drawer-chip.metrics {
  background: rgba(249, 115, 22, 0.2);
  color: #fdba74;
}

.ghost-btn {
  width: 40px;
  height: 40px;
  border-radius: 10px;
  border: 1px solid var(--border);
  background: transparent;
  color: var(--text-muted);
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s ease;
}

.ghost-btn:hover {
  background: var(--bg-hover);
  color: var(--text);
}

.drawer-body {
  flex: 1;
  overflow-y: auto;
  padding: 20px;
}

/* Chat */
.drawer-chat {
  display: flex;
  flex-direction: column;
}

.chat-list {
  flex: 1;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 16px;
  padding-bottom: 16px;
}

.chat-row {
  display: flex;
  gap: 10px;
}

.chat-row.user {
  flex-direction: row-reverse;
}

.avatar {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.avatar.ai {
  background: linear-gradient(135deg, var(--purple), #6366f1);
}

.avatar.user {
  background: #374151;
}

.dot-small {
  width: 8px;
  height: 8px;
  background: white;
  border-radius: 50%;
}

.bubble {
  max-width: 80%;
  padding: 12px 16px;
  border-radius: 16px;
  font-size: 14px;
  line-height: 1.5;
}

.bubble.ai {
  background: var(--bg-card);
  border: 1px solid var(--border);
  color: var(--text);
}

.bubble.user {
  background: var(--purple);
  color: white;
}

.bubble-meta {
  margin-top: 8px;
  font-size: 11px;
  color: var(--text-dim);
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.chat-input {
  display: flex;
  gap: 8px;
  padding-top: 16px;
  border-top: 1px solid var(--border);
}

.input-wrap {
  flex: 1;
  position: relative;
}

.input-icon {
  position: absolute;
  left: 12px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--text-dim);
}

.input-wrap input {
  width: 100%;
  padding: 12px 12px 12px 40px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text);
  font-size: 14px;
}

.input-wrap input:focus {
  outline: none;
  border-color: var(--purple);
}

.input-wrap input::placeholder {
  color: var(--text-dim);
}

.send-btn {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  background: var(--purple);
  border: none;
  color: white;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s ease;
}

.send-btn:hover:not(:disabled) {
  background: #7c3aed;
}

.send-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Lexicon */
.drawer-lexicon {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.stats-grid {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 10px;
}

.stat-card {
  padding: 12px;
  border-radius: 12px;
  background: var(--bg-card);
  border: 1px solid var(--border);
  text-align: center;
}

.stat-label {
  font-size: 10px;
  text-transform: uppercase;
  color: var(--text-dim);
  letter-spacing: 0.5px;
  margin: 0 0 4px;
}

.stat-value {
  font-size: 16px;
  font-family: ui-monospace, monospace;
  color: var(--text);
  margin: 0;
}

.search-row {
  position: relative;
}

.search-row input {
  width: 100%;
  padding: 10px 12px 10px 36px;
  border-radius: 10px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text);
  font-size: 13px;
}

.lex-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.lex-card {
  padding: 14px;
  border-radius: 12px;
  background: var(--bg-card);
  border: 1px solid var(--border);
}

.lex-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.lex-row h4 {
  margin: 0;
  font-size: 15px;
  color: var(--text);
}

.freq {
  font-size: 11px;
  color: var(--text-dim);
  font-family: ui-monospace, monospace;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.status-dot.ok {
  background: var(--green);
}

.bar {
  height: 6px;
  background: var(--bg-hover);
  border-radius: 999px;
  overflow: hidden;
  margin-top: 10px;
}

.bar-fill {
  height: 100%;
  border-radius: 999px;
  transition: width 0.3s ease;
}

.bar-fill.ok {
  background: linear-gradient(90deg, var(--green), #a3e635);
}

.mini-bars {
  display: flex;
  align-items: flex-end;
  gap: 3px;
  height: 60px;
  margin-top: 12px;
}

.mini-bar {
  flex: 1;
  min-height: 4px;
  background: linear-gradient(180deg, var(--green), #15803d);
  border-radius: 2px 2px 0 0;
}

.mini-bar.chat {
  background: linear-gradient(180deg, #60a5fa, #2563eb);
}

.empty-placeholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: 40px 20px;
  text-align: center;
  color: var(--text-dim);
  border: 1px dashed var(--border);
  border-radius: 12px;
}

.empty-placeholder p {
  margin: 12px 0 0;
  font-size: 13px;
}

/* ==================== Modal ==================== */
.overlay {
  position: fixed;
  inset: 0;
  z-index: 60;
  background: rgba(0, 0, 0, 0.7);
  backdrop-filter: blur(4px);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  animation: fadeIn 0.2s ease;
}

@keyframes fadeIn {
  from { opacity: 0; }
  to { opacity: 1; }
}

.modal {
  width: 100%;
  max-width: 600px;
  max-height: 80vh;
  background: var(--bg-elevated);
  border: 1px solid var(--border);
  border-radius: 24px;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  animation: scaleIn 0.2s ease;
}

@keyframes scaleIn {
  from { transform: scale(0.95); opacity: 0; }
  to { transform: scale(1); opacity: 1; }
}

.settings-modal {
  max-width: 800px;
}

.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 20px 24px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-card);
}

.modal-title {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 18px;
  font-weight: 600;
}

.modal-body {
  flex: 1;
  overflow-y: auto;
  padding: 24px;
}

.modal-footer {
  padding: 16px 24px;
  border-top: 1px solid var(--border);
  display: flex;
  justify-content: flex-end;
}

/* Settings */
.settings-body {
  display: flex;
  flex: 1;
  overflow: hidden;
}

.settings-tabs {
  width: 180px;
  padding: 16px;
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.tab {
  width: 100%;
  padding: 12px 16px;
  border-radius: 12px;
  border: 1px solid transparent;
  background: transparent;
  color: var(--text-muted);
  font-size: 14px;
  text-align: left;
  cursor: pointer;
  transition: all 0.2s ease;
}

.tab:hover {
  background: var(--bg-hover);
  color: var(--text);
}

.tab.active {
  background: rgba(139, 92, 246, 0.15);
  border-color: rgba(139, 92, 246, 0.3);
  color: var(--text);
}

.settings-content {
  flex: 1;
  padding: 24px;
  overflow-y: auto;
}

.settings-section {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.label {
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-dim);
}

.mt-4 {
  margin-top: 16px;
}

.input {
  width: 100%;
  padding: 12px 16px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text);
  font-size: 14px;
}

.input:focus {
  outline: none;
  border-color: var(--purple);
}

.textarea {
  width: 100%;
  padding: 12px 16px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text);
  font-size: 14px;
  resize: vertical;
  font-family: inherit;
}

.textarea:focus {
  outline: none;
  border-color: var(--purple);
}

.model-grid {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 10px;
}

.model-btn {
  padding: 14px 16px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text);
  font-size: 13px;
  text-align: left;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: space-between;
  transition: all 0.2s ease;
}

.model-btn:hover {
  background: var(--bg-hover);
  border-color: var(--border-strong);
}

.model-btn.active {
  border-color: var(--green);
  background: rgba(34, 197, 94, 0.1);
}

.active-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--green);
  box-shadow: 0 0 8px var(--green-glow);
}

.switches {
  display: grid;
  grid-template-columns: repeat(2, 1fr);
  gap: 12px;
}

.switch-label {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 16px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text);
  font-size: 13px;
  cursor: pointer;
  transition: all 0.2s ease;
}

.switch-label:hover {
  background: var(--bg-hover);
}

.switch-label input {
  accent-color: var(--purple);
}

.primary-btn {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 12px 20px;
  border-radius: 12px;
  background: var(--purple);
  border: none;
  color: white;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
}

.primary-btn:hover {
  background: #7c3aed;
  transform: translateY(-1px);
}

/* Managed API Notice */
.managed-api-notice {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  text-align: center;
  padding: 40px 20px;
  border: 1px dashed var(--border);
  border-radius: 16px;
  background: var(--bg-card);
}

.managed-api-notice .notice-icon {
  width: 64px;
  height: 64px;
  border-radius: 50%;
  background: rgba(139, 92, 246, 0.15);
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--purple);
  margin-bottom: 16px;
}

.managed-api-notice h4 {
  font-size: 16px;
  font-weight: 600;
  color: var(--text);
  margin: 0 0 8px;
}

.managed-api-notice p {
  font-size: 13px;
  color: var(--text-muted);
  margin: 0;
  max-width: 300px;
  line-height: 1.5;
}

/* History */
.history-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.history-item {
  padding: 16px;
  border-radius: 12px;
  border: 1px solid var(--border);
  background: var(--bg-card);
}

.history-title {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.history-title strong {
  font-size: 14px;
  color: var(--text);
}

.history-title span {
  font-size: 12px;
  color: var(--text-dim);
}

.history-summary {
  font-size: 13px;
  color: var(--text-muted);
  margin-bottom: 12px;
}

.history-actions {
  display: flex;
  gap: 8px;
}

.pill-btn {
  padding: 8px 14px;
  border-radius: 8px;
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text);
  font-size: 12px;
  cursor: pointer;
  transition: all 0.2s ease;
}

.pill-btn:hover {
  background: var(--bg-hover);
  border-color: var(--border-strong);
}

/* Scrollbar */
::-webkit-scrollbar {
  width: 6px;
}

::-webkit-scrollbar-track {
  background: transparent;
}

::-webkit-scrollbar-thumb {
  background: var(--border);
  border-radius: 3px;
}

::-webkit-scrollbar-thumb:hover {
  background: var(--border-strong);
}
</style>
