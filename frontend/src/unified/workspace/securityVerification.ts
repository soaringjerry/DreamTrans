import {
  clearUserApiKey,
  getUserApiKey,
  setUserApiKey,
} from '../../utils/userApiKey'
import {
  DREAMTRANS_WEBSOCKET_PROTOCOL,
  websocketAuthProtocols,
} from '../../utils/websocketAuth'
import {
  AUTH_STATE_CHANGED_EVENT,
  AuthUnavailableError,
  authFetch,
  checkSpeechmaticsPreflight,
  deleteSession as deleteCloudSession,
  getAccessToken,
  getRefreshToken,
  getStoredUser,
  initAuth,
  login,
  logout as logoutAuth,
  refreshAccessToken,
  register,
  setStoredUser,
  setTokens,
  type User,
} from '../../pro/api/auth'
import { getUserBalance } from '../../api'
import {
  persistUnifiedSettings,
  type UnifiedSettings,
} from '../hooks/useUnifiedSettings'
import { chatHistoryKey } from './browserStorageKeys'
import { adminNavigationState } from './adminNavigation'
import {
  CloudTranscriptQueue,
  CloudTranscriptSyncError,
} from './CloudTranscriptQueue'
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
const browserEvents = new EventTarget()
Object.defineProperty(browserEvents, 'location', {
  configurable: true,
  value: { origin: '' },
})
Object.defineProperty(globalThis, 'window', {
  configurable: true,
  value: browserEvents,
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
  translationEngine: 'ai',
  translatePrompt: '',
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

const switchedUser: User = {
  ...verificationUser,
  id: 'user-other-tab',
  email: 'other-tab@example.test',
}
setUserApiKey('account-a-provider-key')
let externalAuthEvents = 0
browserEvents.addEventListener(AUTH_STATE_CHANGED_EVENT, () => {
  externalAuthEvents += 1
})
local.setItem('dt_access_token', 'other-tab-access')
local.setItem('dt_refresh_token', 'other-tab-refresh')
local.setItem('dt_user', JSON.stringify(switchedUser))
local.setItem('dt_auth_sync_v1', JSON.stringify({
  source: 'another-tab',
  sequence: 1,
  timestamp: Date.now(),
}))
const storageEvent = new Event('storage')
Object.defineProperty(storageEvent, 'key', { value: 'dt_auth_sync_v1' })
browserEvents.dispatchEvent(storageEvent)
assert(
  getStoredUser()?.id === switchedUser.id
    && getAccessToken() === 'other-tab-access'
    && getRefreshToken() === 'other-tab-refresh',
  'a cross-tab auth marker atomically adopts the new account credentials',
)
assert(
  externalAuthEvents >= 1 && getUserApiKey() === '',
  'cross-tab account switching notifies the UI and clears the previous account provider key',
)

setTokens('outage-access-token', 'outage-refresh-token')
setStoredUser(verificationUser)
setUserApiKey('keep-during-outage')
globalThis.fetch = async (input) => {
  assert(
    String(input) === '/api/auth/refresh',
    'the outage check targets the refresh endpoint',
  )
  return new Response(JSON.stringify({ error: 'refresh service restarting' }), {
    status: 503,
    headers: { 'Content-Type': 'application/json' },
  })
}
let refreshOutageError: unknown
try {
  await refreshAccessToken()
} catch (reason) {
  refreshOutageError = reason
}
assert(
  refreshOutageError instanceof AuthUnavailableError
    && getAccessToken() === 'outage-access-token'
    && getRefreshToken() === 'outage-refresh-token'
    && getUserApiKey() === 'keep-during-outage',
  'a refresh 5xx is retryable and never clears cached credentials',
)
globalThis.fetch = async () => new Response(
  JSON.stringify({ error: 'backend restarting' }),
  {
    status: 503,
    headers: { 'Content-Type': 'application/json' },
  },
)
assert(
  (await initAuth())?.id === verificationUser.id
    && getAccessToken() === 'outage-access-token'
    && getRefreshToken() === 'outage-refresh-token'
    && getUserApiKey() === 'keep-during-outage',
  'a profile 5xx keeps cached authentication and the tab-scoped provider key',
)
globalThis.fetch = async () => {
  throw new TypeError('network disconnected')
}
assert(
  (await initAuth())?.id === verificationUser.id
    && getAccessToken() === 'outage-access-token'
    && getRefreshToken() === 'outage-refresh-token'
    && getUserApiKey() === 'keep-during-outage',
  'a network outage keeps cached authentication instead of hiding owner-scoped history',
)

setTokens('abort-access-token', 'abort-refresh-token')
setStoredUser(verificationUser)
setUserApiKey('keep-after-abort')
globalThis.fetch = (_input, init) => new Promise<Response>((_resolve, reject) => {
  const signal = init?.signal
  const rejectAbort = () => reject(
    signal?.reason ?? new DOMException('request aborted', 'AbortError'),
  )
  if (signal?.aborted) rejectAbort()
  else signal?.addEventListener('abort', rejectAbort, { once: true })
})
const abortController = new AbortController()
const abortedProfile = authFetch('/api/user/profile', {
  signal: abortController.signal,
})
abortController.abort()
let abortedProfileError: unknown
try {
  await abortedProfile
} catch (reason) {
  abortedProfileError = reason
}
assert(
  abortedProfileError instanceof Error
    && getAccessToken() === 'abort-access-token'
    && getRefreshToken() === 'abort-refresh-token'
    && getUserApiKey() === 'keep-after-abort',
  'an aborted authenticated request never clears otherwise valid cached credentials',
)

setTokens('logout-a-access', 'logout-a-refresh')
setStoredUser(verificationUser)
let finishLogoutRequest: (() => void) | undefined
globalThis.fetch = async (input) => {
  assert(String(input) === '/api/auth/logout', 'logout calls the revocation endpoint')
  await new Promise<void>((resolve) => {
    finishLogoutRequest = resolve
  })
  return new Response(JSON.stringify({ success: true }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}
const pendingLogout = logoutAuth()
await Promise.resolve()
assert(getStoredUser() === null, 'logout clears the initiating identity immediately')
local.setItem('dt_access_token', 'logout-b-access')
local.setItem('dt_refresh_token', 'logout-b-refresh')
local.setItem('dt_user', JSON.stringify(switchedUser))
local.setItem('dt_auth_sync_v1', JSON.stringify({
  source: 'login-during-logout',
  sequence: 2,
  timestamp: Date.now(),
}))
const loginDuringLogoutEvent = new Event('storage')
Object.defineProperty(loginDuringLogoutEvent, 'key', { value: 'dt_auth_sync_v1' })
browserEvents.dispatchEvent(loginDuringLogoutEvent)
finishLogoutRequest?.()
await pendingLogout
assert(
  getStoredUser()?.id === switchedUser.id
    && getAccessToken() === 'logout-b-access'
    && getRefreshToken() === 'logout-b-refresh',
  'a slow logout completion cannot erase a newer cross-tab login',
)

setTokens('balance-a-access', 'balance-a-refresh')
setStoredUser(verificationUser)
let finishBalanceRequest: ((response: Response) => void) | undefined
globalThis.fetch = async (input) => {
  assert(String(input) === '/api/user/balance', 'balance uses the generation-safe auth wrapper')
  return new Promise<Response>((resolve) => {
    finishBalanceRequest = resolve
  })
}
const staleBalance = getUserBalance()
await Promise.resolve()
local.setItem('dt_access_token', 'balance-b-access')
local.setItem('dt_refresh_token', 'balance-b-refresh')
local.setItem('dt_user', JSON.stringify(switchedUser))
local.setItem('dt_auth_sync_v1', JSON.stringify({
  source: 'switch-during-balance',
  sequence: 3,
  timestamp: Date.now(),
}))
const switchDuringBalanceEvent = new Event('storage')
Object.defineProperty(switchDuringBalanceEvent, 'key', { value: 'dt_auth_sync_v1' })
browserEvents.dispatchEvent(switchDuringBalanceEvent)
finishBalanceRequest?.(new Response(JSON.stringify({
  user_id: verificationUser.id,
  email: verificationUser.email,
  name: verificationUser.name,
  dreampoints: 10,
  dreampoints_used: 1,
}), {
  status: 200,
  headers: { 'Content-Type': 'application/json' },
}))
let staleBalanceError = ''
try {
  await staleBalance
} catch (reason) {
  staleBalanceError = reason instanceof Error ? reason.message : String(reason)
}
assert(
  staleBalanceError === 'Authentication state changed'
    && getStoredUser()?.id === switchedUser.id,
  'an account-A balance response is rejected after the browser switches to account B',
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
  expiredPreflightError === 'Session expired'
    && getAccessToken() === null
    && getUserApiKey() === '',
  'a definitive 401 refresh failure clears expired authentication and its provider key',
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

setTokens('queue-access-token', 'queue-refresh-token')
setStoredUser(verificationUser)
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
  queue.setOwner(verificationUser.id)
  queue.setSession('deleted-session')
  queue.queue({
    client_segment_id: 'segment-a',
    text: 'must not retry after deletion',
    start_time: 0,
  })
  const flush = queue.flush()
  await Promise.resolve()
  queue.discardSession(verificationUser.id, 'deleted-session')
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
  queue.discardSession(verificationUser.id, 'pending-session')
  await new Promise((resolve) => globalThis.setTimeout(resolve, 150))
  assert(requestCount === 1, 'discarded pending batches are never sent')
  assert(visiblePending === 0, 'discarded pending batches leave no visible work')
  queue.destroy()
} finally {
  globalThis.fetch = originalFetch
}

let payloadRequestCount = 0
let rejectedCallbackCount = 0
let payloadPending = 0
let payloadFailure: Error | null = null
globalThis.fetch = async () => {
  payloadRequestCount += 1
  return new Response(JSON.stringify({ error: 'invalid client_segment_id' }), {
    status: 400,
    headers: { 'Content-Type': 'application/json' },
  })
}
try {
  const queue = new CloudTranscriptQueue({
    batchSize: 2,
    flushDelayMs: 100,
    retryBaseDelayMs: 5_000,
    retryMaxDelayMs: 5_000,
    onPendingChange: (count) => {
      payloadPending = count
    },
    onError: (error) => {
      payloadFailure = error
    },
    onEntriesRejected: () => {
      rejectedCallbackCount += 1
    },
  })
  queue.setOwner(verificationUser.id)
  queue.setSession('schema-mismatch-session')
  queue.queue({
    client_segment_id: 'segment-invalid-a',
    text: 'durable a',
    start_time: 0,
  }, 1)
  queue.queue({
    client_segment_id: 'segment-invalid-b',
    text: 'durable b',
    start_time: 1,
  }, 2)
  await queue.flush()
  await queue.flush()
  assert(
    payloadRequestCount === 1,
    'a permanent batch rejection enters backoff instead of starting a per-record request storm',
  )
  assert(
    rejectedCallbackCount === 0 && payloadPending === 2,
    'schema rejections remain pending and never acknowledge the durable outbox',
  )
  // Callback writes are opaque to TypeScript's synchronous control-flow
  // analysis; widen the observed value before checking its runtime class.
  const observedPayloadFailure = payloadFailure as unknown
  assert(
    observedPayloadFailure instanceof CloudTranscriptSyncError
      && observedPayloadFailure.kind === 'payload'
      && observedPayloadFailure.retryAt !== undefined,
    'schema rejection exposes a recognizable payload/backoff error',
  )
  queue.destroy()
} finally {
  globalThis.fetch = originalFetch
}

let networkRequestCount = 0
let networkRecoveredBatches = 0
let networkFailure: Error | null = null
let networkAvailable = false
globalThis.fetch = async () => {
  networkRequestCount += 1
  if (!networkAvailable) throw new TypeError('network disconnected')
  return new Response(JSON.stringify({ saved: [], count: 1 }), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}
try {
  const queue = new CloudTranscriptQueue({
    flushDelayMs: 100,
    retryBaseDelayMs: 5_000,
    retryMaxDelayMs: 5_000,
    onError: (error) => {
      networkFailure = error
    },
    onBatchSaved: () => {
      networkRecoveredBatches += 1
    },
  })
  queue.setOwner(verificationUser.id)
  queue.setSession('offline-session')
  queue.queue({
    client_segment_id: 'segment-offline',
    text: 'retry after online',
    start_time: 0,
  }, 3)
  await queue.flush().catch(() => undefined)
  const observedNetworkFailure = networkFailure as unknown
  assert(
    networkRequestCount === 1
      && observedNetworkFailure instanceof CloudTranscriptSyncError
      && observedNetworkFailure.kind === 'network',
    'network failures retain the entry and expose a recognizable retryable error',
  )
  networkAvailable = true
  browserEvents.dispatchEvent(new Event('online'))
  await new Promise((resolve) => globalThis.setTimeout(resolve, 50))
  assert(
    Number(networkRequestCount) === 2 && Number(networkRecoveredBatches) === 1,
    'the online event immediately resumes a transiently failed cloud outbox',
  )
  queue.destroy()
} finally {
  globalThis.fetch = originalFetch
}

setTokens('preserved-access-token', 'preserved-refresh-token')
setStoredUser(verificationUser)
const timedAuthSubmissionURLs: string[] = []
globalThis.fetch = async (input, init) => {
  timedAuthSubmissionURLs.push(String(input))
  assert(
    init?.signal instanceof AbortSignal,
    'login and registration requests install a bounded timeout signal',
  )
  throw new DOMException('simulated timeout', 'TimeoutError')
}
try {
  for (const submit of [
    () => login('login@example.test', 'password'),
    () => register('register@example.test', 'password', 'Register User'),
  ]) {
    let message = ''
    try {
      await submit()
    } catch (reason) {
      message = reason instanceof Error ? reason.message : String(reason)
    }
    assert(
      message.includes('请求超时') && message.includes('检查网络'),
      'timed-out login and registration requests expose an actionable error',
    )
  }
  assert(
    timedAuthSubmissionURLs.join(',') === '/api/auth/login,/api/auth/register',
    'login and registration use their expected bounded request endpoints',
  )
  assert(
    getAccessToken() === 'preserved-access-token'
      && getRefreshToken() === 'preserved-refresh-token'
      && getStoredUser()?.id === verificationUser.id,
    'a timed-out login or registration does not erase existing credentials',
  )
} finally {
  globalThis.fetch = originalFetch
}

console.log(JSON.stringify({
  authOutageRetention: true,
  authSubmissionTimeouts: true,
  chatOwnerIsolation: true,
  cloudDiscardAbort: true,
  cloudRetryBackoff: true,
  crossTabAuthSync: true,
  providerCredentialStorage: 'session-only',
  status: 'ok',
}))
