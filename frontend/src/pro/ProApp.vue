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
.pro-app {
  position: relative;
  height: 100vh;
  width: 100%;
  background: radial-gradient(1200px 600px at 20% -10%, #25134f 0%, transparent 60%),
    radial-gradient(1000px 600px at 100% 20%, #0a1f48 0%, transparent 60%),
    #080808;
  color: #f8fafc;
  font-family: 'Inter', system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  overflow: hidden;
}
.blob { position: absolute; width: 50%; height: 50%; border-radius: 9999px; filter: blur(120px); opacity: 0.5; animation: blob 8s ease-in-out infinite; }
.blob-1 { top: -20%; left: -10%; background: rgba(124, 58, 237, 0.35); }
.blob-2 { bottom: -20%; right: -10%; background: rgba(79, 70, 229, 0.3); animation-delay: 2s; }
.pro-header { position: absolute; inset: 0 0 auto 0; padding: 20px 24px; display: flex; align-items: center; justify-content: space-between; z-index: 20; }
.brand-chip { display: inline-flex; align-items: center; gap: 10px; background: rgba(255, 255, 255, 0.06); border: 1px solid rgba(255, 255, 255, 0.12); padding: 10px 14px; border-radius: 999px; backdrop-filter: blur(8px); }
.dot { width: 10px; height: 10px; border-radius: 999px; background: #22c55e; box-shadow: 0 0 10px rgba(34, 197, 94, 0.6); }
.dot--off { background: #6b7280; box-shadow: none; }
.brand-text { color: #e5e7eb; font-weight: 600; letter-spacing: 0.2px; display: grid; gap: 2px; }
.brand-line { display: inline-flex; align-items: baseline; gap: 8px; }
.strong { font-weight: 700; }
.tag { font-size: 11px; opacity: 0.6; }
.elapsed { font-size: 12px; opacity: 0.7; }
.header-actions { display: flex; gap: 8px; align-items: center; }
.ghost-btn { width: 40px; height: 40px; border-radius: 50%; border: 1px solid rgba(255, 255, 255, 0.12); background: rgba(255, 255, 255, 0.05); color: #cbd5e1; cursor: pointer; display: inline-flex; align-items: center; justify-content: center; transition: all 0.2s ease; }
.ghost-btn:hover { color: #fff; border-color: rgba(255, 255, 255, 0.2); }
.ghost-btn.strong { width: auto; padding: 0 14px; border-radius: 12px; font-weight: 600; }
.icon { width: 18px; height: 18px; stroke: currentColor; fill: none; stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
.stream { position: relative; z-index: 10; height: 100vh; overflow-y: auto; padding: 96px 0 180px; display: flex; justify-content: center; transition: margin-right 0.3s ease; }
.stream--narrow { margin-right: 400px; }
.stream-inner { width: min(960px, 100%); padding: 0 18px; display: flex; flex-direction: column; gap: 48px; }
.hidden-hint { color: rgba(255, 255, 255, 0.5); font-size: 13px; margin-bottom: 8px; }
.bubble { position: relative; transition: transform 0.4s ease, opacity 0.4s ease; }
.bubble--hoverable { opacity: 0.85; }
.bubble--hoverable:hover { transform: translateY(-4px); opacity: 1; }
.bubble--live { opacity: 1; transform: scale(1.01); }
.connect-line { position: absolute; top: -48px; left: 8px; width: 2px; height: 48px; background: linear-gradient(to bottom, transparent, rgba(255, 255, 255, 0.1)); }
.bubble-meta { display: flex; gap: 10px; align-items: center; margin-bottom: 10px; }
.speaker { padding: 4px 10px; border-radius: 8px; font-weight: 700; font-size: 12px; color: #0b0b0f; }
.speaker-a { background: #e2d9ff; }
.speaker-b { background: #cfe5ff; }
.timestamp { color: rgba(255, 255, 255, 0.4); font-size: 12px; font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace; }
.bubble-body { padding: 24px; border-radius: 24px; border: 1px solid rgba(255, 255, 255, 0.08); background: rgba(255, 255, 255, 0.02); backdrop-filter: blur(10px); box-shadow: 0 0 50px -16px rgba(124, 58, 237, 0.35); }
.bubble--hoverable .bubble-body:hover { background: rgba(255, 255, 255, 0.04); }
.bubble-title { font-size: 22px; letter-spacing: 0.2px; margin: 0; color: #e5e7eb; display: flex; align-items: center; gap: 10px; white-space: pre-wrap; }
.line-top { display: flex; justify-content: flex-end; margin-bottom: 6px; }
.state-badge { padding: 4px 8px; border-radius: 10px; font-size: 12px; font-weight: 600; }
.state-stream { background: rgba(147, 51, 234, 0.2); color: #c4b5fd; border: 1px solid rgba(147, 51, 234, 0.4); }
.state-confirmed { background: rgba(59, 130, 246, 0.18); color: #bfdbfe; border: 1px solid rgba(59, 130, 246, 0.35); }
.state-translated { background: rgba(34, 197, 94, 0.18); color: #bbf7d0; border: 1px solid rgba(34, 197, 94, 0.35); }
.bubble-translation { margin-top: 12px; padding-left: 14px; border-left: 2px solid rgba(255, 255, 255, 0.12); min-height: 28px; }
.bubble-translation.accent { border-color: rgba(124, 58, 237, 0.6); }
.translation-text { color: #cbd5e1; line-height: 1.6; font-size: 18px; }
.pill-soft { display: inline-block; margin-left: 8px; padding: 2px 8px; border-radius: 999px; background: rgba(124, 58, 237, 0.15); color: #c4b5fd; font-size: 12px; }
.placeholder { display: flex; align-items: center; gap: 10px; color: rgba(255, 255, 255, 0.5); min-height: 20px; }
.pulse-dots { display: inline-flex; gap: 6px; }
.pulse-dots span { width: 10px; height: 10px; background: #a855f7; border-radius: 999px; animation: pulse 1s infinite alternate; }
.pulse-dots span:nth-child(2) { animation-delay: 0.15s; }
.pulse-dots span:nth-child(3) { animation-delay: 0.3s; }
.skeleton { width: 100%; }
.skeleton div { height: 10px; background: linear-gradient(90deg, rgba(168, 85, 247, 0.2), rgba(255, 255, 255, 0.05)); border-radius: 6px; margin-bottom: 8px; }
.skeleton .loader { display: flex; align-items: center; gap: 8px; font-size: 12px; color: #a855f7; }
.spinner { width: 14px; height: 14px; border: 2px solid rgba(168, 85, 247, 0.3); border-top-color: rgba(168, 85, 247, 0.9); border-radius: 999px; animation: spin 1s linear infinite; }
.loader-text { font-family: ui-monospace, monospace; }
.blink { display: inline-block; width: 8px; height: 24px; background: #a855f7; animation: blink 1s steps(1) infinite; }
.dock { position: absolute; left: 50%; bottom: 32px; transform: translateX(-50%); z-index: 30; width: min(640px, 100%); padding: 0 20px; transition: transform 0.3s ease; }
.dock--offset { transform: translate(calc(-50% - 200px), 0); }
.dock-inner { background: rgba(26, 26, 26, 0.85); border: 1px solid rgba(255, 255, 255, 0.12); border-radius: 16px; padding: 10px 14px; display: flex; align-items: center; justify-content: space-between; gap: 10px; backdrop-filter: blur(12px); box-shadow: 0 24px 60px rgba(0, 0, 0, 0.35); }
.dock-group { display: inline-flex; gap: 8px; }
.dock-btn { width: 44px; height: 44px; border-radius: 14px; background: transparent; border: 1px solid rgba(255, 255, 255, 0.12); color: #cbd5e1; cursor: pointer; transition: all 0.2s ease; font-size: 18px; display: inline-flex; align-items: center; justify-content: center; }
.dock-btn:hover { border-color: rgba(255, 255, 255, 0.2); color: #fff; }
.dock-btn--active { color: #fff; }
.dock-btn--active.chat { background: #2563eb; border-color: #2563eb; }
.dock-btn--active.lexicon { background: #16a34a; border-color: #16a34a; }
.dock-btn--active.metrics { background: #f97316; border-color: #f97316; }
.record { position: relative; }
.record-btn { width: 72px; height: 72px; border-radius: 999px; border: none; cursor: pointer; position: relative; display: inline-flex; align-items: center; justify-content: center; transition: all 0.2s ease; }
.record-btn--on { background: rgba(248, 113, 113, 0.9); box-shadow: 0 0 0 8px rgba(248, 113, 113, 0.15); }
.record-btn--off { background: #fff; color: #111; }
.record-btn--on:hover, .record-btn--off:hover { transform: scale(1.04); }
.ping { position: absolute; width: 110%; height: 110%; border-radius: 999px; border: 1px solid rgba(248, 113, 113, 0.35); animation: pulse 1.4s infinite; }
.square { width: 18px; height: 18px; background: #fff; border-radius: 4px; position: relative; z-index: 2; }
.mic .icon { stroke: #111; }
.drawer { position: fixed; right: 0; top: 0; width: 400px; height: 100vh; background: rgba(10, 10, 10, 0.92); border-left: 1px solid rgba(255, 255, 255, 0.08); backdrop-filter: blur(12px); z-index: 40; display: flex; flex-direction: column; }
.drawer-header { height: 64px; display: flex; align-items: center; justify-content: space-between; padding: 0 18px; border-bottom: 1px solid rgba(255, 255, 255, 0.08); }
.drawer-title { display: flex; align-items: center; gap: 10px; color: #fff; font-weight: 600; }
.drawer-chip { width: 32px; height: 32px; border-radius: 10px; display: inline-flex; align-items: center; justify-content: center; background: rgba(37, 99, 235, 0.25); color: #bfdbfe; font-weight: 700; text-transform: uppercase; }
.drawer-chip.lexicon { background: rgba(34, 197, 94, 0.25); color: #bbf7d0; }
.drawer-chip.metrics { background: rgba(249, 115, 22, 0.25); color: #fed7aa; }
.drawer-body { flex: 1; overflow-y: auto; padding: 16px; }
.drawer-chat { display: flex; flex-direction: column; }
.chat-list { flex: 1; overflow-y: auto; display: grid; gap: 12px; padding: 4px; }
.empty-placeholder { width: 100%; padding: 32px 12px; text-align: center; color: rgba(255, 255, 255, 0.6); border: 1px dashed rgba(255, 255, 255, 0.14); border-radius: 14px; background: rgba(255, 255, 255, 0.02); }
.chat-row { display: flex; gap: 10px; align-items: flex-start; }
.chat-row.user { flex-direction: row-reverse; }
.avatar { width: 32px; height: 32px; border-radius: 999px; display: inline-flex; align-items: center; justify-content: center; font-size: 14px; background: #475569; }
.avatar.ai { background: linear-gradient(135deg, #a855f7, #6366f1); }
.bubble-chat { max-width: 75%; padding: 12px 14px; border-radius: 14px; line-height: 1.5; font-size: 14px; }
.bubble-chat.ai { background: rgba(255, 255, 255, 0.06); border: 1px solid rgba(255, 255, 255, 0.08); color: #e5e7eb; }
.bubble-chat.user { background: #8b5cf6; color: #fff; }
.bubble-chat .meta { margin-top: 6px; font-size: 11px; color: #cbd5e1; display: flex; gap: 8px; flex-wrap: wrap; }
.chat-input { display: grid; grid-template-columns: 1fr 44px; gap: 8px; padding-top: 12px; border-top: 1px solid rgba(255, 255, 255, 0.08); }
.chat-input input { background: rgba(255, 255, 255, 0.06); border: 1px solid rgba(255, 255, 255, 0.12); border-radius: 12px; padding: 12px; color: #fff; }
.chat-input .send { border-radius: 12px; background: #8b5cf6; color: #fff; border: none; cursor: pointer; transition: all 0.2s ease; display: inline-flex; align-items: center; justify-content: center; }
.chat-input .send:hover { background: #7c3aed; }
.lex-stats { display: grid; grid-template-columns: repeat(3, 1fr); gap: 10px; margin-bottom: 16px; }
.stat { background: rgba(255, 255, 255, 0.05); border: 1px solid rgba(255, 255, 255, 0.08); border-radius: 10px; padding: 10px; text-align: center; }
.stat-label { display: block; font-size: 11px; text-transform: uppercase; color: rgba(255, 255, 255, 0.5); letter-spacing: 0.6px; }
.stat-value { font-family: ui-monospace, monospace; color: #fff; }
.lex-list { display: grid; gap: 12px; }
.lex-card { background: rgba(255, 255, 255, 0.04); border: 1px solid rgba(255, 255, 255, 0.08); border-radius: 12px; padding: 12px; }
.lex-row { display: flex; justify-content: space-between; align-items: center; }
.lex-row h4 { margin: 0; color: #fff; }
.freq { font-size: 12px; color: rgba(255, 255, 255, 0.6); }
.status { width: 10px; height: 10px; border-radius: 999px; display: inline-block; }
.status.ok { background: #22c55e; }
.bar { height: 8px; background: rgba(255, 255, 255, 0.08); border-radius: 999px; overflow: hidden; margin-top: 10px; }
.bar-fill { height: 100%; border-radius: 999px; transition: width 0.3s ease; }
.bar-fill.ok { background: linear-gradient(90deg, #22c55e, #a3e635); }
.mini-bars { display: grid; grid-auto-flow: column; grid-auto-columns: 4px; align-items: end; gap: 3px; height: 80px; }
.mini-bar { width: 4px; background: linear-gradient(180deg, #22c55e, #15803d); border-radius: 4px 4px 0 0; }
.mini-bar.chat { background: linear-gradient(180deg, #60a5fa, #2563eb); }
.modal { position: fixed; inset: 0; background: rgba(0, 0, 0, 0.65); backdrop-filter: blur(6px); display: flex; align-items: center; justify-content: center; padding: 20px; z-index: 60; }
.modal-card { width: min(820px, 95vw); background: #0e0e10; border: 1px solid rgba(255, 255, 255, 0.08); border-radius: 24px; box-shadow: 0 30px 80px rgba(0, 0, 0, 0.4); display: flex; flex-direction: column; max-height: 80vh; }
.modal-header { padding: 18px 20px; display: flex; align-items: center; justify-content: space-between; border-bottom: 1px solid rgba(255, 255, 255, 0.06); }
.modal-header .title { display: inline-flex; align-items: center; gap: 10px; font-weight: 600; }
.modal-body { display: grid; grid-template-columns: 180px 1fr; gap: 0; min-height: 320px; }
.tabs { border-right: 1px solid rgba(255, 255, 255, 0.08); padding: 14px; display: grid; gap: 8px; }
.tab { width: 100%; padding: 12px; border-radius: 12px; background: transparent; border: 1px solid transparent; color: #cbd5e1; text-align: left; cursor: pointer; }
.tab.active { background: rgba(168, 85, 247, 0.14); border-color: rgba(168, 85, 247, 0.35); color: #e5e7eb; }
.tab-panel { padding: 18px; display: grid; gap: 12px; align-content: start; }
.field { display: grid; gap: 8px; }
.field label { font-size: 12px; letter-spacing: 0.6px; text-transform: uppercase; color: rgba(255, 255, 255, 0.6); }
.field input, .field textarea { width: 100%; border: 1px solid rgba(255, 255, 255, 0.12); border-radius: 12px; background: rgba(255, 255, 255, 0.04); color: #fff; padding: 10px; }
.field textarea { min-height: 80px; resize: vertical; }
.pill-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 10px; }
.pill { width: 100%; padding: 12px; border-radius: 12px; border: 1px solid rgba(255, 255, 255, 0.12); background: rgba(255, 255, 255, 0.04); color: #e5e7eb; cursor: pointer; }
.pill--active { border-color: rgba(34, 197, 94, 0.8); box-shadow: 0 0 0 1px rgba(34, 197, 94, 0.4); }
.switches { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: 8px; }
.modal-footer { padding: 14px 18px; border-top: 1px solid rgba(255, 255, 255, 0.06); display: flex; justify-content: flex-end; }
.primary { padding: 10px 16px; border-radius: 12px; background: #8b5cf6; color: #fff; border: none; cursor: pointer; font-weight: 600; transition: background 0.2s ease; }
.primary:hover { background: #7c3aed; }
.history { grid-template-columns: 1fr; }
.history-list { display: grid; gap: 12px; padding: 12px; }
.history-item { border: 1px solid rgba(255, 255, 255, 0.1); border-radius: 12px; padding: 12px; background: rgba(255, 255, 255, 0.03); }
.history-title { display: flex; justify-content: space-between; color: #e5e7eb; font-size: 14px; }
.history-title span { color: #94a3b8; font-size: 12px; }
.history-summary { color: #cbd5e1; margin-top: 6px; font-size: 13px; }
.history-actions { display: flex; gap: 8px; margin-top: 10px; }
.history-actions .pill { width: auto; }
@keyframes blob { 0% { transform: translate(0, 0) scale(1); } 33% { transform: translate(40px, -40px) scale(1.1); } 66% { transform: translate(-20px, 30px) scale(0.9); } 100% { transform: translate(0, 0) scale(1); } }
@keyframes blink { 0%, 50% { opacity: 1; } 51%, 100% { opacity: 0; } }
@keyframes pulse { from { transform: scale(0.9); opacity: 0.6; } to { transform: scale(1.1); opacity: 1; } }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
const HistoryIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M12 8v4l3 3" /><path d="M3 12a9 9 0 1 0 9-9 9 9 0 0 0-9 9" /></svg>)
const SettingsIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.09A1.65 1.65 0 0 0 9 4.6V4a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51h.09a1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.09a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z" /></svg>)
const MessageIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M7 8h10" /><path d="M7 12h6" /><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2Z" /></svg>)
const BookIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" /><path d="M4 4.5A2.5 2.5 0 0 1 6.5 7H20" /><path d="M6.5 17A2.5 2.5 0 0 0 4 19.5V4.5A2.5 2.5 0 0 1 6.5 7" /><path d="M20 22V2" /></svg>)
const StatsIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M3 3v18h18" /><path d="M7 13h4V7" /><path d="M15 13h4V9" /></svg>)
const MicIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M12 1.5A3.5 3.5 0 0 0 8.5 5v6A3.5 3.5 0 0 0 12 14.5 3.5 3.5 0 0 0 15.5 11V5A3.5 3.5 0 0 0 12 1.5Z" /><path d="M19 11a7 7 0 0 1-14 0" /><path d="M12 19v4" /><path d="M8 23h8" /></svg>)
const StopIcon = () => (<svg viewBox="0 0 24 24" class="icon"><rect x="6" y="6" width="12" height="12" rx="2" /></svg>)
const PlayIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="m7 4 12 8-12 8z" /></svg>)
const DownloadIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M12 5v11" /><path d="m6 11 6 6 6-6" /><path d="M19 19H5" /></svg>)
const DocIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M9 9h6" /><path d="M9 13h6" /><path d="M5 19h14a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-5l-2-2H5a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2Z" /></svg>)
const TranslateIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M5 7h14" /><path d="M9 5v4" /><path d="m15 21 3-7 3 7" /><path d="M15.4 19h5.2" /><path d="M9 17c1.3-1.3 2.7-3.3 3-6" /><path d="m9 12 2 2" /><path d="m15 11-2-2" /></svg>)
const XIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M18 6 6 18" /><path d="m6 6 12 12" /></svg>)
const LoaderIcon = () => (<svg viewBox="0 0 24 24" class="icon spinner-icon"><path d="M21 12a9 9 0 1 1-6.219-8.56" /></svg>)
const SparklesIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M12 3v2" /><path d="M5.22 5.22 6.64 6.64" /><path d="M3 12h2" /><path d="M5.22 18.78 6.64 17.36" /><path d="M12 21v-2" /><path d="M18.78 18.78 17.36 17.36" /><path d="M21 12h-2" /><path d="M18.78 5.22 17.36 6.64" /><circle cx="12" cy="12" r="3" /></svg>)
const SearchIcon = (props: Record<string, unknown> = {}) => (<svg viewBox="0 0 24 24" class="icon" v-bind="props"><circle cx="11" cy="11" r="8" /><path d="m21 21-4.3-4.3" /></svg>)
const ArrowIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="m5 12 7-7 7 7" /><path d="M12 19V5" /></svg>)
const SaveIcon = () => (<svg viewBox="0 0 24 24" class="icon"><path d="M7 21h10" /><path d="M12 17v4" /><path d="M17 3H7a2 2 0 0 0-2 2v14" /><path d="M17 3v6H7V3" /><path d="M13 13h4" /><path d="M13 7v6" /></svg>)
