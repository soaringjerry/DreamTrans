import { clearUserApiKey, getUserApiKey } from '../../utils/userApiKey'
import {
  DREAMTRANS_WEBSOCKET_PROTOCOL,
  websocketAuthProtocols,
} from '../../utils/websocketAuth'
import {
  checkSpeechmaticsPreflight,
  deleteSession as deleteCloudSession,
  getAccessToken,
  setStoredUser,
  setTokens,
  type User,
} from '../../pro/api/auth'
import {
  persistUnifiedSettings,
  type UnifiedSettings,
} from '../hooks/useUnifiedSettings'
import { chatHistoryKey } from './browserStorageKeys'
import { adminNavigationState } from './adminNavigation'
import { CloudTranscriptQueue } from './CloudTranscriptQueue'
import {
  ensureSpeechmaticsPreflight,
  speechmaticsPreflightErrorMessage,
} from './speechmaticsPreflight'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(`Unified security verification failed: ${message}`)
}

const websocketProtocols = websocketAuthProtocols('access-token')
assert(
  websocketProtocols.length === 2
    && websocketProtocols[0] === DREAMTRANS_WEBSOCKET_PROTOCOL
    && websocketProtocols[1] === 'dreamtrans.jwt.access-token',
  'authenticated WebSockets offer a stable application protocol before the JWT transport',
)

assert(
  adminNavigationState(undefined, 'idle') === 'hidden'
    && adminNavigationState('user', 'idle') === 'hidden',
  'administrator navigation stays hidden for guests and regular users',
)
assert(
  adminNavigationState('admin', 'idle') === 'enabled'
    && adminNavigationState('super_admin', 'idle') === 'enabled',
  'administrators can open the management panel while the recorder is idle',
)
for (const recorderStatus of [
  'starting',
  'recording',
  'paused',
  'stopping',
  'reconnecting',
  'error',
] as const) {
  assert(
    adminNavigationState('admin', recorderStatus) === 'disabled'
      && adminNavigationState('super_admin', recorderStatus) === 'disabled',
    `administrator navigation stays visible but disabled during ${recorderStatus}`,
  )
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
const verificationUser: User = {
  id: 'user-preflight',
  tenant_id: 'tenant-preflight',
  email: 'preflight@example.test',
  name: 'Preflight User',
  role: 'user',
  is_active: true,
  email_verified: true,
  created_at: '2026-01-01T00:00:00Z',
  updated_at: '2026-01-01T00:00:00Z',
}

setTokens('stale-access-token', 'valid-refresh-token')
setStoredUser(verificationUser)
const preflightAuthHeaders: string[] = []
let preflightRequestCount = 0
let refreshRequestCount = 0
globalThis.fetch = async (input, init) => {
  const url = String(input)
  if (url === '/api/auth/refresh') {
    refreshRequestCount += 1
    return new Response(JSON.stringify({
      user: verificationUser,
      access_token: 'fresh-access-token',
      refresh_token: 'fresh-refresh-token',
      expires_in: 900,
    }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    })
  }
  assert(
    url === '/api/speechmatics/preflight',
    'preflight uses the protected Speechmatics readiness endpoint',
  )
  preflightRequestCount += 1
  const headers = init?.headers as Record<string, string> | undefined
  preflightAuthHeaders.push(headers?.Authorization ?? '')
  if (preflightRequestCount === 1) {
    return new Response(JSON.stringify({ error: 'invalid or expired access token' }), {
      status: 401,
      headers: { 'Content-Type': 'application/json' },
    })
  }
  return new Response(JSON.stringify({ ready: true }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}
assert(
  (await checkSpeechmaticsPreflight()).ready,
  'preflight returns a typed ready response',
)
assert(
  preflightRequestCount === 2 && refreshRequestCount === 1,
  'an authenticated preflight refreshes once and retries after a 401',
)
assert(
  preflightAuthHeaders[0] === 'Bearer stale-access-token'
    && preflightAuthHeaders[1] === 'Bearer fresh-access-token',
  'the preflight retry uses the rotated access token',
)

setTokens('expired-access-token', 'expired-refresh-token')
setStoredUser(verificationUser)
globalThis.fetch = async (input) => {
  const url = String(input)
  const error = url === '/api/auth/refresh'
    ? 'invalid refresh token'
    : 'invalid or expired access token'
  return new Response(JSON.stringify({ error }), {
    status: 401,
    headers: { 'Content-Type': 'application/json' },
  })
}
let expiredPreflightError = ''
try {
  await checkSpeechmaticsPreflight()
} catch (reason) {
  expiredPreflightError = reason instanceof Error ? reason.message : String(reason)
}
assert(
  expiredPreflightError === 'Session expired' && getAccessToken() === null,
  'a failed preflight refresh clears expired authentication',
)

let anonymousAuthorization = ''
globalThis.fetch = async (input, init) => {
  assert(
    String(input) === '/api/speechmatics/preflight',
    'anonymous preflight uses the same readiness endpoint',
  )
  const headers = init?.headers as Record<string, string> | undefined
  anonymousAuthorization = headers?.Authorization ?? ''
  return new Response(JSON.stringify({ ready: true }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}
await checkSpeechmaticsPreflight()
assert(
  anonymousAuthorization === '',
  'anonymous mode can preflight without inventing an Authorization header',
)

globalThis.fetch = async () => new Response(
  JSON.stringify({ error: 'insufficient balance' }),
  {
    status: 402,
    headers: { 'Content-Type': 'application/json' },
  },
)
let balancePreflightError = ''
try {
  await checkSpeechmaticsPreflight()
} catch (reason) {
  balancePreflightError = reason instanceof Error ? reason.message : String(reason)
}
assert(
  balancePreflightError === 'insufficient balance',
  'preflight preserves the backend balance error',
)
let actionableBalanceError = ''
try {
  await ensureSpeechmaticsPreflight()
} catch (reason) {
  actionableBalanceError = reason instanceof Error ? reason.message : String(reason)
}
assert(
  actionableBalanceError.includes('余额不足'),
  'balance failures receive an actionable Chinese message',
)
assert(
  speechmaticsPreflightErrorMessage(new Error('websocket origin not allowed')).includes(
    'CORS_ALLOWED_ORIGINS',
  ),
  'origin failures receive an actionable reverse-proxy message',
)
assert(
  speechmaticsPreflightErrorMessage(new Error('Session expired')).includes('重新登录'),
  'expired authentication receives an actionable Chinese message',
)
assert(
  speechmaticsPreflightErrorMessage(new Error('rate limit exceeded')).includes('稍后再试'),
  'rate limits receive an actionable Chinese message',
)
assert(
  speechmaticsPreflightErrorMessage(new Error('billing service unavailable')).includes('暂时不可用'),
  'service failures receive an actionable Chinese message',
)
globalThis.fetch = originalFetch

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
