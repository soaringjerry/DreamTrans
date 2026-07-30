import { clearUserApiKey, getUserApiKey } from '../../utils/userApiKey'
import { deleteSession as deleteCloudSession } from '../../pro/api/auth'
import {
  persistUnifiedSettings,
  type UnifiedSettings,
} from '../hooks/useUnifiedSettings'
import { chatHistoryKey } from './browserStorageKeys'
import { CloudTranscriptQueue } from './CloudTranscriptQueue'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(`Unified security verification failed: ${message}`)
}

class MemoryStorage implements Storage {
  private readonly values = new Map<string, string>()

  get length(): number {
    return this.values.size
  }

  clear(): void {
    this.values.clear()
  }

  getItem(key: string): string | null {
    return this.values.get(key) ?? null
  }

  key(index: number): string | null {
    return [...this.values.keys()][index] ?? null
  }

  removeItem(key: string): void {
    this.values.delete(key)
  }

  setItem(key: string, value: string): void {
    this.values.set(key, value)
  }
}

const local = new MemoryStorage()
const session = new MemoryStorage()
Object.defineProperty(globalThis, 'localStorage', {
  configurable: true,
  value: local,
})
Object.defineProperty(globalThis, 'sessionStorage', {
  configurable: true,
  value: session,
})

local.setItem('dt_unified_settings_v1', JSON.stringify({
  aiApiKey: 'legacy-secret',
  autoScroll: false,
}))
assert(
  getUserApiKey() === 'legacy-secret',
  'an old unified credential migrates into tab-scoped storage',
)
const scrubbed = JSON.parse(
  local.getItem('dt_unified_settings_v1') || '{}',
) as Record<string, unknown>
assert(!('aiApiKey' in scrubbed), 'migration scrubs the localStorage credential')
assert(
  session.getItem('dt_user_api_key') === 'legacy-secret',
  'the migrated credential is held only in sessionStorage',
)

const settings: UnifiedSettings = {
  viewMode: 'bilingual',
  autoScroll: true,
  sourceLanguage: 'en',
  targetLanguage: 'cmn',
  translationEnabled: true,
  reducedEffects: false,
  keepLocalAudio: true,
  automaticAiIngest: false,
  aiApiKey: 'current-secret',
  aiApiBase: '',
  aiModel: '',
  aiPrompt: 'prompt',
}
persistUnifiedSettings(settings)
assert(
  !local.getItem('dt_unified_settings_v1')?.includes('current-secret'),
  'new settings writes never persist a provider credential in localStorage',
)
assert(
  session.getItem('dt_user_api_key') === 'current-secret',
  'new provider credentials remain tab-scoped',
)
clearUserApiKey()
assert(
  session.getItem('dt_user_api_key') === null && getUserApiKey() === '',
  'credential clearing removes both session and module state',
)

assert(
  chatHistoryKey('user-a', 'shared-session')
    !== chatHistoryKey('user-b', 'shared-session'),
  'chat history keys are owner-scoped',
)
assert(
  chatHistoryKey(null, 'shared-session')
    !== chatHistoryKey('anonymous', 'shared-session'),
  'anonymous history cannot collide with an account id',
)

const originalFetch = globalThis.fetch
let deleteRequestCount = 0
globalThis.fetch = async () => {
  deleteRequestCount += 1
  return new Response(JSON.stringify({ error: 'session not found' }), {
    status: 404,
    headers: { 'Content-Type': 'application/json' },
  })
}
await deleteCloudSession('already-deleted')
assert(
  deleteRequestCount === 1,
  'a repeated cloud DELETE treats 404 as success so local cleanup can continue',
)

let requestCount = 0
let abortCount = 0
let visiblePending = 0
globalThis.fetch = (_input, init) => {
  requestCount += 1
  return new Promise<Response>((_resolve, reject) => {
    const signal = init?.signal
    const abort = () => {
      abortCount += 1
      reject(new DOMException('Discarded session', 'AbortError'))
    }
    if (signal?.aborted) abort()
    else signal?.addEventListener('abort', abort, { once: true })
  })
}

try {
  const queue = new CloudTranscriptQueue({
    flushDelayMs: 100,
    retryBaseDelayMs: 100,
    onPendingChange: (count) => {
      visiblePending = count
    },
  })
  queue.setOwner('user-a')
  queue.setSession('deleted-session')
  queue.queue({
    client_segment_id: 'segment-a',
    text: 'must not retry after deletion',
    start_time: 0,
  })
  const flush = queue.flush()
  await Promise.resolve()
  queue.discardSession('user-a', 'deleted-session')
  await flush
  await new Promise((resolve) => globalThis.setTimeout(resolve, 150))
  assert(requestCount === 1, 'discarded in-flight batches are not retried')
  assert(abortCount === 1, 'discarding an in-flight session aborts its request')
  assert(visiblePending === 0, 'discard removes pending and in-flight visibility')

  queue.setSession('pending-session')
  queue.queue({
    client_segment_id: 'segment-b',
    text: 'pending only',
    start_time: 1,
  })
  queue.discardSession('user-a', 'pending-session')
  await new Promise((resolve) => globalThis.setTimeout(resolve, 150))
  assert(requestCount === 1, 'discarded pending batches are never sent')
  assert(visiblePending === 0, 'discarded pending batches leave no visible work')
  queue.destroy()
} finally {
  globalThis.fetch = originalFetch
}

console.log(JSON.stringify({
  chatOwnerIsolation: true,
  cloudDiscardAbort: true,
  providerCredentialStorage: 'session-only',
  status: 'ok',
}))
