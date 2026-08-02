import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react'
import { getAccessToken, ensureValidAccessToken } from '../../pro/api/auth'
import {
  ApiRequestError,
  createSession as createCloudSession,
  deleteSession as deleteCloudSession,
  getSession as getCloudSession,
  getSessionTranscriptsPage,
  listSessions as listCloudSessions,
  updateSession as updateCloudSession,
  type Transcript as CloudTranscript,
  type TranscriptInput,
  type User,
} from '../../pro/api/auth'
import { migrateLegacySessionStorage } from '../../db'
import {
  BrowserAudioCapture,
  probePreferredAudioSampleRate,
  type AudioCaptureError,
} from '../../core/audio/BrowserAudioCapture'
import { IndexedDbSessionRepository } from '../../core/session'
import {
  createStableTranscriptId,
  createStableTranslationId,
  resolveSpeechmaticsProxyUrl,
  SpeechmaticsProxyClient,
  TranscriptStore,
  type TranscriptSegment,
  type TranslationSegment,
} from '../../core/transcription'
import type { RecorderStatus } from '../components/RecorderBar'
import type {
  HistoryOpenProgress,
  HistorySession,
} from '../components/HistoryPanel'
import type { WorkspaceStats } from '../WorkspaceShell'
import type { UnifiedSettings } from './useUnifiedSettings'
import { lexIngest, lexReplace, lexReset } from '../../utils/lexicon'
import { websocketAuthProtocols } from '../../utils/websocketAuth'
import {
  AiTranslateClient,
  type AiTranslateChunk,
  type AiTranslationResult,
} from '../workspace/AiTranslateClient'
import { CloudTranscriptQueue } from '../workspace/CloudTranscriptQueue'
import {
  downloadCompleteAudio,
  downloadSessionText,
  requestCompleteAudioSave,
} from '../workspace/downloads'
import { RagIngestQueue } from '../workspace/RagIngestQueue'
import { TranscriptFeedModel } from '../workspace/TranscriptFeedModel'
import {
  mergeSessionRecords,
  type StoredSessionRecords,
} from '../workspace/mergeSessionRecords'
import {
  chatHistoryKey,
  legacyChatHistoryKey,
} from '../workspace/browserStorageKeys'
import { ensureSpeechmaticsPreflight } from '../workspace/speechmaticsPreflight'

const ANONYMOUS_TOKEN_SENTINEL = '__dreamtrans_anonymous__'
const backendURL = import.meta.env.VITE_BACKEND_URL || 'http://localhost:8080'

function resolveTranslateProxyUrl(backendUrl: string): string {
  if (backendUrl === '/') {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    return `${protocol}//${window.location.host}/ws/translate`
  }
  const base = backendUrl
    .replace(/^http:\/\//i, 'ws://')
    .replace(/^https:\/\//i, 'wss://')
    .replace(/\/+$/, '')
  return base.endsWith('/ws/translate') ? base : `${base}/ws/translate`
}

const languageNames: Record<string, string> = {
  en: 'English',
  cmn: 'Simplified Chinese',
  ja: 'Japanese',
  ko: 'Korean',
  es: 'Spanish',
  fr: 'French',
  de: 'German',
}

/**
 * The server's built-in translate prompt is written for English → Chinese.
 * Any other language pair gets an equivalent generated prompt so the AI
 * engine stays usable without manual configuration.
 */
function defaultTranslatePromptFor(source: string, target: string): string {
  if (source === 'en' && target === 'cmn') return ''
  const sourceName = languageNames[source] ?? source
  const targetName = languageNames[target] ?? target
  return `You are a professional simultaneous interpreter translating spoken ${sourceName} `
    + `into fluent, natural ${targetName}. Use <context> only to understand the situation. `
    + `Translate only the text inside <text>...</text> into ${targetName}, polishing it so it `
    + 'reads smoothly while preserving the original meaning and tone: merge incomplete '
    + 'sentences, fix word order, and drop filler words. Keep terminology accurate and keep '
    + 'numbers/units unchanged. Do not include anything from <context>. Do not add '
    + 'explanations, quotes, speaker labels, timestamps, or language tags. Return only the '
    + `final polished ${targetName} sentence.`
}

const commonWords = new Set([
  'a', 'an', 'and', 'are', 'as', 'at', 'be', 'but', 'by', 'for', 'from',
  'had', 'has', 'have', 'he', 'her', 'his', 'i', 'if', 'in', 'is', 'it',
  'its', 'me', 'my', 'not', 'of', 'on', 'or', 'our', 'she', 'so', 'that',
  'the', 'their', 'them', 'there', 'they', 'this', 'to', 'was', 'we', 'were',
  'will', 'with', 'you', 'your',
  '一个', '一些', '这个', '那个', '然后', '就是', '可以', '我们', '你们',
  '他们', '因为', '所以', '但是', '还是', '没有', '已经', '现在', '什么',
])

function formatDiagSeconds(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '0s'
  if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 1 : 0)}s`
  const minutes = Math.floor(seconds / 60)
  const rest = Math.floor(seconds % 60)
  return `${minutes}m${rest.toString().padStart(2, '0')}s`
}

function formatDiagBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0'
  if (bytes < 1024) return `${Math.round(bytes)}B`
  if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)}KB`
  return `${(bytes / (1024 * 1024)).toFixed(1)}MB`
}

function formatDiagMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !Number.isFinite(ms)) return '—'
  if (ms < 1000) return `${Math.round(ms)}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function formatLagSample(
  lastMs: number | null,
  avgMs: number | null,
  maxMs: number,
  samples: number,
): string {
  if (samples <= 0 || lastMs === null) return '尚无样本'
  const parts = [`最近 ${formatDiagMs(lastMs)}`]
  if (avgMs !== null) parts.push(`均 ${formatDiagMs(avgMs)}`)
  if (maxMs > 0) parts.push(`峰 ${formatDiagMs(maxMs)}`)
  parts.push(`n=${samples}`)
  return parts.join(' · ')
}

function buildTransportDiagnostics(
  audio: ReturnType<SpeechmaticsProxyClient['getDiagnostics']>,
  ai: { pendingChunks: number; bufferedChars: number },
): TransportDiagnostics {
  const outboundQueueMs = Math.max(0, audio.outboundQueueMs)
  const finalBehindMs = Math.max(0, audio.finalBehindMs)
  const partialBehindMs = audio.partialBehindMs
  const sentAudioSeconds = audio.bytesPerSecond > 0
    ? audio.sentAudioBytes / audio.bytesPerSecond
    : 0
  const dropped = audio.droppedAudioBytes
  const partialSlow = (audio.avgPartialLagMs ?? audio.lastPartialLagMs ?? 0) >= 900
    || (partialBehindMs !== null && partialBehindMs >= 1_200)
  const finalSlow = (audio.avgFinalLagMs ?? audio.lastFinalLagMs ?? 0) >= 2_500
    || finalBehindMs >= 2_500

  let tone: TransportDiagnostics['tone'] = 'ok'
  if (dropped > 0 || outboundQueueMs >= 800 || ai.pendingChunks >= 4 || partialSlow) {
    tone = 'bad'
  } else if (
    outboundQueueMs >= 200
    || finalSlow
    || ai.pendingChunks >= 2
  ) {
    tone = 'warn'
  }

  const partialLive = audio.hasActivePartial && partialBehindMs !== null
    ? `实时落后 ${formatDiagMs(partialBehindMs)}`
    : audio.hasActivePartial
      ? '有待确定文本'
      : '无待确定'
  const partialSample = formatLagSample(
    audio.lastPartialLagMs,
    audio.avgPartialLagMs,
    audio.maxPartialLagMs,
    audio.partialSampleCount,
  )
  const finalSample = formatLagSample(
    audio.lastFinalLagMs,
    audio.avgFinalLagMs,
    audio.maxFinalLagMs,
    audio.finalSampleCount,
  )

  const rows: TransportDiagRow[] = [
    {
      label: '发送积压',
      value: formatDiagMs(outboundQueueMs),
      note: outboundQueueMs >= 200 ? '网络/主线程' : '正常',
    },
    {
      label: '待确定',
      value: partialLive,
      note: partialSample,
    },
    {
      label: '已确认',
      value: `实时落后 ${formatDiagMs(finalBehindMs)}`,
      note: finalSample,
    },
    {
      label: '已发送',
      value: formatDiagSeconds(sentAudioSeconds),
      note: audio.lastPartialAgeMs !== null
        ? `上次待确定 ${formatDiagMs(audio.lastPartialAgeMs)}前 · 上次确认 ${formatDiagMs(audio.lastFinalAgeMs)}前`
        : `上次确认 ${formatDiagMs(audio.lastFinalAgeMs)}前`,
    },
  ]
  if (dropped > 0) {
    rows.push({
      label: '丢弃',
      value: formatDiagBytes(dropped),
      note: '未发出的音频被丢弃',
    })
  }
  if (ai.pendingChunks > 0 || ai.bufferedChars > 0) {
    rows.push({
      label: 'AI 翻译',
      value: `待处理 ${ai.pendingChunks}`,
      note: ai.bufferedChars > 0 ? `缓冲 ${ai.bufferedChars} 字` : '队列中',
    })
  }

  const summary = [
    `积压 ${formatDiagMs(outboundQueueMs)}`,
    `待确定 ${
      audio.lastPartialLagMs === null
        ? '—'
        : `${formatDiagMs(audio.lastPartialLagMs)}（均 ${formatDiagMs(audio.avgPartialLagMs)}）`
    }`,
    `已确认 ${
      audio.lastFinalLagMs === null
        ? '—'
        : `${formatDiagMs(audio.lastFinalLagMs)}（均 ${formatDiagMs(audio.avgFinalLagMs)}）`
    }`,
    `已发 ${formatDiagSeconds(sentAudioSeconds)}`,
  ]
  if (dropped > 0) summary.push(`丢 ${formatDiagBytes(dropped)}`)
  if (ai.pendingChunks > 0) summary.push(`AI ${ai.pendingChunks}`)

  const hints: string[] = []
  if (outboundQueueMs >= 200) {
    hints.push('发送队列有积压（网络或主线程卡顿）；本地录音仍按实时写入。')
  }
  if (partialSlow && outboundQueueMs < 200) {
    hints.push('待确定文本也偏慢：更可能是上行链路或识别输入延迟，不只是定稿等待。')
  }
  if (!partialSlow && finalSlow && outboundQueueMs < 200) {
    hints.push('待确定正常、已确认偏慢：发送健康，延迟主要在定稿（等句尾），初步文本应仍先出现。')
  }
  if (dropped > 0) {
    hints.push('曾因网络拥塞丢弃未发送音频，字幕可能跳句。')
  }
  if (ai.pendingChunks >= 2) {
    hints.push('AI 翻译队列积压，译文会比原文更晚。')
  }
  if (hints.length === 0) {
    hints.push('发送链路健康。对比「待确定」与「已确认」：前者应明显更短。')
  }

  return {
    outboundQueueMs,
    socketBufferedBytes: audio.socketBufferedBytes,
    droppedAudioBytes: dropped,
    sentAudioSeconds,
    acceptedAudioSeconds: audio.acceptedAudioSeconds,
    transcriptBehindMs: finalBehindMs,
    partialBehindMs,
    finalBehindMs,
    lastPartialLagMs: audio.lastPartialLagMs,
    lastFinalLagMs: audio.lastFinalLagMs,
    avgPartialLagMs: audio.avgPartialLagMs,
    avgFinalLagMs: audio.avgFinalLagMs,
    maxPartialLagMs: audio.maxPartialLagMs,
    maxFinalLagMs: audio.maxFinalLagMs,
    partialSampleCount: audio.partialSampleCount,
    finalSampleCount: audio.finalSampleCount,
    lastPartialAgeMs: audio.lastPartialAgeMs,
    lastFinalAgeMs: audio.lastFinalAgeMs,
    hasActivePartial: audio.hasActivePartial,
    aiPendingChunks: ai.pendingChunks,
    aiBufferedChars: ai.bufferedChars,
    tone,
    summary: summary.join(' · '),
    detail: hints.join(' '),
    rows,
  }
}

interface UnifiedWorkspaceOptions {
  ragEnabled: boolean
  settings: UnifiedSettings
  user: User | null
  onBalanceUpdated?: () => void
}

export interface TransportDiagRow {
  label: string
  value: string
  note: string
}

/**
 * Live transport / recognition lag snapshot. Local recording can stay healthy
 * while outboundQueueMs climbs — that mismatch is exactly the "录音正常、字幕慢"
 * class of bugs.
 */
export interface TransportDiagnostics {
  /** Audio still waiting to leave the browser (queue + WS buffer), in ms. */
  outboundQueueMs: number
  socketBufferedBytes: number
  droppedAudioBytes: number
  sentAudioSeconds: number
  acceptedAudioSeconds: number
  /** @deprecated Prefer finalBehindMs. */
  transcriptBehindMs: number
  /** Live gap for the active preliminary (partial) text, if any. */
  partialBehindMs: number | null
  /** Live gap for the latest confirmed (final) text. */
  finalBehindMs: number
  lastPartialLagMs: number | null
  lastFinalLagMs: number | null
  avgPartialLagMs: number | null
  avgFinalLagMs: number | null
  maxPartialLagMs: number
  maxFinalLagMs: number
  partialSampleCount: number
  finalSampleCount: number
  lastPartialAgeMs: number | null
  lastFinalAgeMs: number | null
  hasActivePartial: boolean
  aiPendingChunks: number
  aiBufferedChars: number
  tone: 'ok' | 'warn' | 'bad'
  /** Compact Chinese status for the top bar / toolbar. */
  summary: string
  detail: string
  /** Structured rows for the debug panel. */
  rows: TransportDiagRow[]
}

export interface UnifiedWorkspaceState {
  connectionLabel: string
  durationLabel: string
  error: string | null
  feedGeneration: number
  feedItems: ReturnType<TranscriptFeedModel['getSnapshot']>['items']
  historyLoading: boolean
  historyOpening: HistoryOpenProgress | null
  historySessions: HistorySession[]
  legacyHistoryCount: number
  pendingWrites: number
  recorderStatus: RecorderStatus
  sessionId: string
  sessionSourceLanguage: string
  stats: WorkspaceStats
  title: string
  /** Null when not in an active capture session. */
  transportDiagnostics: TransportDiagnostics | null
  transcriptContext: string
  clearError: () => void
  deleteHistory: (session: HistorySession) => Promise<void>
  downloadAudio: () => Promise<void>
  downloadText: (mode: 'original' | 'translation' | 'bilingual') => Promise<void>
  loadHistory: (session: HistorySession) => Promise<void>
  migrateLegacyHistory: () => Promise<void>
  pauseToggle: () => void
  refreshHistory: () => Promise<void>
  continueSession: () => Promise<void>
  start: () => Promise<void>
  stop: () => Promise<void>
  updateTitle: (title: string) => Promise<void>
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function isTranscriptInput(value: unknown): value is TranscriptInput {
  if (!isRecord(value)) return false
  return (
    typeof value.client_segment_id === 'string'
    && value.client_segment_id.trim().length > 0
    && typeof value.text === 'string'
    && typeof value.start_time === 'number'
    && Number.isFinite(value.start_time)
  )
}

function numberValue(value: unknown, fallback = 0): number {
  return typeof value === 'number' && Number.isFinite(value)
    ? Math.max(0, value)
    : fallback
}

function stringValue(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback
}

function defaultSessionTitle(now = Date.now()): string {
  const date = new Intl.DateTimeFormat('zh-CN', {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(now)
  return `会话 · ${date}`
}

function formatDuration(totalSeconds: number): string {
  const seconds = Math.max(0, Math.floor(totalSeconds))
  const hours = Math.floor(seconds / 3_600)
  const minutes = Math.floor(seconds % 3_600 / 60)
  const remainder = seconds % 60
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
    : `${String(minutes).padStart(2, '0')}:${String(remainder).padStart(2, '0')}`
}

async function updateCloudSessionWithTimeout(
  sessionId: string,
  data: Parameters<typeof updateCloudSession>[1],
  timeoutMs = 15_000,
): Promise<void> {
  const controller = new AbortController()
  const timeout = globalThis.setTimeout(() => controller.abort(), timeoutMs)
  try {
    await updateCloudSession(sessionId, data, controller.signal)
  } finally {
    globalThis.clearTimeout(timeout)
  }
}

function shouldRetryCloudRequest(reason: unknown): boolean {
  if (!(reason instanceof ApiRequestError)) return true
  return reason.status === 408
    || reason.status === 425
    || reason.status === 429
    || reason.status >= 500
}

async function waitForRetry(delayMs: number): Promise<void> {
  await new Promise<void>((resolve) => {
    globalThis.setTimeout(resolve, delayMs)
  })
}

async function withOperationTimeout<T>(
  operation: Promise<T>,
  timeoutMs: number,
  message: string,
): Promise<T> {
  let timeout: ReturnType<typeof globalThis.setTimeout> | null = null
  const timeoutPromise = new Promise<never>((_resolve, reject) => {
    timeout = globalThis.setTimeout(() => reject(new Error(message)), timeoutMs)
  })
  try {
    return await Promise.race([operation, timeoutPromise])
  } finally {
    if (timeout !== null) globalThis.clearTimeout(timeout)
  }
}

async function updateCloudSessionWithRetry(
  sessionId: string,
  data: Parameters<typeof updateCloudSession>[1],
  attempts = 3,
  isCurrent: () => boolean = () => true,
): Promise<void> {
  let lastFailure: unknown
  const delays = [0, 500, 1_500, 4_000]
  for (let attempt = 0; attempt < Math.max(1, attempts); attempt += 1) {
    if (!isCurrent()) throw new Error('云端会话所属账号已变化')
    const delayMs = delays[Math.min(attempt, delays.length - 1)] ?? 4_000
    if (delayMs > 0) await waitForRetry(delayMs)
    if (!isCurrent()) throw new Error('云端会话所属账号已变化')
    try {
      await updateCloudSessionWithTimeout(sessionId, data, 8_000)
      return
    } catch (reason) {
      lastFailure = reason
      if (!shouldRetryCloudRequest(reason)) throw reason
    }
  }
  throw lastFailure ?? new Error('云端会话更新失败')
}

function canonicalTranscript(
  value: unknown,
  fallbackSequence: number,
  receivedAt: number,
): TranscriptSegment | null {
  if (!isRecord(value)) return null
  const text = stringValue(value.text).trim()
  const startTime = numberValue(value.startTime)
  const endTime = Math.max(startTime, numberValue(value.endTime, startTime))
  if (!text || value.status !== 'final') return null
  const speaker = stringValue(value.speaker, 'Speaker').trim() || 'Speaker'
  const id = stringValue(value.id).trim() || createStableTranscriptId({
    speaker,
    text,
    startTime,
    endTime,
  })
  return Object.freeze({
    id,
    sequence: fallbackSequence,
    speaker,
    text,
    status: 'final',
    startTime,
    endTime,
    receivedAt: numberValue(value.receivedAt, receivedAt),
    source: stringValue(value.source, 'local'),
  })
}

function appendLegacyTranscriptLine(
  target: TranscriptSegment[],
  value: Record<string, unknown>,
  receivedAt: number,
): boolean {
  if (!Array.isArray(value.confirmedSegments)) return false
  const speaker = stringValue(value.speaker, 'Speaker').trim() || 'Speaker'
  for (const rawSegment of value.confirmedSegments) {
    if (!isRecord(rawSegment)) continue
    const text = stringValue(rawSegment.text).trim()
    if (!text) continue
    const startTime = numberValue(rawSegment.startTime)
    const endTime = Math.max(startTime, numberValue(rawSegment.endTime, startTime))
    const id = createStableTranscriptId({ speaker, text, startTime, endTime })
    target.push(Object.freeze({
      id,
      sequence: target.length,
      speaker,
      text,
      status: 'final',
      startTime,
      endTime,
      receivedAt,
      source: 'legacy-classic',
    }))
  }
  return true
}

function canonicalTranslation(
  value: unknown,
  fallbackSequence: number,
  receivedAt: number,
  store: TranscriptStore,
  targetLanguage: string,
): TranslationSegment | null {
  if (!isRecord(value)) return null
  const canonicalText = stringValue(value.text).trim()
  const legacyText = stringValue(value.content).trim()
  const text = canonicalText || legacyText
  if (!text || value.isPartial === true) return null
  const speaker = stringValue(value.speaker, 'Speaker').trim() || 'Speaker'
  const startTime = numberValue(value.startTime)
  const endTime = Math.max(startTime, numberValue(value.endTime, startTime))
  const language = stringValue(value.language, targetLanguage).trim() || targetLanguage
  const storedSegmentId = typeof value.segmentId === 'string' ? value.segmentId : undefined
  const segmentId = storedSegmentId && store.getSegment(storedSegmentId)
    ? storedSegmentId
    : store.findSegmentId(speaker, startTime, endTime)
  const id = stringValue(value.id).trim() || createStableTranslationId({
    segmentId,
    speaker,
    language,
    text,
    startTime,
    endTime,
  })
  return Object.freeze({
    id,
    sequence: fallbackSequence,
    segmentId,
    speaker,
    language,
    text,
    status: 'final',
    startTime,
    endTime,
    receivedAt: numberValue(value.receivedAt, receivedAt),
    source: stringValue(value.source, canonicalText ? 'local' : 'legacy-classic'),
  })
}

function transcriptRecordIsCanonical(
  record: {
    sequence: number
    recordId: string
    data: unknown
  },
  canonical: TranscriptSegment,
): boolean {
  const data = record.data
  return isRecord(data)
    && record.sequence === canonical.sequence
    && record.recordId === canonical.id
    && data.id === canonical.id
    && data.sequence === canonical.sequence
    && data.speaker === canonical.speaker
    && data.text === canonical.text
    && data.status === canonical.status
    && data.startTime === canonical.startTime
    && data.endTime === canonical.endTime
    && data.receivedAt === canonical.receivedAt
    && data.source === canonical.source
}

function translationRecordIsCanonical(
  record: {
    sequence: number
    recordId: string
    data: unknown
  },
  canonical: TranslationSegment,
): boolean {
  const data = record.data
  return isRecord(data)
    && record.sequence === canonical.sequence
    && record.recordId === canonical.id
    && data.id === canonical.id
    && data.sequence === canonical.sequence
    && data.segmentId === canonical.segmentId
    && data.speaker === canonical.speaker
    && data.language === canonical.language
    && data.text === canonical.text
    && data.status === canonical.status
    && data.startTime === canonical.startTime
    && data.endTime === canonical.endTime
    && data.receivedAt === canonical.receivedAt
    && data.source === canonical.source
}

class WordCounter {
  private readonly counts = new Map<string, number>()
  private topEntries: Array<{ word: string; count: number }> = []

  reset(): void {
    this.counts.clear()
    this.topEntries = []
  }

  add(text: string): void {
    const words = text.toLocaleLowerCase().match(/[\p{L}\p{N}]{2,}/gu) ?? []
    for (const word of words) {
      if (commonWords.has(word)) continue
      const count = (this.counts.get(word) ?? 0) + 1
      this.counts.set(word, count)
      const existing = this.topEntries.find((entry) => entry.word === word)
      if (existing) {
        existing.count = count
      } else if (
        this.topEntries.length < 12
        || count > (this.topEntries.at(-1)?.count ?? 0)
      ) {
        if (this.topEntries.length >= 12) this.topEntries.pop()
        this.topEntries.push({ word, count })
      }
      this.topEntries.sort((left, right) => (
        right.count - left.count || left.word.localeCompare(right.word)
      ))
    }
  }

  getTop(): Array<{ word: string; count: number }> {
    return this.topEntries.map((entry) => ({ ...entry }))
  }
}

async function canonicalizeLocalSession(
  repository: IndexedDbSessionRepository<TranscriptSegment, TranslationSegment>,
  sessionId: string,
  targetLanguage: string,
): Promise<StoredSessionRecords> {
  const metadata = await repository.getSessionMetadata(sessionId)
  const receivedAt = metadata?.createdAt ?? Date.now()
  const segments: TranscriptSegment[] = []
  let rewriteTranscripts = false

  for await (const record of repository.iterateTranscripts(sessionId, 500)) {
    const canonical = canonicalTranscript(record.data, segments.length, receivedAt)
    if (canonical) {
      segments.push(Object.freeze({ ...canonical, sequence: segments.length }))
      if (!transcriptRecordIsCanonical(record, canonical)) rewriteTranscripts = true
      continue
    }
    rewriteTranscripts = true
    if (isRecord(record.data) && appendLegacyTranscriptLine(segments, record.data, receivedAt)) {
      continue
    }
  }

  const lookupStore = new TranscriptStore()
  lookupStore.batch(() => {
    for (const segment of segments) {
      lookupStore.appendTranscript(segment)
    }
  })

  const translations: TranslationSegment[] = []
  let rewriteTranslations = false
  for await (const record of repository.iterateTranslations(sessionId, 500)) {
    const translation = canonicalTranslation(
      record.data,
      translations.length,
      receivedAt,
      lookupStore,
      targetLanguage,
    )
    if (!translation) {
      rewriteTranslations = true
      continue
    }
    translations.push(Object.freeze({ ...translation, sequence: translations.length }))
    if (!translationRecordIsCanonical(record, translation)) rewriteTranslations = true
  }

  if (rewriteTranscripts) {
    await repository.writeTranscriptRecords(
      sessionId,
      segments.map((segment) => ({
        sequence: segment.sequence,
        recordId: segment.id,
        data: segment,
      })),
      segments.length,
    )
  }
  if (rewriteTranslations) {
    await repository.writeTranslationRecords(
      sessionId,
      translations.map((translation) => ({
        sequence: translation.sequence,
        recordId: translation.id,
        data: translation,
      })),
      translations.length,
    )
  }
  return { segments, translations }
}

export function useUnifiedWorkspace({
  ragEnabled,
  settings,
  user,
  onBalanceUpdated,
}: UnifiedWorkspaceOptions): UnifiedWorkspaceState {
  const repositoryOwnerRef = useRef<string | null>(user?.id ?? null)
  const [repository] = useState(
    () => new IndexedDbSessionRepository<TranscriptSegment, TranslationSegment>({
      ownerId: () => repositoryOwnerRef.current,
    }),
  )
  const scopedRepositoriesRef = useRef(new Map<
    string,
    IndexedDbSessionRepository<TranscriptSegment, TranslationSegment>
  >())
  const repositoryForOwner = useCallback((ownerId: string | null) => {
    const key = ownerId === null ? 'anonymous' : `account:${ownerId}`
    let scoped = scopedRepositoriesRef.current.get(key)
    if (!scoped) {
      scoped = new IndexedDbSessionRepository<TranscriptSegment, TranslationSegment>({
        ownerId,
      })
      scopedRepositoriesRef.current.set(key, scoped)
    }
    return scoped
  }, [])
  const [transcriptStore] = useState(() => new TranscriptStore())
  const [feedModel] = useState(() => new TranscriptFeedModel({
    sourceLanguage: settings.sourceLanguage,
    targetLanguage: settings.targetLanguage,
    translationEnabled: settings.translationEnabled,
  }))
  const sessionAuthRequiredRef = useRef(false)
  const [client] = useState(() => new SpeechmaticsProxyClient({
    url: () => resolveSpeechmaticsProxyUrl(backendURL),
    tokenProvider: async () => {
      const token = getAccessToken()
      if (sessionAuthRequiredRef.current && !token) {
        throw new Error('登录状态已失效，无法继续当前云端会话')
      }
      return token
        ? ensureValidAccessToken(90)
        : ANONYMOUS_TOKEN_SENTINEL
    },
    protocolFactory: (token) => (
      token === ANONYMOUS_TOKEN_SENTINEL ? [] : websocketAuthProtocols(token)
    ),
    store: transcriptStore,
    resetStoreOnStart: false,
    partialUpdateIntervalMs: 50,
    // Keep trying through a short mobile hand-off or Wi-Fi outage. The
    // byte-bounded queue below caps memory while preserving roughly 30 seconds
    // of speech instead of dropping audio after the previous five seconds.
    reconnect: { maxAttempts: 8 },
    audio: {
      sampleRate: 48_000,
      frameDurationMs: 40,
      maxQueuedAudioSeconds: 30,
    },
  }))
  const aiTranslationHandlerRef = useRef<
    ((chunk: AiTranslateChunk, result: AiTranslationResult) => void) | null
  >(null)
  const [aiTranslator] = useState(() => new AiTranslateClient({
    url: () => resolveTranslateProxyUrl(backendURL),
    tokenProvider: async () => {
      const token = getAccessToken()
      return token ? ensureValidAccessToken(90) : ANONYMOUS_TOKEN_SENTINEL
    },
    protocolFactory: (token) => (
      token === ANONYMOUS_TOKEN_SENTINEL ? [] : websocketAuthProtocols(token)
    ),
    onTranslation: (chunk, result) => {
      aiTranslationHandlerRef.current?.(chunk, result)
    },
    onChunkError: (chunk, message) => {
      feedModel.markTranslationError(chunk.segmentIds, message)
    },
    onError: (message) => setError(message),
    onRecovered: () => setError(null),
  }))
  const [localPending, setLocalPending] = useState(0)
  const [cloudPending, setCloudPending] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [recorderStatus, setRecorderStatusState] = useState<RecorderStatus>('idle')
  const [sessionId, setSessionId] = useState('')
  const [sessionSourceLanguage, setSessionSourceLanguage] = useState(
    settings.sourceLanguage,
  )
  const [title, setTitle] = useState(() => defaultSessionTitle())
  const [elapsedSeconds, setElapsedSeconds] = useState(0)
  const [historySessions, setHistorySessions] = useState<HistorySession[]>([])
  const [historyLoading, setHistoryLoading] = useState(false)
  const [historyOpening, setHistoryOpening] = useState<HistoryOpenProgress | null>(null)
  const historySessionsRef = useRef<HistorySession[]>([])
  historySessionsRef.current = historySessions
  const [legacyHistoryCount, setLegacyHistoryCount] = useState(0)
  const [topWords, setTopWords] = useState<Array<{ word: string; count: number }>>([])
  const [transportDiagnostics, setTransportDiagnostics] = useState<TransportDiagnostics | null>(
    null,
  )
  const feedSnapshot = useSyncExternalStore(feedModel.subscribe, feedModel.getSnapshot)
  const transcriptContext = useMemo(
    () => feedSnapshot.items
      .filter((item) => item.original?.status === 'final' && item.original.text?.trim())
      .map((item) => {
        const start = item.startTime === undefined ? '' : `[${item.startTime.toFixed(1)}s] `
        return `${start}${item.speaker}: ${item.original?.text?.trim() ?? ''}`
      })
      .join('\n'),
    [feedSnapshot],
  )
  const transcriptSnapshot = useSyncExternalStore(
    transcriptStore.subscribe,
    transcriptStore.getSnapshot,
  )
  const clientSnapshot = useSyncExternalStore(client.subscribe, client.getSnapshot)

  const settingsRef = useRef(settings)
  const ragEnabledRef = useRef(ragEnabled)
  const userRef = useRef(user)
  const balanceCallbackRef = useRef(onBalanceUpdated)
  const statusRef = useRef<RecorderStatus>('idle')
  const currentSessionRef = useRef('')
  const currentAudioMimeTypeRef = useRef('audio/webm')
  const currentLocationRef = useRef<'cloud' | 'local'>('local')
  const cloudSessionRef = useRef<string | null>(null)
  const cloudSessionVerifiedRef = useRef(false)
  /** Translation engine locked in for the active session ('' when none). */
  const sessionTranslationEngineRef = useRef<'' | 'ai' | 'speechmatics'>('')
  const sessionTargetLanguageRef = useRef(settings.targetLanguage)
  const captureRef = useRef<BrowserAudioCapture | null>(null)
  const localAudioHealthyRef = useRef(false)
  const localWriteChainRef = useRef<Promise<void>>(Promise.resolve())
  const elapsedAccumulatedRef = useRef(0)
  const elapsedRunStartedRef = useRef<number | null>(null)
  const orphanTranslationsRef = useRef(new Map<string, TranslationSegment>())
  const wordCounterRef = useRef(new WordCounter())
  const historyRequestRef = useRef(0)
  const historyLoadRequestRef = useRef(0)
  const destroyTimerRef = useRef<number | null>(null)
  const lifecycleEpochRef = useRef(0)
  const startPromiseRef = useRef<Promise<void> | null>(null)
  const stopPromiseRef = useRef<Promise<void> | null>(null)
  const ownerGenerationRef = useRef(0)
  const sessionLockKeyRef = useRef('')
  const sessionLockReleaseRef = useRef<(() => void) | null>(null)
  const cloudMetadataSyncRef = useRef(new Map<string, {
    appliedRevision: number
    desired: Parameters<typeof updateCloudSession>[1]
    operation: Promise<void> | null
    ownerId: string
    revision: number
  }>())
  const onlineRecoveryRef = useRef<Promise<void> | null>(null)
  const renderedOwnerIdRef = useRef<string | null>(user?.id ?? null)
  const renderedOwnerId = user?.id ?? null
  if (renderedOwnerIdRef.current !== renderedOwnerId) {
    renderedOwnerIdRef.current = renderedOwnerId
    ownerGenerationRef.current += 1
  }

  const [cloudQueue] = useState(() => new CloudTranscriptQueue({
    maxPending: 100_000,
    onPendingChange: setCloudPending,
    onError: (reason) => setError(`云端同步暂时失败：${reason.message}`),
    onBatchSaved: async (batch) => {
      await repository.acknowledgeCloudTranscriptOutbox(
        batch.entries.flatMap((entry) => (
          entry.durableVersion === undefined
            ? []
            : [{
                ownerId: batch.ownerId,
                sessionId: batch.sessionId,
                clientSegmentId: entry.clientSegmentId,
                updatedAt: entry.durableVersion,
              }]
        )),
      )
    },
  }))
  const [ragQueue] = useState(() => new RagIngestQueue())

  settingsRef.current = settings
  ragEnabledRef.current = ragEnabled
  userRef.current = user
  balanceCallbackRef.current = onBalanceUpdated

  const releaseSessionLock = useCallback(() => {
    sessionLockReleaseRef.current?.()
    sessionLockReleaseRef.current = null
    sessionLockKeyRef.current = ''
  }, [])

  const acquireSessionLock = useCallback(async (
    activeSessionId: string,
    ownerId: string | null,
  ): Promise<boolean> => {
    const lockManager = navigator.locks
    if (!lockManager) return true
    const lockKey = `dreamtrans:${ownerId ?? 'anonymous'}:${activeSessionId}`
    if (
      sessionLockKeyRef.current === lockKey
      && sessionLockReleaseRef.current
    ) {
      return true
    }
    releaseSessionLock()
    let releaseGate!: () => void
    const gate = new Promise<void>((resolve) => {
      releaseGate = resolve
    })
    const granted = new Promise<boolean>((resolve, reject) => {
      void lockManager.request(
        lockKey,
        { mode: 'exclusive', ifAvailable: true },
        async (lock) => {
          if (!lock) {
            resolve(false)
            return
          }
          sessionLockKeyRef.current = lockKey
          sessionLockReleaseRef.current = releaseGate
          resolve(true)
          await gate
        },
      ).catch(reject)
    })
    return granted
  }, [releaseSessionLock])

  const syncCloudMetadata = useCallback((
    activeSessionId: string,
    data: Parameters<typeof updateCloudSession>[1],
  ): Promise<void> => {
    const ownerId = repository.currentOwnerId()
    if (!ownerId || userRef.current?.id !== ownerId) {
      return Promise.reject(new Error('云端会话所属账号已变化'))
    }
    const queueKey = `${ownerId}\u0000${activeSessionId}`
    let state = cloudMetadataSyncRef.current.get(queueKey)
    if (!state) {
      state = {
        appliedRevision: 0,
        desired: {},
        operation: null,
        ownerId,
        revision: 0,
      }
      cloudMetadataSyncRef.current.set(queueKey, state)
    }
    state.desired = { ...state.desired, ...data }
    state.revision += 1
    if (state.operation) return state.operation

    const target = state
    const operation = (async () => {
      while (target.appliedRevision < target.revision) {
        if (userRef.current?.id !== target.ownerId) {
          throw new Error('云端会话所属账号已变化')
        }
        const revision = target.revision
        const desired = { ...target.desired }
        await updateCloudSessionWithRetry(
          activeSessionId,
          desired,
          3,
          () => (
            repository.currentOwnerId() === target.ownerId
            && userRef.current?.id === target.ownerId
          ),
        )
        target.appliedRevision = revision
      }
    })()
      .finally(() => {
        if (target.operation === operation) target.operation = null
        if (
          target.appliedRevision >= target.revision
          && cloudMetadataSyncRef.current.get(queueKey) === target
        ) {
          cloudMetadataSyncRef.current.delete(queueKey)
        }
      })
    target.operation = operation
    return operation
  }, [repository])

  const ownerScopeIsCurrent = useCallback((
    generation: number,
    ownerId: string | null,
  ) => (
    ownerGenerationRef.current === generation
    && repositoryOwnerRef.current === ownerId
    && (userRef.current?.id ?? null) === ownerId
  ), [])

  const setRecorderStatus = useCallback((status: RecorderStatus) => {
    statusRef.current = status
    setRecorderStatusState(status)
  }, [])

  const updateElapsed = useCallback(() => {
    const runningSince = elapsedRunStartedRef.current
    const elapsed = elapsedAccumulatedRef.current + (
      runningSince === null ? 0 : performance.now() - runningSince
    )
    setElapsedSeconds(Math.floor(elapsed / 1_000))
  }, [])

  const enqueueLocal = useCallback((
    operation: (
      scopedRepository: IndexedDbSessionRepository<
        TranscriptSegment,
        TranslationSegment
      >,
    ) => Promise<unknown>,
    propagateError = false,
  ): Promise<void> => {
    // Capture the owner before this write waits behind earlier IndexedDB work.
    // Even if a 15s UI timeout lets logout continue, the underlying operation
    // can only ever finish inside the account that produced it.
    const scopedRepository = repositoryForOwner(repository.currentOwnerId())
    setLocalPending((count) => count + 1)
    const operationResult = localWriteChainRef.current.then(async () => {
      await withOperationTimeout(
        Promise.resolve().then(() => operation(scopedRepository)),
        15_000,
        '本地数据库写入超过 15 秒，已停止等待',
      )
    })
    localWriteChainRef.current = operationResult
      .catch((reason: unknown) => {
        setError(`本地保存失败：${reason instanceof Error ? reason.message : String(reason)}`)
      })
      .finally(() => setLocalPending((count) => Math.max(0, count - 1)))
    return propagateError ? operationResult : localWriteChainRef.current
  }, [repository, repositoryForOwner])

  const queueCloudInput = useCallback((
    cloudSessionId: string,
    input: TranscriptInput,
  ) => {
    // During logout/account transition the live session is stopped before the
    // repository owner changes. Persist its final records under that captured
    // owner even though the React user may already be null; CloudTranscriptQueue
    // independently refuses to send them with another account's token.
    const ownerId = repository.currentOwnerId()
    if (!ownerId) return
    void enqueueLocal(async (scopedRepository) => {
      const outbox = await scopedRepository.upsertCloudTranscriptOutbox(
        cloudSessionId,
        input.client_segment_id,
        input,
      )
      if (
        currentSessionRef.current === cloudSessionId
        && !cloudSessionVerifiedRef.current
      ) {
        return
      }
      cloudQueue.restore([{
        ownerId: outbox.ownerId,
        sessionId: outbox.sessionId,
        input,
        durableVersion: outbox.updatedAt,
      }])
    })
  }, [cloudQueue, enqueueLocal, repository])

  const restoreCloudSessionOutbox = useCallback(async (
    cloudSessionId: string,
    expectedOwnerId = repository.currentOwnerId(),
  ) => {
    if (!expectedOwnerId || userRef.current?.id !== expectedOwnerId) return
    const scopedRepository = repositoryForOwner(expectedOwnerId)
    let after:
      | { createdAt: number; clientSegmentId: string }
      | undefined
    do {
      if (userRef.current?.id !== expectedOwnerId) return
      const page = await scopedRepository.getCloudTranscriptOutboxPage<TranscriptInput>(
        cloudSessionId,
        {
          limit: 500,
          ...(after ? { after } : {}),
        },
      )
      cloudQueue.restore(page.items.flatMap((record) => (
        isTranscriptInput(record.payload)
          ? [{
              ownerId: record.ownerId,
              sessionId: record.sessionId,
              input: record.payload,
              durableVersion: record.updatedAt,
            }]
          : []
      )))
      if (!page.hasMore || !page.nextCursor) return
      after = page.nextCursor
    } while (after)
  }, [cloudQueue, repository, repositoryForOwner])

  const restoreCloudOutbox = useCallback(async () => {
    const ownerId = repository.currentOwnerId()
    if (!ownerId) return
    for await (const metadata of repository.iterateSessions(100)) {
      if (metadata.origin !== 'cloud') continue
      if (metadata.cloudSessionPending) {
        const recoveryUser = userRef.current
        if (!recoveryUser || recoveryUser.id !== ownerId) continue
        try {
          try {
            await getCloudSession(metadata.id, { includeTranscripts: false })
          } catch (reason) {
            if (!(reason instanceof ApiRequestError) || reason.status !== 404) {
              throw reason
            }
            const recreated = await createCloudSession({
              client_session_id: metadata.id,
              title: metadata.title,
              source_language:
                metadata.sourceLanguage ?? settingsRef.current.sourceLanguage,
              target_language:
                metadata.targetLanguage ?? settingsRef.current.targetLanguage,
            })
            if (recreated.id !== metadata.id) {
              throw new Error(
                '云端会话恢复返回了不一致的会话 ID',
                { cause: reason },
              )
            }
          }
          if (
            repository.currentOwnerId() !== ownerId
            || userRef.current?.id !== ownerId
          ) {
            return
          }
          await repository.updateSessionMetadata(metadata.id, {
            cloudSessionPending: false,
          }, { touch: false })
          if (currentSessionRef.current === metadata.id) {
            cloudSessionVerifiedRef.current = true
            if (cloudSessionRef.current === metadata.id) {
              cloudQueue.setSession(metadata.id)
            }
          }
          void syncCloudMetadata(metadata.id, {
            ...(metadata.title ? { title: metadata.title } : {}),
            status: metadata.status === 'active' ? 'active' : 'completed',
            duration_seconds: Math.round((metadata.durationMs ?? 0) / 1_000),
          }).catch(() => undefined)
        } catch {
          // Keep the durable pending marker and outbox untouched. A later
          // online event or application restart will retry the same UUID.
          continue
        }
      }
      await restoreCloudSessionOutbox(metadata.id, ownerId)
    }
  }, [
    cloudQueue,
    repository,
    restoreCloudSessionOutbox,
    syncCloudMetadata,
  ])

  const refreshHistory = useCallback(async () => {
    const ownerGeneration = ownerGenerationRef.current
    const ownerId = repositoryOwnerRef.current
    if (!ownerScopeIsCurrent(ownerGeneration, ownerId)) return
    const request = ++historyRequestRef.current
    const isCurrent = () => (
      request === historyRequestRef.current
      && ownerScopeIsCurrent(ownerGeneration, ownerId)
    )
    // HistoryPanel keeps an existing list visible while loading; only blanks
    // the panel when there is nothing to show yet.
    setHistoryLoading(true)
    try {
      const [localPage, legacyCount] = await Promise.all([
        repository.listSessions({ limit: 60 }),
        repository.countLegacySessions(),
      ])
      if (!isCurrent()) return
      const merged = new Map<string, HistorySession>()
      const localById = new Map(
        localPage.items.map((metadata) => [metadata.id, metadata]),
      )
      for (const metadata of localPage.items) {
        merged.set(metadata.id, {
          id: metadata.id,
          title: metadata.title || '未命名会话',
          createdAt: metadata.createdAt,
          durationSeconds: Math.round((metadata.durationMs ?? 0) / 1_000),
          status: metadata.status,
          location: metadata.origin,
        })
      }

      // Paint local cache immediately so the sidebar is usable while the cloud
      // list is still in flight.
      setLegacyHistoryCount(legacyCount)
      setHistorySessions(
        [...merged.values()]
          .sort((left, right) => right.createdAt - left.createdAt)
          .slice(0, 60),
      )
      if (!userRef.current) setHistoryLoading(false)

      if (userRef.current) {
        try {
          const cloud = await listCloudSessions(1, 60)
          if (!isCurrent()) return
          for (const session of cloud.sessions) {
            const local = localById.get(session.id)
            const cloudUpdatedAt = Date.parse(session.updated_at) || 0
            const localWins = Boolean(local && local.updatedAt > cloudUpdatedAt)
            const status = localWins
              ? local?.status ?? 'active'
              : session.status === 'active' || session.status === 'paused'
                ? 'active'
                : 'completed'
            merged.set(session.id, {
              id: session.id,
              title: localWins
                ? local?.title || session.title || '未命名会话'
                : session.title || '未命名会话',
              createdAt: Date.parse(session.created_at) || Date.now(),
              durationSeconds: Math.max(
                session.duration_seconds || 0,
                (local?.durationMs ?? 0) / 1_000,
              ),
              status,
              location: 'cloud',
            })
            if (localWins && local) {
              const desiredStatus = local.status === 'active'
                ? 'active'
                : 'completed'
              const desiredDuration = Math.round((local.durationMs ?? 0) / 1_000)
              if (
                (local.title && local.title !== session.title)
                || desiredStatus !== session.status
                || desiredDuration > (session.duration_seconds || 0)
              ) {
                void syncCloudMetadata(session.id, {
                  ...(local.title ? { title: local.title } : {}),
                  status: desiredStatus,
                  duration_seconds: Math.max(
                    desiredDuration,
                    session.duration_seconds || 0,
                  ),
                }).catch(() => undefined)
              }
            }
          }
        } catch (reason) {
          if (isCurrent()) {
            setError(`云端历史读取失败：${reason instanceof Error ? reason.message : String(reason)}`)
          }
        }
      }
      if (isCurrent()) {
        setHistorySessions(
          [...merged.values()]
            .sort((left, right) => right.createdAt - left.createdAt)
            .slice(0, 60),
        )
      }
    } catch (reason) {
      if (isCurrent()) {
        setError(`历史会话读取失败：${reason instanceof Error ? reason.message : String(reason)}`)
      }
    } finally {
      if (isCurrent()) setHistoryLoading(false)
    }
  }, [ownerScopeIsCurrent, repository, syncCloudMetadata])

  const migrateLegacyHistory = useCallback(async () => {
    if (statusRef.current !== 'idle') {
      setError('请先结束当前录音，再迁移旧版历史。')
      return
    }
    setHistoryLoading(true)
    setError(null)
    try {
      await migrateLegacySessionStorage()
      setLegacyHistoryCount(0)
      await refreshHistory()
    } catch (reason) {
      setError(
        `旧版历史迁移失败：${reason instanceof Error ? reason.message : String(reason)}`,
      )
    } finally {
      setHistoryLoading(false)
    }
  }, [refreshHistory])

  const applyLoadedRecords = useCallback((
    records: StoredSessionRecords,
    loadedDurationSeconds: number,
    loadedSessionId = '',
  ) => {
    transcriptStore.reset()
    transcriptStore.batch(() => {
      for (const segment of records.segments) transcriptStore.appendTranscript(segment)
      for (const translation of records.translations) {
        try {
          transcriptStore.appendTranslation(translation)
        } catch {
          transcriptStore.appendTranslation({ ...translation, segmentId: null })
        }
      }
    })
    feedModel.hydrate(records.segments, records.translations)
    wordCounterRef.current.reset()
    for (const segment of records.segments) wordCounterRef.current.add(segment.text)
    setTopWords(wordCounterRef.current.getTop())
    orphanTranslationsRef.current.clear()
    if (loadedSessionId) {
      lexReplace(
        loadedSessionId,
        records.segments.map((segment) => segment.text),
      )
    }
    elapsedAccumulatedRef.current = loadedDurationSeconds * 1_000
    elapsedRunStartedRef.current = null
    setElapsedSeconds(Math.floor(loadedDurationSeconds))
  }, [feedModel, transcriptStore])

  const stop = useCallback((): Promise<void> => {
    if (stopPromiseRef.current) return stopPromiseRef.current
    if (statusRef.current === 'idle') return Promise.resolve()

    lifecycleEpochRef.current += 1
    setRecorderStatus('stopping')
    if (elapsedRunStartedRef.current !== null) {
      elapsedAccumulatedRef.current += performance.now() - elapsedRunStartedRef.current
      elapsedRunStartedRef.current = null
    }
    updateElapsed()

    const operation = (async () => {
      const stopFailures: Error[] = []
      let aiDrainCompleted = true
      const rememberFailure = (reason: unknown) => {
        if (stopFailures.length === 0) {
          stopFailures.push(
            reason instanceof Error ? reason : new Error(String(reason)),
          )
        }
      }

      // Stop capture first so its final PCM reaches the still-open
      // transcription socket. Then wait for Speechmatics EndOfStream finals,
      // feed those finals into the AI chunker, and only then drain translation.
      // This order keeps the last sentence instead of closing its consumer
      // before the provider has emitted it.
      const initialCapture = captureRef.current
      await initialCapture?.stop().catch(rememberFailure)
      const capture = captureRef.current
      captureRef.current = null
      if (capture && capture !== initialCapture) {
        await capture.stop().catch(rememberFailure)
      }
      // Let already-finalized speech translate while Speechmatics emits its
      // EndOfStream finals. The translator remains active until client.stop()
      // resolves, so those last finals are still accepted before the final
      // drain is sealed.
      if (sessionTranslationEngineRef.current === 'ai') aiTranslator.flush()
      await client.stop().catch(rememberFailure)
      try {
        aiDrainCompleted = await aiTranslator.stopSession()
      } catch (reason) {
        rememberFailure(reason)
      }
      sessionTranslationEngineRef.current = ''
      sessionAuthRequiredRef.current = false

      const activeSessionId = currentSessionRef.current
      if (activeSessionId) {
        await localWriteChainRef.current
        const durationMs = Math.round(elapsedAccumulatedRef.current)
        try {
          await repository.completeSession(activeSessionId, { durationMs })
        } catch (reason) {
          rememberFailure(reason)
        }

        if (cloudSessionRef.current === activeSessionId) {
          try {
            // Local completion is the durability boundary. Cloud transcript
            // batches continue in the bounded background queue so stopping a
            // long session cannot wait on every network request.
            void cloudQueue.flush().catch(() => undefined)
            void syncCloudMetadata(activeSessionId, {
              status: 'completed',
              duration_seconds: Math.round(durationMs / 1_000),
            }).catch((reason: unknown) => {
              setError(
                `会话已在本地完成，云端状态将在网络恢复后重试：${
                  reason instanceof Error ? reason.message : String(reason)
                }`,
              )
            })
          } catch (reason) {
            rememberFailure(reason)
          }
        }
      }

      cloudQueue.setSession(null)
      cloudSessionRef.current = null
      releaseSessionLock()
      setRecorderStatus('idle')
      const stopFailure = stopFailures[0]
      if (stopFailure) {
        setError(`会话已在本地收尾，但有一步失败：${stopFailure.message}`)
      } else if (!aiDrainCompleted) {
        setError('原文和录音已保存，但弱网下仍有 AI 翻译等待超时。')
      }
      void refreshHistory()
      void balanceCallbackRef.current?.()
    })()

    const tracked = operation
      .catch((reason: unknown) => {
        sessionAuthRequiredRef.current = false
        releaseSessionLock()
        setRecorderStatus('idle')
        setError(
          `会话收尾失败：${reason instanceof Error ? reason.message : String(reason)}`,
        )
      })
      .finally(() => {
        if (stopPromiseRef.current === tracked) stopPromiseRef.current = null
      })
    stopPromiseRef.current = tracked
    return tracked
  }, [
    aiTranslator,
    client,
    cloudQueue,
    refreshHistory,
    releaseSessionLock,
    repository,
    setRecorderStatus,
    syncCloudMetadata,
    updateElapsed,
  ])

  const handleCaptureError = useCallback((captureError: AudioCaptureError) => {
    if (captureError.code === 'microphone-ended') {
      const source = settingsRef.current.audioSource
      const sourceLabel = source === 'system'
        ? '系统音频'
        : source === 'mixed'
          ? '音频输入'
          : '麦克风'
      setError(`${sourceLabel}已断开，正在安全结束会话：${captureError.message}`)
      void stop()
      return
    }
    if (
      captureError.code === 'audio-encoder-failed'
      || captureError.code === 'audio-storage-backpressure'
      || captureError.code === 'audio-storage-write-failed'
    ) {
      localAudioHealthyRef.current = false
      const activeSessionId = currentSessionRef.current
      if (activeSessionId) {
        void enqueueLocal(
          (scopedRepository) => scopedRepository.markLocalAudioIncomplete(
            activeSessionId,
          ),
        )
      }
      setError(`本地录音保存已停止；实时转录可能继续：${captureError.message}`)
      return
    }
    setError(captureError.message)
  }, [enqueueLocal, stop])

  const start = useCallback((): Promise<void> => {
    if (statusRef.current !== 'idle') {
      return startPromiseRef.current ?? Promise.resolve()
    }

    const epoch = ++lifecycleEpochRef.current
    const isCurrent = () => lifecycleEpochRef.current === epoch
    const assertCurrent = () => {
      if (!isCurrent()) {
        throw new DOMException('Session start was cancelled', 'AbortError')
      }
    }

    setError(null)
    setRecorderStatus('starting')
    const activeSettings = settingsRef.current
    const startingUser = userRef.current
    const startingOwnerId = repositoryOwnerRef.current
    sessionAuthRequiredRef.current = startingUser !== null
    const sessionTitle = defaultSessionTitle()
    const createdAt = Date.now()
    let nextSessionId: string = crypto.randomUUID()
    let cloudCreated = false
    let cloudCreationUncertain = false
    let startingCapture: BrowserAudioCapture | null = null

    const operation = (async () => {
      try {
        await ensureSpeechmaticsPreflight()
        assertCurrent()
        if (startingUser) {
          try {
            let cloudSession: Awaited<ReturnType<typeof createCloudSession>> | null = null
            let lastCreateFailure: unknown
            for (const delayMs of [0, 400, 1_200]) {
              if (delayMs > 0) await waitForRetry(delayMs)
              assertCurrent()
              try {
                cloudSession = await createCloudSession({
                  client_session_id: nextSessionId,
                  title: sessionTitle,
                  source_language: activeSettings.sourceLanguage,
                  target_language: activeSettings.targetLanguage,
                })
                break
              } catch (reason) {
                lastCreateFailure = reason
                cloudCreationUncertain = (
                  cloudCreationUncertain || shouldRetryCloudRequest(reason)
                )
                if (!shouldRetryCloudRequest(reason)) throw reason
              }
            }
            if (!cloudSession) throw lastCreateFailure ?? new Error('云端会话创建失败')
            nextSessionId = cloudSession.id
            cloudCreated = true
            cloudCreationUncertain = false
          } catch (reason) {
            assertCurrent()
            setError(
              cloudCreationUncertain
                ? `云端会话响应未能确认；本地记录会保留同一会话 ID，网络恢复后可安全续传：${
                    reason instanceof Error ? reason.message : String(reason)
                  }`
                : `云端会话创建失败，已切换为本地保存：${
                    reason instanceof Error ? reason.message : String(reason)
                  }`,
            )
          }
        }
        assertCurrent()
        if (!await acquireSessionLock(nextSessionId, startingOwnerId)) {
          throw new Error('这个会话已在另一个标签页录制，请先在那里结束录制')
        }
        assertCurrent()

        currentSessionRef.current = nextSessionId
        const cloudIntended = cloudCreated || cloudCreationUncertain
        currentLocationRef.current = cloudIntended ? 'cloud' : 'local'
        cloudSessionRef.current = cloudIntended ? nextSessionId : null
        cloudSessionVerifiedRef.current = cloudCreated
        cloudQueue.setOwner(startingUser?.id ?? null)
        cloudQueue.setSession(cloudCreated ? nextSessionId : null)
        setSessionId(nextSessionId)
        setSessionSourceLanguage(activeSettings.sourceLanguage)
        setTitle(sessionTitle)
        elapsedAccumulatedRef.current = 0
        elapsedRunStartedRef.current = null
        setElapsedSeconds(0)
        orphanTranslationsRef.current.clear()
        wordCounterRef.current.reset()
        setTopWords([])
        lexReset(nextSessionId)
        transcriptStore.reset()
        feedModel.reset({
          sourceLanguage: activeSettings.sourceLanguage,
          targetLanguage: activeSettings.targetLanguage,
          translationEnabled: activeSettings.translationEnabled,
        })
        ragQueue.clear()

        await repository.ensureSession(nextSessionId, {
          createdAt,
          origin: cloudIntended ? 'cloud' : 'local',
          cloudSessionPending: cloudCreationUncertain,
          sourceLanguage: activeSettings.sourceLanguage,
          targetLanguage: activeSettings.targetLanguage,
          title: sessionTitle,
          status: 'active',
        })
        assertCurrent()
        if (cloudCreationUncertain && startingUser) {
          // navigator.onLine often remains true during a proxy/server outage,
          // so an `online` event may never arrive. Retry deterministic cloud
          // verification in the background while recording continues locally.
          void (async () => {
            for (const delayMs of [5_000, 15_000, 30_000, 60_000]) {
              await waitForRetry(delayMs)
              if (
                !isCurrent()
                || userRef.current?.id !== startingUser.id
                || cloudSessionVerifiedRef.current
              ) {
                return
              }
              await restoreCloudOutbox().catch(() => undefined)
            }
          })()
        }
        // Never silently change the translation provider. If the user chose
        // AI, keep that engine and surface an AI capability/connectivity error
        // while preserving the original transcript.
        // Learning mode is original-first with local glosses; never stack AI
        // translation latency on the live path.
        const learningLive = activeSettings.assistMode === 'learn'
        const useAiTranslation = !learningLive
          && activeSettings.translationEnabled
          && activeSettings.translationEngine === 'ai'
        const useSpeechmaticsTranslation = !learningLive
          && activeSettings.translationEnabled
          && !useAiTranslation
        sessionTranslationEngineRef.current = useAiTranslation
          ? 'ai'
          : useSpeechmaticsTranslation
            ? 'speechmatics'
            : ''
        sessionTargetLanguageRef.current = activeSettings.targetLanguage
        // Match Speechmatics clock to the AudioContext the browser will run,
        // otherwise resampling drift shows up as growing / random transcript lag.
        const captureSampleRate = await probePreferredAudioSampleRate(48_000)
        assertCurrent()
        await client.start({
          language: activeSettings.sourceLanguage,
          enable_partials: true,
          diarization: 'speaker',
          operating_point: 'enhanced',
          max_delay: 2.0,
          audio_format: {
            type: 'raw',
            encoding: 'pcm_f32le',
            sample_rate: captureSampleRate,
            channels: 1,
          },
          ...(useSpeechmaticsTranslation
            ? {
                translation_config: {
                  target_languages: [activeSettings.targetLanguage],
                  enable_partials: true,
                },
              }
            : {}),
        })
        assertCurrent()
        if (useAiTranslation) {
          aiTranslator.startSession({
            ...(cloudCreated ? { sessionId: nextSessionId } : {}),
            translatePrompt: activeSettings.translatePrompt.trim()
              || defaultTranslatePromptFor(
                activeSettings.sourceLanguage,
                activeSettings.targetLanguage,
              ),
          })
        }

        const capture = new BrowserAudioCapture({
          audioSource: activeSettings.audioSource,
          sampleRate: captureSampleRate,
          onError: handleCaptureError,
          onPCM: (audio) => {
            try {
              client.sendAudio(audio)
            } catch (reason) {
              if (isCurrent()) {
                setError(
                  `音频发送失败：${reason instanceof Error ? reason.message : String(reason)}`,
                )
              }
            }
          },
          ...(activeSettings.keepLocalAudio
            ? {
                onChunk: async (chunk: { sequence: number; recordedAt: number; blob: Blob }) => {
                  await enqueueLocal((scopedRepository) => (
                    scopedRepository.appendAudioChunk(
                    nextSessionId,
                    chunk.blob,
                    {
                      sequence: chunk.sequence,
                      capturedAt: chunk.recordedAt,
                      durationMs: 2_000,
                      mimeType: chunk.blob.type,
                    },
                    )
                  ), true)
                },
              }
            : {}),
        })
        startingCapture = capture
        localAudioHealthyRef.current = activeSettings.keepLocalAudio
        captureRef.current = capture
        currentAudioMimeTypeRef.current = capture.mimeType
        await capture.start()
        assertCurrent()
        if (activeSettings.keepLocalAudio) {
          await repository.updateSessionMetadata(nextSessionId, {
            audioMimeType: capture.mimeType,
          })
          assertCurrent()
        }
        elapsedRunStartedRef.current = performance.now()
        setRecorderStatus('recording')
        void refreshHistory()
      } catch (reason) {
        const cancelled = !isCurrent()
        const failure = reason instanceof Error ? reason : new Error(String(reason))
        if (captureRef.current === startingCapture) captureRef.current = null
        await startingCapture?.stop().catch(() => undefined)
        if (!cancelled) await client.stop().catch(() => undefined)
        if (sessionTranslationEngineRef.current === 'ai') {
          await aiTranslator.stopSession().catch(() => false)
        }
        sessionTranslationEngineRef.current = ''
        await localWriteChainRef.current
        if (cloudSessionRef.current === nextSessionId) {
          cloudSessionRef.current = null
          cloudSessionVerifiedRef.current = false
          cloudQueue.setSession(null)
        }
        let preserveCloudRecovery = false
        if (
          (cloudCreated || cloudCreationUncertain)
          && startingUser
          && userRef.current?.id === startingUser.id
        ) {
          try {
            await deleteCloudSession(nextSessionId)
          } catch {
            // A create or delete response can both be lost on the same bad
            // network. Keep the local cloud intent so a later retry can
            // reconcile the deterministic session ID instead of orphaning a
            // quota-consuming server session.
            preserveCloudRecovery = true
          }
        } else if (cloudCreated || cloudCreationUncertain) {
          preserveCloudRecovery = true
        }
        if (repositoryOwnerRef.current === startingOwnerId) {
          if (preserveCloudRecovery) {
            await repository.completeSession(nextSessionId).catch(() => undefined)
          } else {
            await repository.deleteSession(nextSessionId).catch(() => undefined)
          }
        }
        if (currentSessionRef.current === nextSessionId) {
          currentSessionRef.current = ''
          setSessionId('')
          setSessionSourceLanguage(settingsRef.current.sourceLanguage)
        }
        if (sessionLockKeyRef.current.endsWith(`:${nextSessionId}`)) {
          releaseSessionLock()
        }
        sessionAuthRequiredRef.current = false
        if (!cancelled) {
          setRecorderStatus('idle')
          setError(`无法开始转录：${failure.message}`)
        }
      }
    })()

    const tracked = operation.finally(() => {
      if (startPromiseRef.current === tracked) startPromiseRef.current = null
    })
    startPromiseRef.current = tracked
    return tracked
  }, [
    aiTranslator,
    acquireSessionLock,
    client,
    cloudQueue,
    enqueueLocal,
    feedModel,
    handleCaptureError,
    ragQueue,
    refreshHistory,
    repository,
    releaseSessionLock,
    restoreCloudOutbox,
    setRecorderStatus,
    transcriptStore,
  ])

  const continueSession = useCallback((): Promise<void> => {
    if (statusRef.current !== 'idle') {
      return startPromiseRef.current ?? Promise.resolve()
    }
    const continuingSessionId = currentSessionRef.current
    if (!continuingSessionId) return start()

    const epoch = ++lifecycleEpochRef.current
    const isCurrent = () => lifecycleEpochRef.current === epoch
    const assertCurrent = () => {
      if (!isCurrent()) {
        throw new DOMException('Session continue was cancelled', 'AbortError')
      }
    }
    const activeSettings = settingsRef.current
    const startingUser = userRef.current
    const startingOwnerId = repositoryOwnerRef.current
    const continuingCloud = currentLocationRef.current === 'cloud' && startingUser !== null
    const transcriptTimelineEnd = transcriptStore.getSnapshot().stats.durationSeconds
    let startingCapture: BrowserAudioCapture | null = null
    let previousStatus: 'active' | 'completed' = 'completed'

    sessionAuthRequiredRef.current = startingUser !== null
    setError(null)
    setRecorderStatus('starting')

    const operation = (async () => {
      try {
        const metadata = await repository.getSessionMetadata(continuingSessionId)
        if (!metadata) throw new Error('本地会话不存在，无法继续录制')
        const sessionSourceLanguage =
          metadata.sourceLanguage ?? activeSettings.sourceLanguage
        const sessionTargetLanguage =
          metadata.targetLanguage ?? activeSettings.targetLanguage
        if (!await acquireSessionLock(continuingSessionId, startingOwnerId)) {
          throw new Error('这个会话已在另一个标签页录制，请先在那里结束录制')
        }
        assertCurrent()
        if (
          activeSettings.keepLocalAudio
          && metadata.audioChunkCount > 0
          && metadata.audioMimeType
          && !metadata.audioMimeType.includes('mpeg')
          && !metadata.audioMimeType.includes('mp3')
        ) {
          throw new Error(
            '旧版录音容器不能安全续接。请先下载旧录音，或在设置中关闭“保留本地录音”后继续转录。',
          )
        }
        previousStatus = metadata.status
        if (continuingCloud && !cloudSessionVerifiedRef.current) {
          try {
            await getCloudSession(continuingSessionId, {
              includeTranscripts: false,
            })
            cloudSessionVerifiedRef.current = true
          } catch (reason) {
            if (reason instanceof ApiRequestError && reason.status === 404) {
              const recreated = await createCloudSession({
                client_session_id: continuingSessionId,
                title: metadata.title,
                source_language: sessionSourceLanguage,
                target_language: sessionTargetLanguage,
              })
              if (recreated.id !== continuingSessionId) {
                throw new Error(
                  '云端会话恢复返回了不一致的会话 ID',
                  { cause: reason },
                )
              }
              cloudSessionVerifiedRef.current = true
            } else {
              throw new Error(
                `无法确认云端会话状态：${
                  reason instanceof Error ? reason.message : String(reason)
                }`,
                { cause: reason },
              )
            }
          }
          await repository.updateSessionMetadata(continuingSessionId, {
            cloudSessionPending: false,
            sourceLanguage: sessionSourceLanguage,
            targetLanguage: sessionTargetLanguage,
          }, { touch: false })
          assertCurrent()
          await restoreCloudSessionOutbox(
            continuingSessionId,
            startingOwnerId,
          )
          assertCurrent()
        }
        const audioSequenceOffset = metadata.nextAudioSequence
        const timelineOffset = Math.max(
          transcriptTimelineEnd,
          (metadata.durationMs ?? 0) / 1_000,
        )
        await ensureSpeechmaticsPreflight()
        assertCurrent()
        await repository.updateSessionMetadata(continuingSessionId, {
          status: 'active',
          sourceLanguage: sessionSourceLanguage,
          targetLanguage: sessionTargetLanguage,
        })
        assertCurrent()
        setSessionSourceLanguage(sessionSourceLanguage)

        cloudQueue.setOwner(startingUser?.id ?? null)
        cloudSessionRef.current = continuingCloud ? continuingSessionId : null
        cloudQueue.setSession(continuingCloud ? continuingSessionId : null)

        const learningLive = activeSettings.assistMode === 'learn'
        const useAiTranslation = !learningLive
          && activeSettings.translationEnabled
          && activeSettings.translationEngine === 'ai'
        const useSpeechmaticsTranslation = !learningLive
          && activeSettings.translationEnabled
          && !useAiTranslation
        sessionTranslationEngineRef.current = useAiTranslation
          ? 'ai'
          : useSpeechmaticsTranslation
            ? 'speechmatics'
            : ''
        sessionTargetLanguageRef.current = sessionTargetLanguage
        const captureSampleRate = await probePreferredAudioSampleRate(48_000)
        assertCurrent()
        await client.start({
          timeline_offset_seconds: timelineOffset,
          language: sessionSourceLanguage,
          enable_partials: true,
          diarization: 'speaker',
          operating_point: 'enhanced',
          max_delay: 2.0,
          audio_format: {
            type: 'raw',
            encoding: 'pcm_f32le',
            sample_rate: captureSampleRate,
            channels: 1,
          },
          ...(useSpeechmaticsTranslation
            ? {
                translation_config: {
                  target_languages: [sessionTargetLanguage],
                  enable_partials: true,
                },
              }
            : {}),
        })
        assertCurrent()
        if (useAiTranslation) {
          aiTranslator.startSession({
            ...(continuingCloud ? { sessionId: continuingSessionId } : {}),
            translatePrompt: activeSettings.translatePrompt.trim()
              || defaultTranslatePromptFor(
                sessionSourceLanguage,
                sessionTargetLanguage,
              ),
          })
        }

        const capture = new BrowserAudioCapture({
          audioSource: activeSettings.audioSource,
          sampleRate: captureSampleRate,
          onError: handleCaptureError,
          onPCM: (audio) => {
            try {
              client.sendAudio(audio)
            } catch (reason) {
              if (isCurrent()) {
                setError(
                  `音频发送失败：${reason instanceof Error ? reason.message : String(reason)}`,
                )
              }
            }
          },
          ...(activeSettings.keepLocalAudio
            ? {
                onChunk: async (chunk: { sequence: number; recordedAt: number; blob: Blob }) => {
                  await enqueueLocal((scopedRepository) => (
                    scopedRepository.appendAudioChunk(
                    continuingSessionId,
                    chunk.blob,
                    {
                      sequence: audioSequenceOffset + chunk.sequence,
                      capturedAt: chunk.recordedAt,
                      durationMs: 2_000,
                      mimeType: chunk.blob.type,
                    },
                    )
                  ), true)
                },
              }
            : {}),
        })
        startingCapture = capture
        localAudioHealthyRef.current = activeSettings.keepLocalAudio
        captureRef.current = capture
        currentAudioMimeTypeRef.current = activeSettings.keepLocalAudio
          ? capture.mimeType
          : metadata.audioMimeType || currentAudioMimeTypeRef.current
        await capture.start()
        assertCurrent()
        if (activeSettings.keepLocalAudio) {
          await repository.updateSessionMetadata(continuingSessionId, {
            audioMimeType: capture.mimeType,
          })
          assertCurrent()
        }
        if (continuingCloud) {
          void syncCloudMetadata(continuingSessionId, {
            status: 'active',
          }).catch(() => undefined)
        }
        elapsedAccumulatedRef.current = Math.max(
          elapsedAccumulatedRef.current,
          (metadata.durationMs ?? 0),
        )
        elapsedRunStartedRef.current = performance.now()
        setRecorderStatus('recording')
        void refreshHistory()
      } catch (reason) {
        const cancelled = !isCurrent()
        const failure = reason instanceof Error ? reason : new Error(String(reason))
        if (captureRef.current === startingCapture) captureRef.current = null
        await startingCapture?.stop().catch(() => undefined)
        if (!cancelled) await client.stop().catch(() => undefined)
        if (sessionTranslationEngineRef.current === 'ai') {
          await aiTranslator.stopSession().catch(() => false)
        }
        sessionTranslationEngineRef.current = ''
        if (cloudSessionRef.current === continuingSessionId) {
          cloudSessionRef.current = null
          cloudSessionVerifiedRef.current = false
          cloudQueue.setSession(null)
        }
        if (repositoryOwnerRef.current === startingOwnerId) {
          await repository.updateSessionMetadata(continuingSessionId, {
            status: previousStatus,
          }).catch(() => undefined)
        }
        sessionAuthRequiredRef.current = false
        if (sessionLockKeyRef.current.endsWith(`:${continuingSessionId}`)) {
          releaseSessionLock()
        }
        if (!cancelled) {
          setRecorderStatus('idle')
          setError(`无法继续录制：${failure.message}`)
        }
      }
    })()

    const tracked = operation.finally(() => {
      if (startPromiseRef.current === tracked) startPromiseRef.current = null
    })
    startPromiseRef.current = tracked
    return tracked
  }, [
    aiTranslator,
    acquireSessionLock,
    client,
    cloudQueue,
    enqueueLocal,
    handleCaptureError,
    refreshHistory,
    repository,
    releaseSessionLock,
    restoreCloudSessionOutbox,
    setRecorderStatus,
    start,
    syncCloudMetadata,
    transcriptStore,
  ])

  const pauseToggle = useCallback(() => {
    if (statusRef.current === 'recording' || statusRef.current === 'reconnecting') {
      const previousStatus = statusRef.current
      if (elapsedRunStartedRef.current !== null) {
        elapsedAccumulatedRef.current += performance.now() - elapsedRunStartedRef.current
        elapsedRunStartedRef.current = null
      }
      updateElapsed()
      captureRef.current?.setPaused(true)
      try {
        client.pause()
        setRecorderStatus('paused')
      } catch (reason) {
        // Keep capture, timer and UI state atomic with the transcription
        // client. A failed remote pause must not leave the microphone silently
        // paused while the interface still claims to be recording.
        captureRef.current?.setPaused(false)
        elapsedRunStartedRef.current = performance.now()
        setRecorderStatus(previousStatus)
        setError(`暂停失败：${reason instanceof Error ? reason.message : String(reason)}`)
      }
      return
    }
    if (statusRef.current !== 'paused') return
    const resumeEpoch = lifecycleEpochRef.current
    const resumeSessionId = currentSessionRef.current
    setRecorderStatus('reconnecting')
    void client.resume()
      .then(() => {
        if (
          lifecycleEpochRef.current !== resumeEpoch
          || currentSessionRef.current !== resumeSessionId
          || statusRef.current !== 'reconnecting'
        ) {
          return
        }
        captureRef.current?.setPaused(false)
        elapsedRunStartedRef.current = performance.now()
        setRecorderStatus('recording')
      })
      .catch((reason: unknown) => {
        if (
          lifecycleEpochRef.current !== resumeEpoch
          || currentSessionRef.current !== resumeSessionId
          || statusRef.current !== 'reconnecting'
        ) {
          return
        }
        setRecorderStatus('paused')
        setError(`继续录音失败：${reason instanceof Error ? reason.message : String(reason)}`)
      })
  }, [client, setRecorderStatus, updateElapsed])

  const loadHistory = useCallback(async (session: HistorySession) => {
    const ownerGeneration = ownerGenerationRef.current
    const ownerId = repositoryOwnerRef.current
    const loadRequest = ++historyLoadRequestRef.current
    const loadIsCurrent = () => (
      loadRequest === historyLoadRequestRef.current
      && ownerScopeIsCurrent(ownerGeneration, ownerId)
    )
    const assertLoadCurrent = () => {
      if (!loadIsCurrent()) {
        throw new DOMException('Session load owner changed', 'AbortError')
      }
    }
    if (!loadIsCurrent()) return
    if (statusRef.current === 'starting' || statusRef.current === 'stopping') {
      setError('会话正在切换状态，请稍候再加载历史记录。')
      return
    }
    if (statusRef.current !== 'idle') {
      const shouldStop = window.confirm('加载历史会话前需要结束当前录音，是否继续？')
      if (!shouldStop) return
      await stop()
      assertLoadCurrent()
    }
    // Session open must not blank the history list — report progress on the row.
    const reportOpening = (label: string, percent: number | null = null) => {
      if (!loadIsCurrent()) return
      setHistoryOpening({ sessionId: session.id, label, percent })
    }
    reportOpening('正在打开会话…', null)
    setError(null)
    try {
      let records: StoredSessionRecords
      let loadedTitle = session.title
      let loadedDuration = session.durationSeconds
      let loadedSourceLanguage = settingsRef.current.sourceLanguage

      if (session.location === 'cloud' && userRef.current) {
        reportOpening('正在读取云端元数据…', null)
        const cloud = await getCloudSession(session.id, {
          includeTranscripts: false,
        })
        assertLoadCurrent()
        loadedSourceLanguage = cloud.source_language
        const cloudUpdatedAt = Date.parse(cloud.updated_at) || 0
        const localMetadata = await repository.getSessionMetadata(cloud.id)
        assertLoadCurrent()

        // Prefer local cache whenever we already have content. Full multi-page
        // cloud downloads only run when the cache is missing, pending, or known
        // to be behind the cloud revision.
        const localCacheUsable = Boolean(
          localMetadata
          && !localMetadata.cloudSessionPending
          && localMetadata.transcriptCount > 0,
        )
        const localCacheFresh = Boolean(
          localCacheUsable
          && localMetadata
          && (
            (
              localMetadata.cloudContentUpdatedAt !== undefined
              && localMetadata.cloudContentUpdatedAt >= cloudUpdatedAt
            )
            // Local writes are still at least as new as the cloud metadata
            // (typical after a just-finished recording on this device).
            || localMetadata.updatedAt >= cloudUpdatedAt
          ),
        )
        if (localCacheFresh && localMetadata) {
          reportOpening('正在读取本机缓存…', null)
          records = await canonicalizeLocalSession(
            repository,
            cloud.id,
            localMetadata.targetLanguage ?? cloud.target_language,
          )
          assertLoadCurrent()
          const localWins = localMetadata.updatedAt > cloudUpdatedAt
          loadedTitle = localWins
            ? localMetadata.title || cloud.title
            : cloud.title
          loadedDuration = Math.max(
            cloud.duration_seconds,
            (localMetadata.durationMs ?? 0) / 1_000,
          )
          loadedSourceLanguage =
            localMetadata.sourceLanguage ?? cloud.source_language
          // Stamp the revision we validated so subsequent opens stay on the
          // fast path even if a title/status push advances cloud.updated_at.
          if (
            localMetadata.cloudContentUpdatedAt === undefined
            || localMetadata.cloudContentUpdatedAt < cloudUpdatedAt
          ) {
            await repository.updateSessionMetadata(cloud.id, {
              cloudContentUpdatedAt: Math.max(
                cloudUpdatedAt,
                localMetadata.cloudContentUpdatedAt ?? 0,
              ),
            }, { touch: false })
            assertLoadCurrent()
          }
          if (localWins) {
            const desiredStatus = localMetadata.status === 'active'
              ? 'active'
              : 'completed'
            if (
              (localMetadata.title && localMetadata.title !== cloud.title)
              || desiredStatus !== cloud.status
              || (localMetadata.durationMs ?? 0) > cloud.duration_seconds * 1_000
            ) {
              void syncCloudMetadata(cloud.id, {
                ...(localMetadata.title ? { title: localMetadata.title } : {}),
                status: desiredStatus,
                duration_seconds: Math.round(
                  Math.max(
                    localMetadata.durationMs ?? 0,
                    cloud.duration_seconds * 1_000,
                  ) / 1_000,
                ),
              }).catch(() => undefined)
            }
          }
        } else {
        reportOpening(
          localMetadata?.transcriptCount
            ? '正在同步云端转录…'
            : '正在下载云端转录…',
          2,
        )
        const localRecords = localMetadata
          ? await canonicalizeLocalSession(
              repository,
              cloud.id,
              cloud.target_language,
            )
          : { segments: [], translations: [] }
        assertLoadCurrent()
        // If we already have a usable local cache, paint it immediately so the
        // user can read while remaining cloud pages download.
        if (localCacheUsable && localMetadata && localRecords.segments.length > 0) {
          const localWinsEarly = localMetadata.updatedAt > cloudUpdatedAt
          const earlyTitle = localWinsEarly
            ? localMetadata.title || cloud.title
            : cloud.title
          const earlyDuration = Math.max(
            cloud.duration_seconds,
            (localMetadata.durationMs ?? 0) / 1_000,
          )
          currentSessionRef.current = cloud.id
          currentAudioMimeTypeRef.current = localMetadata.audioMimeType || 'audio/webm'
          currentLocationRef.current = 'cloud'
          cloudSessionVerifiedRef.current = true
          cloudSessionRef.current = null
          cloudQueue.setSession(null)
          setSessionId(cloud.id)
          setSessionSourceLanguage(
            localMetadata.sourceLanguage ?? cloud.source_language,
          )
          setTitle(earlyTitle)
          applyLoadedRecords(localRecords, earlyDuration, cloud.id)
          setRecorderStatus('idle')
          reportOpening('已打开缓存，正在同步云端…', 5)
        }
        const segments: TranscriptSegment[] = []
        const translations: TranslationSegment[] = []
        const cloudByClientId = new Map<string, Pick<
          CloudTranscript,
          'text' | 'translation' | 'translation_group_id'
        > & { segment: TranscriptSegment }>()
        // Every covered atom carries the group id, while only the anchor
        // stores the paragraph text. Keep the group accumulator across page
        // boundaries so a long paragraph is reconstructed exactly once.
        const translationGroups = new Map<string, {
          anchorSegment?: TranscriptSegment
          firstIndex: number
          firstSegment: TranscriptSegment
          lastEndTime: number
          receivedAt: number
          speaker: string
          text?: string
        }>()
        let transcriptIndex = 0
        let cursor: { start_time: number; id: string } | null = null
        let pagesLoaded = 0
        // Progress without a total: asymptotic curve that approaches ~90% while
        // pages keep arriving, then jumps to 100% after merge/write.
        const pageProgress = (page: number) => Math.min(88, 8 + page * 12)

        for (;;) {
          let page: Awaited<ReturnType<typeof getSessionTranscriptsPage>> | null = null
          for (let attempt = 0; attempt < 3; attempt += 1) {
            try {
              page = await getSessionTranscriptsPage(cloud.id, {
                limit: 500,
                after: cursor,
              })
              break
            } catch (reason) {
              const retryable = !(reason instanceof ApiRequestError)
                || reason.status === 408
                || reason.status === 425
                || reason.status === 429
                || reason.status >= 500
              if (!retryable || attempt === 2) throw reason
              await new Promise<void>((resolve) => {
                window.setTimeout(resolve, 400 * (2 ** attempt))
              })
              assertLoadCurrent()
            }
          }
          if (!page) throw new Error('云端转录分页读取失败')
          assertLoadCurrent()
          pagesLoaded += 1
          reportOpening(
            `正在下载云端转录… 第 ${pagesLoaded} 页`,
            pageProgress(pagesLoaded),
          )

          const pageTranscripts = Array.isArray(page.transcripts)
            ? page.transcripts
            : []
          for (const transcript of pageTranscripts) {
            const index = transcriptIndex
            transcriptIndex += 1
            const transcriptText = transcript.text.trim()
            if (transcript.is_partial || !transcriptText) continue

            const clientSegmentId = transcript.client_segment_id || transcript.id
            let segment = cloudByClientId.get(clientSegmentId)?.segment
            if (!segment) {
              const startTime = transcript.start_time
              segment = Object.freeze({
                id: clientSegmentId,
                sequence: segments.length,
                speaker: transcript.speaker.trim() || 'Speaker',
                text: transcriptText,
                status: 'final',
                startTime,
                endTime: Math.max(startTime, transcript.end_time ?? startTime),
                receivedAt: Date.parse(transcript.created_at) || Date.now(),
                source: 'cloud',
              })
              segments.push(segment)
            }
            cloudByClientId.set(clientSegmentId, {
              segment,
              text: transcript.text,
              ...(transcript.translation
                ? { translation: transcript.translation }
                : {}),
              ...(transcript.translation_group_id
                ? { translation_group_id: transcript.translation_group_id }
                : {}),
            })

            const text = transcript.translation?.trim()
            const persistedGroupId = transcript.translation_group_id?.trim()
            if (!persistedGroupId && !text) continue
            const groupId = persistedGroupId || `single:${clientSegmentId}`
            const receivedAt = Date.parse(transcript.updated_at) || Date.now()
            const existing = translationGroups.get(groupId)
            if (!existing) {
              translationGroups.set(groupId, {
                ...(text ? { anchorSegment: segment, text } : {}),
                firstIndex: index,
                firstSegment: segment,
                lastEndTime: segment.endTime,
                receivedAt,
                speaker: transcript.speaker,
              })
              continue
            }
            existing.lastEndTime = Math.max(existing.lastEndTime, segment.endTime)
            if (text && (!existing.text || receivedAt >= existing.receivedAt)) {
              existing.anchorSegment = segment
              existing.receivedAt = receivedAt
              existing.text = text
              existing.speaker = transcript.speaker
            }
          }

          if (!page.has_more) break
          const nextCursor = page.next_cursor
          const lastTranscript = pageTranscripts.at(-1)
          const advances = Boolean(
            nextCursor
            && (
              cursor === null
              || nextCursor.start_time > cursor.start_time
              || (
                nextCursor.start_time === cursor.start_time
                && nextCursor.id > cursor.id
              )
            ),
          )
          if (
            !nextCursor
            || !lastTranscript
            || !Number.isFinite(nextCursor.start_time)
            || nextCursor.start_time < 0
            || nextCursor.start_time !== lastTranscript.start_time
            || nextCursor.id !== lastTranscript.id
            || !advances
          ) {
            throw new Error('云端转录分页游标无效')
          }
          cursor = nextCursor
          // Let input, paint and cancellation handlers run between pages.
          await new Promise<void>((resolve) => window.setTimeout(resolve, 0))
          assertLoadCurrent()
        }

        for (const [groupId, group] of [...translationGroups.entries()]
          .sort((left, right) => left[1].firstIndex - right[1].firstIndex)) {
          if (!group.text) continue
          const anchor = group.anchorSegment ?? group.firstSegment
          const translationInput = {
            segmentId: anchor.id,
            speaker: group.speaker.trim() || 'Speaker',
            language: cloud.target_language.trim().toLowerCase(),
            text: group.text,
            startTime: group.firstSegment.startTime,
            endTime: Math.max(
              group.firstSegment.startTime,
              group.lastEndTime,
            ),
          }
          const translation: TranslationSegment = Object.freeze({
            ...translationInput,
            id: groupId.startsWith('single:')
              ? createStableTranslationId(translationInput)
              : groupId,
            sequence: translations.length,
            status: 'final',
            receivedAt: group.receivedAt,
            source: 'cloud',
          })
          translations.push(translation)
        }
        const mergedRecords = mergeSessionRecords(
          localRecords,
          { segments, translations },
        )
        records = mergedRecords
        const cloudDurationMs = cloud.duration_seconds * 1_000
        const localWins = Boolean(
          localMetadata && localMetadata.updatedAt > cloudUpdatedAt,
        )
        loadedTitle = localWins
          ? localMetadata?.title || cloud.title
          : cloud.title
        loadedDuration = Math.max(
          cloud.duration_seconds,
          (localMetadata?.durationMs ?? 0) / 1_000,
        )
        const mergedStatus = localWins
          ? localMetadata?.status ?? 'active'
          : cloud.status === 'active' || cloud.status === 'paused'
            ? 'active'
            : 'completed'
        await repository.ensureSession(cloud.id, {
          createdAt: Date.parse(cloud.created_at) || Date.now(),
          origin: 'cloud',
          sourceLanguage: cloud.source_language,
          targetLanguage: cloud.target_language,
          title: loadedTitle,
          status: mergedStatus,
          durationMs: Math.max(localMetadata?.durationMs ?? 0, cloudDurationMs),
        })
        assertLoadCurrent()
        if (!localMetadata || mergedRecords.addedSegments > 0) {
          reportOpening('正在写入本机转录…', 90)
          for (
            let offset = localRecords.segments.length;
            offset < mergedRecords.segments.length;
            offset += 500
          ) {
            await repository.writeTranscriptRecords(
              cloud.id,
              mergedRecords.segments
                .slice(offset, offset + 500)
                .map((segment) => ({
                  sequence: segment.sequence,
                  recordId: segment.id,
                  data: segment,
                })),
            )
            assertLoadCurrent()
            await new Promise<void>((resolve) => window.setTimeout(resolve, 0))
          }
        }
        if (!localMetadata || mergedRecords.addedTranslations > 0) {
          for (
            let offset = localRecords.translations.length;
            offset < mergedRecords.translations.length;
            offset += 500
          ) {
            await repository.writeTranslationRecords(
              cloud.id,
              mergedRecords.translations
                .slice(offset, offset + 500)
                .map((translation) => ({
                  sequence: translation.sequence,
                  recordId: translation.id,
                  data: translation,
                })),
            )
            assertLoadCurrent()
            await new Promise<void>((resolve) => window.setTimeout(resolve, 0))
          }
        }
        reportOpening('正在写入本机缓存…', 92)
        await repository.updateSessionMetadata(cloud.id, {
          cloudSessionPending: false,
          cloudContentUpdatedAt: cloudUpdatedAt,
          sourceLanguage: cloud.source_language,
          targetLanguage: cloud.target_language,
          title: loadedTitle,
          durationMs: Math.max(localMetadata?.durationMs ?? 0, cloudDurationMs),
          status: mergedStatus,
        }, { touch: false })
        assertLoadCurrent()
        if (localWins && localMetadata) {
          const desiredStatus = localMetadata.status === 'active'
            ? 'active'
            : 'completed'
          if (
            (localMetadata.title && localMetadata.title !== cloud.title)
            || desiredStatus !== cloud.status
            || (localMetadata.durationMs ?? 0) > cloudDurationMs
          ) {
            void syncCloudMetadata(cloud.id, {
              ...(localMetadata.title ? { title: localMetadata.title } : {}),
              status: desiredStatus,
              duration_seconds: Math.round(
                Math.max(localMetadata.durationMs ?? 0, cloudDurationMs) / 1_000,
              ),
            }).catch(() => undefined)
          }
        }

        // A page reload can lose the in-memory write-behind queue, but never
        // the local records. Requeue only local records missing or stale in the
        // cloud snapshot; the server upserts by client_segment_id.
        if (localRecords.segments.length > 0) {
          reportOpening('正在核对未同步片段…', 96)
          const localTranslationBySegment = new Map<string, {
            groupId: string
            isAnchor: boolean
            translation: TranslationSegment
          }>()
          const localSegmentIndex = new Map(
            localRecords.segments.map((segment, index) => [segment.id, index]),
          )
          for (const translation of localRecords.translations) {
            if (!translation.segmentId) continue
            const anchorIndex = localSegmentIndex.get(translation.segmentId)
            if (anchorIndex === undefined) continue
            for (
              let index = anchorIndex;
              index < localRecords.segments.length;
              index += 1
            ) {
              const segment = localRecords.segments[index]
              if (!segment || segment.startTime > translation.endTime + 0.3) break
              if (
                segment.speaker === translation.speaker
                && segment.startTime >= translation.startTime - 0.3
                && segment.endTime <= translation.endTime + 0.3
              ) {
                localTranslationBySegment.set(segment.id, {
                  groupId: translation.id,
                  isAnchor: segment.id === translation.segmentId,
                  translation,
                })
              }
            }
          }
          const reconciliation: TranscriptInput[] = []
          for (const segment of localRecords.segments) {
            const remote = cloudByClientId.get(segment.id)
            const translationMatch = localTranslationBySegment.get(segment.id)
            const translation = translationMatch?.translation
            if (
              remote
              && remote.text === segment.text
              && (
                !translation
                || (
                  remote.translation_group_id === translationMatch.groupId
                  && (
                    !translationMatch.isAnchor
                    || remote.translation === translation.text
                  )
                )
              )
            ) {
              continue
            }
            reconciliation.push({
              client_segment_id: segment.id,
              ...(translationMatch
                ? { translation_group_id: translationMatch.groupId }
                : {}),
              speaker: segment.speaker,
              text: segment.text,
              ...(translation && translationMatch?.isAnchor
                ? { translation: translation.text }
                : {}),
              start_time: segment.startTime,
              end_time: segment.endTime,
              status: translation ? 'translated' : 'confirmed',
              is_partial: false,
            })
          }
          if (reconciliation.length > 0) {
            const inputById = new Map(
              reconciliation.map((input) => [input.client_segment_id, input]),
            )
            for (let offset = 0; offset < reconciliation.length; offset += 500) {
              const durableRecords = await repository.upsertCloudTranscriptOutboxBatch(
                cloud.id,
                reconciliation
                  .slice(offset, offset + 500)
                  .map((input) => ({
                    clientSegmentId: input.client_segment_id,
                    payload: input,
                  })),
              )
              assertLoadCurrent()
              cloudQueue.restore(durableRecords.flatMap((record) => {
                const input = inputById.get(record.clientSegmentId)
                return input
                  ? [{
                      ownerId: record.ownerId,
                      sessionId: record.sessionId,
                      input,
                      durableVersion: record.updatedAt,
                    }]
                  : []
              }))
            }
          }
        }
        reportOpening('正在整理会话…', 99)
        } // end full cloud sync (non-fast-path)
      } else {
        reportOpening('正在读取本机会话…', null)
        const metadata = await repository.getSessionMetadata(session.id)
        assertLoadCurrent()
        loadedSourceLanguage =
          metadata?.sourceLanguage ?? settingsRef.current.sourceLanguage
        records = await canonicalizeLocalSession(
          repository,
          session.id,
          metadata?.targetLanguage ?? settingsRef.current.targetLanguage,
        )
        assertLoadCurrent()
        loadedTitle = metadata?.title || loadedTitle
        loadedDuration = (metadata?.durationMs ?? loadedDuration * 1_000) / 1_000
      }

      reportOpening('正在渲染会话…', 100)
      const loadedMetadata = await repository.getSessionMetadata(session.id)
      assertLoadCurrent()
      currentSessionRef.current = session.id
      currentAudioMimeTypeRef.current = loadedMetadata?.audioMimeType || 'audio/webm'
      currentLocationRef.current = session.location
      cloudSessionVerifiedRef.current = session.location === 'cloud'
      cloudSessionRef.current = null
      cloudQueue.setSession(null)
      setSessionId(session.id)
      setSessionSourceLanguage(
        loadedMetadata?.sourceLanguage ?? loadedSourceLanguage,
      )
      setTitle(loadedTitle)
      applyLoadedRecords(records, loadedDuration, session.id)
      setRecorderStatus('idle')
    } catch (reason) {
      if (!loadIsCurrent()) return
      if (session.location === 'cloud') {
        try {
          const metadata = await repository.getSessionMetadata(session.id)
          assertLoadCurrent()
          if (metadata) {
            reportOpening('云端不可用，打开本机缓存…', null)
            const cachedRecords = await canonicalizeLocalSession(
              repository,
              session.id,
              metadata.targetLanguage ?? settingsRef.current.targetLanguage,
            )
            assertLoadCurrent()
            const cachedDuration = (metadata.durationMs ?? 0) / 1_000
            currentSessionRef.current = session.id
            currentAudioMimeTypeRef.current = metadata.audioMimeType || 'audio/webm'
            currentLocationRef.current = 'cloud'
            cloudSessionVerifiedRef.current = false
            cloudSessionRef.current = null
            cloudQueue.setSession(null)
            setSessionId(session.id)
            setSessionSourceLanguage(
              metadata.sourceLanguage ?? settingsRef.current.sourceLanguage,
            )
            setTitle(metadata.title || session.title)
            applyLoadedRecords(cachedRecords, cachedDuration, session.id)
            setRecorderStatus('idle')
            setError(
              `云端暂时不可用，已打开本机缓存：${
                reason instanceof Error ? reason.message : String(reason)
              }`,
            )
            return
          }
        } catch {
          // Report the original cloud failure below when no usable cache exists.
        }
      }
      setError(`会话加载失败：${reason instanceof Error ? reason.message : String(reason)}`)
    } finally {
      if (loadIsCurrent()) setHistoryOpening(null)
    }
  }, [
    applyLoadedRecords,
    cloudQueue,
    ownerScopeIsCurrent,
    repository,
    setRecorderStatus,
    stop,
    syncCloudMetadata,
  ])

  const deleteHistory = useCallback(async (session: HistorySession) => {
    const ownerGeneration = ownerGenerationRef.current
    const ownerId = repositoryOwnerRef.current
    const deleteIsCurrent = () => (
      ownerScopeIsCurrent(ownerGeneration, ownerId)
    )
    if (!deleteIsCurrent()) return
    if (session.id === currentSessionRef.current && statusRef.current !== 'idle') {
      setError('正在录制的会话不能删除，请先停止录音。')
      return
    }
    try {
      const deletingUser = userRef.current
      if (
        session.location === 'cloud'
        && deletingUser
        && deletingUser.id === ownerId
      ) {
        await deleteCloudSession(session.id)
        cloudQueue.discardSession(deletingUser.id, session.id)
      }
      if (!deleteIsCurrent()) return
      await repository.deleteSession(session.id)
      if (!deleteIsCurrent()) return
      try {
        localStorage.removeItem(chatHistoryKey(ownerId, session.id))
        localStorage.removeItem(legacyChatHistoryKey(session.id))
      } catch {
        // The session itself is deleted even if browser storage is unavailable.
      }
      if (session.id === currentSessionRef.current) {
        lexReset(session.id)
        currentSessionRef.current = ''
        currentAudioMimeTypeRef.current = 'audio/webm'
        setSessionId('')
        setSessionSourceLanguage(settingsRef.current.sourceLanguage)
        setTitle(defaultSessionTitle())
        transcriptStore.reset()
        feedModel.reset()
        applyLoadedRecords({ segments: [], translations: [] }, 0)
      }
      await refreshHistory()
    } catch (reason) {
      if (deleteIsCurrent()) {
        setError(`删除失败：${reason instanceof Error ? reason.message : String(reason)}`)
      }
    }
  }, [
    applyLoadedRecords,
    cloudQueue,
    feedModel,
    ownerScopeIsCurrent,
    refreshHistory,
    repository,
    transcriptStore,
  ])

  const updateTitle = useCallback(async (nextTitle: string) => {
    const id = currentSessionRef.current
    if (!id || !nextTitle.trim()) return
    const normalized = nextTitle.trim()
    setTitle(normalized)
    try {
      await repository.updateSessionMetadata(id, { title: normalized })
      if (currentLocationRef.current === 'cloud' && userRef.current) {
        await syncCloudMetadata(id, { title: normalized })
      }
      void refreshHistory()
    } catch (reason) {
      setError(`标题保存失败：${reason instanceof Error ? reason.message : String(reason)}`)
    }
  }, [refreshHistory, repository, syncCloudMetadata])

  const downloadAudio = useCallback(async () => {
    const id = currentSessionRef.current
    if (!id) {
      setError('当前没有可下载的会话。')
      return
    }
    try {
      // This must happen before the first await in the click callback.
      // Chromium otherwise drops transient user activation and rejects it.
      const saveRequest = requestCompleteAudioSave(
        title,
        captureRef.current?.mimeType || currentAudioMimeTypeRef.current,
      )
      await captureRef.current?.flushCompressedChunk()
      await localWriteChainRef.current
      const result = await downloadCompleteAudio(repository, id, title, saveRequest)
      if (result === 'empty') setError('当前会话没有保存本地音频。')
    } catch (reason) {
      setError(`音频下载失败：${reason instanceof Error ? reason.message : String(reason)}`)
    }
  }, [repository, title])

  const downloadText = useCallback(async (
    mode: 'original' | 'translation' | 'bilingual',
  ) => {
    const id = currentSessionRef.current
    if (!id) {
      setError('当前没有可下载的会话。')
      return
    }
    try {
      await localWriteChainRef.current
      await canonicalizeLocalSession(repository, id, settingsRef.current.targetLanguage)
      const downloaded = await downloadSessionText(repository, id, title, mode)
      if (!downloaded) setError('当前会话没有对应的文本内容。')
    } catch (reason) {
      setError(`文本下载失败：${reason instanceof Error ? reason.message : String(reason)}`)
    }
  }, [repository, title])

  useEffect(() => {
    feedModel.configure({
      sourceLanguage: settings.sourceLanguage,
      targetLanguage: settings.targetLanguage,
      translationEnabled: settings.translationEnabled,
    })
  }, [
    feedModel,
    settings.sourceLanguage,
    settings.targetLanguage,
    settings.translationEnabled,
  ])

  // Learning mode can be toggled mid-session. Start/stop the AI translator to
  // match the live assist mode so switching back to 同传 actually resumes
  // context-aware translation (and backfills untranslated finals).
  useEffect(() => {
    const live = recorderStatus === 'recording'
      || recorderStatus === 'reconnecting'
      || recorderStatus === 'paused'
      || recorderStatus === 'error'
    if (!live || !currentSessionRef.current) return

    const activeSettings = settingsRef.current
    const wantAi = activeSettings.assistMode !== 'learn'
      && activeSettings.translationEnabled
      && activeSettings.translationEngine === 'ai'

    if (!wantAi) {
      // Keep the session engine flag honest while learning so new segments are
      // not enqueued; leave any in-flight AI work alone (no abrupt cancel).
      if (sessionTranslationEngineRef.current === 'ai') {
        sessionTranslationEngineRef.current = ''
      }
      return
    }

    const alreadyLive = sessionTranslationEngineRef.current === 'ai'
      && aiTranslator.isSessionActive()
    if (!alreadyLive) {
      sessionTranslationEngineRef.current = 'ai'
      sessionTargetLanguageRef.current = activeSettings.targetLanguage
      aiTranslator.startSession({
        ...(cloudSessionRef.current
          ? { sessionId: cloudSessionRef.current }
          : {}),
        translatePrompt: activeSettings.translatePrompt.trim()
          || defaultTranslatePromptFor(
            activeSettings.sourceLanguage,
            activeSettings.targetLanguage,
          ),
      })
    } else {
      sessionTranslationEngineRef.current = 'ai'
    }

    // Backfill cards that never received a final translation while learning.
    const snapshot = feedModel.getSnapshot()
    for (const item of snapshot.items) {
      const original = item.original?.text?.trim()
      if (!original) continue
      const hasFinalTranslation = Boolean(item.translation?.text?.trim())
      if (hasFinalTranslation) continue
      const segmentId = item.segmentIds?.[0] ?? item.id
      aiTranslator.addSegment(
        {
          id: segmentId,
          speaker: item.speaker,
          text: original,
          startTime: item.startTime ?? 0,
          endTime: item.endTime ?? item.startTime ?? 0,
        },
        item.id,
      )
    }
  }, [
    aiTranslator,
    feedModel,
    recorderStatus,
    settings.assistMode,
    settings.translationEnabled,
    settings.translationEngine,
    settings.translatePrompt,
    settings.sourceLanguage,
    settings.targetLanguage,
  ])

  useEffect(() => {
    if (!currentSessionRef.current) {
      setSessionSourceLanguage(settings.sourceLanguage)
    }
  }, [settings.sourceLanguage])

  useEffect(() => {
    if (!ragEnabled || !settings.automaticAiIngest) ragQueue.clear()
  }, [ragEnabled, ragQueue, settings.automaticAiIngest])

  useEffect(() => {
    const handleOnline = () => {
      if (onlineRecoveryRef.current) return
      const operation = restoreCloudOutbox()
        .then(refreshHistory)
        .catch((reason: unknown) => {
          setError(
            `网络已恢复，但云端补同步失败：${
              reason instanceof Error ? reason.message : String(reason)
            }`,
          )
        })
        .finally(() => {
          if (onlineRecoveryRef.current === operation) {
            onlineRecoveryRef.current = null
          }
        })
      onlineRecoveryRef.current = operation
    }
    window.addEventListener('online', handleOnline)
    return () => window.removeEventListener('online', handleOnline)
  }, [refreshHistory, restoreCloudOutbox])

  useEffect(() => {
    const handlePageHide = () => {
      if (statusRef.current !== 'idle' && statusRef.current !== 'stopping') {
        // Start local finalization while the page is still alive. Browsers do
        // not guarantee enough time for cloud I/O here, so durable IndexedDB
        // writes remain the recovery boundary and sync resumes next launch.
        void stop()
      }
    }
    window.addEventListener('pagehide', handlePageHide)
    return () => window.removeEventListener('pagehide', handlePageHide)
  }, [stop])

  useEffect(() => {
    let cancelled = false
    const transitionOwner = async () => {
      const nextOwnerId = user?.id ?? null
      const ownerChanged = repositoryOwnerRef.current !== nextOwnerId
      if (
        ownerChanged
        && statusRef.current !== 'idle'
        && statusRef.current !== 'stopping'
      ) {
        setError('登录状态已变化，正在安全结束当前会话。')
        await stop()
      } else if (ownerChanged && statusRef.current === 'stopping') {
        await stopPromiseRef.current
      }
      if (cancelled) return

      if (ownerChanged) {
        historyRequestRef.current += 1
        historyLoadRequestRef.current += 1
        if (currentSessionRef.current) lexReset(currentSessionRef.current)
        repositoryOwnerRef.current = nextOwnerId
        currentSessionRef.current = ''
        currentLocationRef.current = 'local'
        cloudSessionVerifiedRef.current = false
        currentAudioMimeTypeRef.current = 'audio/webm'
        cloudSessionRef.current = null
        cloudQueue.setSession(null)
        setSessionId('')
        setSessionSourceLanguage(settingsRef.current.sourceLanguage)
        setTitle(defaultSessionTitle())
        applyLoadedRecords({ segments: [], translations: [] }, 0)
        setHistorySessions([])
        setHistoryLoading(false)
        setHistoryOpening(null)
        setLegacyHistoryCount(0)
        ragQueue.clear()
        setError(null)
      }

      cloudQueue.setOwner(nextOwnerId)
      // History list should not wait on outbox recovery (can walk every cloud
      // session). Restore outbox in parallel so the sidebar appears quickly.
      const restorePromise = restoreCloudOutbox()
      if (!cancelled) await refreshHistory()
      await restorePromise.catch((reason: unknown) => {
        if (!cancelled) {
          setError(
            `云端补同步失败：${
              reason instanceof Error ? reason.message : String(reason)
            }`,
          )
        }
      })
    }
    void transitionOwner().catch((reason: unknown) => {
      if (!cancelled) {
        setError(
          `账户数据切换失败：${reason instanceof Error ? reason.message : String(reason)}`,
        )
      }
    })
    return () => {
      cancelled = true
    }
  }, [
    applyLoadedRecords,
    cloudQueue,
    ragQueue,
    refreshHistory,
    restoreCloudOutbox,
    stop,
    user?.id,
  ])

  useEffect(() => {
    if (
      recorderStatus !== 'recording'
      && recorderStatus !== 'reconnecting'
      && recorderStatus !== 'error'
    ) {
      return
    }
    updateElapsed()
    const timer = window.setInterval(updateElapsed, 1_000)
    return () => window.clearInterval(timer)
  }, [recorderStatus, updateElapsed])

  useEffect(() => {
    const unsubscribers = [
      client.on('transcript', (segment) => {
        const activeSessionId = currentSessionRef.current
        if (!activeSessionId) return
        feedModel.appendSegment(segment)
        const liveSettings = settingsRef.current
        const wantAiNow = liveSettings.assistMode !== 'learn'
          && liveSettings.translationEnabled
          && liveSettings.translationEngine === 'ai'
        // Prefer live settings over the session snapshot so mid-session
        // assistMode toggles take effect without restarting the recorder.
        if (wantAiNow) {
          if (
            sessionTranslationEngineRef.current !== 'ai'
            || !aiTranslator.isSessionActive()
          ) {
            sessionTranslationEngineRef.current = 'ai'
            aiTranslator.startSession({
              ...(cloudSessionRef.current
                ? { sessionId: cloudSessionRef.current }
                : {}),
              translatePrompt: liveSettings.translatePrompt.trim()
                || defaultTranslatePromptFor(
                  liveSettings.sourceLanguage,
                  liveSettings.targetLanguage,
                ),
            })
          }
          aiTranslator.addSegment(
            {
              id: segment.id,
              speaker: segment.speaker,
              text: segment.text,
              startTime: segment.startTime,
              endTime: segment.endTime,
            },
            feedModel.cardIdOf(segment.id) ?? segment.id,
          )
        }
        void enqueueLocal((scopedRepository) => scopedRepository.appendTranscript(
          activeSessionId,
          segment,
          { sequence: segment.sequence, recordId: segment.id },
        ))
        if (cloudSessionRef.current === activeSessionId) {
          const input: TranscriptInput = {
            client_segment_id: segment.id,
            speaker: segment.speaker,
            text: segment.text,
            start_time: segment.startTime,
            end_time: segment.endTime,
            status: 'confirmed',
            is_partial: false,
          }
          queueCloudInput(activeSessionId, input)
        }
        if (ragEnabledRef.current && settingsRef.current.automaticAiIngest) {
          const cardId = feedModel.cardIdOf(segment.id) ?? segment.id
          const card = feedModel.getSnapshot().items.find((item) => item.id === cardId)
          ragQueue.queue({
            id: cardId,
            sessionId: activeSessionId,
            speaker: card?.speaker ?? segment.speaker,
            text: card?.original?.text ?? segment.text,
            startTime: card?.startTime ?? segment.startTime,
            endTime: card?.endTime ?? segment.endTime,
          })
        }
        wordCounterRef.current.add(segment.text)
        setTopWords(wordCounterRef.current.getTop())
        lexIngest(activeSessionId, segment.text)

        // Final translations can arrive just before their matching final
        // transcript. Relink only the small bounded orphan set and update the
        // existing persisted record in place.
        for (const [translationId, orphan] of orphanTranslationsRef.current) {
          const matchingSegmentId = transcriptStore.findSegmentId(
            orphan.speaker,
            orphan.startTime,
            orphan.endTime,
          )
          if (matchingSegmentId !== segment.id) continue
          orphanTranslationsRef.current.delete(translationId)
          const linked = transcriptStore.relinkTranslation(translationId, segment.id)
          if (!linked) continue
          feedModel.appendTranslation(linked)
          void enqueueLocal((scopedRepository) => scopedRepository.upsertTranslation(
            activeSessionId,
            linked.id,
            linked,
          ))
          if (cloudSessionRef.current === activeSessionId) {
            queueCloudInput(activeSessionId, {
              client_segment_id: segment.id,
              speaker: segment.speaker,
              text: segment.text,
              translation: linked.text,
              start_time: segment.startTime,
              end_time: segment.endTime,
              status: 'translated',
              is_partial: false,
            })
          }
        }
      }),
      client.on('partial', (partial) => {
        if (partial) feedModel.setPartial(partial)
        else feedModel.clearPartial()
      }),
      client.on('translation', (translation) => {
        const activeSessionId = currentSessionRef.current
        if (!activeSessionId) return
        feedModel.appendTranslation(translation)
        if (!translation.segmentId) {
          if (orphanTranslationsRef.current.size >= 256) {
            const oldestId = orphanTranslationsRef.current.keys().next().value
            if (typeof oldestId === 'string') {
              orphanTranslationsRef.current.delete(oldestId)
            }
          }
          orphanTranslationsRef.current.set(translation.id, translation)
        }
        void enqueueLocal((scopedRepository) => scopedRepository.appendTranslation(
          activeSessionId,
          translation,
          { sequence: translation.sequence, recordId: translation.id },
        ))
        if (
          cloudSessionRef.current === activeSessionId
          && translation.segmentId
        ) {
          const segment = transcriptStore.getSegment(translation.segmentId)
          if (segment) {
            queueCloudInput(activeSessionId, {
              client_segment_id: segment.id,
              speaker: segment.speaker,
              text: segment.text,
              translation: translation.text,
              start_time: segment.startTime,
              end_time: segment.endTime,
              status: 'translated',
              is_partial: false,
            })
          }
        }
      }),
      client.on('translationPartial', (partial) => {
        if (partial) feedModel.setTranslationPartial(partial)
        else feedModel.clearTranslationPartial()
      }),
    ]

    // Context-aware AI paragraph translations reuse the same persistence and
    // sync path as provider translations; the record anchors on the chunk's
    // first segment and spans the chunk's full time range.
    aiTranslationHandlerRef.current = (chunk, result) => {
      const activeSessionId = currentSessionRef.current
      if (!activeSessionId || sessionTranslationEngineRef.current !== 'ai') return
      const firstSegmentId = chunk.segmentIds[0]
      if (!firstSegmentId) return
      let translation: TranslationSegment
      try {
        translation = transcriptStore.appendTranslation({
          segmentId: firstSegmentId,
          speaker: chunk.speaker,
          language: sessionTargetLanguageRef.current,
          text: result.text,
          startTime: chunk.startTime,
          endTime: chunk.endTime,
        }).record
      } catch {
        // The segment may have been cleared by a session switch mid-flight.
        return
      }
      feedModel.appendTranslation(translation)
      void enqueueLocal((scopedRepository) => scopedRepository.appendTranslation(
        activeSessionId,
        translation,
        { sequence: translation.sequence, recordId: translation.id },
      ))
      if (cloudSessionRef.current === activeSessionId) {
        for (const segmentId of chunk.segmentIds) {
          const segment = transcriptStore.getSegment(segmentId)
          if (!segment) continue
          const isAnchor = segment.id === firstSegmentId
          queueCloudInput(activeSessionId, {
            client_segment_id: segment.id,
            translation_group_id: translation.id,
            speaker: segment.speaker,
            text: segment.text,
            // The group ID marks every covered atom, but the paragraph text is
            // stored once on its anchor. Repeating it on every provider
            // fragment multiplies storage/quota/export payloads and causes old
            // clients to render the same translation many times.
            ...(isAnchor ? { translation: translation.text } : {}),
            start_time: segment.startTime,
            end_time: segment.endTime,
            status: 'translated',
            is_partial: false,
          })
        }
      }
    }

    const remaining = [
      client.on('state', (snapshot) => {
        if (snapshot.status === 'reconnecting' && (
          statusRef.current === 'recording'
          || statusRef.current === 'reconnecting'
          || statusRef.current === 'error'
        )) {
          setRecorderStatus('reconnecting')
        } else if (
          snapshot.status === 'error'
          && (
            statusRef.current === 'recording'
            || statusRef.current === 'reconnecting'
            || statusRef.current === 'error'
          )
        ) {
          setRecorderStatus('error')
        } else if (
          snapshot.status === 'running'
          && (
            statusRef.current === 'reconnecting'
            || statusRef.current === 'error'
          )
        ) {
          setRecorderStatus('recording')
        }
      }),
      client.on('error', (event) => {
        if (statusRef.current !== 'idle' && statusRef.current !== 'stopping') {
          setError(event.message)
        }
      }),
      client.on('balance', () => {
        void balanceCallbackRef.current?.()
      }),
      client.on('audioDropped', (event) => {
        const kilobytes = Math.ceil(event.bytes / 1_024)
        setError(`网络拥塞，已丢弃约 ${kilobytes} KB 尚未发送的音频。`)
      }),
    ]
    return () => {
      aiTranslationHandlerRef.current = null
      for (const unsubscribe of [...unsubscribers, ...remaining]) unsubscribe()
    }
  }, [
    aiTranslator,
    client,
    cloudQueue,
    enqueueLocal,
    feedModel,
    ragQueue,
    repository,
    queueCloudInput,
    setRecorderStatus,
    transcriptStore,
  ])

  useEffect(() => {
    if (destroyTimerRef.current !== null) {
      window.clearTimeout(destroyTimerRef.current)
      destroyTimerRef.current = null
    }
    return () => {
      destroyTimerRef.current = window.setTimeout(() => {
        destroyTimerRef.current = null
        void captureRef.current?.stop()
        captureRef.current = null
        releaseSessionLock()
        client.destroy()
        aiTranslator.destroy()
        cloudQueue.destroy()
        ragQueue.destroy()
      }, 0)
    }
  }, [aiTranslator, client, cloudQueue, ragQueue, releaseSessionLock])

  useEffect(() => {
    const live = recorderStatus === 'recording'
      || recorderStatus === 'paused'
      || recorderStatus === 'reconnecting'
      || recorderStatus === 'error'
    if (!live || !settings.debugTransport) {
      setTransportDiagnostics(null)
      return
    }
    const tick = () => {
      try {
        setTransportDiagnostics(buildTransportDiagnostics(
          client.getDiagnostics(),
          aiTranslator.getDiagnostics(),
        ))
      } catch {
        // Diagnostics must never interfere with capture.
      }
    }
    tick()
    const timer = window.setInterval(tick, 500)
    return () => window.clearInterval(timer)
  }, [aiTranslator, client, recorderStatus, settings.debugTransport])

  const connectionLabel = recorderStatus === 'error'
    ? localAudioHealthyRef.current
      ? '转录断线 · 本地录音中'
      : '转录连接失败'
    : clientSnapshot.status === 'reconnecting'
    ? `重连 ${clientSnapshot.reconnectAttempt}/${clientSnapshot.maxReconnectAttempts}`
    : clientSnapshot.connected
      ? '已连接'
      : user
        ? '云端就绪'
        : '本地就绪'
  const ownerTransitioning = repositoryOwnerRef.current !== (user?.id ?? null)

  return {
    connectionLabel,
    durationLabel: formatDuration(ownerTransitioning ? 0 : elapsedSeconds),
    error,
    feedGeneration: feedSnapshot.generation,
    feedItems: ownerTransitioning ? [] : feedSnapshot.items,
    historyLoading: ownerTransitioning ? true : historyLoading,
    historyOpening: ownerTransitioning ? null : historyOpening,
    historySessions: ownerTransitioning ? [] : historySessions,
    legacyHistoryCount: ownerTransitioning ? 0 : legacyHistoryCount,
    pendingWrites: localPending + cloudPending,
    recorderStatus,
    sessionId: ownerTransitioning ? '' : sessionId,
    sessionSourceLanguage: ownerTransitioning
      ? settings.sourceLanguage
      : sessionSourceLanguage,
    stats: {
      finalSegments: ownerTransitioning ? 0 : transcriptSnapshot.stats.segmentCount,
      // Chunked AI translations cover several segments per record; the feed
      // model counts covered segments so the progress ratio stays truthful.
      translatedSegments: ownerTransitioning ? 0 : feedSnapshot.translatedSegmentCount,
      speakers: ownerTransitioning ? 0 : transcriptSnapshot.stats.speakerCount,
      topWords: ownerTransitioning ? [] : topWords,
    },
    title: ownerTransitioning ? defaultSessionTitle() : title,
    transportDiagnostics: ownerTransitioning ? null : transportDiagnostics,
    transcriptContext: ownerTransitioning ? '' : transcriptContext,
    clearError: () => setError(null),
    deleteHistory,
    downloadAudio,
    downloadText,
    loadHistory,
    migrateLegacyHistory,
    pauseToggle,
    refreshHistory,
    continueSession,
    start,
    stop,
    updateTitle,
  }
}
