import {
  askRag,
  createAIIndexJob,
  generateAIArtifact,
  listAIProjects,
  previewAIContext,
  uploadKnowledgeFile,
  type AIIndexJob,
} from '../../api'

function assert(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(`AI API verification failed: ${message}`)
}

interface CapturedRequest {
  url: string
  init?: RequestInit
}

const requests: CapturedRequest[] = []
const originalFetch = globalThis.fetch
const readyJob: AIIndexJob = {
  id: 'index-job',
  target_type: 'session',
  target_id: 'session-1',
  model: 'text-embedding-3-small',
  dimensions: 1536,
  status: 'queued',
  chunk_count: 2,
  processed_chunks: 0,
  estimated_tokens: 20,
  estimated_dp: 0.01,
  attempt_count: 0,
  max_attempts: 3,
  created_at: '2026-07-31T00:00:00Z',
  updated_at: '2026-07-31T00:00:00Z',
}

globalThis.fetch = async (input, init) => {
  const url = String(input)
  requests.push({ url, init })
  let body: unknown
  if (url.endsWith('/api/ai/context/preview')) {
    body = {
      effective_mode: 'smart',
      rag_used: true,
      retrieval_mode: 'lexical_fallback',
      index_status: 'unindexed',
      estimated_tokens: 20,
      truncated: false,
      segment_count: 1,
      preview: 'preview',
      sources: [{ kind: 'transcript', label: 'Speaker: context' }],
    }
  } else if (url.endsWith('/api/ai/index/jobs')) {
    body = { job: readyJob }
  } else if (url.includes('/sources')) {
    body = {
      source: {
        id: 'source-1',
        name: 'scan.pdf',
        source_type: 'file',
        status: 'queued',
        chunk_count: 0,
      },
    }
  } else if (url.endsWith('/api/ai/artifacts')) {
    body = {
      artifact: {
        id: 'artifact-1',
        artifact_type: 'summary',
        title: 'Summary',
        content: 'Content',
        context_tokens: 20,
        created_at: '2026-07-31T00:00:00Z',
      },
      context: {
        effective_mode: 'smart',
        rag_used: true,
        retrieval_mode: 'lexical_fallback',
        index_status: 'unindexed',
        estimated_tokens: 20,
        truncated: false,
      },
    }
  } else if (url.endsWith('/api/rag/ask')) {
    body = {
      answer: 'Answer',
      context: {
        effective_mode: 'retrieval',
        rag_used: true,
        retrieval_mode: 'lexical_fallback',
        index_status: 'unindexed',
        estimated_tokens: 20,
        truncated: false,
      },
    }
  } else if (url.includes('/api/ai/projects?')) {
    body = { projects: [], linked_project_id: 'project-linked' }
  } else {
    return new Response('unexpected request', { status: 500 })
  }
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

try {
  const context = await previewAIContext(
    'session-1',
    [{ id: 'segment-1', text: 'context' }],
    { mode: 'smart', max_tokens: 64_000 },
    {
      question: 'What happened?',
      history: [{ role: 'user', content: 'Earlier question' }],
      projectId: 'project-1',
    },
  )
  assert(
    context.retrieval_mode === 'lexical_fallback'
      && context.sources?.[0]?.label === 'Speaker: context',
    'context preview preserves actual retrieval mode and sources',
  )

  await createAIIndexJob({
    targetType: 'session',
    targetId: 'session-1',
    sessionId: 'session-1',
    clientRequestId: 'index-request-1',
    confirmationToken: 'signed-preview-token',
  })
  await uploadKnowledgeFile(
    'project-1',
    new File(['scan'], 'scan.pdf', { type: 'application/pdf' }),
    ['eng', 'chi_sim', 'jpn'],
    'session-1',
  )
  await generateAIArtifact(
    'session-1',
    'summary',
    [{ text: 'context' }],
    { mode: 'smart', max_tokens: 64_000 },
    undefined,
    'project-1',
    'lexical_only',
    'artifact-request-1',
  )
  await askRag('session-1', 'Question', 6, undefined, 5_000, {
    contextPolicy: { mode: 'retrieval', max_tokens: 64_000 },
    retrievalPreference: 'lexical_only',
    clientRequestId: 'chat-request-1',
  })
  assert(
    (await listAIProjects('session-1')).linked_project_id === 'project-linked',
    'project listing retains the linked project for session restoration',
  )

  const requestBody = (path: string): Record<string, unknown> => {
    const request = requests.find(({ url }) => url.endsWith(path))
    return JSON.parse(String(request?.init?.body)) as Record<string, unknown>
  }
  const contextBody = requestBody('/api/ai/context/preview')
  assert(
    contextBody.question === 'What happened?'
      && contextBody.project_id === 'project-1'
      && Array.isArray(contextBody.history)
      && contextBody.execute_semantic === false,
    'context preview sends resolved inputs without starting paid semantic work',
  )
  const indexBody = requestBody('/api/ai/index/jobs')
  assert(
    indexBody.target_type === 'session'
      && indexBody.target_id === 'session-1'
      && indexBody.client_request_id === 'index-request-1'
      && indexBody.confirmation_token === 'signed-preview-token'
      && indexBody.confirmed === true,
    'index creation is explicit, targeted, and idempotent',
  )
  const uploadBody = requests.find(({ url }) => url.includes('/sources'))?.init?.body
  assert(
    uploadBody instanceof FormData
      && uploadBody.getAll('ocr_language').join(',') === 'eng,chi_sim,jpn'
      && uploadBody.get('session_id') === 'session-1',
    'file upload sends OCR languages and the associated session',
  )
  const artifactBody = requestBody('/api/ai/artifacts')
  assert(
    artifactBody.retrieval_preference === 'lexical_only'
      && artifactBody.client_request_id === 'artifact-request-1',
    'artifact generation carries the one-shot retrieval choice and idempotency key',
  )
  const chatBody = requestBody('/api/rag/ask')
  assert(
    chatBody.retrieval_preference === 'lexical_only'
      && chatBody.client_request_id === 'chat-request-1',
    'chat carries the one-shot retrieval choice and idempotency key',
  )
} finally {
  globalThis.fetch = originalFetch
}

console.log(JSON.stringify({
  chatIdempotency: true,
  contextPreviewContract: true,
  indexConfirmationContract: true,
  linkedProjectRestore: true,
  repeatedOCRLanguage: true,
  status: 'ok',
}))
