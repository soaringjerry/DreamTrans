import { ref, computed, readonly } from 'vue'
import type { Session, SessionWithTranscripts, Transcript, TranscriptInput } from '../api/auth'
import * as authApi from '../api/auth'

// Global state
const sessions = ref<Session[]>([])
const currentSession = ref<SessionWithTranscripts | null>(null)
const loading = ref(false)
const saving = ref(false)
const error = ref<string | null>(null)

// Every queued item carries its owning cloud session. This prevents a delayed
// batch from being written into whichever session happens to be current later.
const pendingTranscripts = ref<Array<{
  sessionId: string
  transcript: TranscriptInput
}>>([])

let saveTimeout: ReturnType<typeof setTimeout> | null = null
let flushChain: Promise<void> = Promise.resolve()
let stateGeneration = 0
const BATCH_SAVE_DELAY = 2000 // Save every 2 seconds

function mergeSavedTranscripts(existing: Transcript[], saved: Transcript[]): Transcript[] {
  const merged = [...existing]
  for (const transcript of saved) {
    const index = merged.findIndex((item) =>
      item.client_segment_id === transcript.client_segment_id || item.id === transcript.id
    )
    if (index === -1) merged.push(transcript)
    else merged[index] = transcript
  }
  return merged
}

function compactPending(
  items: Array<{ sessionId: string; transcript: TranscriptInput }>,
): Array<{ sessionId: string; transcript: TranscriptInput }> {
  const compacted: Array<{ sessionId: string; transcript: TranscriptInput }> = []
  const indexes = new Map<string, number>()

  for (const item of items) {
    const key = `${item.sessionId}:${item.transcript.client_segment_id}`
    const existingIndex = indexes.get(key)
    if (existingIndex === undefined) {
      indexes.set(key, compacted.length)
      compacted.push(item)
    } else {
      compacted[existingIndex] = {
        sessionId: item.sessionId,
        transcript: {
          ...compacted[existingIndex].transcript,
          ...item.transcript,
        },
      }
    }
  }

  return compacted
}

export function resetCloudSessionState(): void {
  stateGeneration++
  if (saveTimeout) {
    clearTimeout(saveTimeout)
    saveTimeout = null
  }
  sessions.value = []
  currentSession.value = null
  pendingTranscripts.value = []
  loading.value = false
  saving.value = false
  error.value = null
  // New-account work should not wait for a request owned by the account that
  // just logged out. Generation guards below keep late responses quarantined.
  flushChain = Promise.resolve()
}

if (typeof window !== 'undefined') {
  window.addEventListener('dt-auth-cleared', resetCloudSessionState)
}

export function useCloudSession() {
  const hasSession = computed(() => !!currentSession.value)
  const sessionId = computed(() => currentSession.value?.id || '')
  const isActive = computed(() => currentSession.value?.status === 'active')

  // Load user's sessions
  async function loadSessions(page = 1, pageSize = 20): Promise<void> {
    const generation = stateGeneration
    loading.value = true
    error.value = null

    try {
      const result = await authApi.listSessions(page, pageSize)
      if (generation !== stateGeneration) return
      sessions.value = result.sessions || []
    } catch (e) {
      if (generation !== stateGeneration) return
      error.value = e instanceof Error ? e.message : 'Failed to load sessions'
      throw e
    } finally {
      if (generation === stateGeneration) loading.value = false
    }
  }

  // Create a new session
  async function createSession(data?: {
    title?: string
    source_language?: string
    target_language?: string
  }): Promise<Session> {
    const generation = stateGeneration
    loading.value = true
    error.value = null

    try {
      if (currentSession.value?.id) {
        await flushTranscripts(currentSession.value.id)
      }
      if (generation !== stateGeneration) {
        throw new Error('Authentication state changed while creating the session')
      }
      const session = await authApi.createSession(data)
      if (generation !== stateGeneration) {
        throw new Error('Authentication state changed while creating the session')
      }
      currentSession.value = { ...session, transcripts: [] }
      sessions.value = [session, ...sessions.value]
      return session
    } catch (e) {
      if (generation === stateGeneration) {
        error.value = e instanceof Error ? e.message : 'Failed to create session'
      }
      throw e
    } finally {
      if (generation === stateGeneration) loading.value = false
    }
  }

  // Load a specific session
  async function loadSession(id: string): Promise<void> {
    const generation = stateGeneration
    loading.value = true
    error.value = null

    try {
      if (currentSession.value?.id && currentSession.value.id !== id) {
        await flushTranscripts(currentSession.value.id)
      }
      if (generation !== stateGeneration) return
      const session = await authApi.getSession(id)
      if (generation !== stateGeneration) return
      currentSession.value = session
    } catch (e) {
      if (generation !== stateGeneration) return
      error.value = e instanceof Error ? e.message : 'Failed to load session'
      throw e
    } finally {
      if (generation === stateGeneration) loading.value = false
    }
  }

  // Update current session
  async function updateCurrentSession(data: {
    title?: string
    status?: 'active' | 'paused' | 'completed' | 'archived'
    duration_seconds?: number
  }): Promise<void> {
    if (!currentSession.value) return
    const generation = stateGeneration
    const updatingSessionId = currentSession.value.id

    try {
      const updated = await authApi.updateSession(updatingSessionId, data)
      if (generation !== stateGeneration) return
      if (currentSession.value?.id === updatingSessionId) {
        currentSession.value = { ...currentSession.value, ...updated }
      }
      // Update in sessions list
      const idx = sessions.value.findIndex(s => s.id === updated.id)
      if (idx !== -1) {
        sessions.value[idx] = updated
      }
    } catch (e) {
      if (generation !== stateGeneration) return
      error.value = e instanceof Error ? e.message : 'Failed to update session'
      throw e
    }
  }

  // Delete a session
  async function deleteSession(id: string): Promise<void> {
    const generation = stateGeneration
    try {
      await authApi.deleteSession(id)
      if (generation !== stateGeneration) return
      pendingTranscripts.value = pendingTranscripts.value.filter((item) => item.sessionId !== id)
      sessions.value = sessions.value.filter(s => s.id !== id)
      if (currentSession.value?.id === id) {
        currentSession.value = null
      }
    } catch (e) {
      if (generation !== stateGeneration) return
      error.value = e instanceof Error ? e.message : 'Failed to delete session'
      throw e
    }
  }

  // Add transcript to pending queue
  function queueTranscript(transcript: TranscriptInput): void {
    const owningSessionId = currentSession.value?.id
    if (!owningSessionId) {
      const queueError = new Error('Cannot save transcript without an active cloud session')
      error.value = queueError.message
      throw queueError
    }
    pendingTranscripts.value = compactPending([
      ...pendingTranscripts.value,
      { sessionId: owningSessionId, transcript },
    ])
    scheduleBatchSave()
  }

  // Schedule batch save
  function scheduleBatchSave(): void {
    if (saveTimeout) return
    saveTimeout = setTimeout(() => {
      saveTimeout = null
      void flushTranscripts().catch((e) => {
        console.error('Failed to save transcript batch:', e)
      })
    }, BATCH_SAVE_DELAY)
  }

  // Flush pending transcripts to server
  function flushTranscripts(sessionId = currentSession.value?.id): Promise<void> {
    if (saveTimeout) {
      clearTimeout(saveTimeout)
      saveTimeout = null
    }

    if (!sessionId) return flushChain

    const operation = flushChain.then(async () => {
      const generation = stateGeneration
      const queuedForSession = pendingTranscripts.value.filter((item) => item.sessionId === sessionId)
      if (queuedForSession.length === 0) return

      pendingTranscripts.value = pendingTranscripts.value.filter((item) => item.sessionId !== sessionId)
      saving.value = true

      try {
        const result = await authApi.saveTranscriptsBatch(
          sessionId,
          queuedForSession.map((item) => item.transcript),
        )
        if (generation !== stateGeneration) return
        // Only mutate the loaded session when the response belongs to it.
        if (currentSession.value?.id === sessionId) {
          currentSession.value.transcripts = mergeSavedTranscripts(
            currentSession.value.transcripts,
            result.saved,
          )
        }
        error.value = null
      } catch (e) {
        if (generation !== stateGeneration) return
        pendingTranscripts.value = compactPending([
          ...queuedForSession,
          ...pendingTranscripts.value,
        ])
        error.value = e instanceof Error ? e.message : 'Failed to save transcripts'
        throw e
      } finally {
        if (generation === stateGeneration) saving.value = false
      }
    })
    flushChain = operation.catch(() => undefined)
    return operation
  }

  // Save a single transcript immediately
  async function saveTranscript(transcript: TranscriptInput): Promise<Transcript | null> {
    if (!currentSession.value) return null
    const generation = stateGeneration
    const savingSessionId = currentSession.value.id

    saving.value = true
    try {
      const saved = await authApi.saveTranscript(savingSessionId, transcript)
      if (generation !== stateGeneration) return null
      if (currentSession.value?.id === savingSessionId) {
        currentSession.value.transcripts = mergeSavedTranscripts(
          currentSession.value.transcripts,
          [saved],
        )
      }
      return saved
    } catch (e) {
      if (generation !== stateGeneration) return null
      error.value = e instanceof Error ? e.message : 'Failed to save transcript'
      throw e
    } finally {
      if (generation === stateGeneration) saving.value = false
    }
  }

  // Export session
  async function exportSession(
    format: 'json' | 'txt' | 'srt' = 'json'
  ): Promise<void> {
    if (!currentSession.value) return
    const generation = stateGeneration
    const exportingSession = currentSession.value

    try {
      const blob = await authApi.exportSession(exportingSession.id, format)
      if (generation !== stateGeneration) return
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${exportingSession.title || 'session'}.${format}`
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
    } catch (e) {
      if (generation !== stateGeneration) return
      error.value = e instanceof Error ? e.message : 'Export failed'
      throw e
    }
  }

  // End current session
  async function endSession(): Promise<void> {
    if (!currentSession.value) return
    const generation = stateGeneration
    const endingSessionId = currentSession.value.id

    // Flush any pending transcripts
    await flushTranscripts(endingSessionId)
    if (generation !== stateGeneration) return

    // Update status to completed
    await updateCurrentSession({ status: 'completed' })
    if (currentSession.value?.id === endingSessionId) {
      currentSession.value = null
    }
  }

  // Clear current session (without saving)
  function clearSession(): void {
    const clearingSessionId = currentSession.value?.id
    currentSession.value = null
    if (clearingSessionId) {
      pendingTranscripts.value = pendingTranscripts.value.filter((item) => item.sessionId !== clearingSessionId)
    }
    if (saveTimeout) {
      clearTimeout(saveTimeout)
      saveTimeout = null
    }
  }

  // Clear error
  function clearError(): void {
    error.value = null
  }

  // Get transcript counts
  const transcriptCount = computed(() => currentSession.value?.transcripts.length || 0)
  const pendingCount = computed(() => pendingTranscripts.value.length)

  return {
    // State (readonly)
    sessions: readonly(sessions),
    currentSession: readonly(currentSession),
    loading: readonly(loading),
    saving: readonly(saving),
    error: readonly(error),

    // Computed
    hasSession,
    sessionId,
    isActive,
    transcriptCount,
    pendingCount,

    // Actions
    loadSessions,
    createSession,
    loadSession,
    updateCurrentSession,
    deleteSession,
    queueTranscript,
    saveTranscript,
    flushTranscripts,
    exportSession,
    endSession,
    clearSession,
    clearError,
  }
}
