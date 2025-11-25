<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { askRag } from '../api'
import type { RagAskResponse, RagConfig } from '../api'
import { formatDuration } from '../utils/format'
import { lexSnapshot, type LexSnapshot } from '../utils/lexicon'
import { getMetrics, getMetricsByKind, type MetricEvent } from '../utils/metrics'
import { listSessions, getSessionMeta } from '../db'
import { emitProCommand, onProState, type ProStateSnapshot } from './bridge'


type Panel = 'none' | 'chat' | 'lexicon' | 'metrics'
type SettingsTab = 'general' | 'prompts' | 'experimental' | 'api'

const props = defineProps<{ onBackToClassic?: () => void }>()

const rightPanel = ref<Panel>('none')
const showSettings = ref(false)
const showHistory = ref(false)
const settingsTab = ref<SettingsTab>('general')
const streamRef = ref<HTMLElement | null>(null)

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
interface ChatMessage { role: 'user' | 'assistant'; content: string; meta?: { tokens?: string; latency?: string; model?: string } }
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
onMounted(() => {
  lexUpdated()
  const handler = () => lexUpdated()
  window.addEventListener('dt-lex-updated', handler as EventListener)
  onUnmounted(() => window.removeEventListener('dt-lex-updated', handler as EventListener))
})

// Metrics
const metrics = ref<MetricEvent[]>([])
const metricsTranslate = ref<MetricEvent[]>([])
const metricsChat = ref<MetricEvent[]>([])
const refreshMetrics = () => {
  metrics.value = getMetrics()
  metricsTranslate.value = getMetricsByKind('translation', 20)
  metricsChat.value = getMetricsByKind('chat', 20)
}
onMounted(() => {
  refreshMetrics()
  const handler = () => refreshMetrics()
  window.addEventListener('dt-metrics', handler as EventListener)
  onUnmounted(() => window.removeEventListener('dt-metrics', handler as EventListener))
})

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

// Stream items computed from live snapshot
const elapsedLabel = computed(() => {
  const s = snapshot.value.elapsedTime || 0
  const m = Math.floor(s / 60)
  const ss = s % 60
  return `${String(m).padStart(2, '0')}:${String(ss).padStart(2, '0')}`
})

const isRecording = computed(() => snapshot.value.isTranscribing || snapshot.value.isInitializing)

const streamItems = computed(() =>
  snapshot.value.lines.map((line) => {
    const start = line.confirmedSegments[0]?.startTime ?? 0
    const translation = snapshot.value.translations
      .filter((t) => t.speaker === line.speaker && Math.abs(t.startTime - start) < 1.2)
      .at(-1)
      ?? snapshot.value.translations.filter((t) => t.speaker === line.speaker).at(-1)
    const confirmedText = line.confirmedSegments.map((s) => s.text).join('\n')
    const state: 'streaming' | 'confirmed' | 'translated' =
      line.partialText ? 'streaming' : translation ? 'translated' : 'confirmed'
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

onMounted(() => {
  loadSettings()
  loadChatHistory()
  const off = onProState((state) => {
    snapshot.value = state
  })
  scrollToBottom()
  onUnmounted(() => off())
})

watch(
  () => snapshot.value.lines,
  () => scrollToBottom(),
)

const formatTimestamp = (seconds: number) => {
  const m = Math.floor(seconds / 60)
  const s = Math.floor(seconds % 60)
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

// Actions via bridge
const start = () => emitProCommand({ type: 'start' })
const stop = () => emitProCommand({ type: 'stop' })
const resume = () => emitProCommand({ type: 'continue' })
const pause = () => emitProCommand({ type: 'pause-toggle' })
const downloadAudio = () => emitProCommand({ type: 'download-audio' })
const downloadTranscript = () => emitProCommand({ type: 'download-transcript' })
const downloadTranslation = () => emitProCommand({ type: 'download-translation' })
const openClassicSettings = () => emitProCommand({ type: 'open-settings' })
const openClassicHistory = () => emitProCommand({ type: 'open-history' })
</script>

<template>
  <div class="pro-root">
    <div class="ambient ambient-1" />
    <div class="ambient ambient-2" />

    <!-- Header -->
    <header class="pro-header">
      <div class="brand-pill">
        <span class="dot" :class="isRecording ? 'dot--on' : 'dot--off'" />
        <span class="brand-text">DreamTrans <span class="brand-sub">PRO</span></span>
      </div>
      <div class="header-actions">
        <button class="circle-btn" title="History" @click="showHistory = true">
          <HistoryIcon />
        </button>
        <button class="circle-btn" title="Settings" @click="showSettings = true">
          <SettingsIcon />
        </button>
        <button v-if="props.onBackToClassic" class="pill-btn" @click="props.onBackToClassic?.()">
          返回经典版
        </button>
      </div>
    </header>

    <!-- Main Stream -->
    <main ref="streamRef" class="stream" :class="rightPanel !== 'none' ? 'stream--offset' : ''">
      <div class="stream-inner">
        <div v-if="(snapshot.hiddenCounts?.transcripts || 0) > 0" class="hint">
          仅显示最新片段 · 已隐藏 {{ snapshot.hiddenCounts?.transcripts }} 行
        </div>
        <article
          v-for="(item, idx) in streamItems"
          :key="item.id"
          class="line"
          :class="[{ 'line--live': !!item.partial }, { 'line--hover': !item.partial }]"
        >
          <div v-if="idx !== 0" class="connector" />
          <div class="meta">
            <span class="speaker" :class="item.speaker === 'Speaker A' ? 'a' : 'b'">{{ item.speaker }}</span>
            <span class="time">{{ formatTimestamp(item.start) }}</span>
          </div>
          <div class="card" :class="item.partial ? 'card--live' : ''">
            <div class="line-top">
              <span
                class="state-badge"
                :class="{
                  'state-stream': item.state === 'streaming',
                  'state-confirmed': item.state === 'confirmed',
                  'state-translated': item.state === 'translated',
                }"
              >
                {{ item.state === 'streaming' ? '流式' : item.state === 'translated' ? '已翻译' : '待翻译' }}
              </span>
            </div>
            <h3 class="text">
              {{ item.text }}
              <span v-if="item.partial" class="blink" />
            </h3>
            <div class="translation" :class="item.partial ? 'translation--accent' : ''">
              <p v-if="item.translation" class="translation-text">
                {{ item.translation }}
                <span v-if="item.translationPartial" class="tag-soft">partial</span>
              </p>
              <div v-else class="translation-placeholder">
                <span v-if="item.partial" class="pulse-dots"><span /><span /><span /></span>
                <div v-else class="skeleton">
                  <div class="sk sk-1" />
                  <div class="sk sk-2" />
                  <div class="loader">
                    <LoaderIcon />
                    <span class="loader-text">AI Translating...</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </article>
      </div>
    </main>

    <!-- Command Bar -->
    <div class="command" :class="rightPanel !== 'none' ? 'command--offset' : ''">
      <div class="command-inner">
        <div class="cmd-group">
          <button :class="['cmd-btn', rightPanel === 'chat' ? 'active chat' : '']" title="AI Chat" @click="rightPanel = rightPanel === 'chat' ? 'none' : 'chat'">
            <MessageIcon />
          </button>
          <button :class="['cmd-btn', rightPanel === 'lexicon' ? 'active lexicon' : '']" title="Lexicon" @click="rightPanel = rightPanel === 'lexicon' ? 'none' : 'lexicon'">
            <BookIcon />
          </button>
          <button :class="['cmd-btn', rightPanel === 'metrics' ? 'active metrics' : '']" title="Performance" @click="rightPanel = rightPanel === 'metrics' ? 'none' : 'metrics'">
            <StatsIcon />
          </button>
        </div>
        <div class="record-wrap">
          <button class="record-btn" :class="isRecording ? 'on' : 'off'" @click="isRecording ? pause() : start()" title="Record">
            <span v-if="isRecording" class="ping" />
            <span v-if="isRecording" class="square" />
            <MicIcon v-else class="mic" />
          </button>
        </div>
        <div class="cmd-group">
          <button class="cmd-btn" title="Stop" @click="stop"><StopIcon /></button>
          <button class="cmd-btn" title="Resume" @click="resume"><PlayIcon /></button>
          <button class="cmd-btn" title="Download audio" @click="downloadAudio"><DownloadIcon /></button>
          <button class="cmd-btn" title="下载原文" @click="downloadTranscript"><DocIcon /></button>
          <button class="cmd-btn" title="下载翻译" @click="downloadTranslation"><TranslateIcon /></button>
        </div>
      </div>
    </div>

    <!-- Side Drawer -->
    <aside v-if="rightPanel !== 'none'" class="drawer">
      <header class="drawer-header">
        <div class="drawer-title">
          <span class="drawer-chip" :class="rightPanel">{{ rightPanel === 'chat' ? 'AI' : rightPanel === 'lexicon' ? 'LX' : 'PF' }}</span>
          <span>{{ rightPanel === 'chat' ? 'AI Assistant' : rightPanel === 'lexicon' ? 'Lexicon' : 'Performance' }}</span>
        </div>
        <button class="ghost-btn" @click="rightPanel = 'none'"><XIcon /></button>
      </header>
      <div v-if="rightPanel === 'chat'" class="drawer-body drawer-chat">
        <div class="chat-list">
          <div v-for="(msg, i) in chatMessages" :key="i" class="chat-row" :class="msg.role === 'user' ? 'user' : 'ai'">
            <div class="avatar" :class="msg.role === 'ai' ? 'ai' : 'user'">
              <SparklesIcon v-if="msg.role === 'ai'" />
              <div v-else class="dot-small" />
            </div>
            <div class="bubble-chat" :class="msg.role === 'ai' ? 'ai' : 'user'">
              <div>{{ msg.content }}</div>
              <div v-if="msg.meta" class="meta">
                <span v-if="msg.meta.model">model {{ msg.meta.model }}</span>
                <span v-if="msg.meta.tokens">tokens {{ msg.meta.tokens }}</span>
                <span v-if="msg.meta.latency">latency {{ msg.meta.latency }}</span>
              </div>
            </div>
          </div>
          <div v-if="!chatMessages.length" class="empty-placeholder">
            <span class="icon">🧠</span>
            <p>结合上下文的问答助手，输入问题开始对话。</p>
          </div>
        </div>
        <div class="chat-input">
          <div class="input-wrap">
            <SearchIcon class="input-icon" />
            <input v-model="chatInput" type="text" :placeholder="chatLoading ? '正在生成...' : 'Ask about the context...'" :disabled="chatLoading" @keyup.enter="sendChat()" />
          </div>
          <button class="send" :disabled="chatLoading" @click="sendChat()"><ArrowIcon /></button>
        </div>
      </div>
      <div v-else-if="rightPanel === 'lexicon'" class="drawer-body drawer-lexicon">
        <div class="lex-stats">
          <div class="stat"><p class="stat-label">Tokens</p><p class="stat-value">{{ lex?.total || 0 }}</p></div>
          <div class="stat"><p class="stat-label">Words</p><p class="stat-value">{{ lex?.words.length || 0 }}</p></div>
          <div class="stat"><p class="stat-label">Bigrams</p><p class="stat-value">{{ lex?.bigrams.length || 0 }}</p></div>
        </div>
        <div class="search-row">
          <SearchIcon class="input-icon" />
          <input type="text" placeholder="Search words..." disabled />
        </div>
        <div class="lex-list">
          <div v-for="(item, i) in (lex?.words || []).slice().sort((a,b)=>b[1]-a[1]).slice(0, 20)" :key="i" class="lex-card">
            <div class="lex-row">
              <div>
                <h4>{{ item[0] }}</h4>
                <span class="freq">freq: {{ item[1] }}</span>
              </div>
              <span class="status ok" />
            </div>
            <div class="bar"><div class="bar-fill ok" :style="{ width: `${Math.min(item[1] * 2, 100)}%` }" /></div>
          </div>
          <div v-if="!lex || (lex.words.length === 0)" class="empty-placeholder">
            <span class="icon">📚</span>
            <p>等待转写累积后展示词频。</p>
          </div>
        </div>
      </div>
      <div v-else class="drawer-body drawer-lexicon">
        <div class="lex-stats">
          <div class="stat"><p class="stat-label">Latest</p><p class="stat-value">{{ metrics[0]?.latency_ms ? formatDuration(metrics[0].latency_ms) : '—' }}</p></div>
          <div class="stat"><p class="stat-label">Translates</p><p class="stat-value">{{ metricsTranslate.length }}</p></div>
          <div class="stat"><p class="stat-label">Chat</p><p class="stat-value">{{ metricsChat.length }}</p></div>
        </div>
        <div class="lex-list">
          <div v-if="metricsTranslate.length" class="lex-card">
            <div class="lex-row"><h4>Translate Latency (ms)</h4></div>
            <div class="mini-bars">
              <span v-for="(m, i) in metricsTranslate" :key="i" class="mini-bar" :style="{ height: `${Math.min((m.latency_ms || 0) / 20, 100)}%` }" />
            </div>
          </div>
          <div v-if="metricsChat.length" class="lex-card">
            <div class="lex-row"><h4>Chat Latency (ms)</h4></div>
            <div class="mini-bars">
              <span v-for="(m, i) in metricsChat" :key="i" class="mini-bar chat" :style="{ height: `${Math.min((m.latency_ms || 0) / 20, 100)}%` }" />
            </div>
          </div>
          <div v-if="!metricsTranslate.length && !metricsChat.length" class="empty-placeholder">
            <span class="icon">📊</span>
            <p>暂无性能数据。</p>
          </div>
        </div>
      </div>
    </aside>

    <!-- Settings -->
    <div v-if="showSettings" class="overlay">
      <div class="settings-card">
        <div class="settings-header">
          <div class="settings-title">
            <SettingsIcon /> <span>Settings</span>
          </div>
          <button class="ghost-btn" @click="showSettings = false"><XIcon /></button>
        </div>
        <div class="settings-body">
          <div class="settings-tabs">
            <button v-for="tab in ['general','prompts','experimental','api']" :key="tab" :class="['tab', settingsTab === tab ? 'active' : '']" @click="settingsTab = tab as SettingsTab">
              {{ tab.charAt(0).toUpperCase() + tab.slice(1) }}
            </button>
          </div>
          <div class="settings-content">
            <div v-if="settingsTab === 'general'" class="settings-section">
              <label class="label">Translation Model</label>
              <div class="grid-2">
                <button v-for="m in ['gpt-4o-2024','gpt-4.1-mini','claude-3.5','gemini-pro']" :key="m" :class="['pill', settings.modelTranslate === m ? 'pill--active' : '']" @click="settings.modelTranslate = m">
                  <span>{{ m }}</span>
                  <span v-if="m === 'gpt-4.1-mini'" class="dot dot--green" />
                </button>
              </div>
              <label class="label mt">Context Window</label>
              <input type="range" min="4" max="128" class="range" />
              <div class="range-hint"><span>4k</span><span>128k</span></div>
            </div>
            <div v-else-if="settingsTab === 'prompts'" class="settings-section">
              <label class="label">Chat Prompt</label>
              <textarea v-model="settings.promptChat" rows="3" />
              <label class="label">Translation Prompt</label>
              <textarea v-model="settings.promptTranslate" rows="4" />
              <label class="label">Summary Prompt</label>
              <textarea v-model="settings.promptSummary" rows="3" />
            </div>
            <div v-else-if="settingsTab === 'experimental'" class="settings-section switches">
              <label><input type="checkbox" v-model="settings.expSmart" /> Smart Algorithm</label>
              <label><input type="checkbox" v-model="settings.expStreaming" /> Streaming Output</label>
              <label><input type="checkbox" v-model="settings.expTypewriter" /> Typewriter</label>
              <label><input type="checkbox" v-model="settings.expBilingual" /> Bilingual</label>
              <label><input type="checkbox" v-model="settings.expSummary" /> Summarization</label>
              <label><input type="checkbox" v-model="settings.expEmbeddings" /> Embeddings</label>
            </div>
            <div v-else class="settings-section">
              <label class="label">API Base</label>
              <input v-model="settings.apiBase" type="text" />
              <label class="label">API Key</label>
              <input v-model="settings.apiKey" type="password" />
            </div>
          </div>
        </div>
        <div class="settings-footer">
          <button class="primary" @click="saveSettings"><SaveIcon /> 保存</button>
        </div>
      </div>
    </div>

    <!-- History -->
    <div v-if="showHistory" class="overlay">
      <div class="settings-card">
        <div class="settings-header">
          <div class="settings-title">
            <HistoryIcon /> <span>历史会话</span>
          </div>
          <button class="ghost-btn" @click="showHistory = false"><XIcon /></button>
        </div>
        <div class="settings-content history">
          <div v-if="!sessions.length" class="empty-placeholder">
            <span class="icon">🗃</span>
            <p>暂无历史记录</p>
          </div>
          <div v-else class="history-list">
            <div v-for="s in sessions" :key="s.id" class="history-item">
              <div class="history-title">
                <strong>{{ sessionMeta[s.id]?.title || s.id }}</strong>
                <span>{{ new Date(s.timestamp).toLocaleString() }}</span>
              </div>
              <div class="history-summary">{{ sessionMeta[s.id]?.summary || '暂无摘要' }}</div>
              <div class="history-actions">
                <button class="pill" @click="emitProCommand({ type: 'continue' }); showHistory = false">继续当前</button>
                <button class="pill" @click="openClassicHistory(); showHistory = false">在经典版查看</button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.pro-root {
  --bg: #0a0c15;
  --panel: rgba(255, 255, 255, 0.04);
  --panel-strong: rgba(255, 255, 255, 0.08);
  --border: rgba(255, 255, 255, 0.1);
  --muted: rgba(226, 232, 240, 0.68);
  --text: #e8ecf5;
  --accent: #8b5cf6;
  --accent-2: #22d3ee;
  position: relative;
  min-height: 100vh;
  background:
    radial-gradient(1200px 700px at 12% -10%, rgba(91, 33, 182, 0.38) 0%, transparent 60%),
    radial-gradient(900px 620px at 90% 10%, rgba(14, 165, 233, 0.32) 0%, transparent 60%),
    #06070f;
  color: var(--text);
  font-family: 'Inter', system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  overflow: hidden;
}

.ambient {
  position: absolute;
  width: 60vw;
  height: 60vw;
  filter: blur(180px);
  opacity: 0.4;
  z-index: 0;
}

.ambient-1 {
  top: -30%;
  left: -20%;
  background: #4f46e5;
}

.ambient-2 {
  bottom: -35%;
  right: -15%;
  background: #0ea5e9;
}

.pro-header {
  position: sticky;
  top: 0;
  z-index: 10;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 18px 28px;
  backdrop-filter: blur(14px);
  background: rgba(6, 7, 15, 0.78);
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}

.brand-pill {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  padding: 10px 14px;
  border-radius: 999px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.04);
  box-shadow: 0 10px 32px rgba(0, 0, 0, 0.4);
}

.dot {
  width: 10px;
  height: 10px;
  border-radius: 999px;
  background: #22c55e;
  box-shadow: 0 0 12px rgba(34, 197, 94, 0.7);
}

.dot--on {
  background: #22c55e;
}

.dot--off {
  background: #6b7280;
  box-shadow: none;
}

.brand-text {
  display: inline-flex;
  align-items: baseline;
  gap: 8px;
  font-weight: 700;
  letter-spacing: 0.2px;
  color: var(--text);
}

.brand-sub {
  font-size: 12px;
  color: #c084fc;
  letter-spacing: 0.4px;
}

.header-actions {
  display: inline-flex;
  gap: 10px;
  align-items: center;
}

.ghost-btn {
  border: 1px solid rgba(255, 255, 255, 0.14);
  background: rgba(255, 255, 255, 0.06);
  color: #e2e8f0;
  border-radius: 12px;
  padding: 8px 10px;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  min-width: 40px;
  height: 40px;
  transition: all 0.15s ease;
}

.ghost-btn:hover {
  border-color: rgba(255, 255, 255, 0.22);
  color: #fff;
}

.icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.circle-btn {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  border: 1px solid rgba(255, 255, 255, 0.14);
  background: rgba(255, 255, 255, 0.05);
  color: #e0e7ff;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s ease;
}

.circle-btn:hover {
  transform: translateY(-1px);
  border-color: rgba(255, 255, 255, 0.24);
  color: #fff;
}

.pill-btn {
  padding: 10px 14px;
  border-radius: 12px;
  background: linear-gradient(135deg, #a855f7, #22d3ee);
  border: none;
  color: #0b0d16;
  font-weight: 700;
  cursor: pointer;
  box-shadow: 0 10px 30px rgba(168, 85, 247, 0.3);
  transition: transform 0.15s ease, box-shadow 0.15s ease;
}

.pill-btn:hover {
  transform: translateY(-1px);
  box-shadow: 0 12px 34px rgba(168, 85, 247, 0.35);
}

.stream {
  position: relative;
  z-index: 5;
  height: calc(100vh - 86px);
  overflow-y: auto;
  padding: 100px 0 180px;
  display: flex;
  justify-content: center;
  transition: margin-right 0.25s ease;
}

.stream--offset {
  margin-right: 380px;
}

.stream-inner {
  width: min(1040px, 100%);
  padding: 0 18px;
  display: flex;
  flex-direction: column;
  gap: 36px;
}

.hint {
  padding: 10px 12px;
  border-radius: 12px;
  border: 1px dashed rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.04);
  color: var(--muted);
  font-size: 13px;
}

.line {
  position: relative;
  display: grid;
  gap: 10px;
}

.line--hover:hover .card {
  border-color: rgba(255, 255, 255, 0.18);
}

.line--live .card {
  border-color: rgba(139, 92, 246, 0.6);
  box-shadow: 0 12px 46px rgba(99, 102, 241, 0.3);
}

.connector {
  position: absolute;
  left: 36px;
  top: -26px;
  width: 2px;
  height: 26px;
  background: linear-gradient(to bottom, transparent, rgba(255, 255, 255, 0.2));
}

.meta {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  font-size: 12px;
  color: var(--muted);
}

.speaker {
  padding: 4px 10px;
  border-radius: 10px;
  font-weight: 700;
  letter-spacing: 0.2px;
  color: #0f172a;
}

.speaker.a {
  background: #e0e7ff;
  color: #1e1b4b;
}

.speaker.b {
  background: #cffafe;
  color: #0f172a;
}

.time {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
  color: var(--muted);
}

.card {
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 18px;
  padding: 14px 16px;
  background: rgba(255, 255, 255, 0.03);
  backdrop-filter: blur(10px);
  transition: border-color 0.2s ease, transform 0.2s ease, box-shadow 0.2s ease;
}

.card--live {
  border-color: rgba(168, 85, 247, 0.55);
}

.line-top {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 6px;
}

.state-badge {
  padding: 4px 8px;
  border-radius: 10px;
  font-size: 12px;
  font-weight: 600;
  border: 1px solid transparent;
}

.state-stream {
  background: rgba(168, 85, 247, 0.2);
  color: #e9d5ff;
  border-color: rgba(168, 85, 247, 0.45);
}

.state-confirmed {
  background: rgba(59, 130, 246, 0.18);
  color: #dbeafe;
  border-color: rgba(59, 130, 246, 0.35);
}

.state-translated {
  background: rgba(34, 197, 94, 0.18);
  color: #bbf7d0;
  border-color: rgba(34, 197, 94, 0.35);
}

.text {
  margin: 0;
  line-height: 1.55;
  color: #e5e7eb;
  letter-spacing: 0.1px;
}

.blink {
  display: inline-block;
  width: 10px;
  height: 18px;
  background: #c084fc;
  animation: blink 1s steps(1) infinite;
  vertical-align: middle;
  margin-left: 4px;
}

.translation {
  margin-top: 10px;
  padding: 12px 14px;
  border-radius: 12px;
  border: 1px dashed rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.02);
}

.translation--accent {
  border-color: rgba(168, 85, 247, 0.5);
  background: rgba(168, 85, 247, 0.07);
}

.translation-text {
  margin: 0;
  color: #cbd5e1;
  line-height: 1.6;
}

.tag-soft {
  margin-left: 8px;
  padding: 2px 8px;
  border-radius: 999px;
  border: 1px solid rgba(255, 255, 255, 0.18);
  font-size: 11px;
  color: #cbd5e1;
}

.translation-placeholder {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--muted);
}

.pulse-dots {
  display: inline-flex;
  gap: 6px;
}

.pulse-dots span {
  width: 9px;
  height: 9px;
  background: #a855f7;
  border-radius: 999px;
  animation: pulse 1s infinite alternate;
}

.pulse-dots span:nth-child(2) {
  animation-delay: 0.15s;
}

.pulse-dots span:nth-child(3) {
  animation-delay: 0.3s;
}

.skeleton {
  width: 100%;
}

.skeleton .sk {
  height: 10px;
  background: linear-gradient(90deg, rgba(168, 85, 247, 0.2), rgba(255, 255, 255, 0.05));
  border-radius: 6px;
  margin-bottom: 8px;
}

.skeleton .sk-1 {
  width: 100%;
}

.skeleton .sk-2 {
  width: 60%;
}

.loader {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  color: #a855f7;
  font-size: 12px;
}

.loader-text {
  font-family: ui-monospace, monospace;
}

.command {
  position: fixed;
  bottom: 20px;
  left: 50%;
  transform: translateX(-50%);
  width: min(1080px, calc(100% - 30px));
  z-index: 20;
}

.command--offset {
  width: min(1080px, calc(100% - 410px));
}

.command-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  background: rgba(12, 14, 24, 0.9);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 16px;
  padding: 10px 12px;
  box-shadow: 0 18px 46px rgba(0, 0, 0, 0.4);
  backdrop-filter: blur(12px);
}

.cmd-group {
  display: inline-flex;
  gap: 8px;
}

.cmd-btn {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.04);
  color: #e2e8f0;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: all 0.15s ease;
}

.cmd-btn:hover {
  transform: translateY(-1px);
  border-color: rgba(255, 255, 255, 0.18);
}

.cmd-btn.active {
  color: #fff;
  border-color: var(--accent);
  box-shadow: 0 8px 20px rgba(139, 92, 246, 0.28);
}

.cmd-btn.chat.active {
  background: #2563eb;
  border-color: #2563eb;
}

.cmd-btn.lexicon.active {
  background: #16a34a;
  border-color: #16a34a;
}

.cmd-btn.metrics.active {
  background: #f97316;
  border-color: #f97316;
}

.record-wrap {
  display: flex;
  align-items: center;
  justify-content: center;
}

.record-btn {
  width: 70px;
  height: 70px;
  border-radius: 999px;
  border: none;
  cursor: pointer;
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: transform 0.15s ease, box-shadow 0.15s ease;
}

.record-btn.on {
  background: #ef4444;
  box-shadow: 0 0 0 8px rgba(239, 68, 68, 0.18);
}

.record-btn.off {
  background: #f8fafc;
  color: #0f172a;
  border: 1px solid rgba(255, 255, 255, 0.12);
}

.record-btn:hover {
  transform: translateY(-1px);
}

.ping {
  position: absolute;
  width: 110%;
  height: 110%;
  border-radius: 999px;
  border: 1px solid rgba(239, 68, 68, 0.32);
  animation: pulse 1.4s infinite;
}

.square {
  width: 18px;
  height: 18px;
  background: #fff;
  border-radius: 4px;
}

.drawer {
  position: fixed;
  right: 0;
  top: 0;
  width: 380px;
  height: 100vh;
  background: rgba(8, 10, 18, 0.95);
  border-left: 1px solid rgba(255, 255, 255, 0.08);
  backdrop-filter: blur(12px);
  box-shadow: -18px 0 36px rgba(0, 0, 0, 0.35);
  z-index: 25;
  display: flex;
  flex-direction: column;
}

.drawer-header {
  height: 64px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 18px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
}

.drawer-title {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  color: #fff;
  font-weight: 600;
}

.drawer-chip {
  width: 32px;
  height: 32px;
  border-radius: 10px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  background: rgba(37, 99, 235, 0.25);
  color: #bfdbfe;
  font-weight: 700;
  text-transform: uppercase;
}

.drawer-chip.lexicon {
  background: rgba(34, 197, 94, 0.25);
  color: #bbf7d0;
}

.drawer-chip.metrics {
  background: rgba(249, 115, 22, 0.25);
  color: #fed7aa;
}

.drawer-body {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
}

.drawer-chat {
  display: flex;
  flex-direction: column;
}

.chat-list {
  flex: 1;
  overflow-y: auto;
  display: grid;
  gap: 12px;
  padding: 4px;
}

.chat-row {
  display: flex;
  gap: 10px;
  align-items: flex-start;
}

.chat-row.user {
  flex-direction: row-reverse;
}

.avatar {
  width: 32px;
  height: 32px;
  border-radius: 999px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  font-size: 14px;
  background: #475569;
}

.avatar.ai {
  background: linear-gradient(135deg, #a855f7, #6366f1);
}

.bubble-chat {
  max-width: 75%;
  padding: 12px 14px;
  border-radius: 14px;
  line-height: 1.5;
  font-size: 14px;
}

.bubble-chat.ai {
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.08);
  color: #e5e7eb;
}

.bubble-chat.user {
  background: #8b5cf6;
  color: #fff;
}

.bubble-chat .meta {
  margin-top: 6px;
  font-size: 11px;
  color: #cbd5e1;
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.chat-input {
  display: grid;
  grid-template-columns: 1fr 44px;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
}

.input-wrap {
  position: relative;
}

.input-icon {
  position: absolute;
  left: 10px;
  top: 50%;
  transform: translateY(-50%);
  color: var(--muted);
}

.chat-input input {
  width: 100%;
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 12px;
  padding: 12px 12px 12px 36px;
  color: #fff;
}

.chat-input .send {
  border-radius: 12px;
  background: #8b5cf6;
  color: #fff;
  border: none;
  cursor: pointer;
  transition: all 0.2s ease;
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.chat-input .send:hover {
  background: #7c3aed;
}

.search-row {
  position: relative;
  margin-bottom: 10px;
}

.search-row input {
  width: 100%;
  padding: 10px 12px 10px 34px;
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.05);
  color: #fff;
}

.lex-stats {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 10px;
  margin-bottom: 16px;
}

.stat {
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 10px;
  padding: 10px;
  text-align: center;
}

.stat-label {
  display: block;
  font-size: 11px;
  text-transform: uppercase;
  color: rgba(255, 255, 255, 0.6);
  letter-spacing: 0.6px;
}

.stat-value {
  font-family: ui-monospace, monospace;
  color: #fff;
}

.lex-list {
  display: grid;
  gap: 12px;
}

.lex-card {
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 12px;
  padding: 12px;
}

.lex-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.lex-row h4 {
  margin: 0;
  color: #fff;
}

.freq {
  font-size: 12px;
  color: rgba(255, 255, 255, 0.6);
}

.status {
  width: 10px;
  height: 10px;
  border-radius: 999px;
  display: inline-block;
}

.status.ok {
  background: #22c55e;
}

.bar {
  height: 8px;
  background: rgba(255, 255, 255, 0.08);
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
  background: linear-gradient(90deg, #22c55e, #a3e635);
}

.mini-bars {
  display: grid;
  grid-auto-flow: column;
  grid-auto-columns: 4px;
  align-items: end;
  gap: 3px;
  height: 80px;
}

.mini-bar {
  width: 4px;
  background: linear-gradient(180deg, #22c55e, #15803d);
  border-radius: 4px 4px 0 0;
}

.mini-bar.chat {
  background: linear-gradient(180deg, #60a5fa, #2563eb);
}

.overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.6);
  backdrop-filter: blur(6px);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  z-index: 40;
}

.settings-card {
  width: min(900px, 96vw);
  background: rgba(10, 12, 20, 0.96);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 20px;
  box-shadow: 0 28px 70px rgba(0, 0, 0, 0.45);
  display: flex;
  flex-direction: column;
  max-height: 88vh;
}

.settings-header {
  padding: 16px 18px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}

.settings-title {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  font-weight: 600;
}

.settings-body {
  display: grid;
  grid-template-columns: 180px 1fr;
}

.settings-tabs {
  border-right: 1px solid rgba(255, 255, 255, 0.06);
  padding: 12px;
  display: grid;
  gap: 8px;
}

.tab {
  width: 100%;
  padding: 12px;
  border-radius: 12px;
  background: transparent;
  border: 1px solid transparent;
  color: #cbd5e1;
  text-align: left;
  cursor: pointer;
}

.tab.active {
  background: rgba(139, 92, 246, 0.18);
  border-color: rgba(139, 92, 246, 0.4);
  color: #fff;
}

.settings-content {
  padding: 16px;
  display: grid;
  align-content: start;
}

.settings-section {
  display: grid;
  gap: 12px;
}

.label {
  font-size: 12px;
  letter-spacing: 0.4px;
  text-transform: uppercase;
  color: var(--muted);
}

.grid-2 {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 10px;
}

.pill {
  width: 100%;
  padding: 12px;
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.04);
  color: #e5e7eb;
  cursor: pointer;
  text-align: left;
}

.pill--active {
  border-color: rgba(34, 197, 94, 0.9);
  box-shadow: 0 0 0 1px rgba(34, 197, 94, 0.35);
}

.mt {
  margin-top: 6px;
}

.range {
  width: 100%;
}

.range-hint {
  display: flex;
  justify-content: space-between;
  color: var(--muted);
  font-size: 12px;
}

.switches {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 8px;
}

.switches label {
  display: inline-flex;
  gap: 8px;
  align-items: center;
  color: #e5e7eb;
}

.settings-footer {
  padding: 12px 16px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
  display: flex;
  justify-content: flex-end;
}

.primary {
  padding: 10px 16px;
  border-radius: 12px;
  background: #8b5cf6;
  color: #fff;
  border: none;
  cursor: pointer;
  font-weight: 600;
  display: inline-flex;
  gap: 8px;
  align-items: center;
}

.history {
  grid-template-columns: 1fr;
}

.history-list {
  display: grid;
  gap: 12px;
  padding: 12px;
}

.history-item {
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 12px;
  padding: 12px;
  background: rgba(255, 255, 255, 0.03);
}

.history-title {
  display: flex;
  justify-content: space-between;
  color: #e5e7eb;
  font-size: 14px;
}

.history-title span {
  color: #94a3b8;
  font-size: 12px;
}

.history-summary {
  color: #cbd5e1;
  margin-top: 6px;
  font-size: 13px;
}

.history-actions {
  display: flex;
  gap: 8px;
  margin-top: 10px;
}

.history-actions .pill {
  width: auto;
}

.empty-placeholder {
  width: 100%;
  padding: 32px 12px;
  text-align: center;
  color: rgba(255, 255, 255, 0.6);
  border: 1px dashed rgba(255, 255, 255, 0.14);
  border-radius: 14px;
  background: rgba(255, 255, 255, 0.02);
}

@keyframes blink {
  0%,
  50% {
    opacity: 1;
  }
  51%,
  100% {
    opacity: 0;
  }
}

@keyframes pulse {
  from {
    transform: scale(0.92);
    opacity: 0.7;
  }
  to {
    transform: scale(1.05);
    opacity: 1;
  }
}
</style>
