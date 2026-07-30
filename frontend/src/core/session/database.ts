import {
  openDB,
  unwrap,
  type DBSchema,
  type IDBPDatabase,
  type IDBPObjectStore,
  type IDBPTransaction,
} from 'idb'
import type {
  AudioChunkRecord,
  CloudTranscriptOutboxRecord,
  SequencedSessionRecord,
  SessionMetadata,
  SessionOrigin,
  SessionOwnerId,
} from './types'

export const SESSION_DATABASE_NAME = 'dreamtrans-db'
export const SESSION_DATABASE_VERSION = 3

const ANONYMOUS_OWNER_KEY = 'anonymous'
const ACCOUNT_OWNER_KEY_PREFIX = 'account:'

export const SESSION_STORES = {
  legacySessions: 'sessions',
  metadata: 'session-metadata',
  transcripts: 'transcript-records',
  translations: 'translation-records',
  audioChunks: 'audio-chunks',
  cloudTranscriptOutbox: 'cloud-transcript-outbox',
} as const

export interface StoredSessionMetadata extends SessionMetadata {
  /**
   * IndexedDB cannot index null, so the public nullable ownerId is mirrored to
   * a collision-safe, non-null internal key.
   */
  ownerKey: string
}

export function normalizeSessionOwnerId(
  ownerId: SessionOwnerId | undefined,
): SessionOwnerId {
  if (ownerId === null || ownerId === undefined) return null
  const normalized = ownerId.trim()
  if (!normalized) throw new TypeError('Session owner id cannot be blank')
  return normalized
}

export function sessionOwnerKey(ownerId: SessionOwnerId | undefined): string {
  const normalized = normalizeSessionOwnerId(ownerId)
  return normalized === null
    ? ANONYMOUS_OWNER_KEY
    : `${ACCOUNT_OWNER_KEY_PREFIX}${normalized}`
}

function normalizedSessionOrigin(origin: unknown): SessionOrigin {
  return origin === 'cloud' ? 'cloud' : 'local'
}

/**
 * v2 metadata did not carry owner/origin fields. Treating it as anonymous and
 * local is deliberately conservative: no future signed-in user inherits data
 * that cannot be attributed safely.
 */
export function normalizeStoredSessionMetadata(
  metadata: SessionMetadata,
): StoredSessionMetadata {
  const rawOwnerId = (metadata as Partial<SessionMetadata>).ownerId
  const ownerId =
    typeof rawOwnerId === 'string' && rawOwnerId.trim()
      ? rawOwnerId.trim()
      : null
  const origin = normalizedSessionOrigin(
    (metadata as Partial<SessionMetadata>).origin,
  )
  const localAudioIncomplete =
    (metadata as Partial<SessionMetadata>).localAudioIncomplete === true
  const cloudSessionPending =
    origin === 'cloud'
    && (metadata as Partial<SessionMetadata>).cloudSessionPending === true
  return {
    ...metadata,
    ownerId,
    ownerKey: sessionOwnerKey(ownerId),
    origin,
    cloudSessionPending,
    localAudioIncomplete,
  }
}

export function publicSessionMetadata(
  metadata: StoredSessionMetadata,
): SessionMetadata {
  const normalized = normalizeStoredSessionMetadata(metadata)
  const publicMetadata: SessionMetadata & { ownerKey?: string } = { ...normalized }
  delete publicMetadata.ownerKey
  return publicMetadata
}

export interface DreamTransSessionDatabase extends DBSchema {
  sessions: {
    key: string
    value: unknown
  }
  'session-metadata': {
    key: string
    value: StoredSessionMetadata
    indexes: {
      'by-updated-at': [number, string]
      'by-owner-updated-at': [string, number, string]
    }
  }
  'transcript-records': {
    key: [string, number]
    value: SequencedSessionRecord<unknown>
    indexes: {
      'by-session-sequence': [string, number]
      'by-session-record-id': [string, string]
    }
  }
  'translation-records': {
    key: [string, number]
    value: SequencedSessionRecord<unknown>
    indexes: {
      'by-session-sequence': [string, number]
      'by-session-record-id': [string, string]
    }
  }
  'audio-chunks': {
    key: [string, number]
    value: AudioChunkRecord
    indexes: {
      'by-session-sequence': [string, number]
    }
  }
  'cloud-transcript-outbox': {
    key: [string, string, string]
    value: CloudTranscriptOutboxRecord
    indexes: {
      'by-owner-session-created-at': [string, string, number, string]
    }
  }
}

const databasePromises = new Map<string, Promise<IDBPDatabase<DreamTransSessionDatabase>>>()

type SessionStoreName =
  (typeof SESSION_STORES)[keyof typeof SESSION_STORES]

type UpgradeTransaction = IDBPTransaction<
  DreamTransSessionDatabase,
  ArrayLike<SessionStoreName>,
  'versionchange'
>

type UpgradeMetadataStore = IDBPObjectStore<
  DreamTransSessionDatabase,
  ArrayLike<SessionStoreName>,
  typeof SESSION_STORES.metadata,
  'versionchange'
>

function abortUpgrade(transaction: UpgradeTransaction): void {
  try {
    unwrap(transaction).abort()
  } catch {
    // The original request error remains authoritative if the transaction has
    // already become inactive.
  }
}

/**
 * Walk only the small metadata store during v2 -> v3. Audio chunks and legacy
 * session values are intentionally not opened, so their Blob payloads are not
 * cloned into memory during database upgrade.
 */
function migrateMetadataOwnership(
  store: UpgradeMetadataStore,
  transaction: UpgradeTransaction,
): void {
  const request = unwrap(store).openCursor()
  request.onerror = () => abortUpgrade(transaction)
  request.onsuccess = () => {
    const cursor = request.result
    if (!cursor) return
    try {
      const current = cursor.value as SessionMetadata
      const normalized = normalizeStoredSessionMetadata(current)
      const currentOwnerId = (current as Partial<SessionMetadata>).ownerId
      const currentOrigin = (current as Partial<SessionMetadata>).origin
      const currentOwnerKey = (current as Partial<StoredSessionMetadata>).ownerKey
      if (
        currentOwnerId !== normalized.ownerId
        || currentOrigin !== normalized.origin
        || currentOwnerKey !== normalized.ownerKey
      ) {
        const update = cursor.update(normalized)
        update.onerror = () => abortUpgrade(transaction)
        update.onsuccess = () => cursor.continue()
        return
      }
      cursor.continue()
    } catch {
      abortUpgrade(transaction)
    }
  }
}

function createStores(
  db: IDBPDatabase<DreamTransSessionDatabase>,
  transaction: UpgradeTransaction,
  oldVersion: number,
): void {
  if (!db.objectStoreNames.contains(SESSION_STORES.legacySessions)) {
    db.createObjectStore(SESSION_STORES.legacySessions)
  }

  let metadata: UpgradeMetadataStore
  if (!db.objectStoreNames.contains(SESSION_STORES.metadata)) {
    metadata = db.createObjectStore(SESSION_STORES.metadata, { keyPath: 'id' })
    metadata.createIndex('by-updated-at', ['updatedAt', 'id'])
    metadata.createIndex('by-owner-updated-at', ['ownerKey', 'updatedAt', 'id'])
  } else {
    metadata = transaction.objectStore(SESSION_STORES.metadata)
    if (!metadata.indexNames.contains('by-owner-updated-at')) {
      metadata.createIndex('by-owner-updated-at', ['ownerKey', 'updatedAt', 'id'])
    }
  }

  if (!db.objectStoreNames.contains(SESSION_STORES.transcripts)) {
    const transcripts = db.createObjectStore(SESSION_STORES.transcripts, {
      keyPath: ['sessionId', 'sequence'],
    })
    transcripts.createIndex('by-session-sequence', ['sessionId', 'sequence'], { unique: true })
    transcripts.createIndex('by-session-record-id', ['sessionId', 'recordId'], { unique: true })
  }

  if (!db.objectStoreNames.contains(SESSION_STORES.translations)) {
    const translations = db.createObjectStore(SESSION_STORES.translations, {
      keyPath: ['sessionId', 'sequence'],
    })
    translations.createIndex('by-session-sequence', ['sessionId', 'sequence'], { unique: true })
    translations.createIndex('by-session-record-id', ['sessionId', 'recordId'], { unique: true })
  }

  if (!db.objectStoreNames.contains(SESSION_STORES.audioChunks)) {
    const audioChunks = db.createObjectStore(SESSION_STORES.audioChunks, {
      keyPath: ['sessionId', 'sequence'],
    })
    audioChunks.createIndex('by-session-sequence', ['sessionId', 'sequence'], { unique: true })
  }

  if (!db.objectStoreNames.contains(SESSION_STORES.cloudTranscriptOutbox)) {
    const outbox = db.createObjectStore(SESSION_STORES.cloudTranscriptOutbox, {
      keyPath: ['ownerId', 'sessionId', 'clientSegmentId'],
    })
    outbox.createIndex(
      'by-owner-session-created-at',
      ['ownerId', 'sessionId', 'createdAt', 'clientSegmentId'],
      { unique: true },
    )
  }

  if (oldVersion > 0 && oldVersion < SESSION_DATABASE_VERSION) {
    migrateMetadataOwnership(metadata, transaction)
  }
}

export function openSessionDatabase(
  databaseName = SESSION_DATABASE_NAME,
): Promise<IDBPDatabase<DreamTransSessionDatabase>> {
  const existing = databasePromises.get(databaseName)
  if (existing) return existing

  const promise = openDB<DreamTransSessionDatabase>(
    databaseName,
    SESSION_DATABASE_VERSION,
    {
      upgrade(db, oldVersion, _newVersion, transaction) {
        createStores(db, transaction, oldVersion)
      },
      blocking(_currentVersion, _blockedVersion, event) {
        const database = event.target
        if (database instanceof IDBDatabase) database.close()
      },
    },
  )

  databasePromises.set(databaseName, promise)
  void promise.catch(() => {
    databasePromises.delete(databaseName)
  })
  return promise
}
