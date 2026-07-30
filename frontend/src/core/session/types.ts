export type SessionStatus = 'active' | 'completed'
export type SessionOwnerId = string | null
export type SessionOrigin = 'local' | 'cloud'

export interface SessionMetadata {
  id: string
  /**
   * The authenticated user that owns this browser-local cache entry.
   * Null is the explicit anonymous scope; it never means "all users".
   */
  ownerId: SessionOwnerId
  /**
   * Whether this is a local-only session or a cache of a cloud session.
   */
  origin: SessionOrigin
  /**
   * A cloud create response was lost or never arrived. While true, transcript
   * outbox entries stay local until the deterministic session id is verified
   * or recreated, preventing a weak-network 404 retry storm.
   */
  cloudSessionPending: boolean
  /** Session language choices are frozen so weak-network recreation is exact. */
  sourceLanguage?: string
  targetLanguage?: string
  createdAt: number
  updatedAt: number
  status: SessionStatus
  completedAt?: number
  title?: string
  summary?: string
  durationMs?: number
  audioMimeType?: string
  audioBytes: number
  audioChunkCount: number
  /**
   * True once local capture or persistence has failed. This is intentionally
   * sticky so later downloads cannot be presented as a complete recording.
   */
  localAudioIncomplete: boolean
  transcriptCount: number
  translationCount: number
  nextAudioSequence: number
  nextTranscriptSequence: number
  nextTranslationSequence: number
}

export interface CreateSessionInput {
  id: string
  ownerId?: SessionOwnerId
  origin?: SessionOrigin
  cloudSessionPending?: boolean
  sourceLanguage?: string
  targetLanguage?: string
  createdAt?: number
  updatedAt?: number
  status?: SessionStatus
  completedAt?: number
  title?: string
  summary?: string
  durationMs?: number
  audioMimeType?: string
  localAudioIncomplete?: boolean
}

export type SessionMetadataPatch = Partial<
  Pick<
    SessionMetadata,
    | 'title'
    | 'summary'
    | 'durationMs'
    | 'audioMimeType'
    | 'cloudSessionPending'
    | 'localAudioIncomplete'
    | 'sourceLanguage'
    | 'status'
    | 'targetLanguage'
    | 'completedAt'
  >
>

export interface UpdateSessionMetadataOptions {
  /**
   * Set false for cached labels/summaries that should not reorder history.
   */
  touch?: boolean
  updatedAt?: number
}

export interface SequencedSessionRecord<T> {
  sessionId: string
  sequence: number
  recordId: string
  data: T
  createdAt: number
  updatedAt: number
}

export interface AudioChunkRecord {
  sessionId: string
  sequence: number
  blob: Blob
  mimeType: string
  byteLength: number
  capturedAt: number
  durationMs?: number
}

export interface AppendRecordOptions {
  sequence?: number
  recordId?: string
  timestamp?: number
}

export interface AppendAudioChunkOptions {
  sequence?: number
  capturedAt?: number
  durationMs?: number
  mimeType?: string
}

export interface SequencePageOptions {
  /**
   * Exclusive cursor. Use the page's nextSequence to request the next page.
   */
  afterSequence?: number
  /**
   * Exclusive cursor for reverse pagination.
   */
  beforeSequence?: number
  direction?: 'forward' | 'backward'
  limit?: number
}

export interface SequencePage<T> {
  items: T[]
  hasMore: boolean
  nextSequence?: number
}

export interface SessionListCursor {
  updatedAt: number
  id: string
}

export interface SessionListPageOptions {
  /**
   * Exclusive descending cursor returned by the previous page.
   */
  before?: SessionListCursor
  /**
   * Optionally restrict this owner-scoped page to local or cloud cache entries.
   */
  origins?: readonly SessionOrigin[]
  limit?: number
}

export interface SessionListPage {
  items: SessionMetadata[]
  hasMore: boolean
  nextCursor?: SessionListCursor
}

export interface RecordWrite<T> {
  sequence: number
  data: T
  recordId?: string
  timestamp?: number
}

export interface LegacySessionImport<TTranscript, TTranslation> {
  id: string
  createdAt: number
  updatedAt?: number
  status?: SessionStatus
  completedAt?: number
  title?: string
  summary?: string
  durationMs?: number
  transcripts?: readonly TTranscript[]
  translations?: readonly TTranslation[]
  audioChunks?: readonly Blob[]
  audioMimeType?: string
  localAudioIncomplete?: boolean
}

export interface LegacyMigrationResult {
  migrated: number
  skipped: number
}

export type SerializableSessionValue =
  | null
  | boolean
  | number
  | string
  | readonly SerializableSessionValue[]
  | { readonly [key: string]: SerializableSessionValue }

export interface CloudTranscriptOutboxRecord<TPayload = unknown> {
  ownerId: string
  sessionId: string
  clientSegmentId: string
  payload: TPayload
  createdAt: number
  updatedAt: number
}

export interface CloudTranscriptOutboxWrite<TPayload = unknown> {
  clientSegmentId: string
  payload: TPayload
}

export interface CloudTranscriptOutboxCursor {
  createdAt: number
  clientSegmentId: string
}

export interface CloudTranscriptOutboxPageOptions {
  /**
   * Exclusive ascending cursor returned by the previous page.
   */
  after?: CloudTranscriptOutboxCursor
  limit?: number
}

export interface CloudTranscriptOutboxPage<TPayload = unknown> {
  items: CloudTranscriptOutboxRecord<TPayload>[]
  hasMore: boolean
  nextCursor?: CloudTranscriptOutboxCursor
}
