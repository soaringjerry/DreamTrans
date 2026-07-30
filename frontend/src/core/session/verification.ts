import 'fake-indexeddb/auto'
import {
  IndexedDbSessionRepository,
  assertSerializableOutboxPayload,
  buildCloudTranscriptOutboxRecord,
} from './IndexedDbSessionRepository'
import {
  normalizeStoredSessionMetadata,
  openSessionDatabase,
  SESSION_STORES,
  sessionOwnerKey,
} from './database'
import { recordWriteMode } from './writePlan'

interface MemoryRecord {
  sequence: number
  recordId: string
  value: string
}

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(`Session repository verification failed: ${message}`)
}

function applyWritePlan(
  initial: readonly MemoryRecord[],
  writes: readonly MemoryRecord[],
  truncateAfter?: number,
): MemoryRecord[] {
  const bySequence = new Map(initial.map((record) => [record.sequence, record]))
  const mode = recordWriteMode(writes, truncateAfter)

  if (mode === 'replace-all') {
    bySequence.clear()
  } else if (truncateAfter !== undefined) {
    for (const sequence of bySequence.keys()) {
      if (sequence >= truncateAfter) bySequence.delete(sequence)
    }
  }

  for (const write of writes) {
    for (const [sequence, existing] of bySequence) {
      if (sequence !== write.sequence && existing.recordId === write.recordId) {
        throw new Error(`Unique record id collision: ${write.recordId}`)
      }
    }
    bySequence.set(write.sequence, write)
  }
  return [...bySequence.values()].sort((left, right) => left.sequence - right.sequence)
}

const swapped = applyWritePlan(
  [
    { sequence: 0, recordId: 'a', value: 'A' },
    { sequence: 1, recordId: 'b', value: 'B' },
  ],
  [
    { sequence: 0, recordId: 'b', value: 'B moved' },
    { sequence: 1, recordId: 'a', value: 'A moved' },
  ],
  2,
)
assert(swapped[0]?.recordId === 'b', 'bulk replacement permits record-id sequence swaps')
assert(swapped[1]?.recordId === 'a', 'bulk replacement preserves the complete replacement')

const sparse = applyWritePlan(
  [
    { sequence: 0, recordId: 'a', value: 'A' },
    { sequence: 1, recordId: 'b', value: 'B' },
    { sequence: 2, recordId: 'c', value: 'C' },
  ],
  [{ sequence: 1, recordId: 'b', value: 'B updated' }],
  2,
)
assert(sparse.length === 2, 'sparse truncate removes only the stale tail')
assert(sparse[0]?.recordId === 'a', 'sparse truncate preserves untouched prefix records')
assert(sparse[1]?.value === 'B updated', 'sparse truncate applies changed records')

assert(
  recordWriteMode(
    [{ sequence: 1 }, { sequence: 0 }],
    2,
  ) === 'replace-all',
  'unordered dense snapshots are complete replacements',
)
assert(
  recordWriteMode(
    [{ sequence: 0 }, { sequence: 0 }],
    2,
  ) === 'truncate-tail',
  'duplicate sequences are not mistaken for complete replacements',
)
assert(
  recordWriteMode([], 0) === 'replace-all',
  'an empty dense snapshot clears the session',
)

const legacyMetadata = normalizeStoredSessionMetadata({
  id: 'legacy',
  createdAt: 1,
  updatedAt: 1,
  status: 'completed',
  audioBytes: 0,
  audioChunkCount: 0,
  transcriptCount: 0,
  translationCount: 0,
  nextAudioSequence: 0,
  nextTranscriptSequence: 0,
  nextTranslationSequence: 0,
} as never)
assert(legacyMetadata.ownerId === null, 'v2 metadata migrates to anonymous ownership')
assert(legacyMetadata.origin === 'local', 'v2 metadata migrates to local origin')
assert(
  legacyMetadata.localAudioIncomplete === false,
  'legacy metadata defaults to a non-failed local-audio state',
)
assert(
  sessionOwnerKey(null) !== sessionOwnerKey('anonymous'),
  'anonymous and an account literally named anonymous cannot collide',
)

const firstOutbox = buildCloudTranscriptOutboxRecord(
  'user-a',
  'session-a',
  'segment-a',
  { text: 'first' },
  10,
)
const updatedOutbox = buildCloudTranscriptOutboxRecord(
  'user-a',
  'session-a',
  'segment-a',
  { text: 'translated' },
  10,
  firstOutbox,
)
assert(
  updatedOutbox.updatedAt > firstOutbox.updatedAt,
  'outbox versions remain monotonic within one clock tick',
)
let rejectedBlob = false
try {
  assertSerializableOutboxPayload({ audio: new Blob(['secret']) })
} catch {
  rejectedBlob = true
}
assert(rejectedBlob, 'outbox rejects Blob payloads')

const databaseName = `dreamtrans-session-verification-${crypto.randomUUID()}`
let activeOwner: string | null = 'user-a'
const now = 100
const repository = new IndexedDbSessionRepository<string, string>({
  databaseName,
  now: () => now,
  ownerId: () => activeOwner,
})

await repository.ensureSession('shared-id', {
  origin: 'cloud',
  title: 'User A session',
})
await repository.appendTranscript('shared-id', 'private-a', {
  recordId: 'segment-a',
})
await repository.appendAudioChunk(
  'shared-id',
  new Blob(['account-a-audio'], { type: 'audio/mpeg' }),
  { mimeType: 'audio/mpeg' },
)
const incompleteMetadata = await repository.markLocalAudioIncomplete('shared-id')
assert(
  incompleteMetadata.localAudioIncomplete,
  'local audio failure is persisted in session metadata',
)
assert(
  incompleteMetadata.updatedAt === now,
  'marking local audio incomplete does not reorder the session',
)
const stickyIncompleteMetadata = await repository.updateSessionMetadata(
  'shared-id',
  { localAudioIncomplete: false },
)
assert(
  stickyIncompleteMetadata.localAudioIncomplete,
  'local audio incompleteness cannot be cleared accidentally',
)
const aOutboxV1 = await repository.upsertCloudTranscriptOutbox(
  'shared-id',
  'segment-a',
  { client_segment_id: 'segment-a', text: 'first', start_time: 0 },
)
const aOutboxV2 = await repository.upsertCloudTranscriptOutbox(
  'shared-id',
  'segment-a',
  { client_segment_id: 'segment-a', text: 'translated', start_time: 0 },
)
await repository.acknowledgeCloudTranscriptOutbox([aOutboxV1])
assert(
  (await repository.getCloudTranscriptOutboxPage('shared-id')).items.length === 1,
  'a stale acknowledgement cannot delete a newer durable outbox update',
)
await repository.acknowledgeCloudTranscriptOutbox([aOutboxV2])
assert(
  (await repository.getCloudTranscriptOutboxPage('shared-id')).items.length === 0,
  'the matching durable outbox version is acknowledged',
)

activeOwner = 'user-b'
assert(
  await repository.getSessionMetadata('shared-id') === undefined,
  'another owner cannot read metadata by a known session id',
)
assert(
  (await repository.getTranscriptPage('shared-id')).items.length === 0,
  'another owner cannot read transcript records by a known session id',
)
assert(
  (await repository.getAudioChunkPage('shared-id')).items.length === 0,
  'another owner cannot read audio chunks by a known session id',
)
let rejectedForeignCollision = false
try {
  await repository.ensureSession('shared-id', {
    origin: 'cloud',
    title: 'Must not overwrite A',
  })
} catch {
  rejectedForeignCollision = true
}
assert(
  rejectedForeignCollision,
  'a cross-owner session-id collision fails closed instead of changing ownership',
)
let rejectedForeignDelete = false
try {
  await repository.deleteSession('shared-id')
} catch {
  rejectedForeignDelete = true
}
assert(
  rejectedForeignDelete,
  'deleting a known foreign session id fails closed',
)
await repository.deleteSession('cloud-only-without-local-cache')
assert(
  await repository.getSessionMetadata('cloud-only-without-local-cache') === undefined,
  'deleting a cloud-only session without local metadata is idempotent',
)

await repository.ensureSession('user-b-session', {
  origin: 'cloud',
  title: 'User B session',
})
await repository.appendTranscript('user-b-session', 'private-b', {
  recordId: 'segment-b',
})
assert(
  (await repository.listSessions()).items.every((session) => session.ownerId === 'user-b'),
  'owner-scoped history lists only the active account',
)

const rawDatabase = await openSessionDatabase(databaseName)
await rawDatabase.put(SESSION_STORES.legacySessions, { title: 'anonymous only' }, 'legacy')
assert(
  await repository.getLegacySession('legacy') === undefined,
  'authenticated owners cannot read anonymous legacy values',
)
activeOwner = null
assert(
  (await repository.getLegacySession('legacy') as { title?: string })?.title === 'anonymous only',
  'the anonymous owner can explicitly read its legacy value',
)

activeOwner = 'user-a'
assert(
  (await repository.getTranscriptPage('shared-id')).items[0]?.data === 'private-a',
  'switching back restores only the original owner data',
)
assert(
  (await repository.listSessions()).items.every((session) => session.ownerId === 'user-a'),
  'history remains isolated after repeated owner switches',
)

console.log(JSON.stringify({
  indexedDbOwnerIsolation: true,
  bulkSwap: swapped.map((record) => record.recordId),
  sparsePrefix: sparse.map((record) => record.recordId),
  status: 'ok',
}))
