import {
  IndexedDbSessionRepository,
  type LegacyMigrationResult,
  type LegacySessionImport,
  type RecordWrite,
  type SessionMetadata,
} from './core/session'

export interface ConfirmedSegment {
  text: string
  startTime: number
  endTime: number
}

export interface TranscriptLine {
  id: number
  speaker: string
  confirmedSegments: ConfirmedSegment[]
  partialText: string
  lastSegmentEndTime: number
}

export interface TranslationLine {
  id: string
  speaker: string
  startTime: number
  content: string
  isPartial: boolean
  original?: string
}

export interface SessionData {
  id: string
  audioBlob: Blob | null
  lines: TranscriptLine[]
  translations: TranslationLine[]
  timestamp: number
  title?: string
  summary?: string
}

const HISTORY_LIMIT = 10
const MUTABLE_TRANSCRIPT_TAIL = 2
const MUTABLE_TRANSLATION_TAIL = 4

const repository = new IndexedDbSessionRepository<TranscriptLine, TranslationLine>()
let legacyMigrationPromise: Promise<LegacyMigrationResult> | undefined
const compatibilitySnapshots = new Map<
  string,
  {
    lines: readonly TranscriptLine[]
    translations: readonly TranslationLine[]
  }
>()

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function finiteTimestamp(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

function mapLegacySession(
  value: unknown,
  key: string,
): LegacySessionImport<TranscriptLine, TranslationLine> {
  const now = Date.now()
  const raw = isObject(value) ? value : {}
  const id = typeof raw.id === 'string' && raw.id ? raw.id : key
  const timestamp = finiteTimestamp(raw.timestamp, now)
  const lines = Array.isArray(raw.lines)
    ? (raw.lines as TranscriptLine[])
    : []
  const translations = Array.isArray(raw.translations)
    ? (raw.translations as TranslationLine[])
    : []
  const audioBlob = raw.audioBlob instanceof Blob ? raw.audioBlob : null

  return {
    id,
    createdAt: timestamp,
    updatedAt: timestamp,
    status: 'completed',
    completedAt: timestamp,
    transcripts: lines,
    translations,
    ...(audioBlob ? { audioChunks: [audioBlob] } : {}),
    ...(audioBlob?.type ? { audioMimeType: audioBlob.type } : {}),
    ...(typeof raw.title === 'string' ? { title: raw.title } : {}),
    ...(typeof raw.summary === 'string' ? { summary: raw.summary } : {}),
  }
}

async function ensureLegacySessionsMigrated(): Promise<LegacyMigrationResult> {
  if (!legacyMigrationPromise) {
    const migration = repository.migrateLegacySessions(mapLegacySession)
    legacyMigrationPromise = migration.catch((error: unknown) => {
      legacyMigrationPromise = undefined
      throw error
    })
  }
  return legacyMigrationPromise
}

/**
 * Explicit migration hook for a new shell that uses the repository directly.
 * Existing db.ts APIs call this automatically.
 */
export function migrateLegacySessionStorage(): Promise<LegacyMigrationResult> {
  return ensureLegacySessionsMigrated()
}

function recordWrites<T>(
  values: readonly T[],
  previousValues: readonly T[] | undefined,
  previouslyStored: number,
  mutableTail: number,
  idFor: (value: T, sequence: number) => string,
): RecordWrite<T>[] {
  const changedSequences = new Set<number>()
  if (!previousValues) {
    for (let sequence = 0; sequence < values.length; sequence += 1) {
      changedSequences.add(sequence)
    }
  } else {
    for (let sequence = 0; sequence < values.length; sequence += 1) {
      if (
        sequence >= previouslyStored ||
        previousValues[sequence] !== values[sequence]
      ) {
        changedSequences.add(sequence)
      }
    }
  }

  // Classic keeps the live paragraph mutable. Rewriting a tiny tail also
  // covers callers that mutate an item in place rather than replacing it.
  const tailStart = Math.max(
    0,
    Math.min(previouslyStored, values.length) - mutableTail,
  )
  for (let sequence = tailStart; sequence < values.length; sequence += 1) {
    changedSequences.add(sequence)
  }

  return [...changedSequences]
    .sort((left, right) => left - right)
    .map((sequence) => {
      const data = values[sequence]
      return {
        sequence,
        recordId: idFor(data, sequence),
        data,
      }
    })
}

async function trimLegacyHistoryLimit(): Promise<void> {
  let index = 0
  for await (const metadata of repository.iterateSessions()) {
    if (index >= HISTORY_LIMIT) {
      await repository.deleteSession(metadata.id)
      compatibilitySnapshots.delete(metadata.id)
    }
    index += 1
  }
}

/**
 * Compatibility adapter for the Classic UI.
 *
 * The caller still supplies a cumulative snapshot, but only the new audio suffix,
 * newly appended records, and a small mutable tail are written to IndexedDB.
 * New code should use IndexedDbSessionRepository directly and append each chunk or
 * record at capture time.
 */
export async function saveSession(
  id: string,
  data: Omit<SessionData, 'id' | 'timestamp'>,
): Promise<boolean> {
  try {
    await ensureLegacySessionsMigrated()
    const previousSnapshot = compatibilitySnapshots.get(id)
    let metadata = await repository.ensureSession(id, {
      ...(data.title !== undefined ? { title: data.title } : {}),
      ...(data.summary !== undefined ? { summary: data.summary } : {}),
      ...(data.audioBlob?.type ? { audioMimeType: data.audioBlob.type } : {}),
    })

    await repository.writeTranscriptRecords(
      id,
      recordWrites(
        data.lines,
        previousSnapshot?.lines,
        metadata.transcriptCount,
        MUTABLE_TRANSCRIPT_TAIL,
        (_line, sequence) => `${id}:classic-line:${sequence}`,
      ),
      data.lines.length,
    )

    await repository.writeTranslationRecords(
      id,
      recordWrites(
        data.translations,
        previousSnapshot?.translations,
        metadata.translationCount,
        MUTABLE_TRANSLATION_TAIL,
        (_translation, sequence) =>
          `${id}:classic-translation:${sequence}`,
      ),
      data.translations.length,
    )

    metadata = (await repository.getSessionMetadata(id)) ?? metadata
    const audioBlob = data.audioBlob
    if (!audioBlob && metadata.audioBytes > 0) {
      await repository.replaceAudioWithChunk(id, null)
    } else if (audioBlob && audioBlob.size < metadata.audioBytes) {
      // A shorter snapshot means the caller reset/replaced the recording.
      await repository.replaceAudioWithChunk(id, audioBlob, {
        mimeType: audioBlob.type,
      })
    } else if (audioBlob && audioBlob.size > metadata.audioBytes) {
      const suffix = audioBlob.slice(
        metadata.audioBytes,
        audioBlob.size,
        audioBlob.type,
      )
      await repository.appendAudioChunk(id, suffix, {
        mimeType: audioBlob.type,
      })
    }

    await repository.updateSessionMetadata(id, {
      ...(data.title !== undefined ? { title: data.title } : {}),
      ...(data.summary !== undefined ? { summary: data.summary } : {}),
      ...(audioBlob?.type ? { audioMimeType: audioBlob.type } : {}),
    })
    await trimLegacyHistoryLimit()
    compatibilitySnapshots.set(id, {
      lines: data.lines.slice(),
      translations: data.translations.slice(),
    })
    return true
  } catch (error) {
    console.error('Failed to save session:', error)
    return false
  }
}

export async function loadSession(id: string): Promise<SessionData | undefined> {
  try {
    await ensureLegacySessionsMigrated()
    const metadata = await repository.getSessionMetadata(id)
    if (!metadata) return undefined

    const lines: TranscriptLine[] = []
    for await (const record of repository.iterateTranscripts(id)) {
      lines.push(record.data)
    }
    const translations: TranslationLine[] = []
    for await (const record of repository.iterateTranslations(id)) {
      translations.push(record.data)
    }

    const audioBlob = await repository.getCompleteAudioBlob(
      id,
      metadata.audioMimeType,
    )
    compatibilitySnapshots.set(id, {
      lines: lines.slice(),
      translations: translations.slice(),
    })
    return {
      id,
      audioBlob,
      lines,
      translations,
      timestamp: metadata.updatedAt,
      ...(metadata.title !== undefined ? { title: metadata.title } : {}),
      ...(metadata.summary !== undefined ? { summary: metadata.summary } : {}),
    }
  } catch (error) {
    console.error('Failed to load session:', error)
    return loadUnmigratedLegacySession(id)
  }
}

async function loadUnmigratedLegacySession(
  id: string,
): Promise<SessionData | undefined> {
  try {
    const value = await repository.getLegacySession(id)
    if (!isObject(value)) return undefined
    const mapped = mapLegacySession(value, id)
    return {
      id: mapped.id,
      audioBlob: mapped.audioChunks?.[0] ?? null,
      lines: [...(mapped.transcripts ?? [])],
      translations: [...(mapped.translations ?? [])],
      timestamp: mapped.updatedAt ?? mapped.createdAt,
      ...(mapped.title !== undefined ? { title: mapped.title } : {}),
      ...(mapped.summary !== undefined ? { summary: mapped.summary } : {}),
    }
  } catch (error) {
    console.error('Failed to read unmigrated session:', error)
    return undefined
  }
}

export async function clearSession(id: string): Promise<boolean> {
  try {
    await ensureLegacySessionsMigrated()
    await repository.deleteSession(id)
    compatibilitySnapshots.delete(id)
    return true
  } catch (error) {
    console.error('Failed to clear session:', error)
    return false
  }
}

export async function listSessions(): Promise<
  Pick<SessionData, 'id' | 'timestamp'>[]
> {
  try {
    await ensureLegacySessionsMigrated()
    const sessions: Pick<SessionData, 'id' | 'timestamp'>[] = []
    for await (const metadata of repository.iterateSessions()) {
      sessions.push({ id: metadata.id, timestamp: metadata.updatedAt })
    }
    return sessions
  } catch (error) {
    console.error('Failed to list sessions:', error)
    return []
  }
}

export async function getSessionMeta(
  id: string,
): Promise<{ title?: string; summary?: string }> {
  try {
    await ensureLegacySessionsMigrated()
    const metadata = await repository.getSessionMetadata(id)
    if (!metadata) return {}
    return {
      ...(metadata.title !== undefined ? { title: metadata.title } : {}),
      ...(metadata.summary !== undefined ? { summary: metadata.summary } : {}),
    }
  } catch (error) {
    console.error('Failed to get session meta:', error)
    return {}
  }
}

export async function saveSessionMeta(
  id: string,
  meta: { title?: string; summary?: string },
): Promise<void> {
  try {
    await ensureLegacySessionsMigrated()
    const existing = await repository.getSessionMetadata(id)
    if (!existing) return
    await repository.updateSessionMetadata(id, meta, { touch: false })
  } catch (error) {
    console.error('Failed to save session meta:', error)
  }
}

export type { SessionMetadata }
