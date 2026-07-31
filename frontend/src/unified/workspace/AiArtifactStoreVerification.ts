import 'fake-indexeddb/auto'
import type { AIArtifact } from '../../api'
import {
  deleteLocalArtifact,
  listLocalArtifacts,
  saveLocalArtifact,
} from './AiArtifactStore'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(`AI artifact store verification failed: ${message}`)
}

const first: AIArtifact = {
  id: crypto.randomUUID(),
  artifact_type: 'summary',
  title: 'Session A summary',
  content: 'Private session A content',
  context_tokens: 12,
  created_at: '2026-07-31T00:00:00Z',
}
const second: AIArtifact = {
  id: crypto.randomUUID(),
  artifact_type: 'notes',
  title: 'Session B notes',
  content: 'Private session B content',
  context_tokens: 8,
  created_at: '2026-07-31T00:01:00Z',
}
const runId = crypto.randomUUID()
const firstSessionId = `session-a-${runId}`
const secondSessionId = `session-b-${runId}`

await saveLocalArtifact(firstSessionId, first)
await saveLocalArtifact(secondSessionId, second)

assert(
  (await listLocalArtifacts(firstSessionId)).map((artifact) => artifact.id).join() === first.id,
  'listing remains isolated by session',
)
await deleteLocalArtifact(firstSessionId, second.id)
assert(
  (await listLocalArtifacts(secondSessionId)).length === 1,
  'a session cannot delete an artifact stored under another session',
)
await deleteLocalArtifact(firstSessionId, first.id)
assert(
  (await listLocalArtifacts(firstSessionId)).length === 0,
  'the matching local artifact is deleted',
)
await deleteLocalArtifact(secondSessionId, second.id)

console.log(JSON.stringify({
  artifactDelete: true,
  artifactSessionIsolation: true,
  status: 'ok',
}))
