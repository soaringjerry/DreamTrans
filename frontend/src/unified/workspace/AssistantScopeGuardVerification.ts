import { AssistantScopeGuard } from './AssistantScopeGuard'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(`Assistant scope verification failed: ${message}`)
}

const guard = new AssistantScopeGuard('owner-a', 'session-a')
const sessionA = guard.update('owner-a', 'session-a')
assert(guard.isCurrent(sessionA), 'the active session snapshot is accepted')

const sessionB = guard.update('owner-a', 'session-b')
assert(!guard.isCurrent(sessionA), 'a response from the previous session is rejected')
assert(guard.isCurrent(sessionB), 'the replacement session snapshot is accepted')

const ownerB = guard.update('owner-b', 'session-b')
assert(!guard.isCurrent(sessionB), 'an owner change invalidates in-flight responses')
assert(guard.isCurrent(ownerB), 'the replacement owner snapshot is accepted')

console.log(JSON.stringify({
  ownerSwitchInvalidation: true,
  sessionSwitchInvalidation: true,
  status: 'ok',
}))
