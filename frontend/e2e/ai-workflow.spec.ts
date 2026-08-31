import { expect, test, type Page, type Route } from '@playwright/test'


const e2eFreePlan = {
  code: 'free', name: 'Free', is_public: true, active: true, sort: 0,
  price_usd_month: 0, price_usd_year: 0, usage_discount_percent: 0,
  storage_gb: 1, retention_days: 30, max_concurrent_sessions: 1, seats: 1,
  features: {},
}

function e2eAccountBalance(userId: string) {
  return {
    user_id: userId,
    account_id: 'account-e2e',
    wallet_usd: 100,
    grant_usd: 0,
    available_usd: 100,
    lifetime_charged_usd: 1,
    plan_code: 'free',
    member_active: false,
    auto_topup_enabled: false,
  }
}

interface APIRecord {
  body?: Record<string, unknown>
  method: string
  path: string
  postData?: string
}

interface MockSession {
  id: string
  sourceLanguage: string
  title: string
}

interface MockProject {
  context_mode: 'smart'
  description: string
  id: string
  max_context_tokens: number
  name: string
}

interface MockSource {
  chunk_count: number
  id: string
  index_status: 'unindexed'
  name: string
  ocr_languages: string[]
  source_type: 'file'
  status: 'ready'
}

interface MockArtifact {
  artifact_type: 'summary' | 'notes' | 'action_items'
  content: string
  context_tokens: number
  created_at: string
  id: string
  model: string
  title: string
}

const now = '2026-07-31T02:00:00.000Z'
const user = {
  id: 'user-1',
  tenant_id: 'tenant-1',
  email: 'e2e@example.test',
  name: 'E2E User',
  role: 'user',
  is_active: true,
  email_verified: true,
  created_at: now,
  updated_at: now,
}

function accessToken(): string {
  const encode = (value: object) => Buffer
    .from(JSON.stringify(value))
    .toString('base64url')
  return `${encode({ alg: 'none', typ: 'JWT' })}.${encode({
    exp: Math.floor(Date.now() / 1_000) + 3_600,
    sub: user.id,
  })}.e2e`
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({
    body: JSON.stringify(body),
    contentType: 'application/json',
    status,
  })
}

function jsonBody(route: Route): Record<string, unknown> | undefined {
  try {
    return route.request().postDataJSON() as Record<string, unknown>
  } catch {
    return undefined
  }
}

class MockAIBackend {
  readonly records: APIRecord[] = []
  readonly unhandled: string[] = []
  readonly sessions: MockSession[]
  readonly unavailableSessionIds = new Set<string>()
  readonly responseDelayMs = new Map<string, number>()
  readonly projectsBySession = new Map<string, {
    linkedProjectId: string
    projects: MockProject[]
  }>()
  readonly artifactsBySession = new Map<string, MockArtifact[]>()
  readonly noRetrievalSessions = new Set<string>()
  artifacts: MockArtifact[] = []
  emptyProjectFallsThroughToSession = false
  indexJobCanFinish = false
  indexJobReturnsError = false
  indexRetryReturnsConflict = false
  linkedProjectId = ''
  project: MockProject | undefined
  projectIndexReady = false
  sessionIndexReady = false
  sources: MockSource[] = []

  constructor(sessions: MockSession[]) {
    this.sessions = sessions
  }

  private async delay(key: string): Promise<void> {
    const milliseconds = this.responseDelayMs.get(key) ?? 0
    if (milliseconds <= 0) return
    await new Promise((resolve) => globalThis.setTimeout(resolve, milliseconds))
  }

  async install(page: Page): Promise<void> {
    await page.route(/^https?:\/\/[^/]+\/api\//, async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      const method = request.method()
      const body = jsonBody(route)
      const record: APIRecord = {
        method,
        path: url.pathname,
        ...(body ? { body } : {}),
        ...(request.postData() ? { postData: request.postData() ?? undefined } : {}),
      }
      this.records.push(record)

      if (method === 'GET' && url.pathname === '/api/system/access') {
        await json(route, {
          anonymous_api_enabled: false,
          authentication_enabled: true,
          registration_enabled: false,
          rag_enabled: true,
        })
        return
      }
      if (method === 'GET' && url.pathname === '/api/system/settings') {
        await json(route, { allow_user_api_key: false })
        return
      }
      if (method === 'POST' && url.pathname === '/api/auth/login') {
        await json(route, {
          access_token: accessToken(),
          expires_in: 3_600,
          refresh_token: 'e2e-refresh',
          user,
        })
        return
      }
      if (method === 'GET' && url.pathname === '/api/user/profile') {
        await json(route, { user })
        return
      }
      if (method === 'GET' && url.pathname === '/api/user/balance') {
        await json(route, e2eAccountBalance(user.id))
        return
      }
      if (method === 'GET' && url.pathname === '/api/user/billing/account') {
        await json(route, {
          account: {
            ...e2eAccountBalance(user.id),
            email: user.email,
            name: user.name,
            status: 'active',
            plan: e2eFreePlan,
            effective_plan: e2eFreePlan,
            discount_percent: 0,
            grants: [],
            has_payment_method: false,
            storage_bytes: 0,
            realtime_hour_usd: 0.645,
            estimated_realtime_hours: 155,
          },
          payments_enabled: false,
        })
        return
      }
      if (method === 'GET' && url.pathname === '/api/user/billing/plans') {
        await json(route, {
          plans: [e2eFreePlan],
          topup_tiers: [],
          hourly: [{
            plan_code: 'free', plan_name: 'Free', discount_percent: 0,
            realtime_hour_usd: 0.645, realtime_upstream_usd: 0.43, realtime_gross_margin_percent: 33.3,
          }],
          payments_enabled: false,
        })
        return
      }
      if (method === 'GET' && (url.pathname === '/api/user/billing/usage' || url.pathname === '/api/user/billing/ledger')) {
        await json(route, { usage: [], ledger: [], payments: [] })
        return
      }
      if (method === 'GET' && url.pathname === '/api/sessions') {
        await json(route, {
          page: 1,
          page_size: 60,
          sessions: this.sessions.map((session) => ({
            id: session.id,
            user_id: user.id,
            tenant_id: user.tenant_id,
            title: session.title,
            source_language: session.sourceLanguage,
            target_language: 'cmn',
            duration_seconds: 60,
            status: 'completed',
            started_at: now,
            created_at: now,
            updated_at: now,
          })),
        })
        return
      }
      const sessionMatch = url.pathname.match(/^\/api\/sessions\/([^/]+)$/)
      if (sessionMatch && method === 'GET') {
        if (this.unavailableSessionIds.has(sessionMatch[1] ?? '')) {
          await json(route, { error: 'temporary session outage' }, 503)
          return
        }
        const session = this.sessions.find(({ id }) => id === sessionMatch[1])
        if (!session) {
          await json(route, { error: 'session not found' }, 404)
          return
        }
        await json(route, {
          id: session.id,
          user_id: user.id,
          tenant_id: user.tenant_id,
          title: session.title,
          source_language: session.sourceLanguage,
          target_language: 'cmn',
          duration_seconds: 60,
          status: 'completed',
          started_at: now,
          created_at: now,
          updated_at: now,
          transcripts: [],
        })
        return
      }
      if (sessionMatch && method === 'PUT') {
        const session = this.sessions.find(({ id }) => id === sessionMatch[1])
        await json(route, {
          ...body,
          id: session?.id,
          title: session?.title,
          source_language: session?.sourceLanguage,
          target_language: 'cmn',
          duration_seconds: 60,
          status: 'completed',
          started_at: now,
          created_at: now,
          updated_at: now,
        })
        return
      }
      const transcriptsMatch = url.pathname.match(
        /^\/api\/sessions\/([^/]+)\/transcripts$/,
      )
      if (transcriptsMatch && method === 'GET') {
        await json(route, {
          has_more: false,
          next_cursor: null,
          transcripts: [{
            id: `transcript-${transcriptsMatch[1]}`,
            session_id: transcriptsMatch[1],
            client_segment_id: `segment-${transcriptsMatch[1]}`,
            speaker: 'Speaker 1',
            text: 'The launch decision is Friday and Alice owns the release notes.',
            start_time: 0,
            end_time: 4,
            status: 'confirmed',
            is_partial: false,
            created_at: now,
            updated_at: now,
          }],
        })
        return
      }

      if (method === 'GET' && url.pathname === '/api/ai/projects') {
        const requestedSessionId = url.searchParams.get('session_id') ?? ''
        await this.delay(`projects:${requestedSessionId}`)
        const scoped = this.projectsBySession.get(requestedSessionId)
        await json(route, {
          linked_project_id: scoped
            ? scoped.linkedProjectId || null
            : this.linkedProjectId || null,
          projects: scoped?.projects ?? (this.project ? [this.project] : []),
        })
        return
      }
      if (method === 'POST' && url.pathname === '/api/ai/projects') {
        this.project = {
          context_mode: 'smart',
          description: '',
          id: 'project-1',
          max_context_tokens: 64_000,
          name: String(body?.name ?? 'E2E project'),
        }
        await json(route, { project: this.project }, 201)
        return
      }
      if (method === 'DELETE' && url.pathname === '/api/ai/projects/project-1') {
        this.project = undefined
        this.linkedProjectId = ''
        this.sources = []
        await route.fulfill({ status: 204 })
        return
      }
      if (
        method === 'POST'
        && url.pathname === '/api/ai/projects/project-1/sessions'
      ) {
        this.linkedProjectId = 'project-1'
        await route.fulfill({ status: 204 })
        return
      }
      if (
        method === 'DELETE'
        && /^\/api\/ai\/projects\/project-1\/sessions\//.test(url.pathname)
      ) {
        this.linkedProjectId = ''
        await route.fulfill({ status: 204 })
        return
      }
      if (
        method === 'GET'
        && url.pathname === '/api/ai/projects/project-1/sources'
      ) {
        await json(route, { sources: this.sources })
        return
      }
      if (
        method === 'POST'
        && url.pathname === '/api/ai/projects/project-1/sources'
      ) {
        const postData = request.postData() ?? ''
        const contentType = await request.headerValue('content-type')
        if (!contentType?.startsWith('multipart/form-data')) {
          await json(route, { error: 'expected multipart upload' }, 400)
          return
        }
        const source: MockSource = {
          chunk_count: 2,
          id: 'source-1',
          index_status: 'unindexed',
          name: postData.includes('launch-notes.txt')
            ? 'launch-notes.txt'
            : 'uploaded knowledge',
          ocr_languages: postData.includes('chi_sim') ? ['eng', 'chi_sim'] : ['eng'],
          source_type: 'file',
          status: 'ready',
        }
        this.sources = [source]
        await json(route, { source }, 201)
        return
      }
      if (
        method === 'DELETE'
        && url.pathname === '/api/ai/projects/project-1/sources/source-1'
      ) {
        this.sources = []
        await route.fulfill({ status: 204 })
        return
      }

      if (method === 'POST' && url.pathname === '/api/ai/context/preview') {
        const sessionId = String(body?.session_id ?? '')
        await this.delay(`context:${sessionId}`)
        const noRetrieval = this.noRetrievalSessions.has(sessionId)
        const emptyProject = this.emptyProjectFallsThroughToSession
          && this.sources.length === 0
        const indexTargets = noRetrieval
          ? []
          : this.project && !emptyProject
          ? [
              {
                target_type: 'project',
                target_id: this.project.id,
                index_status: this.projectIndexReady ? 'ready' : 'unindexed',
              },
              ...(this.emptyProjectFallsThroughToSession
                ? [{
                    target_type: 'session',
                    target_id: sessionId,
                    index_status: this.sessionIndexReady ? 'ready' : 'unindexed',
                  }]
                : []),
            ]
          : [{
              target_type: 'session',
              target_id: sessionId,
              index_status: this.sessionIndexReady ? 'ready' : 'unindexed',
            }]
        await json(route, {
          effective_mode: 'smart',
          estimated_tokens: 180,
          index_status: indexTargets.some(({ index_status }) => index_status !== 'ready')
            ? 'unindexed'
            : 'ready',
          index_targets: indexTargets,
          preview: `Context preview for ${sessionId}.`,
          rag_used: !noRetrieval,
          retrieval_mode: noRetrieval
            ? 'none'
            : this.projectIndexReady || this.sessionIndexReady
            ? 'hybrid'
            : 'lexical_fallback',
          segment_count: 1,
          sources: [{
            kind: 'transcript',
            id: sessionId,
            label: `Session transcript ${sessionId}`,
          }],
          truncated: false,
        })
        return
      }
      if (method === 'POST' && url.pathname === '/api/ai/index/preview') {
        const targetType = String(body?.target_type)
        const targetId = String(body?.target_id)
        const isProject = targetType === 'project'
        const ready = isProject ? this.projectIndexReady : this.sessionIndexReady
        const emptyProject = isProject
          && this.emptyProjectFallsThroughToSession
          && this.sources.length === 0
        await json(route, {
          target_type: targetType,
          target_id: targetId,
          model: 'text-embedding-3-small',
          dimensions: 1_536,
          source_count: emptyProject ? 0 : 1,
          chunk_count: emptyProject ? 0 : 2,
          indexed_chunks: ready ? 2 : 0,
          pending_chunks: ready || emptyProject ? 0 : 2,
          estimated_tokens: ready || emptyProject ? 0 : 20,
          estimated_dp: ready || emptyProject ? 0 : 0.01,
          confirmation_token: ready || emptyProject
            ? undefined
            : `signed-confirmation-${targetType}`,
          index_status: ready ? 'ready' : 'unindexed',
          requires_indexing: !ready && !emptyProject,
        })
        return
      }
      if (method === 'POST' && url.pathname === '/api/ai/index/jobs') {
        await json(route, {
          job: {
            id: `job-${String(body?.target_type)}`,
            target_type: body?.target_type,
            target_id: body?.target_id,
            model: 'text-embedding-3-small',
            dimensions: 1_536,
            status: 'processing',
            chunk_count: 2,
            processed_chunks: 1,
            estimated_tokens: 20,
            estimated_dp: 0.01,
            client_request_id: body?.client_request_id,
            attempt_count: 0,
            max_attempts: 3,
            created_at: now,
            updated_at: now,
          },
        }, 201)
        return
      }
      const jobMatch = url.pathname.match(/^\/api\/ai\/index\/jobs\/([^/]+)$/)
      if (method === 'GET' && jobMatch) {
        const targetType = jobMatch[1]?.replace('job-', '') === 'session'
          ? 'session'
          : 'project'
        if (this.indexJobCanFinish) {
          if (targetType === 'project') this.projectIndexReady = true
          else this.sessionIndexReady = true
        }
        await json(route, {
          job: {
            id: jobMatch[1],
            target_type: targetType,
            target_id: targetType === 'project'
              ? this.project?.id
              : this.sessions[0]?.id,
            model: 'text-embedding-3-small',
            dimensions: 1_536,
            status: this.indexJobReturnsError
              ? 'error'
              : this.indexJobCanFinish ? 'ready' : 'processing',
            chunk_count: 2,
            processed_chunks: this.indexJobCanFinish ? 2 : 1,
            estimated_tokens: 20,
            actual_tokens: this.indexJobCanFinish ? 18 : undefined,
            estimated_dp: 0.01,
            error_message: this.indexJobReturnsError
              ? 'mock indexing failure'
              : undefined,
            attempt_count: 1,
            max_attempts: 3,
            created_at: now,
            updated_at: now,
          },
        })
        return
      }
      const retryJobMatch = url.pathname.match(
        /^\/api\/ai\/index\/jobs\/([^/]+)\/retry$/,
      )
      if (method === 'POST' && retryJobMatch) {
        if (this.indexRetryReturnsConflict) {
          this.indexRetryReturnsConflict = false
          this.indexJobReturnsError = false
          this.indexJobCanFinish = true
          await json(route, {
            error: 'index target changed after confirmation; preview again',
          }, 409)
          return
        }
        await json(route, {
          job: {
            id: retryJobMatch[1],
            target_type: 'session',
            target_id: this.sessions[0]?.id,
            model: 'text-embedding-3-small',
            dimensions: 1_536,
            status: 'processing',
            chunk_count: 2,
            processed_chunks: 0,
            estimated_tokens: 20,
            estimated_dp: 0.01,
            attempt_count: 2,
            max_attempts: 3,
            created_at: now,
            updated_at: now,
          },
        }, 202)
        return
      }
      if (method === 'DELETE' && jobMatch) {
        await json(route, {
          job: {
            id: jobMatch[1],
            target_type: jobMatch[1] === 'job-session' ? 'session' : 'project',
            target_id: jobMatch[1] === 'job-session'
              ? this.sessions[0]?.id
              : this.project?.id,
            model: 'text-embedding-3-small',
            dimensions: 1_536,
            status: 'cancelled',
            chunk_count: 2,
            processed_chunks: 1,
            estimated_tokens: 20,
            estimated_dp: 0.01,
            attempt_count: 1,
            max_attempts: 3,
            created_at: now,
            updated_at: now,
          },
        })
        return
      }

      if (method === 'POST' && url.pathname === '/api/rag/ask') {
        const requestedSessionId = String(body?.session_id ?? '')
        await this.delay(`chat:${requestedSessionId}`)
        await json(route, {
          answer: this.noRetrievalSessions.has(requestedSessionId)
            ? `Chat answer for ${requestedSessionId}.`
            : 'Alice owns the release notes and the launch decision is Friday.',
          context: {
            effective_mode: 'smart',
            estimated_tokens: 180,
            index_status: this.projectIndexReady || this.sessionIndexReady
              ? 'ready'
              : 'unindexed',
            rag_used: true,
            retrieval_mode: body?.retrieval_preference === 'lexical_only'
              ? 'lexical_fallback'
              : 'hybrid',
            truncated: false,
          },
          latency_ms: 25,
          usage: {
            prompt_tokens: 180,
            completion_tokens: 20,
            total_tokens: 200,
            model: 'gpt-5-mini',
          },
        })
        return
      }
      if (method === 'GET' && url.pathname === '/api/ai/artifacts') {
        const requestedSessionId = url.searchParams.get('session_id') ?? ''
        await this.delay(`artifacts:${requestedSessionId}`)
        await json(route, {
          artifacts: this.artifactsBySession.get(requestedSessionId) ?? this.artifacts,
        })
        return
      }
      if (method === 'POST' && url.pathname === '/api/ai/artifacts') {
        const requestedSessionId = String(body?.session_id ?? '')
        await this.delay(`artifact-create:${requestedSessionId}`)
        const artifactType = body?.artifact_type as MockArtifact['artifact_type']
        const titles: Record<MockArtifact['artifact_type'], string> = {
          summary: 'Session summary',
          notes: 'Session notes',
          action_items: 'Session action items',
        }
        const artifact: MockArtifact = {
          artifact_type: artifactType,
          content: this.noRetrievalSessions.has(requestedSessionId)
            ? `Generated ${artifactType} for ${requestedSessionId}.`
            : `Generated ${artifactType} from the session and project knowledge.`,
          context_tokens: 180,
          created_at: now,
          id: `artifact-${artifactType}`,
          model: 'gpt-5-mini',
          title: this.noRetrievalSessions.has(requestedSessionId)
            ? `${titles[artifactType]} ${requestedSessionId}`
            : titles[artifactType],
        }
        const scopedArtifacts = this.artifactsBySession.get(requestedSessionId)
        if (scopedArtifacts) {
          this.artifactsBySession.set(requestedSessionId, [...scopedArtifacts, artifact])
        } else {
          this.artifacts = [...this.artifacts, artifact]
        }
        await json(route, {
          artifact,
          context: {
            effective_mode: 'smart',
            estimated_tokens: 180,
            index_status: 'ready',
            rag_used: true,
            retrieval_mode: 'hybrid',
            truncated: false,
          },
          latency_ms: 25,
        }, 201)
        return
      }
      const artifactMatch = url.pathname.match(/^\/api\/ai\/artifacts\/([^/]+)$/)
      if (method === 'DELETE' && artifactMatch) {
        this.artifacts = this.artifacts.filter(({ id }) => id !== artifactMatch[1])
        await route.fulfill({ status: 204 })
        return
      }

      this.unhandled.push(`${method} ${url.pathname}`)
      await json(route, { error: `Unhandled E2E API route: ${method} ${url.pathname}` }, 501)
    })
  }
}

async function login(page: Page): Promise<void> {
  await page.goto('/pro.html')
  await page.locator('.dt-auth__card input[type="email"]').fill(user.email)
  await page.locator('.dt-auth__card input[type="password"]').fill('password123')
  await page.locator('.dt-auth__card .dt-button--primary').click()
  await expect(page.locator('.dt-sidebar')).toBeVisible()
}

async function loadSession(page: Page, title: string): Promise<void> {
  const sessionButton = page.locator('.dt-history-item__main', { hasText: title })
  await expect(sessionButton).toBeVisible()
  await sessionButton.click()
  await expect(sessionButton.locator('..')).toHaveClass(/is-active/)
}

async function loadSessionBehindSheet(page: Page, title: string): Promise<void> {
  const sessionButton = page.locator('.dt-history-item__main', { hasText: title })
  await expect(sessionButton).toBeVisible()
  await sessionButton.evaluate((element) => (element as HTMLButtonElement).click())
  await expect(sessionButton.locator('..')).toHaveClass(/is-active/)
}

async function openAssistant(page: Page): Promise<void> {
  await page.locator('.dt-sidebar__tools button', { hasText: 'AI 助手' }).click()
  await expect(page.locator('.dt-assistant')).toBeVisible()
}

async function selectAssistantTab(page: Page, index: 0 | 1 | 2): Promise<void> {
  await page.locator('.dt-assistant [role="tablist"] button').nth(index).click()
}

async function openAssistantSettings(page: Page): Promise<void> {
  const settings = page.locator('.dt-ai-settings')
  if (!await settings.evaluate((element) => (element as HTMLDetailsElement).open)) {
    await settings.locator(':scope > summary').click()
  }
  // Advanced options (context mode, preview, OCR) stay collapsed by default for UX.
  const advanced = settings.locator('.dt-ai-advanced')
  if (
    await advanced.count()
    && !await advanced.evaluate((element) => (element as HTMLDetailsElement).open)
  ) {
    await advanced.locator(':scope > summary').click()
  }
}

test('AI project workflow survives index progress reload and keeps API contracts', async ({
  page,
}) => {
  const backend = new MockAIBackend([{
    id: 'session-1',
    sourceLanguage: 'en',
    title: 'AI E2E session',
  }])
  await backend.install(page)
  page.on('dialog', (dialog) => { void dialog.accept() })

  await login(page)
  await loadSession(page, 'AI E2E session')
  await openAssistant(page)
  await selectAssistantTab(page, 2)

  const newProjectRow = page.locator('.dt-ai-memory-row').filter({
    has: page.locator('input'),
  }).first()
  await newProjectRow.locator('input').fill('Launch knowledge')
  await newProjectRow.locator('button').click()
  await expect(page.locator('.dt-ai-project-editor')).toBeVisible()

  await page.locator('.dt-ai-file-button input[type="file"]').setInputFiles({
    buffer: Buffer.from('Launch is Friday. Alice owns the release notes.'),
    mimeType: 'text/plain',
    name: 'launch-notes.txt',
  })
  await expect(page.locator('.dt-ai-source-list article')).toContainText(
    'launch-notes.txt',
  )
  await selectAssistantTab(page, 0)
  await openAssistantSettings(page)
  await page.locator('.dt-ai-reasoning [role="radio"]').filter({
    hasText: '深入',
  }).click()
  await page.locator('.dt-chat__composer textarea').fill('Who owns the release notes?')
  await page.locator('.dt-chat__composer button[type="submit"]').click()
  await expect(page.locator('.dt-ai-index-gate')).toBeVisible()
  await page.locator('.dt-ai-index-gate .dt-button--primary').click()
  await expect(page.locator('.dt-ai-index-gate progress')).toHaveAttribute('value', '50')
  await expect.poll(() => page.evaluate(() => (
    Object.keys(sessionStorage).some((key) => key.startsWith('dreamtrans:ai-index:'))
  ))).toBe(true)

  await page.reload()
  await expect(page.locator('.dt-sidebar')).toBeVisible()
  await loadSession(page, 'AI E2E session')
  backend.indexJobCanFinish = true
  await openAssistant(page)
  await expect(page.locator('.dt-chat__message--assistant')).toContainText(
    'Alice owns the release notes',
    { timeout: 10_000 },
  )
  await expect.poll(() => page.evaluate(() => (
    Object.keys(sessionStorage).some((key) => key.startsWith('dreamtrans:ai-index:'))
  ))).toBe(false)

  await selectAssistantTab(page, 1)
  for (const [buttonIndex, title] of [
    [0, 'Session summary'],
    [1, 'Session notes'],
    [2, 'Session action items'],
  ] as const) {
    await page.locator('.dt-ai-artifact-actions button').nth(buttonIndex).click()
    await expect(page.locator('.dt-ai-artifact', { hasText: title })).toBeVisible()
  }
  await expect(page.locator('.dt-ai-artifact')).toHaveCount(3)

  await page.locator('.dt-ai-artifact').first().locator('button').nth(2).click()
  await expect(page.locator('.dt-ai-artifact')).toHaveCount(2)

  await selectAssistantTab(page, 2)
  await page.locator('.dt-ai-source-list article').first().locator('button').last().click()
  await expect(page.locator('.dt-ai-source-list article')).toHaveCount(0)
  await page.locator('.dt-ai-project-editor > summary').click()
  await page.locator('.dt-ai-project-editor .dt-ai-action-row button').nth(1).click()
  await expect(page.locator('.dt-ai-project-editor')).toHaveCount(0)

  const record = (method: string, path: string) => backend.records.find(
    (candidate) => candidate.method === method && candidate.path === path,
  )
  expect(record('POST', '/api/auth/login')?.body).toMatchObject({
    email: user.email,
  })
  expect(record('POST', '/api/ai/projects')?.body).toMatchObject({
    context_mode: 'smart',
    max_context_tokens: 64_000,
    name: 'Launch knowledge',
  })
  expect(record('POST', '/api/ai/projects/project-1/sessions')?.body).toEqual({
    session_id: 'session-1',
  })
  const upload = record('POST', '/api/ai/projects/project-1/sources')
  expect(upload?.postData).toContain('name="session_id"')
  expect(upload?.postData).toContain('session-1')
  expect(upload?.postData).toContain('name="ocr_language"')
  expect(upload?.postData).toContain('eng')
  expect(upload?.postData).toContain('launch-notes.txt')

  const indexCreate = record('POST', '/api/ai/index/jobs')
  expect(indexCreate?.body).toMatchObject({
    confirmation_token: 'signed-confirmation-project',
    confirmed: true,
    project_id: 'project-1',
    session_id: 'session-1',
    target_id: 'project-1',
    target_type: 'project',
  })
  expect(indexCreate?.body?.client_request_id).toEqual(expect.any(String))

  const ask = record('POST', '/api/rag/ask')
  expect(ask?.body).toMatchObject({
    project_id: 'project-1',
    question: 'Who owns the release notes?',
    reasoning_effort: 'high',
    retrieval_preference: 'auto',
    session_id: 'session-1',
  })
  expect(ask?.body?.client_request_id).toEqual(expect.any(String))

  const artifactCreates = backend.records.filter(
    ({ method, path }) => method === 'POST' && path === '/api/ai/artifacts',
  )
  expect(artifactCreates.map(({ body }) => body?.artifact_type)).toEqual([
    'summary',
    'notes',
    'action_items',
  ])
  expect(artifactCreates.map(({ body }) => body?.reasoning_effort)).toEqual([
    'high',
    'high',
    'high',
  ])
  expect(new Set(artifactCreates.map(({ body }) => body?.client_request_id)).size).toBe(3)
  expect(record('DELETE', '/api/ai/artifacts/artifact-summary')).toBeDefined()
  expect(record('DELETE', '/api/ai/projects/project-1/sources/source-1')).toBeDefined()
  expect(record('DELETE', '/api/ai/projects/project-1')).toBeDefined()
  expect(backend.unhandled).toEqual([])
})

test('empty project falls through to session indexing and OCR follows session language', async ({
  page,
}) => {
  const backend = new MockAIBackend([
    { id: 'session-en', sourceLanguage: 'en', title: 'English session' },
    { id: 'session-ja', sourceLanguage: 'ja', title: 'Japanese session' },
  ])
  backend.emptyProjectFallsThroughToSession = true
  await backend.install(page)

  await login(page)
  await loadSession(page, 'English session')
  await openAssistant(page)
  await selectAssistantTab(page, 2)
  const newProjectRow = page.locator('.dt-ai-memory-row').filter({
    has: page.locator('input'),
  }).first()
  await newProjectRow.locator('input').fill('Empty project')
  await newProjectRow.locator('button').click()
  await expect(page.locator('.dt-ai-project-editor')).toBeVisible()

  const ocr = page.locator('.dt-ai-language-picker input[type="checkbox"]')
  await expect(ocr.nth(0)).toBeChecked()
  await expect(ocr.nth(2)).not.toBeChecked()

  await page.locator('.dt-sheet__header .dt-icon-button').click()
  await loadSession(page, 'Japanese session')
  await openAssistant(page)
  await selectAssistantTab(page, 2)
  await expect(ocr.nth(0)).not.toBeChecked()
  await expect(ocr.nth(2)).toBeChecked()

  backend.unavailableSessionIds.add('session-ja')
  await page.reload()
  await expect(page.locator('.dt-sidebar')).toBeVisible()
  await loadSession(page, 'Japanese session')
  await openAssistant(page)
  await selectAssistantTab(page, 2)
  await expect(ocr.nth(0)).not.toBeChecked()
  await expect(ocr.nth(2)).toBeChecked()

  await selectAssistantTab(page, 0)
  await page.locator('.dt-chat__composer textarea').fill('What did we decide?')
  await page.locator('.dt-chat__composer button[type="submit"]').click()
  await expect(page.locator('.dt-ai-index-gate')).toBeVisible()

  await expect.poll(() => backend.records.filter(
    ({ method, path }) => method === 'POST' && path === '/api/ai/index/preview',
  ).length).toBe(1)
  const indexPreviews = backend.records.filter(
    ({ method, path }) => method === 'POST' && path === '/api/ai/index/preview',
  )
  expect(indexPreviews.map(({ body }) => [
    body?.target_type,
    body?.target_id,
  ])).toEqual([
    ['session', 'session-ja'],
  ])

  await page.locator('.dt-ai-index-gate .dt-button--secondary').click()
  await expect(page.locator('.dt-chat__message--assistant')).toContainText(
    'Alice owns the release notes',
  )
  const ask = backend.records.find(
    ({ method, path }) => method === 'POST' && path === '/api/rag/ask',
  )
  expect(ask?.body).toMatchObject({
    project_id: 'project-1',
    retrieval_preference: 'lexical_only',
    session_id: 'session-ja',
  })
  expect(backend.unhandled).toEqual([])
})

test('late AI responses never cross owner or session scope', async ({ page }) => {
  const backend = new MockAIBackend([
    { id: 'session-a', sourceLanguage: 'en', title: 'Race session A' },
    { id: 'session-b', sourceLanguage: 'ja', title: 'Race session B' },
  ])
  const projectA: MockProject = {
    context_mode: 'smart',
    description: 'Only session A may read this.',
    id: 'project-a',
    max_context_tokens: 64_000,
    name: 'Project only A',
  }
  const projectB: MockProject = {
    context_mode: 'smart',
    description: 'Only session B may read this.',
    id: 'project-b',
    max_context_tokens: 64_000,
    name: 'Project only B',
  }
  const artifact = (session: 'a' | 'b'): MockArtifact => ({
    artifact_type: 'summary',
    content: `Stored artifact for session ${session}.`,
    context_tokens: 20,
    created_at: now,
    id: `artifact-${session}`,
    model: 'gpt-5-mini',
    title: `Artifact only ${session.toUpperCase()}`,
  })
  backend.projectsBySession.set('session-a', {
    linkedProjectId: '',
    projects: [projectA],
  })
  backend.projectsBySession.set('session-b', {
    linkedProjectId: '',
    projects: [projectB],
  })
  backend.artifactsBySession.set('session-a', [artifact('a')])
  backend.artifactsBySession.set('session-b', [artifact('b')])
  backend.noRetrievalSessions.add('session-a')
  backend.noRetrievalSessions.add('session-b')
  backend.responseDelayMs.set('projects:session-a', 600)
  await backend.install(page)

  await login(page)
  await loadSession(page, 'Race session A')
  const delayedProjects = page.waitForRequest((request) => (
    request.method() === 'GET'
    && request.url().includes('/api/ai/projects?session_id=session-a')
  ))
  await openAssistant(page)
  await delayedProjects
  await loadSessionBehindSheet(page, 'Race session B')
  await selectAssistantTab(page, 2)
  const projectSelect = page.locator('.dt-ai-field select').first()
  await expect(projectSelect.locator('option')).toContainText([
    '不关联项目',
    'Project only B',
  ])
  await page.waitForTimeout(700)
  await expect(projectSelect).not.toContainText('Project only A')

  await loadSessionBehindSheet(page, 'Race session A')
  const delayedArtifacts = page.waitForRequest((request) => (
    request.method() === 'GET'
    && request.url().includes('/api/ai/artifacts?session_id=session-a')
  ))
  await selectAssistantTab(page, 1)
  await delayedArtifacts
  await loadSessionBehindSheet(page, 'Race session B')
  await selectAssistantTab(page, 1)
  await expect(page.locator('.dt-ai-artifact')).toContainText('Artifact only B')
  await page.waitForTimeout(700)
  await expect(page.locator('.dt-ai-artifact')).not.toContainText('Artifact only A')

  await loadSessionBehindSheet(page, 'Race session A')
  await openAssistantSettings(page)
  await expect(page.getByRole('button', { name: '预览实际读取内容' })).toBeEnabled()
  backend.responseDelayMs.set('chat:session-a', 600)
  const delayedChat = page.waitForRequest((request) => (
    request.method() === 'POST'
    && new URL(request.url()).pathname === '/api/rag/ask'
    && request.postDataJSON().session_id === 'session-a'
  ))
  await page.locator('.dt-chat__composer textarea').fill('Old session question')
  await page.locator('.dt-chat__composer button[type="submit"]').click()
  await delayedChat
  await loadSessionBehindSheet(page, 'Race session B')
  await page.waitForTimeout(700)
  await expect(page.locator('.dt-chat__messages')).not.toContainText(
    'Chat answer for session-a',
  )
  const sessionBHistory = await page.evaluate(() => (
    Object.entries(localStorage)
      .filter(([key]) => key.includes('session-b') && key.includes('ai-chat'))
      .map(([, value]) => value)
      .join('\n')
  ))
  expect(sessionBHistory).not.toContain('Old session question')

  await loadSessionBehindSheet(page, 'Race session A')
  await openAssistantSettings(page)
  await expect(page.getByRole('button', { name: '预览实际读取内容' })).toBeEnabled()
  backend.responseDelayMs.set('context:session-a', 600)
  const delayedPreview = page.waitForRequest((request) => (
    request.method() === 'POST'
    && new URL(request.url()).pathname === '/api/ai/context/preview'
    && request.postDataJSON().session_id === 'session-a'
  ))
  await page.getByRole('button', { name: '预览实际读取内容' }).click()
  await delayedPreview
  await loadSessionBehindSheet(page, 'Race session B')
  await page.waitForTimeout(700)
  await expect(page.locator('.dt-ai-context-preview')).toHaveCount(0)

  backend.responseDelayMs.delete('context:session-a')
  await loadSessionBehindSheet(page, 'Race session A')
  await openAssistantSettings(page)
  await expect(page.getByRole('button', { name: '预览实际读取内容' })).toBeEnabled()
  await selectAssistantTab(page, 1)
  backend.responseDelayMs.set('artifact-create:session-a', 600)
  const delayedArtifactCreate = page.waitForRequest((request) => (
    request.method() === 'POST'
    && new URL(request.url()).pathname === '/api/ai/artifacts'
    && request.postDataJSON().session_id === 'session-a'
  ))
  await page.locator('.dt-ai-artifact-actions button').first().click()
  await delayedArtifactCreate
  await loadSessionBehindSheet(page, 'Race session B')
  await selectAssistantTab(page, 1)
  await page.waitForTimeout(700)
  await expect(page.locator('.dt-ai-artifact')).toContainText('Artifact only B')
  await expect(page.locator('.dt-ai-artifact')).not.toContainText(
    'Session summary session-a',
  )
  expect(backend.unhandled).toEqual([])
})

test('index retry conflict requires a fresh preview and client request ID', async ({
  page,
}) => {
  const backend = new MockAIBackend([{
    id: 'session-retry',
    sourceLanguage: 'en',
    title: 'Index retry session',
  }])
  backend.indexJobReturnsError = true
  backend.indexRetryReturnsConflict = true
  await backend.install(page)

  await login(page)
  await loadSession(page, 'Index retry session')
  await openAssistant(page)
  await openAssistantSettings(page)
  await expect(page.getByRole('button', { name: '预览实际读取内容' })).toBeEnabled()
  await page.locator('.dt-chat__composer textarea').fill('Retry this index safely')
  await page.locator('.dt-chat__composer button[type="submit"]').click()
  await expect(page.locator('.dt-ai-index-gate')).toBeVisible()
  await page.locator('.dt-ai-index-gate .dt-button--primary').click()
  await expect(page.locator('.dt-ai-index-gate [role="alert"]')).toContainText(
    'mock indexing failure',
    { timeout: 10_000 },
  )

  await page.locator('.dt-ai-index-gate .dt-button--primary').click()
  await expect.poll(() => backend.records.filter(
    ({ method, path }) => (
      method === 'POST'
      && path === '/api/ai/index/preview'
    ),
  ).length).toBe(2)
  await expect(page.locator('.dt-ai-index-gate progress')).toHaveCount(0)

  await page.locator('.dt-ai-index-gate .dt-button--primary').click()
  await expect(page.locator('.dt-chat__message--assistant')).toContainText(
    'Alice owns the release notes',
    { timeout: 10_000 },
  )
  const indexCreates = backend.records.filter(
    ({ method, path }) => method === 'POST' && path === '/api/ai/index/jobs',
  )
  expect(indexCreates).toHaveLength(2)
  expect(indexCreates[0]?.body?.client_request_id).toEqual(expect.any(String))
  expect(indexCreates[1]?.body?.client_request_id).toEqual(expect.any(String))
  expect(indexCreates[1]?.body?.client_request_id).not.toBe(
    indexCreates[0]?.body?.client_request_id,
  )
  expect(backend.records.some(({ method, path }) => (
    method === 'POST'
    && path === '/api/ai/index/jobs/job-session/retry'
  ))).toBe(true)
  expect(backend.unhandled).toEqual([])
})
