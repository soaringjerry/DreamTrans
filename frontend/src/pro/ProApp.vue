<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { emitProCommand, onProState, type ProStateSnapshot } from './bridge'

type Panel = 'none' | 'chat' | 'lexicon'

const props = defineProps<{ onBackToClassic?: () => void }>()

const rightPanel = ref<Panel>('none')
const showSettings = ref(false)
const tabs = ['model', 'prompts', 'experiment', 'api'] as const
type SettingsTab = (typeof tabs)[number]
const settingsTab = ref<SettingsTab>('model')
const streamRef = ref<HTMLElement | null>(null)

// Live data from the classic pipeline (published via bridge)
const snapshot = ref<ProStateSnapshot>({
  lines: [],
  translations: [],
  isTranscribing: false,
  isInitializing: false,
  isPaused: false,
  elapsedTime: 0,
  hiddenCounts: { transcripts: 0, translations: 0 },
})

const isRecording = computed(() => snapshot.value.isTranscribing || snapshot.value.isInitializing)
const elapsedLabel = computed(() => {
  const s = snapshot.value.elapsedTime || 0
  const minutes = Math.floor(s / 60)
  const secs = s % 60
  return `${String(minutes).padStart(2, '0')}:${String(secs).padStart(2, '0')}`
})

const scrollToBottom = () => {
  nextTick(() => {
    if (streamRef.value) {
      streamRef.value.scrollTop = streamRef.value.scrollHeight
    }
  })
}

// Subscribe to live state updates
let offState: (() => void) | null = null
onMounted(() => {
  offState = onProState((state) => {
    snapshot.value = state
  })
  scrollToBottom()
})
onUnmounted(() => {
  offState?.()
})

watch(
  () => snapshot.value.lines,
  () => {
    scrollToBottom()
  },
)

const togglePanel = (panel: Panel) => {
  rightPanel.value = rightPanel.value === panel ? 'none' : panel
}

const setSettingsTab = (tab: SettingsTab) => {
  settingsTab.value = tab
}

const handleRecordClick = () => {
  if (snapshot.value.isTranscribing || snapshot.value.isInitializing) {
    emitProCommand({ type: 'stop' })
  } else {
    emitProCommand({ type: 'start' })
  }
}

const formatTimestamp = (seconds: number) => {
  const m = Math.floor(seconds / 60)
  const s = Math.floor(seconds % 60)
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

const findTranslationForLine = (speaker: string, startTime: number) => {
  // pick the latest translation that aligns with this line
  const matches = snapshot.value.translations.filter((t) => t.speaker === speaker && Math.abs(t.startTime - startTime) < 1.2)
  if (matches.length > 0) return matches[matches.length - 1]
  // fallback to latest translation from same speaker
  const fallback = [...snapshot.value.translations].reverse().find((t) => t.speaker === speaker)
  return fallback
}

const streamItems = computed(() =>
  snapshot.value.lines.map((line) => {
    const start = line.confirmedSegments[0]?.startTime ?? 0
    const end = line.confirmedSegments[line.confirmedSegments.length - 1]?.endTime ?? start
    const translation = findTranslationForLine(line.speaker, start)
    return {
      id: line.id,
      speaker: line.speaker,
      text: line.confirmedSegments.map((s) => s.text).join(''),
      partial: line.partialText,
      start,
      end,
      translation: translation?.content ?? '',
      translationPartial: translation?.isPartial ?? false,
    }
  }),
)
</script>

<template>
  <div class="pro-app">
    <!-- Ambient blobs -->
    <div class="blob blob-1" />
    <div class="blob blob-2" />

    <!-- Header -->
    <header class="pro-header">
      <div class="brand-chip" title="DreamTrans Pro">
        <span class="dot" :class="isRecording ? 'dot--on' : 'dot--off'" />
        <span class="brand-text">
          DreamTrans <span class="tag">PRO</span>
          <span class="elapsed">· {{ elapsedLabel }}</span>
        </span>
      </div>
      <div class="header-actions">
        <button class="ghost-btn" title="History">
          <span class="icon">⏱</span>
        </button>
        <button class="ghost-btn" title="Settings" @click="showSettings = true">
          <span class="icon">⚙️</span>
        </button>
        <button v-if="props.onBackToClassic" class="ghost-btn strong" @click="props.onBackToClassic?.()">
          返回经典版
        </button>
      </div>
    </header>

    <!-- Main -->
    <main
      ref="streamRef"
      class="stream"
      :class="rightPanel !== 'none' ? 'stream--narrow' : ''"
    >
      <div class="stream-inner">
        <div v-if="(snapshot.hiddenCounts?.transcripts || 0) > 0" class="hidden-hint">
          Showing latest items · {{ snapshot.hiddenCounts?.transcripts }} earlier lines hidden to keep it smooth
        </div>
        <section
          v-for="(item, idx) in streamItems"
          :key="item.id"
          class="bubble"
          :class="[{ 'bubble--live': !!item.partial }, { 'bubble--hoverable': !item.partial }]"
        >
          <div v-if="idx !== 0" class="connect-line" />
          <div class="bubble-meta">
            <span class="speaker" :class="item.speaker === 'Speaker A' ? 'speaker-a' : 'speaker-b'">
              {{ item.speaker }}
            </span>
            <span class="timestamp">{{ formatTimestamp(item.start) }}</span>
          </div>
          <div class="bubble-body">
            <h3 class="bubble-title">
              {{ item.text }}
              <span v-if="item.partial" class="blink" />
            </h3>
            <div class="bubble-translation" :class="item.partial ? 'accent' : ''">
              <p v-if="item.translation" class="translation-text">
                {{ item.translation }}
                <span v-if="item.translationPartial" class="pill-soft">partial</span>
              </p>
              <div v-else class="placeholder">
                <span v-if="item.partial" class="pulse-dots">
                  <span />
                  <span />
                  <span />
                </span>
                <div v-else class="skeleton">
                  <div />
                  <div />
                  <div class="loader">
                    <span class="spinner" />
                    <span class="loader-text">AI Translating...</span>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </section>
      </div>
    </main>

    <!-- Command Dock -->
    <div class="dock" :class="rightPanel !== 'none' ? 'dock--offset' : ''">
      <div class="dock-inner">
        <div class="dock-group">
          <button
            :class="['dock-btn', rightPanel === 'chat' ? 'dock-btn--active chat' : '']"
            title="AI Chat"
            @click="togglePanel('chat')"
          >
            💬
          </button>
          <button
            :class="['dock-btn', rightPanel === 'lexicon' ? 'dock-btn--active lexicon' : '']"
            title="Lexicon"
            @click="togglePanel('lexicon')"
          >
            📚
          </button>
        </div>

        <div class="record">
          <button
            class="record-btn"
            :class="isRecording ? 'record-btn--on' : 'record-btn--off'"
            title="Record"
            @click="handleRecordClick"
          >
            <span v-if="isRecording" class="ping" />
            <span v-if="isRecording" class="square" />
            <span v-else class="mic">🎙</span>
          </button>
        </div>

        <div class="dock-group">
          <button class="dock-btn" title="Stats">📊</button>
          <button class="dock-btn" title="More">↗</button>
        </div>
      </div>
    </div>

    <!-- Right Drawer -->
    <aside v-if="rightPanel !== 'none'" class="drawer">
      <header class="drawer-header">
        <div class="drawer-title">
          <span class="drawer-chip" :class="rightPanel === 'chat' ? 'chat' : 'lexicon'">
            {{ rightPanel === 'chat' ? 'AI' : 'LX' }}
          </span>
          <span>{{ rightPanel === 'chat' ? 'AI Assistant' : 'Lexicon' }}</span>
        </div>
        <button class="ghost-btn" @click="rightPanel = 'none'">✕</button>
      </header>

      <div v-if="rightPanel === 'chat'" class="drawer-body drawer-chat">
        <div class="chat-list">
          <div class="empty-placeholder">
            <span class="icon">🧠</span>
            <p>Chat stream not wired to Pro yet — functionality mirrors classic panel.</p>
          </div>
        </div>
        <div class="chat-input">
          <input type="text" placeholder="Ask about the context..." disabled />
          <button class="send" disabled>➜</button>
        </div>
      </div>

      <div v-else class="drawer-body drawer-lexicon">
        <div class="lex-stats">
          <div class="stat" v-for="stat in ['Tokens: —', 'Words: —', 'Terms: —']" :key="stat">
            <span class="stat-label">{{ stat.split(':')[0] }}</span>
            <span class="stat-value">{{ stat.split(':')[1] }}</span>
          </div>
        </div>
        <div class="search">
          <input type="text" placeholder="Search words..." disabled />
        </div>
        <div class="lex-list">
          <div class="empty-placeholder">
            <span class="icon">📚</span>
            <p>Lexicon view will mirror classic counts; wired soon.</p>
          </div>
        </div>
      </div>
    </aside>

    <!-- Settings Modal -->
    <div v-if="showSettings" class="modal">
      <div class="modal-card">
        <header class="modal-header">
          <div class="title">
            <span class="icon">⚙️</span>
            <span>Settings</span>
          </div>
          <button class="ghost-btn" @click="showSettings = false">✕</button>
        </header>
        <div class="modal-body">
          <nav class="tabs">
            <button
              v-for="tab in tabs"
              :key="tab"
              :class="['tab', settingsTab === tab ? 'active' : '']"
              @click="setSettingsTab(tab)"
            >
              {{ tab }}
            </button>
          </nav>
          <section class="tab-panel" v-if="settingsTab === 'model'">
            <div class="field">
              <label>Translation Model</label>
              <div class="pill-grid">
                <button
                  v-for="model in ['gpt-4o-2024', 'gpt-4.1-mini', 'claude-3.5', 'gemini-pro']"
                  :key="model"
                  class="pill"
                  :class="model === 'gpt-4.1-mini' ? 'pill--active' : ''"
                >
                  {{ model }}
                </button>
              </div>
            </div>
            <div class="field">
              <label>Context Window</label>
              <input type="range" min="4" max="128" value="32" />
              <div class="range-hint">
                <span>4k</span>
                <span>128k</span>
              </div>
            </div>
          </section>
          <section v-else class="tab-placeholder">
            <span class="icon">🧩</span>
            <p>Advanced configuration for {{ settingsTab }}</p>
          </section>
        </div>
        <footer class="modal-footer">
          <button class="primary">保存</button>
        </footer>
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

.blob {
  position: absolute;
  width: 50%;
  height: 50%;
  border-radius: 9999px;
  filter: blur(120px);
  opacity: 0.5;
  animation: blob 8s ease-in-out infinite;
}
.blob-1 {
  top: -20%;
  left: -10%;
  background: rgba(124, 58, 237, 0.35);
}
.blob-2 {
  bottom: -20%;
  right: -10%;
  background: rgba(79, 70, 229, 0.3);
  animation-delay: 2s;
}

.pro-header {
  position: absolute;
  inset: 0 0 auto 0;
  padding: 20px 24px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  z-index: 20;
}

.brand-chip {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.12);
  padding: 10px 14px;
  border-radius: 999px;
  backdrop-filter: blur(8px);
}
.dot {
  width: 10px;
  height: 10px;
  border-radius: 999px;
  background: #22c55e;
  box-shadow: 0 0 10px rgba(34, 197, 94, 0.6);
}
.dot--off {
  background: #6b7280;
  box-shadow: none;
}
.elapsed {
  font-size: 12px;
  opacity: 0.7;
  margin-left: 6px;
}
.brand-text {
  color: #e5e7eb;
  font-weight: 600;
  letter-spacing: 0.4px;
}
.tag {
  font-size: 11px;
  opacity: 0.6;
}

.header-actions {
  display: flex;
  gap: 8px;
  align-items: center;
}

.ghost-btn {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.05);
  color: #cbd5e1;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s ease;
}
.ghost-btn:hover {
  color: #fff;
  border-color: rgba(255, 255, 255, 0.2);
}
.ghost-btn.strong {
  width: auto;
  padding: 0 14px;
  border-radius: 12px;
  font-weight: 600;
}

.icon {
  font-size: 14px;
}

.stream {
  position: relative;
  z-index: 10;
  height: 100vh;
  overflow-y: auto;
  padding: 96px 0 180px;
  display: flex;
  justify-content: center;
  transition: margin-right 0.3s ease, width 0.3s ease;
}
.stream--narrow {
  margin-right: 400px;
}
.stream-inner {
  width: min(960px, 100%);
  padding: 0 18px;
  display: flex;
  flex-direction: column;
  gap: 48px;
}

.bubble {
  position: relative;
  transition: transform 0.4s ease, opacity 0.4s ease;
}
.bubble--hoverable {
  opacity: 0.85;
}
.bubble--hoverable:hover {
  transform: translateY(-4px);
  opacity: 1;
}
.bubble--live {
  opacity: 1;
  transform: scale(1.01);
}
.connect-line {
  position: absolute;
  top: -48px;
  left: 8px;
  width: 2px;
  height: 48px;
  background: linear-gradient(to bottom, transparent, rgba(255, 255, 255, 0.1));
}
.bubble-meta {
  display: flex;
  gap: 10px;
  align-items: center;
  margin-bottom: 10px;
}
.speaker {
  padding: 4px 10px;
  border-radius: 8px;
  font-weight: 700;
  font-size: 12px;
  color: #0b0b0f;
}
.speaker-a {
  background: #e2d9ff;
}
.speaker-b {
  background: #cfe5ff;
}
.timestamp {
  color: rgba(255, 255, 255, 0.4);
  font-size: 12px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', monospace;
}
.bubble-body {
  padding: 24px;
  border-radius: 24px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  background: rgba(255, 255, 255, 0.02);
  backdrop-filter: blur(10px);
  box-shadow: 0 0 50px -16px rgba(124, 58, 237, 0.35);
}
.bubble--hoverable .bubble-body:hover {
  background: rgba(255, 255, 255, 0.04);
}
.bubble-title {
  font-size: 22px;
  letter-spacing: 0.2px;
  margin: 0;
  color: #e5e7eb;
  display: flex;
  align-items: center;
  gap: 10px;
}
.bubble-translation {
  margin-top: 12px;
  padding-left: 14px;
  border-left: 2px solid rgba(255, 255, 255, 0.12);
  min-height: 28px;
}
.bubble-translation.accent {
  border-color: rgba(124, 58, 237, 0.6);
}
.translation-text {
  color: #cbd5e1;
  line-height: 1.6;
  font-size: 18px;
}
.pill-soft {
  display: inline-block;
  margin-left: 8px;
  padding: 2px 8px;
  border-radius: 999px;
  background: rgba(124, 58, 237, 0.15);
  color: #c4b5fd;
  font-size: 12px;
}
.hidden-hint {
  color: rgba(255, 255, 255, 0.5);
  font-size: 13px;
  margin-bottom: 8px;
}
.placeholder {
  display: flex;
  align-items: center;
  gap: 10px;
  color: rgba(255, 255, 255, 0.5);
  min-height: 20px;
}
.pulse-dots {
  display: inline-flex;
  gap: 6px;
}
.pulse-dots span {
  width: 10px;
  height: 10px;
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
.skeleton div {
  height: 10px;
  background: linear-gradient(90deg, rgba(168, 85, 247, 0.2), rgba(255, 255, 255, 0.05));
  border-radius: 6px;
  margin-bottom: 8px;
}
.skeleton div:nth-child(2) {
  width: 60%;
}
.skeleton .loader {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: #a855f7;
}
.spinner {
  width: 14px;
  height: 14px;
  border: 2px solid rgba(168, 85, 247, 0.3);
  border-top-color: rgba(168, 85, 247, 0.9);
  border-radius: 999px;
  animation: spin 1s linear infinite;
}
.loader-text {
  font-family: ui-monospace, monospace;
}
.blink {
  display: inline-block;
  width: 8px;
  height: 24px;
  background: #a855f7;
  animation: blink 1s steps(1) infinite;
}

.dock {
  position: absolute;
  left: 50%;
  bottom: 32px;
  transform: translateX(-50%);
  z-index: 30;
  width: min(640px, 100%);
  padding: 0 20px;
  transition: transform 0.3s ease;
}
.dock--offset {
  transform: translate(calc(-50% - 200px), 0);
}
.dock-inner {
  background: rgba(26, 26, 26, 0.85);
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 16px;
  padding: 10px 14px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  backdrop-filter: blur(12px);
  box-shadow: 0 24px 60px rgba(0, 0, 0, 0.35);
}
.dock-group {
  display: inline-flex;
  gap: 8px;
}
.dock-btn {
  width: 44px;
  height: 44px;
  border-radius: 14px;
  background: transparent;
  border: 1px solid rgba(255, 255, 255, 0.12);
  color: #cbd5e1;
  cursor: pointer;
  transition: all 0.2s ease;
  font-size: 18px;
}
.dock-btn:hover {
  border-color: rgba(255, 255, 255, 0.2);
  color: #fff;
}
.dock-btn--active {
  color: #fff;
}
.dock-btn--active.chat {
  background: #2563eb;
  border-color: #2563eb;
}
.dock-btn--active.lexicon {
  background: #16a34a;
  border-color: #16a34a;
}
.record {
  position: relative;
}
.record-btn {
  width: 72px;
  height: 72px;
  border-radius: 999px;
  border: none;
  cursor: pointer;
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  transition: all 0.2s ease;
}
.record-btn--on {
  background: rgba(248, 113, 113, 0.9);
  box-shadow: 0 0 0 8px rgba(248, 113, 113, 0.15);
}
.record-btn--off {
  background: #fff;
  color: #111;
}
.record-btn--on:hover,
.record-btn--off:hover {
  transform: scale(1.04);
}
.ping {
  position: absolute;
  width: 110%;
  height: 110%;
  border-radius: 999px;
  border: 1px solid rgba(248, 113, 113, 0.35);
  animation: pulse 1.4s infinite;
}
.square {
  width: 18px;
  height: 18px;
  background: #fff;
  border-radius: 4px;
  position: relative;
  z-index: 2;
}
.mic {
  font-size: 26px;
}

.drawer {
  position: fixed;
  right: 0;
  top: 0;
  width: 400px;
  height: 100vh;
  background: rgba(10, 10, 10, 0.92);
  border-left: 1px solid rgba(255, 255, 255, 0.08);
  backdrop-filter: blur(12px);
  z-index: 40;
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
  display: flex;
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
}
.drawer-chip.lexicon {
  background: rgba(34, 197, 94, 0.25);
  color: #bbf7d0;
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
.empty-placeholder {
  width: 100%;
  padding: 32px 12px;
  text-align: center;
  color: rgba(255, 255, 255, 0.6);
  border: 1px dashed rgba(255, 255, 255, 0.14);
  border-radius: 14px;
  background: rgba(255, 255, 255, 0.02);
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
.chat-input {
  display: grid;
  grid-template-columns: 1fr 44px;
  gap: 8px;
  padding-top: 12px;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
}
.chat-input input {
  background: rgba(255, 255, 255, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 12px;
  padding: 12px;
  color: #fff;
}
.chat-input .send {
  border-radius: 12px;
  background: #8b5cf6;
  color: #fff;
  border: none;
  cursor: pointer;
  transition: all 0.2s ease;
}
.chat-input .send:hover {
  background: #7c3aed;
}

.drawer-lexicon .lex-stats {
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
  color: rgba(255, 255, 255, 0.5);
  letter-spacing: 0.6px;
}
.stat-value {
  font-family: ui-monospace, monospace;
  color: #fff;
}
.search {
  margin-bottom: 16px;
}
.search input {
  width: 100%;
  padding: 10px 12px;
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.12);
  background: rgba(255, 255, 255, 0.04);
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
.status.learn {
  background: #38bdf8;
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
.bar-fill.learn {
  background: linear-gradient(90deg, #38bdf8, #6366f1);
}

.modal {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.65);
  backdrop-filter: blur(6px);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 20px;
  z-index: 60;
}
.modal-card {
  width: min(720px, 95vw);
  background: #0e0e10;
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 24px;
  box-shadow: 0 30px 80px rgba(0, 0, 0, 0.4);
  display: flex;
  flex-direction: column;
  max-height: 80vh;
}
.modal-header {
  padding: 18px 20px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}
.modal-header .title {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  font-weight: 600;
}
.modal-body {
  display: grid;
  grid-template-columns: 160px 1fr;
  gap: 0;
  min-height: 320px;
}
.tabs {
  border-right: 1px solid rgba(255, 255, 255, 0.08);
  padding: 14px;
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
  background: rgba(168, 85, 247, 0.14);
  border-color: rgba(168, 85, 247, 0.35);
  color: #e5e7eb;
}
.tab-panel,
.tab-placeholder {
  padding: 18px;
}
.field {
  margin-bottom: 18px;
}
.field label {
  display: block;
  font-size: 12px;
  letter-spacing: 0.6px;
  text-transform: uppercase;
  color: rgba(255, 255, 255, 0.6);
  margin-bottom: 10px;
}
.pill-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 10px;
}
.pill {
  width: 100%;
  padding: 12px;
  border-radius: 12px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  background: rgba(255, 255, 255, 0.04);
  color: #e5e7eb;
  cursor: pointer;
}
.pill--active {
  border-color: rgba(34, 197, 94, 0.8);
  box-shadow: 0 0 0 1px rgba(34, 197, 94, 0.4);
}
.field input[type='range'] {
  width: 100%;
}
.range-hint {
  display: flex;
  justify-content: space-between;
  color: rgba(255, 255, 255, 0.45);
  font-size: 12px;
}
.tab-placeholder {
  display: grid;
  place-items: center;
  gap: 10px;
  color: rgba(255, 255, 255, 0.6);
}
.modal-footer {
  padding: 14px 18px;
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
  transition: background 0.2s ease;
}
.primary:hover {
  background: #7c3aed;
}

@keyframes blob {
  0% {
    transform: translate(0, 0) scale(1);
  }
  33% {
    transform: translate(40px, -40px) scale(1.1);
  }
  66% {
    transform: translate(-20px, 30px) scale(0.9);
  }
  100% {
    transform: translate(0, 0) scale(1);
  }
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
    transform: scale(0.9);
    opacity: 0.6;
  }
  to {
    transform: scale(1.1);
    opacity: 1;
  }
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
