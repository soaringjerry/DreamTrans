import type { IDBPDatabase } from 'idb'
import {
  normalizeSessionOwnerId,
  normalizeStoredSessionMetadata,
  openSessionDatabase,
  publicSessionMetadata,
  SESSION_DATABASE_NAME,
  SESSION_STORES,
  sessionOwnerKey,
  type DreamTransSessionDatabase,
  type StoredSessionMetadata,
} from './database'
import type {
  AppendAudioChunkOptions,
  AppendRecordOptions,
  AudioChunkRecord,
  CloudTranscriptOutboxPage,
  CloudTranscriptOutboxPageOptions,
  CloudTranscriptOutboxRecord,
  CloudTranscriptOutboxWrite,
  CreateSessionInput,
  LegacyMigrationResult,
  LegacySessionImport,
  RecordWrite,
  SequencePage,
  SequencePageOptions,
  SequencedSessionRecord,
  SessionListPage,
  SessionListPageOptions,
  SessionMetadata,
  SessionMetadataPatch,
  SessionOrigin,
  SessionOwnerId,
  UpdateSessionMetadataOptions,
} from './types'
import { recordWriteMode } from './writePlan'

const DEFAULT_PAGE_SIZE = 100
const MAX_PAGE_SIZE = 1_000
const MAX_SEQUENCE = Number.MAX_SAFE_INTEGER
const OWNER_INDEX_UPPER_SENTINEL: IDBValidKey = []

type RecordStoreName =
  | typeof SESSION_STORES.transcripts
  | typeof SESSION_STORES.translations

function normalizeLimit(limit?: number): number {
  if (limit === undefined) return DEFAULT_PAGE_SIZE
  if (!Number.isFinite(limit) || limit < 1) {
    throw new RangeError('Page limit must be a positive finite number')
  }
  return Math.min(Math.floor(limit), MAX_PAGE_SIZE)
}

function validateSequence(sequence: number, label = 'Sequence'): void {
  if (
    !Number.isSafeInteger(sequence) ||
    sequence < 0 ||
    sequence >= MAX_SEQUENCE
  ) {
    throw new RangeError(
      `${label} must be a non-negative safe integer below ${MAX_SEQUENCE}`,
    )
  }
}

function createMetadata(
  input: CreateSessionInput,
  now: number,
  fallbackOwnerId: SessionOwnerId,
): StoredSessionMetadata {
  const createdAt = input.createdAt ?? now
  const updatedAt = input.updatedAt ?? createdAt
  const requestedOwnerId =
    input.ownerId === undefined
      ? fallbackOwnerId
      : normalizeSessionOwnerId(input.ownerId)
  if (requestedOwnerId !== fallbackOwnerId) {
    throw new Error('Session owner does not match the repository owner scope')
  }
  return normalizeStoredSessionMetadata({
    id: input.id,
    ownerId: requestedOwnerId,
    origin: input.origin ?? 'local',
    cloudSessionPending:
      input.origin === 'cloud' && input.cloudSessionPending === true,
    ...(input.sourceLanguage !== undefined
      ? { sourceLanguage: input.sourceLanguage }
      : {}),
    ...(input.targetLanguage !== undefined
      ? { targetLanguage: input.targetLanguage }
      : {}),
    createdAt,
    updatedAt,
    status: input.status ?? 'active',
    ...(input.completedAt !== undefined ? { completedAt: input.completedAt } : {}),
    ...(input.title !== undefined ? { title: input.title } : {}),
    ...(input.summary !== undefined ? { summary: input.summary } : {}),
    ...(input.durationMs !== undefined ? { durationMs: input.durationMs } : {}),
    ...(input.audioMimeType !== undefined ? { audioMimeType: input.audioMimeType } : {}),
    audioBytes: 0,
    audioChunkCount: 0,
    localAudioIncomplete: input.localAudioIncomplete === true,
    transcriptCount: 0,
    translationCount: 0,
    nextAudioSequence: 0,
    nextTranscriptSequence: 0,
    nextTranslationSequence: 0,
  })
}

function applyMetadataPatch(
  metadata: StoredSessionMetadata,
  patch: SessionMetadataPatch,
  updatedAt: number,
): StoredSessionMetadata {
  const next = { ...metadata, updatedAt }
  const keys: Array<keyof SessionMetadataPatch> = [
    'title',
    'summary',
    'durationMs',
    'audioMimeType',
    'cloudSessionPending',
    'localAudioIncomplete',
    'sourceLanguage',
    'status',
    'targetLanguage',
    'completedAt',
  ]
  for (const key of keys) {
    const value = patch[key]
    if (value !== undefined) {
      if (key === 'localAudioIncomplete') {
        next.localAudioIncomplete = metadata.localAudioIncomplete || value === true
        continue
      }
      Object.assign(next, { [key]: value })
    }
  }
  return next
}

function metadataMatchesOwner(
  metadata: StoredSessionMetadata,
  ownerId: SessionOwnerId,
): boolean {
  return normalizeStoredSessionMetadata(metadata).ownerKey === sessionOwnerKey(ownerId)
}

export function sessionMetadataMatchesOwner(
  metadata: SessionMetadata,
  ownerId: SessionOwnerId,
): boolean {
  return normalizeStoredSessionMetadata(metadata).ownerKey === sessionOwnerKey(ownerId)
}

function originAllowed(
  origin: SessionOrigin,
  origins: readonly SessionOrigin[] | undefined,
): boolean {
  return !origins || origins.length === 0 || origins.includes(origin)
}

function requiredIdentifier(value: string, label: string): string {
  const normalized = value.trim()
  if (!normalized) throw new TypeError(`${label} is required`)
  return normalized
}

export function assertSerializableOutboxPayload(
  value: unknown,
  path = '$',
  ancestors = new Set<object>(),
): void {
  if (
    value === null
    || typeof value === 'string'
    || typeof value === 'boolean'
  ) {
    return
  }
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) {
      throw new TypeError(`Outbox payload contains a non-finite number at ${path}`)
    }
    return
  }
  if (typeof value !== 'object') {
    throw new TypeError(`Outbox payload is not serializable at ${path}`)
  }
  if (typeof Blob !== 'undefined' && value instanceof Blob) {
    throw new TypeError(`Outbox payload cannot contain Blob data at ${path}`)
  }
  if (ancestors.has(value)) {
    throw new TypeError(`Outbox payload contains a cycle at ${path}`)
  }
  const prototype = Object.getPrototypeOf(value)
  if (prototype !== Object.prototype && prototype !== null && !Array.isArray(value)) {
    throw new TypeError(`Outbox payload must contain only plain objects at ${path}`)
  }
  ancestors.add(value)
  if (Array.isArray(value)) {
    for (let index = 0; index < value.length; index += 1) {
      assertSerializableOutboxPayload(value[index], `${path}[${index}]`, ancestors)
    }
  } else {
    for (const [key, child] of Object.entries(value)) {
      assertSerializableOutboxPayload(child, `${path}.${key}`, ancestors)
    }
  }
  ancestors.delete(value)
}

export function buildCloudTranscriptOutboxRecord<TPayload>(
  ownerId: string,
  sessionId: string,
  clientSegmentId: string,
  payload: TPayload,
  now: number,
  existing?: CloudTranscriptOutboxRecord,
): CloudTranscriptOutboxRecord<TPayload> {
  const normalizedOwnerId = normalizeSessionOwnerId(ownerId)
  if (normalizedOwnerId === null) {
    throw new TypeError('Cloud outbox owner id is required')
  }
  const normalizedSessionId = requiredIdentifier(sessionId, 'Session id')
  const normalizedSegmentId = requiredIdentifier(
    clientSegmentId,
    'Client segment id',
  )
  assertSerializableOutboxPayload(payload)
  return {
    ownerId: normalizedOwnerId,
    sessionId: normalizedSessionId,
    clientSegmentId: normalizedSegmentId,
    payload,
    createdAt: existing?.createdAt ?? now,
    updatedAt: Math.max(now, (existing?.updatedAt ?? 0) + 1),
  }
}

function sessionSequenceRange(
  sessionId: string,
  options: SequencePageOptions,
): IDBKeyRange | undefined {
  const after = options.afterSequence
  const before = options.beforeSequence
  if (after !== undefined) validateSequence(after, 'afterSequence')
  if (before !== undefined) validateSequence(before, 'beforeSequence')
  if (before === 0) return undefined
  if (after !== undefined && before !== undefined && after >= before) return undefined

  const lowerSequence = after ?? 0
  const upperSequence = before ?? MAX_SEQUENCE
  return IDBKeyRange.bound(
    [sessionId, lowerSequence],
    [sessionId, upperSequence],
    after !== undefined,
    before !== undefined,
  )
}

export interface IndexedDbSessionRepositoryOptions {
  databaseName?: string
  now?: () => number
  /**
   * Every session operation is constrained to this owner. A resolver is useful
   * for applications whose authenticated account can change without replacing
   * the shared IndexedDB connection. Omitted/null means anonymous only.
   */
  ownerId?: SessionOwnerId | (() => SessionOwnerId)
}

export class IndexedDbSessionRepository<TTranscript = unknown, TTranslation = unknown> {
  private readonly databasePromise: Promise<IDBPDatabase<DreamTransSessionDatabase>>
  private readonly now: () => number
  private readonly ownerId: SessionOwnerId | (() => SessionOwnerId)

  constructor(options: IndexedDbSessionRepositoryOptions = {}) {
    this.databasePromise = openSessionDatabase(
      options.databaseName ?? SESSION_DATABASE_NAME,
    )
    this.now = options.now ?? Date.now
    this.ownerId = options.ownerId ?? null
  }

  currentOwnerId(): SessionOwnerId {
    return normalizeSessionOwnerId(
      typeof this.ownerId === 'function' ? this.ownerId() : this.ownerId,
    )
  }

  private requireOwnedMetadata(
    metadata: StoredSessionMetadata | undefined,
    id: string,
    ownerId: SessionOwnerId,
  ): StoredSessionMetadata {
    if (!metadata || !metadataMatchesOwner(metadata, ownerId)) {
      // Use one error for missing and foreign sessions so callers cannot use
      // this API to enumerate another account's browser cache.
      throw new Error(`Session not found in current owner scope: ${id}`)
    }
    return normalizeStoredSessionMetadata(metadata)
  }

  async createSession(input: CreateSessionInput): Promise<SessionMetadata> {
    return this.createSessionForOwner(input, this.currentOwnerId())
  }

  private async createSessionForOwner(
    input: CreateSessionInput,
    ownerId: SessionOwnerId,
  ): Promise<SessionMetadata> {
    if (!input.id) throw new TypeError('Session id is required')
    const db = await this.databasePromise
    const metadata = createMetadata(input, this.now(), ownerId)
    await db.add(SESSION_STORES.metadata, metadata)
    return publicSessionMetadata(metadata)
  }

  async ensureSession(
    id: string,
    input: Omit<CreateSessionInput, 'id'> = {},
  ): Promise<SessionMetadata> {
    const ownerId = this.currentOwnerId()
    const existing = await this.getSessionMetadataForOwner(id, ownerId)
    if (existing) return existing
    try {
      return await this.createSessionForOwner({ id, ...input }, ownerId)
    } catch (error) {
      if (error instanceof DOMException && error.name === 'ConstraintError') {
        const concurrentlyCreated = await this.getSessionMetadataForOwner(id, ownerId)
        if (concurrentlyCreated) return concurrentlyCreated
      }
      throw error
    }
  }

  async getSessionMetadata(id: string): Promise<SessionMetadata | undefined> {
    return this.getSessionMetadataForOwner(id, this.currentOwnerId())
  }

  private async getSessionMetadataForOwner(
    id: string,
    ownerId: SessionOwnerId,
  ): Promise<SessionMetadata | undefined> {
    const db = await this.databasePromise
    const metadata = await db.get(SESSION_STORES.metadata, id)
    if (!metadata || !metadataMatchesOwner(metadata, ownerId)) {
      return undefined
    }
    return publicSessionMetadata(metadata)
  }

  async updateSessionMetadata(
    id: string,
    patch: SessionMetadataPatch,
    options: UpdateSessionMetadataOptions = {},
  ): Promise<SessionMetadata> {
    const db = await this.databasePromise
    const tx = db.transaction(SESSION_STORES.metadata, 'readwrite')
    const ownerId = this.currentOwnerId()
    const existing = this.requireOwnedMetadata(
      await tx.store.get(id),
      id,
      ownerId,
    )
    const updatedAt =
      options.updatedAt ??
      (options.touch === false ? existing.updatedAt : this.now())
    const next = applyMetadataPatch(existing, patch, updatedAt)
    await tx.store.put(next)
    await tx.done
    return publicSessionMetadata(next)
  }

  async completeSession(
    id: string,
    patch: Omit<SessionMetadataPatch, 'status' | 'completedAt'> = {},
  ): Promise<SessionMetadata> {
    const completedAt = this.now()
    return this.updateSessionMetadata(id, {
      ...patch,
      status: 'completed',
      completedAt,
    })
  }

  /**
   * Persistently records that the browser-local audio is only a partial
   * recovery artifact. The flag is sticky by convention and does not reorder
   * history merely because an asynchronous capture failure was reported.
   */
  async markLocalAudioIncomplete(id: string): Promise<SessionMetadata> {
    return this.updateSessionMetadata(
      id,
      { localAudioIncomplete: true },
      { touch: false },
    )
  }

  async listSessions(
    options: SessionListPageOptions = {},
  ): Promise<SessionListPage> {
    return this.listSessionsForOwner(this.currentOwnerId(), options)
  }

  private async listSessionsForOwner(
    ownerId: SessionOwnerId,
    options: SessionListPageOptions,
  ): Promise<SessionListPage> {
    const db = await this.databasePromise
    const limit = normalizeLimit(options.limit)
    const ownerKey = sessionOwnerKey(ownerId)
    const index = db
      .transaction(SESSION_STORES.metadata)
      .store.index('by-owner-updated-at')
    const query = options.before
      ? IDBKeyRange.bound(
          [ownerKey],
          [ownerKey, options.before.updatedAt, options.before.id],
          false,
          true,
        )
      : IDBKeyRange.bound(
          [ownerKey],
          [ownerKey, OWNER_INDEX_UPPER_SENTINEL],
        )
    let cursor = await index.openCursor(query, 'prev')
    const items: SessionMetadata[] = []

    while (cursor && items.length < limit + 1) {
      const metadata = publicSessionMetadata(cursor.value)
      if (originAllowed(metadata.origin, options.origins)) {
        items.push(metadata)
      }
      cursor = await cursor.continue()
    }

    const hasMore = items.length > limit
    if (hasMore) items.pop()
    const last = items.at(-1)
    return {
      items,
      hasMore,
      ...(hasMore && last
        ? { nextCursor: { updatedAt: last.updatedAt, id: last.id } }
        : {}),
    }
  }

  async *iterateSessions(pageSize = DEFAULT_PAGE_SIZE): AsyncGenerator<SessionMetadata> {
    const ownerId = this.currentOwnerId()
    let before: SessionListPageOptions['before']
    do {
      const page = await this.listSessionsForOwner(ownerId, {
        limit: pageSize,
        ...(before ? { before } : {}),
      })
      for (const metadata of page.items) yield metadata
      if (!page.hasMore || !page.nextCursor) return
      before = page.nextCursor
    } while (before)
  }

  async appendTranscript(
    sessionId: string,
    data: TTranscript,
    options: AppendRecordOptions = {},
  ): Promise<SequencedSessionRecord<TTranscript>> {
    return this.appendRecord(
      SESSION_STORES.transcripts,
      sessionId,
      data,
      options,
    )
  }

  async appendTranslation(
    sessionId: string,
    data: TTranslation,
    options: AppendRecordOptions = {},
  ): Promise<SequencedSessionRecord<TTranslation>> {
    return this.appendRecord(
      SESSION_STORES.translations,
      sessionId,
      data,
      options,
    )
  }

  async upsertTranscript(
    sessionId: string,
    recordId: string,
    data: TTranscript,
  ): Promise<SequencedSessionRecord<TTranscript>> {
    return this.upsertRecord(
      SESSION_STORES.transcripts,
      sessionId,
      recordId,
      data,
    )
  }

  async upsertTranslation(
    sessionId: string,
    recordId: string,
    data: TTranslation,
  ): Promise<SequencedSessionRecord<TTranslation>> {
    return this.upsertRecord(
      SESSION_STORES.translations,
      sessionId,
      recordId,
      data,
    )
  }

  /**
   * Writes records by sequence. Supplying a dense snapshot that covers
   * [0, truncateAfter) performs an atomic full replacement. A sparse set with
   * truncateAfter updates only those records and removes the stale tail.
   */
  async writeTranscriptRecords(
    sessionId: string,
    records: readonly RecordWrite<TTranscript>[],
    truncateAfter?: number,
  ): Promise<void> {
    await this.writeRecords(
      SESSION_STORES.transcripts,
      sessionId,
      records,
      truncateAfter,
    )
  }

  /**
   * Translation equivalent of writeTranscriptRecords, with the same dense
   * replacement versus sparse tail-truncate contract.
   */
  async writeTranslationRecords(
    sessionId: string,
    records: readonly RecordWrite<TTranslation>[],
    truncateAfter?: number,
  ): Promise<void> {
    await this.writeRecords(
      SESSION_STORES.translations,
      sessionId,
      records,
      truncateAfter,
    )
  }

  async getTranscriptPage(
    sessionId: string,
    options: SequencePageOptions = {},
  ): Promise<SequencePage<SequencedSessionRecord<TTranscript>>> {
    return this.readRecordPage(SESSION_STORES.transcripts, sessionId, options)
  }

  async getTranslationPage(
    sessionId: string,
    options: SequencePageOptions = {},
  ): Promise<SequencePage<SequencedSessionRecord<TTranslation>>> {
    return this.readRecordPage(SESSION_STORES.translations, sessionId, options)
  }

  async *iterateTranscripts(
    sessionId: string,
    pageSize = DEFAULT_PAGE_SIZE,
  ): AsyncGenerator<SequencedSessionRecord<TTranscript>> {
    let afterSequence: number | undefined
    do {
      const page = await this.getTranscriptPage(sessionId, {
        limit: pageSize,
        ...(afterSequence !== undefined ? { afterSequence } : {}),
      })
      for (const item of page.items) yield item
      if (!page.hasMore || page.nextSequence === undefined) return
      afterSequence = page.nextSequence
    } while (afterSequence !== undefined)
  }

  async *iterateTranslations(
    sessionId: string,
    pageSize = DEFAULT_PAGE_SIZE,
  ): AsyncGenerator<SequencedSessionRecord<TTranslation>> {
    let afterSequence: number | undefined
    do {
      const page = await this.getTranslationPage(sessionId, {
        limit: pageSize,
        ...(afterSequence !== undefined ? { afterSequence } : {}),
      })
      for (const item of page.items) yield item
      if (!page.hasMore || page.nextSequence === undefined) return
      afterSequence = page.nextSequence
    } while (afterSequence !== undefined)
  }

  async appendAudioChunk(
    sessionId: string,
    blob: Blob,
    options: AppendAudioChunkOptions = {},
  ): Promise<AudioChunkRecord> {
    const db = await this.databasePromise
    const tx = db.transaction(
      [SESSION_STORES.metadata, SESSION_STORES.audioChunks],
      'readwrite',
    )
    const metadataStore = tx.objectStore(SESSION_STORES.metadata)
    const audioStore = tx.objectStore(SESSION_STORES.audioChunks)
    const now = options.capturedAt ?? this.now()
    const ownerId = this.currentOwnerId()
    let metadata = await metadataStore.get(sessionId)
    if (!metadata) {
      metadata = createMetadata(
        { id: sessionId, audioMimeType: options.mimeType || blob.type },
        now,
        ownerId,
      )
    } else {
      metadata = this.requireOwnedMetadata(metadata, sessionId, ownerId)
    }
    const sequence = options.sequence ?? metadata.nextAudioSequence
    validateSequence(sequence)
    const existing = await audioStore.get([sessionId, sequence])
    if (existing) {
      throw new DOMException(
        `Audio chunk ${sessionId}/${sequence} already exists`,
        'ConstraintError',
      )
    }
    const mimeType = options.mimeType || blob.type || metadata.audioMimeType || 'application/octet-stream'
    const record: AudioChunkRecord = {
      sessionId,
      sequence,
      blob,
      mimeType,
      byteLength: blob.size,
      capturedAt: now,
      ...(options.durationMs !== undefined ? { durationMs: options.durationMs } : {}),
    }
    await audioStore.add(record)
    metadata = {
      ...metadata,
      updatedAt: now,
      audioMimeType: metadata.audioMimeType || mimeType,
      audioBytes: metadata.audioBytes + blob.size,
      audioChunkCount: metadata.audioChunkCount + 1,
      nextAudioSequence: Math.max(metadata.nextAudioSequence, sequence + 1),
    }
    await metadataStore.put(metadata)
    await tx.done
    return record
  }

  async replaceAudioWithChunk(
    sessionId: string,
    blob: Blob | null,
    options: Omit<AppendAudioChunkOptions, 'sequence'> = {},
  ): Promise<void> {
    const db = await this.databasePromise
    const tx = db.transaction(
      [SESSION_STORES.metadata, SESSION_STORES.audioChunks],
      'readwrite',
    )
    const metadataStore = tx.objectStore(SESSION_STORES.metadata)
    const audioStore = tx.objectStore(SESSION_STORES.audioChunks)
    const now = options.capturedAt ?? this.now()
    const ownerId = this.currentOwnerId()
    let metadata = await metadataStore.get(sessionId)
    if (!metadata) metadata = createMetadata({ id: sessionId }, now, ownerId)
    else metadata = this.requireOwnedMetadata(metadata, sessionId, ownerId)
    await audioStore.delete(
      IDBKeyRange.bound([sessionId, 0], [sessionId, MAX_SEQUENCE]),
    )
    const mimeType = options.mimeType || blob?.type || metadata.audioMimeType
    if (blob && blob.size > 0) {
      const record: AudioChunkRecord = {
        sessionId,
        sequence: 0,
        blob,
        mimeType: mimeType || 'application/octet-stream',
        byteLength: blob.size,
        capturedAt: now,
        ...(options.durationMs !== undefined ? { durationMs: options.durationMs } : {}),
      }
      await audioStore.put(record)
    }
    metadata = {
      ...metadata,
      updatedAt: now,
      ...(mimeType ? { audioMimeType: mimeType } : {}),
      audioBytes: blob?.size ?? 0,
      audioChunkCount: blob && blob.size > 0 ? 1 : 0,
      nextAudioSequence: blob && blob.size > 0 ? 1 : 0,
    }
    await metadataStore.put(metadata)
    await tx.done
  }

  async getAudioChunkPage(
    sessionId: string,
    options: SequencePageOptions = {},
  ): Promise<SequencePage<AudioChunkRecord>> {
    if (!await this.getSessionMetadata(sessionId)) {
      return { items: [], hasMore: false }
    }
    const db = await this.databasePromise
    const limit = normalizeLimit(options.limit)
    const range = sessionSequenceRange(sessionId, options)
    if (!range) return { items: [], hasMore: false }
    const direction = options.direction === 'backward' ? 'prev' : 'next'
    const index = db
      .transaction(SESSION_STORES.audioChunks)
      .store.index('by-session-sequence')
    let cursor = await index.openCursor(range, direction)
    const items: AudioChunkRecord[] = []
    while (cursor && items.length < limit + 1) {
      items.push(cursor.value)
      cursor = await cursor.continue()
    }
    return this.createSequencePage(items, limit)
  }

  async *iterateAudioChunks(
    sessionId: string,
    pageSize = DEFAULT_PAGE_SIZE,
  ): AsyncGenerator<AudioChunkRecord> {
    let afterSequence: number | undefined
    do {
      const page = await this.getAudioChunkPage(sessionId, {
        limit: pageSize,
        ...(afterSequence !== undefined ? { afterSequence } : {}),
      })
      for (const item of page.items) yield item
      if (!page.hasMore || page.nextSequence === undefined) return
      afterSequence = page.nextSequence
    } while (afterSequence !== undefined)
  }

  async getCompleteAudioBlob(
    sessionId: string,
    mimeType?: string,
  ): Promise<Blob | null> {
    const parts: Blob[] = []
    let storedMimeType = mimeType
    for await (const chunk of this.iterateAudioChunks(sessionId)) {
      parts.push(chunk.blob)
      storedMimeType ||= chunk.mimeType
    }
    if (parts.length === 0) return null
    if (!storedMimeType) {
      const metadata = await this.getSessionMetadata(sessionId)
      storedMimeType = metadata?.audioMimeType
    }
    return new Blob(parts, {
      type: storedMimeType || 'application/octet-stream',
    })
  }

  async upsertCloudTranscriptOutbox<TPayload>(
    sessionId: string,
    clientSegmentId: string,
    payload: TPayload,
  ): Promise<CloudTranscriptOutboxRecord<TPayload>> {
    const records = await this.upsertCloudTranscriptOutboxBatch(sessionId, [{
      clientSegmentId,
      payload,
    }])
    const record = records[0]
    if (!record) throw new Error('Cloud outbox write did not produce a record')
    return record
  }

  async upsertCloudTranscriptOutboxBatch<TPayload>(
    sessionId: string,
    writes: readonly CloudTranscriptOutboxWrite<TPayload>[],
  ): Promise<Array<CloudTranscriptOutboxRecord<TPayload>>> {
    if (writes.length === 0) return []
    const ownerId = this.currentOwnerId()
    if (ownerId === null) throw new Error('Cloud outbox requires an authenticated owner')
    const db = await this.databasePromise
    const tx = db.transaction(
      [SESSION_STORES.metadata, SESSION_STORES.cloudTranscriptOutbox],
      'readwrite',
    )
    const metadata = this.requireOwnedMetadata(
      await tx.objectStore(SESSION_STORES.metadata).get(sessionId),
      sessionId,
      ownerId,
    )
    if (metadata.origin !== 'cloud') {
      throw new Error('Cloud outbox can only be used by a cloud session cache')
    }
    const store = tx.objectStore(SESSION_STORES.cloudTranscriptOutbox)
    const records: Array<CloudTranscriptOutboxRecord<TPayload>> = []
    for (const write of writes) {
      const key: [string, string, string] = [
        ownerId,
        sessionId,
        requiredIdentifier(write.clientSegmentId, 'Client segment id'),
      ]
      const existing = await store.get(key)
      const record = buildCloudTranscriptOutboxRecord(
        ownerId,
        sessionId,
        write.clientSegmentId,
        write.payload,
        this.now(),
        existing,
      )
      await store.put(record)
      records.push(record)
    }
    await tx.done
    return records
  }

  async getCloudTranscriptOutboxPage<TPayload = unknown>(
    sessionId: string,
    options: CloudTranscriptOutboxPageOptions = {},
  ): Promise<CloudTranscriptOutboxPage<TPayload>> {
    const ownerId = this.currentOwnerId()
    if (ownerId === null || !await this.getSessionMetadata(sessionId)) {
      return { items: [], hasMore: false }
    }
    const db = await this.databasePromise
    const limit = normalizeLimit(options.limit)
    const index = db
      .transaction(SESSION_STORES.cloudTranscriptOutbox)
      .store.index('by-owner-session-created-at')
    const query = options.after
      ? IDBKeyRange.bound(
          [ownerId, sessionId, options.after.createdAt, options.after.clientSegmentId],
          [ownerId, sessionId, []],
          true,
        )
      : IDBKeyRange.bound(
          [ownerId, sessionId],
          [ownerId, sessionId, []],
        )
    let cursor = await index.openCursor(query)
    const items: Array<CloudTranscriptOutboxRecord<TPayload>> = []
    while (cursor && items.length < limit + 1) {
      items.push(cursor.value as CloudTranscriptOutboxRecord<TPayload>)
      cursor = await cursor.continue()
    }
    const hasMore = items.length > limit
    if (hasMore) items.pop()
    const last = items.at(-1)
    return {
      items,
      hasMore,
      ...(hasMore && last
        ? {
            nextCursor: {
              createdAt: last.createdAt,
              clientSegmentId: last.clientSegmentId,
            },
          }
        : {}),
    }
  }

  async acknowledgeCloudTranscriptOutbox(
    records: readonly Pick<
      CloudTranscriptOutboxRecord,
      'ownerId' | 'sessionId' | 'clientSegmentId' | 'updatedAt'
    >[],
  ): Promise<void> {
    if (records.length === 0) return
    const ownerId = this.currentOwnerId()
    if (ownerId === null) return
    const db = await this.databasePromise
    const tx = db.transaction(SESSION_STORES.cloudTranscriptOutbox, 'readwrite')
    for (const expected of records) {
      if (expected.ownerId !== ownerId) continue
      const key: [string, string, string] = [
        ownerId,
        expected.sessionId,
        expected.clientSegmentId,
      ]
      const current = await tx.store.get(key)
      if (current?.updatedAt === expected.updatedAt) {
        await tx.store.delete(key)
      }
    }
    await tx.done
  }

  async deleteSession(id: string): Promise<void> {
    const db = await this.databasePromise
    const ownerId = this.currentOwnerId()
    const tx = db.transaction(
      [
        SESSION_STORES.metadata,
        SESSION_STORES.transcripts,
        SESSION_STORES.translations,
        SESSION_STORES.audioChunks,
        SESSION_STORES.legacySessions,
        SESSION_STORES.cloudTranscriptOutbox,
      ],
      'readwrite',
    )
    const metadata = await tx.objectStore(SESSION_STORES.metadata).get(id)
    // Deletion is idempotent for a session that has never had a local cache
    // (a normal case for cloud-only history). A known ID owned by another
    // account still fails closed below.
    if (!metadata) {
      await tx.done
      return
    }
    this.requireOwnedMetadata(metadata, id, ownerId)
    const range = IDBKeyRange.bound([id, 0], [id, MAX_SEQUENCE])
    const deletions: Array<Promise<unknown>> = [
      tx.objectStore(SESSION_STORES.metadata).delete(id),
      tx.objectStore(SESSION_STORES.transcripts).delete(range),
      tx.objectStore(SESSION_STORES.translations).delete(range),
      tx.objectStore(SESSION_STORES.audioChunks).delete(range),
    ]
    if (ownerId === null) {
      deletions.push(tx.objectStore(SESSION_STORES.legacySessions).delete(id))
    } else {
      deletions.push(
        tx.objectStore(SESSION_STORES.cloudTranscriptOutbox).delete(
          IDBKeyRange.bound(
            [ownerId, id, ''],
            [ownerId, id, []],
          ),
        ),
      )
    }
    await Promise.all(deletions)
    await tx.done
  }

  async migrateLegacySessions(
    mapper: (
      value: unknown,
      key: string,
    ) => LegacySessionImport<TTranscript, TTranslation> | null,
  ): Promise<LegacyMigrationResult> {
    const ownerId = this.currentOwnerId()
    if (ownerId !== null) {
      throw new Error('Legacy browser sessions belong to the anonymous owner scope')
    }
    const db = await this.databasePromise
    const tx = db.transaction(
      [
        SESSION_STORES.legacySessions,
        SESSION_STORES.metadata,
        SESSION_STORES.transcripts,
        SESSION_STORES.translations,
        SESSION_STORES.audioChunks,
      ],
      'readwrite',
    )
    const legacyStore = tx.objectStore(SESSION_STORES.legacySessions)
    const metadataStore = tx.objectStore(SESSION_STORES.metadata)
    const transcriptStore = tx.objectStore(SESSION_STORES.transcripts)
    const translationStore = tx.objectStore(SESSION_STORES.translations)
    const audioStore = tx.objectStore(SESSION_STORES.audioChunks)
    let migrated = 0
    let skipped = 0
    let cursor = await legacyStore.openCursor()

    while (cursor) {
      let imported: LegacySessionImport<TTranscript, TTranslation> | null
      try {
        imported = mapper(cursor.value, String(cursor.key))
      } catch {
        skipped += 1
        cursor = await cursor.continue()
        continue
      }

      if (!imported) {
        skipped += 1
        cursor = await cursor.continue()
        continue
      }

      const existing = await metadataStore.get(imported.id)
      if (existing && !metadataMatchesOwner(existing, ownerId)) {
        skipped += 1
        cursor = await cursor.continue()
        continue
      }
      if (!existing) {
        const transcripts = imported.transcripts ?? []
        const translations = imported.translations ?? []
        const audioChunks = imported.audioChunks ?? []
        const createdAt = imported.createdAt
        const updatedAt = imported.updatedAt ?? createdAt
        let audioBytes = 0

        for (let sequence = 0; sequence < transcripts.length; sequence += 1) {
          await transcriptStore.put({
            sessionId: imported.id,
            sequence,
            recordId: `${imported.id}:legacy-transcript:${sequence}`,
            data: transcripts[sequence],
            createdAt,
            updatedAt,
          })
        }
        for (let sequence = 0; sequence < translations.length; sequence += 1) {
          await translationStore.put({
            sessionId: imported.id,
            sequence,
            recordId: `${imported.id}:legacy-translation:${sequence}`,
            data: translations[sequence],
            createdAt,
            updatedAt,
          })
        }
        for (let sequence = 0; sequence < audioChunks.length; sequence += 1) {
          const blob = audioChunks[sequence]
          if (!blob) continue
          audioBytes += blob.size
          await audioStore.put({
            sessionId: imported.id,
            sequence,
            blob,
            mimeType: blob.type || imported.audioMimeType || 'application/octet-stream',
            byteLength: blob.size,
            capturedAt: updatedAt,
          })
        }

        await metadataStore.put({
          ...createMetadata(
            {
              id: imported.id,
              createdAt,
              updatedAt,
              status: imported.status ?? 'completed',
              ...(imported.completedAt !== undefined
                ? { completedAt: imported.completedAt }
                : {}),
              ...(imported.title !== undefined ? { title: imported.title } : {}),
              ...(imported.summary !== undefined ? { summary: imported.summary } : {}),
              ...(imported.durationMs !== undefined
                ? { durationMs: imported.durationMs }
                : {}),
              ...(imported.audioMimeType !== undefined
                ? { audioMimeType: imported.audioMimeType }
                : {}),
              localAudioIncomplete: imported.localAudioIncomplete === true,
            },
            createdAt,
            ownerId,
          ),
          audioBytes,
          audioChunkCount: audioChunks.length,
          transcriptCount: transcripts.length,
          translationCount: translations.length,
          nextAudioSequence: audioChunks.length,
          nextTranscriptSequence: transcripts.length,
          nextTranslationSequence: translations.length,
        })
      }

      await cursor.delete()
      migrated += 1
      cursor = await cursor.continue()
    }

    await tx.done
    return { migrated, skipped }
  }

  async getLegacySession(id: string): Promise<unknown> {
    if (this.currentOwnerId() !== null) return undefined
    const db = await this.databasePromise
    return db.get(SESSION_STORES.legacySessions, id)
  }

  /**
   * Count legacy records without materializing their values. In particular,
   * this does not clone or read an embedded legacy audio Blob.
   */
  async countLegacySessions(): Promise<number> {
    if (this.currentOwnerId() !== null) return 0
    const db = await this.databasePromise
    return db.count(SESSION_STORES.legacySessions)
  }

  private async appendRecord<T>(
    storeName: RecordStoreName,
    sessionId: string,
    data: T,
    options: AppendRecordOptions,
  ): Promise<SequencedSessionRecord<T>> {
    const db = await this.databasePromise
    const tx = db.transaction(
      [SESSION_STORES.metadata, storeName],
      'readwrite',
    )
    const metadataStore = tx.objectStore(SESSION_STORES.metadata)
    const recordStore = tx.objectStore(storeName)
    const now = options.timestamp ?? this.now()
    const ownerId = this.currentOwnerId()
    let metadata = await metadataStore.get(sessionId)
    if (!metadata) metadata = createMetadata({ id: sessionId }, now, ownerId)
    else metadata = this.requireOwnedMetadata(metadata, sessionId, ownerId)
    const isTranscript = storeName === SESSION_STORES.transcripts
    const nextSequence = isTranscript
      ? metadata.nextTranscriptSequence
      : metadata.nextTranslationSequence
    const sequence = options.sequence ?? nextSequence
    validateSequence(sequence)
    const record: SequencedSessionRecord<T> = {
      sessionId,
      sequence,
      recordId:
        options.recordId ??
        `${sessionId}:${isTranscript ? 'transcript' : 'translation'}:${sequence}`,
      data,
      createdAt: now,
      updatedAt: now,
    }
    await recordStore.add(record)
    metadata = {
      ...metadata,
      updatedAt: now,
      ...(isTranscript
        ? {
            transcriptCount: metadata.transcriptCount + 1,
            nextTranscriptSequence: Math.max(
              metadata.nextTranscriptSequence,
              sequence + 1,
            ),
          }
        : {
            translationCount: metadata.translationCount + 1,
            nextTranslationSequence: Math.max(
              metadata.nextTranslationSequence,
              sequence + 1,
            ),
          }),
    }
    await metadataStore.put(metadata)
    await tx.done
    return record
  }

  private async upsertRecord<T>(
    storeName: RecordStoreName,
    sessionId: string,
    recordId: string,
    data: T,
  ): Promise<SequencedSessionRecord<T>> {
    if (!recordId) throw new TypeError('recordId is required')
    const db = await this.databasePromise
    const tx = db.transaction(
      [SESSION_STORES.metadata, storeName],
      'readwrite',
    )
    const metadataStore = tx.objectStore(SESSION_STORES.metadata)
    const recordStore = tx.objectStore(storeName)
    const index = recordStore.index('by-session-record-id')
    const now = this.now()
    const ownerId = this.currentOwnerId()
    let metadata = await metadataStore.get(sessionId)
    if (!metadata) metadata = createMetadata({ id: sessionId }, now, ownerId)
    else metadata = this.requireOwnedMetadata(metadata, sessionId, ownerId)
    const isTranscript = storeName === SESSION_STORES.transcripts
    const existing = await index.get([sessionId, recordId])
    const sequence = existing
      ? existing.sequence
      : isTranscript
        ? metadata.nextTranscriptSequence
        : metadata.nextTranslationSequence
    const record: SequencedSessionRecord<T> = {
      sessionId,
      sequence,
      recordId,
      data,
      createdAt: existing?.createdAt ?? now,
      updatedAt: now,
    }
    await recordStore.put(record)
    metadata = {
      ...metadata,
      updatedAt: now,
      ...(isTranscript
        ? {
            transcriptCount:
              metadata.transcriptCount + (existing ? 0 : 1),
            nextTranscriptSequence: Math.max(
              metadata.nextTranscriptSequence,
              sequence + 1,
            ),
          }
        : {
            translationCount:
              metadata.translationCount + (existing ? 0 : 1),
            nextTranslationSequence: Math.max(
              metadata.nextTranslationSequence,
              sequence + 1,
            ),
          }),
    }
    await metadataStore.put(metadata)
    await tx.done
    return record
  }

  private async writeRecords<T>(
    storeName: RecordStoreName,
    sessionId: string,
    records: readonly RecordWrite<T>[],
    truncateAfter?: number,
  ): Promise<void> {
    if (truncateAfter !== undefined) validateSequence(truncateAfter, 'truncateAfter')
    for (const record of records) validateSequence(record.sequence)
    if (
      truncateAfter !== undefined &&
      records.some((record) => record.sequence >= truncateAfter)
    ) {
      throw new RangeError('Record sequence must be below truncateAfter')
    }
    const db = await this.databasePromise
    const tx = db.transaction(
      [SESSION_STORES.metadata, storeName],
      'readwrite',
    )
    const metadataStore = tx.objectStore(SESSION_STORES.metadata)
    const recordStore = tx.objectStore(storeName)
    const now = this.now()
    const ownerId = this.currentOwnerId()
    let metadata = await metadataStore.get(sessionId)
    if (!metadata) metadata = createMetadata({ id: sessionId }, now, ownerId)
    else metadata = this.requireOwnedMetadata(metadata, sessionId, ownerId)
    const isTranscript = storeName === SESSION_STORES.transcripts
    const sessionRange = IDBKeyRange.bound(
      [sessionId, 0],
      [sessionId, MAX_SEQUENCE],
    )
    const mode = recordWriteMode(records, truncateAfter)
    let replacementExisting: Array<
      SequencedSessionRecord<unknown> | undefined
    > | undefined

    if (mode === 'replace-all') {
      // Read timestamps/implicit record ids before clearing. Clearing first
      // releases every unique record-id index entry, so records can safely
      // exchange sequence positions within this same transaction.
      replacementExisting = []
      for (const write of records) {
        replacementExisting.push(
          await recordStore.get([sessionId, write.sequence]),
        )
      }
      await recordStore.delete(sessionRange)
    } else if (truncateAfter !== undefined) {
      await recordStore.delete(
        IDBKeyRange.bound(
          [sessionId, truncateAfter],
          [sessionId, MAX_SEQUENCE],
        ),
      )
    }

    for (let index = 0; index < records.length; index += 1) {
      const write = records[index]
      if (!write) continue
      const existing = replacementExisting
        ? replacementExisting[index]
        : await recordStore.get([sessionId, write.sequence])
      const timestamp = write.timestamp ?? now
      await recordStore.put({
        sessionId,
        sequence: write.sequence,
        recordId:
          write.recordId ??
          existing?.recordId ??
          `${sessionId}:${isTranscript ? 'transcript' : 'translation'}:${write.sequence}`,
        data: write.data,
        createdAt: existing?.createdAt ?? timestamp,
        updatedAt: timestamp,
      })
    }

    const sequenceIndex = recordStore.index('by-session-sequence')
    const recordCount = await sequenceIndex.count(sessionRange)
    const lastRecord = await sequenceIndex.openCursor(sessionRange, 'prev')
    const nextSequence = lastRecord ? lastRecord.value.sequence + 1 : 0
    metadata = {
      ...metadata,
      updatedAt: now,
      ...(isTranscript
        ? {
            transcriptCount: recordCount,
            nextTranscriptSequence: nextSequence,
          }
        : {
            translationCount: recordCount,
            nextTranslationSequence: nextSequence,
          }),
    }
    await metadataStore.put(metadata)
    await tx.done
  }

  private async readRecordPage<T>(
    storeName: RecordStoreName,
    sessionId: string,
    options: SequencePageOptions,
  ): Promise<SequencePage<SequencedSessionRecord<T>>> {
    if (!await this.getSessionMetadata(sessionId)) {
      return { items: [], hasMore: false }
    }
    const db = await this.databasePromise
    const limit = normalizeLimit(options.limit)
    const range = sessionSequenceRange(sessionId, options)
    if (!range) return { items: [], hasMore: false }
    const direction = options.direction === 'backward' ? 'prev' : 'next'
    const index = db
      .transaction(storeName)
      .store.index('by-session-sequence')
    let cursor = await index.openCursor(range, direction)
    const items: SequencedSessionRecord<T>[] = []
    while (cursor && items.length < limit + 1) {
      items.push(cursor.value as SequencedSessionRecord<T>)
      cursor = await cursor.continue()
    }
    return this.createSequencePage(items, limit)
  }

  private createSequencePage<T extends { sequence: number }>(
    items: T[],
    limit: number,
  ): SequencePage<T> {
    const hasMore = items.length > limit
    if (hasMore) items.pop()
    const last = items.at(-1)
    return {
      items,
      hasMore,
      ...(hasMore && last ? { nextSequence: last.sequence } : {}),
    }
  }
}
