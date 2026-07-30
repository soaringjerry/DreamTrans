import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from 'react'
import { getAccessToken, ensureValidAccessToken } from '../../pro/api/auth'
import {
  createSession as createCloudSession,
  deleteSession as deleteCloudSession,
  getSession as getCloudSession,
  listSessions as listCloudSessions,
  updateSession as updateCloudSession,
  type TranscriptInput,
  type User,
} from '../../pro/api/auth'
import { migrateLegacySessionStorage } from '../../db'
import { BrowserAudioCapture } from '../../core/audio/BrowserAudioCapture'
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
import type { HistorySession } from '../components/HistoryPanel'
import type { WorkspaceStats } from '../WorkspaceShell'
import type { UnifiedSettings } from './useUnifiedSettings'
import { lexIngest, lexReplace, lexReset } from '../../utils/lexicon'
import { websocketAuthProtocols } from '../../utils/websocketAuth'
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

const commonWords = new Set([
  'a', 'an', 'and', 'are', 'as', 'at', 'be', 'but', 'by', 'for', 'from',
  'had', 'has', 'have', 'he', 'her', 'his', 'i', 'if', 'in', 'is', 'it',
  'its', 'me', 'my', 'not', 'of', 'on', 'or', 'our', 'she', 'so', 'that',
  'the', 'their', 'them', 'there', 'they', 'this', 'to', 'was', 'we', 'were',
  'will', 'with', 'you', 'your',
  '一个', '一些', '这个', '那个', '然后', '就是', '可以', '我们', '你们',
  '他们', '因为', '所以', '但是', '还是', '没有', '已经', '现在', '什么',
])

interface UnifiedWorkspaceOptions {
  ragEnabled: boolean
  settings: UnifiedSettings
  user: User | null
  onBalanceUpdated?: () => void
}

export interface UnifiedWorkspaceState {
  connectionLabel: string
  durationLabel: string
  error: string | null
  feedGeneration: number
  feedItems: ReturnType<TranscriptFeedModel['getSnapshot']>['items']
  historyLoading: boolean
  historySessions: HistorySession[]
  legacyHistoryCount: number
  pendingWrites: number
  recorderStatus: RecorderStatus
  sessionId: string
  stats: WorkspaceStats
  title: string
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
    reconnect: { maxAttempts: 5 },
    audio: {
      sampleRate: 48_000,
      frameDurationMs: 40,
      maxQueuedAudioSeconds: 5,
    },
  }))
  const [localPending, setLocalPending] = useState(0)
  const [cloudPending, setCloudPending] = useState(0)
  const [error, setError] = useState<string | null>(null)
  const [recorderStatus, setRecorderStatusState] = useState<RecorderStatus>('idle')
  const [sessionId, setSessionId] = useState('')
  const [title, setTitle] = useState(() => defaultSessionTitle())
  const [elapsedSeconds, setElapsedSeconds] = useState(0)
  const [historySessions, setHistorySessions] = useState<HistorySession[]>([])
  const [historyLoading, setHistoryLoading] = useState(false)
  const [legacyHistoryCount, setLegacyHistoryCount] = useState(0)
  const [topWords, setTopWords] = useState<Array<{ word: string; count: number }>>([])
  const [transcriptContext, setTranscriptContext] = useState('')

  const feedSnapshot = useSyncExternalStore(feedModel.subscribe, feedModel.getSnapshot)
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
  const captureRef = useRef<BrowserAudioCapture | null>(null)
  const localWriteChainRef = useRef<Promise<void>>(Promise.resolve())
  const elapsedAccumulatedRef = useRef(0)
  const elapsedRunStartedRef = useRef<number | null>(null)
  const recentContextRef = useRef<TranscriptSegment[]>([])
  const orphanTranslationsRef = useRef(new Map<string, TranslationSegment>())
  const wordCounterRef = useRef(new WordCounter())
  const historyRequestRef = useRef(0)
  const historyLoadRequestRef = useRef(0)
  const destroyTimerRef = useRef<number | null>(null)
  const lifecycleEpochRef = useRef(0)
  const startPromiseRef = useRef<Promise<void> | null>(null)
  const stopPromiseRef = useRef<Promise<void> | null>(null)
  const ownerGenerationRef = useRef(0)
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

  const enqueueLocal = useCallback((operation: () => Promise<unknown>): Promise<void> => {
    setLocalPending((count) => count + 1)
    const next = localWriteChainRef.current.then(async () => {
      await operation()
    })
    localWriteChainRef.current = next
      .catch((reason: unknown) => {
        setError(`本地保存失败：${reason instanceof Error ? reason.message : String(reason)}`)
      })
      .finally(() => setLocalPending((count) => Math.max(0, count - 1)))
    return localWriteChainRef.current
  }, [])

  const queueCloudInput = useCallback((
    cloudSessionId: string,
    input: TranscriptInput,
  ) => {
    const ownerId = userRef.current?.id
    if (!ownerId) return
    void enqueueLocal(async () => {
      const outbox = await repository.upsertCloudTranscriptOutbox(
        cloudSessionId,
        input.client_segment_id,
        input,
      )
      cloudQueue.restore([{
        ownerId: outbox.ownerId,
        sessionId: outbox.sessionId,
        input,
        durableVersion: outbox.updatedAt,
      }])
    })
  }, [cloudQueue, enqueueLocal, repository])

  const restoreCloudOutbox = useCallback(async () => {
    const ownerId = repository.currentOwnerId()
    if (!ownerId) return
    for await (const metadata of repository.iterateSessions(100)) {
      if (metadata.origin !== 'cloud') continue
      let after:
        | { createdAt: number; clientSegmentId: string }
        | undefined
      do {
        const page = await repository.getCloudTranscriptOutboxPage<TranscriptInput>(
          metadata.id,
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
        if (!page.hasMore || !page.nextCursor) break
        after = page.nextCursor
      } while (after)
    }
  }, [cloudQueue, repository])

  const refreshHistory = useCallback(async () => {
    const ownerGeneration = ownerGenerationRef.current
    const ownerId = repositoryOwnerRef.current
    if (!ownerScopeIsCurrent(ownerGeneration, ownerId)) return
    const request = ++historyRequestRef.current
    setHistoryLoading(true)
    try {
      const [localPage, legacyCount] = await Promise.all([
        repository.listSessions({ limit: 60 }),
        repository.countLegacySessions(),
      ])
      if (!ownerScopeIsCurrent(ownerGeneration, ownerId)) return
      const merged = new Map<string, HistorySession>()
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

      if (userRef.current) {
        try {
          const cloud = await listCloudSessions(1, 60)
          if (!ownerScopeIsCurrent(ownerGeneration, ownerId)) return
          for (const session of cloud.sessions) {
            merged.set(session.id, {
              id: session.id,
              title: session.title || '未命名会话',
              createdAt: Date.parse(session.created_at) || Date.now(),
              durationSeconds: session.duration_seconds || 0,
              status: session.status,
              location: 'cloud',
            })
          }
        } catch (reason) {
          if (ownerScopeIsCurrent(ownerGeneration, ownerId)) {
            setError(`云端历史读取失败：${reason instanceof Error ? reason.message : String(reason)}`)
          }
        }
      }
      if (
        request === historyRequestRef.current
        && ownerScopeIsCurrent(ownerGeneration, ownerId)
      ) {
        setLegacyHistoryCount(legacyCount)
        setHistorySessions(
          [...merged.values()]
            .sort((left, right) => right.createdAt - left.createdAt)
            .slice(0, 60),
        )
      }
    } catch (reason) {
      if (
        request === historyRequestRef.current
        && ownerScopeIsCurrent(ownerGeneration, ownerId)
      ) {
        setError(`历史会话读取失败：${reason instanceof Error ? reason.message : String(reason)}`)
      }
    } finally {
      if (
        request === historyRequestRef.current
        && ownerScopeIsCurrent(ownerGeneration, ownerId)
      ) {
        setHistoryLoading(false)
      }
    }
  }, [ownerScopeIsCurrent, repository])

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
    recentContextRef.current = records.segments.slice(-30)
    orphanTranslationsRef.current.clear()
    setTranscriptContext(
      recentContextRef.current
        .map((segment) => `${segment.speaker}: ${segment.text}`)
        .join('\n'),
    )
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
      const rememberFailure = (reason: unknown) => {
        if (stopFailures.length === 0) {
          stopFailures.push(
            reason instanceof Error ? reason : new Error(String(reason)),
          )
        }
      }

      // Cancel both resources immediately. A start operation that is still
      // awaiting microphone permission will observe the epoch change and clean
      // up anything it creates after this point.
      const initialCapture = captureRef.current
      await Promise.all([
        initialCapture?.stop().catch(rememberFailure),
        client.stop().catch(rememberFailure),
      ])

      const capture = captureRef.current
      captureRef.current = null
      if (capture && capture !== initialCapture) {
        await capture.stop().catch(rememberFailure)
      }
      await client.stop().catch(rememberFailure)
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
            await updateCloudSessionWithTimeout(activeSessionId, {
              status: 'completed',
              duration_seconds: Math.round(durationMs / 1_000),
            })
          } catch (reason) {
            rememberFailure(reason)
          }
        }
      }

      cloudQueue.setSession(null)
      cloudSessionRef.current = null
      setRecorderStatus('idle')
      const stopFailure = stopFailures[0]
      if (stopFailure) {
        setError(`会话已在本地收尾，但有一步失败：${stopFailure.message}`)
      }
      void refreshHistory()
      void balanceCallbackRef.current?.()
    })()

    const tracked = operation
      .catch((reason: unknown) => {
        sessionAuthRequiredRef.current = false
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
    client,
    cloudQueue,
    refreshHistory,
    repository,
    setRecorderStatus,
    updateElapsed,
  ])

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
    let startingCapture: BrowserAudioCapture | null = null

    const operation = (async () => {
      try {
        await ensureSpeechmaticsPreflight()
        assertCurrent()
        if (startingUser) {
          try {
            const cloudSession = await createCloudSession({
              title: sessionTitle,
              source_language: activeSettings.sourceLanguage,
              target_language: activeSettings.targetLanguage,
            })
            nextSessionId = cloudSession.id
            cloudCreated = true
          } catch (reason) {
            assertCurrent()
            setError(
              `云端会话创建失败，已切换为本地保存：${
                reason instanceof Error ? reason.message : String(reason)
              }`,
            )
          }
        }
        assertCurrent()

        currentSessionRef.current = nextSessionId
        currentLocationRef.current = cloudCreated ? 'cloud' : 'local'
        cloudSessionRef.current = cloudCreated ? nextSessionId : null
        cloudQueue.setOwner(startingUser?.id ?? null)
        cloudQueue.setSession(cloudCreated ? nextSessionId : null)
        setSessionId(nextSessionId)
        setTitle(sessionTitle)
        elapsedAccumulatedRef.current = 0
        elapsedRunStartedRef.current = null
        setElapsedSeconds(0)
        recentContextRef.current = []
        orphanTranslationsRef.current.clear()
        setTranscriptContext('')
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
          origin: cloudCreated ? 'cloud' : 'local',
          title: sessionTitle,
          status: 'active',
        })
        assertCurrent()
        await client.start({
          language: activeSettings.sourceLanguage,
          enable_partials: true,
          diarization: 'speaker',
          operating_point: 'enhanced',
          max_delay: 1.5,
          audio_format: {
            type: 'raw',
            encoding: 'pcm_f32le',
            sample_rate: 48_000,
            channels: 1,
          },
          ...(activeSettings.translationEnabled
            ? {
                translation_config: {
                  target_languages: [activeSettings.targetLanguage],
                  enable_partials: true,
                },
              }
            : {}),
        })
        assertCurrent()

        const capture = new BrowserAudioCapture({
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
                  await enqueueLocal(() => repository.appendAudioChunk(
                    nextSessionId,
                    chunk.blob,
                    {
                      sequence: chunk.sequence,
                      capturedAt: chunk.recordedAt,
                      durationMs: 2_000,
                      mimeType: chunk.blob.type,
                    },
                  ))
                },
              }
            : {}),
        })
        startingCapture = capture
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
        await localWriteChainRef.current
        if (cloudSessionRef.current === nextSessionId) {
          cloudSessionRef.current = null
          cloudQueue.setSession(null)
        }
        if (repositoryOwnerRef.current === startingOwnerId) {
          await repository.deleteSession(nextSessionId).catch(() => undefined)
        }
        if (
          cloudCreated
          && startingUser
          && userRef.current?.id === startingUser.id
        ) {
          await deleteCloudSession(nextSessionId).catch(() => undefined)
        }
        if (currentSessionRef.current === nextSessionId) {
          currentSessionRef.current = ''
          setSessionId('')
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
    client,
    cloudQueue,
    enqueueLocal,
    feedModel,
    ragQueue,
    refreshHistory,
    repository,
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
        const audioSequenceOffset = metadata.nextAudioSequence
        const timelineOffset = Math.max(
          transcriptTimelineEnd,
          (metadata.durationMs ?? 0) / 1_000,
        )
        await ensureSpeechmaticsPreflight()
        assertCurrent()
        await repository.updateSessionMetadata(continuingSessionId, { status: 'active' })
        assertCurrent()

        cloudQueue.setOwner(startingUser?.id ?? null)
        cloudSessionRef.current = continuingCloud ? continuingSessionId : null
        cloudQueue.setSession(continuingCloud ? continuingSessionId : null)

        await client.start({
          timeline_offset_seconds: timelineOffset,
          language: activeSettings.sourceLanguage,
          enable_partials: true,
          diarization: 'speaker',
          operating_point: 'enhanced',
          max_delay: 1.5,
          audio_format: {
            type: 'raw',
            encoding: 'pcm_f32le',
            sample_rate: 48_000,
            channels: 1,
          },
          ...(activeSettings.translationEnabled
            ? {
                translation_config: {
                  target_languages: [activeSettings.targetLanguage],
                  enable_partials: true,
                },
              }
            : {}),
        })
        assertCurrent()

        const capture = new BrowserAudioCapture({
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
                  await enqueueLocal(() => repository.appendAudioChunk(
                    continuingSessionId,
                    chunk.blob,
                    {
                      sequence: audioSequenceOffset + chunk.sequence,
                      capturedAt: chunk.recordedAt,
                      durationMs: 2_000,
                      mimeType: chunk.blob.type,
                    },
                  ))
                },
              }
            : {}),
        })
        startingCapture = capture
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
          void updateCloudSessionWithTimeout(continuingSessionId, {
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
        if (cloudSessionRef.current === continuingSessionId) {
          cloudSessionRef.current = null
          cloudQueue.setSession(null)
        }
        if (repositoryOwnerRef.current === startingOwnerId) {
          await repository.updateSessionMetadata(continuingSessionId, {
            status: previousStatus,
          }).catch(() => undefined)
        }
        sessionAuthRequiredRef.current = false
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
    client,
    cloudQueue,
    enqueueLocal,
    refreshHistory,
    repository,
    setRecorderStatus,
    start,
    transcriptStore,
  ])

  const pauseToggle = useCallback(() => {
    if (statusRef.current === 'recording' || statusRef.current === 'reconnecting') {
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
    setHistoryLoading(true)
    setError(null)
    try {
      let records: StoredSessionRecords
      let loadedTitle = session.title
      let loadedDuration = session.durationSeconds

      if (session.location === 'cloud' && userRef.current) {
        const cloud = await getCloudSession(session.id)
        assertLoadCurrent()
        const localMetadata = await repository.getSessionMetadata(cloud.id)
        assertLoadCurrent()
        const localRecords = localMetadata
          ? await canonicalizeLocalSession(
              repository,
              cloud.id,
              settingsRef.current.targetLanguage,
            )
          : { segments: [], translations: [] }
        assertLoadCurrent()
        const cloudStore = new TranscriptStore()
        const segments: TranscriptSegment[] = []
        const translations: TranslationSegment[] = []
        cloudStore.batch(() => {
          for (const transcript of cloud.transcripts) {
            if (transcript.is_partial || !transcript.text.trim()) continue
            const result = cloudStore.appendTranscript({
              id: transcript.client_segment_id || transcript.id,
              speaker: transcript.speaker,
              text: transcript.text,
              startTime: transcript.start_time,
              endTime: transcript.end_time ?? transcript.start_time,
              receivedAt: Date.parse(transcript.created_at) || Date.now(),
              source: 'cloud',
            })
            if (result.inserted) segments.push(result.record)
            if (transcript.translation?.trim()) {
              const translated = cloudStore.appendTranslation({
                segmentId: result.record.id,
                speaker: transcript.speaker,
                language: cloud.target_language,
                text: transcript.translation,
                startTime: transcript.start_time,
                endTime: transcript.end_time ?? transcript.start_time,
                receivedAt: Date.parse(transcript.updated_at) || Date.now(),
                source: 'cloud',
              })
              if (translated.inserted) translations.push(translated.record)
            }
          }
        })
        const mergedRecords = mergeSessionRecords(
          localRecords,
          { segments, translations },
        )
        records = mergedRecords
        const cloudDurationMs = cloud.duration_seconds * 1_000
        const localWins = Boolean(localMetadata)
        loadedTitle = localWins
          ? localMetadata?.title || cloud.title
          : cloud.title
        loadedDuration = Math.max(
          cloud.duration_seconds,
          (localMetadata?.durationMs ?? 0) / 1_000,
        )
        const mergedStatus = localMetadata?.status === 'completed'
          || cloud.status !== 'active'
          ? 'completed'
          : 'active'
        await repository.ensureSession(cloud.id, {
          createdAt: Date.parse(cloud.created_at) || Date.now(),
          origin: 'cloud',
          title: loadedTitle,
          status: mergedStatus,
          durationMs: Math.max(localMetadata?.durationMs ?? 0, cloudDurationMs),
        })
        assertLoadCurrent()
        if (!localMetadata || mergedRecords.addedSegments > 0) {
          const addedSegments = mergedRecords.segments.slice(localRecords.segments.length)
          await repository.writeTranscriptRecords(
            cloud.id,
            addedSegments.map((segment) => ({
              sequence: segment.sequence,
              recordId: segment.id,
              data: segment,
            })),
          )
          assertLoadCurrent()
        }
        if (!localMetadata || mergedRecords.addedTranslations > 0) {
          const addedTranslations = mergedRecords.translations.slice(
            localRecords.translations.length,
          )
          await repository.writeTranslationRecords(
            cloud.id,
            addedTranslations.map((translation) => ({
              sequence: translation.sequence,
              recordId: translation.id,
              data: translation,
            })),
          )
          assertLoadCurrent()
        }
        await repository.updateSessionMetadata(cloud.id, {
          title: loadedTitle,
          durationMs: Math.max(localMetadata?.durationMs ?? 0, cloudDurationMs),
          status: mergedStatus,
        }, { touch: false })
        assertLoadCurrent()
        if (localMetadata?.status === 'completed' && cloud.status === 'active') {
          void updateCloudSessionWithTimeout(cloud.id, {
            status: 'completed',
            duration_seconds: Math.round(
              Math.max(localMetadata.durationMs ?? 0, cloudDurationMs) / 1_000,
            ),
          }).catch(() => undefined)
        }

        // A page reload can lose the in-memory write-behind queue, but never
        // the local records. Requeue only local records missing or stale in the
        // cloud snapshot; the server upserts by client_segment_id.
        if (localRecords.segments.length > 0) {
          const cloudByClientId = new Map(
            cloud.transcripts
              .filter((transcript) => !transcript.is_partial)
              .map((transcript) => [transcript.client_segment_id, transcript]),
          )
          const localTranslationBySegment = new Map<string, TranslationSegment>()
          for (const translation of localRecords.translations) {
            if (translation.segmentId) {
              localTranslationBySegment.set(translation.segmentId, translation)
            }
          }
          const reconciliation: TranscriptInput[] = []
          for (const segment of localRecords.segments) {
            const remote = cloudByClientId.get(segment.id)
            const translation = localTranslationBySegment.get(segment.id)
            if (
              remote
              && remote.text === segment.text
              && (!translation || remote.translation === translation.text)
            ) {
              continue
            }
            reconciliation.push({
              client_segment_id: segment.id,
              speaker: segment.speaker,
              text: segment.text,
              ...(translation ? { translation: translation.text } : {}),
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
      } else {
        records = await canonicalizeLocalSession(
          repository,
          session.id,
          settingsRef.current.targetLanguage,
        )
        assertLoadCurrent()
        const metadata = await repository.getSessionMetadata(session.id)
        assertLoadCurrent()
        loadedTitle = metadata?.title || loadedTitle
        loadedDuration = (metadata?.durationMs ?? loadedDuration * 1_000) / 1_000
      }

      const loadedMetadata = await repository.getSessionMetadata(session.id)
      assertLoadCurrent()
      currentSessionRef.current = session.id
      currentAudioMimeTypeRef.current = loadedMetadata?.audioMimeType || 'audio/webm'
      currentLocationRef.current = session.location
      cloudSessionRef.current = null
      cloudQueue.setSession(null)
      setSessionId(session.id)
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
            const cachedRecords = await canonicalizeLocalSession(
              repository,
              session.id,
              settingsRef.current.targetLanguage,
            )
            assertLoadCurrent()
            const cachedDuration = (metadata.durationMs ?? 0) / 1_000
            currentSessionRef.current = session.id
            currentAudioMimeTypeRef.current = metadata.audioMimeType || 'audio/webm'
            currentLocationRef.current = 'cloud'
            cloudSessionRef.current = null
            cloudQueue.setSession(null)
            setSessionId(session.id)
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
      if (loadIsCurrent()) setHistoryLoading(false)
    }
  }, [
    applyLoadedRecords,
    cloudQueue,
    ownerScopeIsCurrent,
    repository,
    setRecorderStatus,
    stop,
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
        await updateCloudSessionWithTimeout(id, { title: normalized })
      }
      void refreshHistory()
    } catch (reason) {
      setError(`标题保存失败：${reason instanceof Error ? reason.message : String(reason)}`)
    }
  }, [refreshHistory, repository])

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
      const downloaded = await downloadCompleteAudio(repository, id, title, saveRequest)
      if (!downloaded) setError('当前会话没有保存本地音频。')
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

  useEffect(() => {
    if (!ragEnabled || !settings.automaticAiIngest) ragQueue.clear()
  }, [ragEnabled, ragQueue, settings.automaticAiIngest])

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
        currentAudioMimeTypeRef.current = 'audio/webm'
        cloudSessionRef.current = null
        cloudQueue.setSession(null)
        setSessionId('')
        setTitle(defaultSessionTitle())
        applyLoadedRecords({ segments: [], translations: [] }, 0)
        setHistorySessions([])
        setHistoryLoading(false)
        setLegacyHistoryCount(0)
        ragQueue.clear()
        setError(null)
      }

      cloudQueue.setOwner(nextOwnerId)
      await restoreCloudOutbox()
      if (!cancelled) await refreshHistory()
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
        void enqueueLocal(() => repository.appendTranscript(
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
          ragQueue.queue({
            id: segment.id,
            sessionId: activeSessionId,
            speaker: segment.speaker,
            text: segment.text,
            startTime: segment.startTime,
            endTime: segment.endTime,
          })
        }
        recentContextRef.current = [...recentContextRef.current.slice(-29), segment]
        setTranscriptContext(
          recentContextRef.current
            .map((item) => `${item.speaker}: ${item.text}`)
            .join('\n'),
        )
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
          void enqueueLocal(() => repository.upsertTranslation(
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
        void enqueueLocal(() => repository.appendTranslation(
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
      }),
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
      for (const unsubscribe of unsubscribers) unsubscribe()
    }
  }, [
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
        client.destroy()
        cloudQueue.destroy()
        ragQueue.destroy()
      }, 0)
    }
  }, [client, cloudQueue, ragQueue])

  const connectionLabel = recorderStatus === 'error'
    ? '仅本地录音'
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
    historyLoading,
    historySessions: ownerTransitioning ? [] : historySessions,
    legacyHistoryCount: ownerTransitioning ? 0 : legacyHistoryCount,
    pendingWrites: localPending + cloudPending,
    recorderStatus,
    sessionId: ownerTransitioning ? '' : sessionId,
    stats: {
      finalSegments: ownerTransitioning ? 0 : transcriptSnapshot.stats.segmentCount,
      translatedSegments: ownerTransitioning ? 0 : transcriptSnapshot.stats.translationCount,
      speakers: ownerTransitioning ? 0 : transcriptSnapshot.stats.speakerCount,
      topWords: ownerTransitioning ? [] : topWords,
    },
    title: ownerTransitioning ? defaultSessionTitle() : title,
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
