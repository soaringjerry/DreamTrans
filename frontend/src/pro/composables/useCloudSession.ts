import { ref, computed, readonly } from 'vue'
import type { Session, SessionWithTranscripts, Transcript } from '../api/auth'
import * as authApi from '../api/auth'

// Global state
const sessions = ref<Session[]>([])
const currentSession = ref<SessionWithTranscripts | null>(null)
const loading = ref(false)
const saving = ref(false)
const error = ref<string | null>(null)

// Pending transcripts queue for batch saving
const pendingTranscripts = ref<Array<{
  speaker?: string
  text: string
  translation?: string
  start_time: number
  end_time?: number
  status?: 'partial' | 'confirmed' | 'translated'
  is_partial?: boolean
}>>([])

let saveTimeout: ReturnType<typeof setTimeout> | null = null
const BATCH_SAVE_DELAY = 2000 // Save every 2 seconds

export function useCloudSession() {
  const hasSession = computed(() => !!currentSession.value)
  const sessionId = computed(() => currentSession.value?.id || '')
  const isActive = computed(() => currentSession.value?.status === 'active')

  // Load user's sessions
  async function loadSessions(page = 1, pageSize = 20): Promise<void> {
    loading.value = true
    error.value = null

    try {
      const result = await authApi.listSessions(page, pageSize)
      sessions.value = result.sessions || []
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load sessions'
      throw e
    } finally {
      loading.value = false
    }
  }

  // Create a new session
  async function createSession(data?: {
    title?: string
    source_language?: string
    target_language?: string
  }): Promise<Session> {
    loading.value = true
    error.value = null

    try {
      const session = await authApi.createSession(data)
      currentSession.value = { ...session, transcripts: [] }
      sessions.value = [session, ...sessions.value]
      return session
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to create session'
      throw e
    } finally {
      loading.value = false
    }
  }

  // Load a specific session
  async function loadSession(id: string): Promise<void> {
    loading.value = true
    error.value = null

    try {
      const session = await authApi.getSession(id)
      currentSession.value = session
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load session'
      throw e
    } finally {
      loading.value = false
    }
  }

  // Update current session
  async function updateCurrentSession(data: {
    title?: string
    status?: 'active' | 'paused' | 'completed' | 'archived'
    duration_seconds?: number
  }): Promise<void> {
    if (!currentSession.value) return

    try {
      const updated = await authApi.updateSession(currentSession.value.id, data)
      currentSession.value = { ...currentSession.value, ...updated }
      // Update in sessions list
      const idx = sessions.value.findIndex(s => s.id === updated.id)
      if (idx !== -1) {
        sessions.value[idx] = updated
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to update session'
      throw e
    }
  }

  // Delete a session
  async function deleteSession(id: string): Promise<void> {
    try {
      await authApi.deleteSession(id)
      sessions.value = sessions.value.filter(s => s.id !== id)
      if (currentSession.value?.id === id) {
        currentSession.value = null
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to delete session'
      throw e
    }
  }

  // Add transcript to pending queue
  function queueTranscript(transcript: {
    speaker?: string
    text: string
    translation?: string
    start_time: number
    end_time?: number
    status?: 'partial' | 'confirmed' | 'translated'
    is_partial?: boolean
  }): void {
    pendingTranscripts.value.push(transcript)
    scheduleBatchSave()
  }

  // Schedule batch save
  function scheduleBatchSave(): void {
    if (saveTimeout) return
    saveTimeout = setTimeout(flushTranscripts, BATCH_SAVE_DELAY)
  }

  // Flush pending transcripts to server
  async function flushTranscripts(): Promise<void> {
    if (saveTimeout) {
      clearTimeout(saveTimeout)
      saveTimeout = null
    }

    if (!currentSession.value || pendingTranscripts.value.length === 0) return

    const toSave = [...pendingTranscripts.value]
    pendingTranscripts.value = []
    saving.value = true

    try {
      const result = await authApi.saveTranscriptsBatch(currentSession.value.id, toSave)
      // Add to current session's transcripts
      if (currentSession.value) {
        currentSession.value.transcripts = [
          ...currentSession.value.transcripts,
          ...result.saved,
        ]
      }
    } catch (e) {
      // Put back failed transcripts
      pendingTranscripts.value = [...toSave, ...pendingTranscripts.value]
      error.value = e instanceof Error ? e.message : 'Failed to save transcripts'
    } finally {
      saving.value = false
    }
  }

  // Save a single transcript immediately
  async function saveTranscript(transcript: {
    speaker?: string
    text: string
    translation?: string
    start_time: number
    end_time?: number
    status?: 'partial' | 'confirmed' | 'translated'
    is_partial?: boolean
  }): Promise<Transcript | null> {
    if (!currentSession.value) return null

    saving.value = true
    try {
      const saved = await authApi.saveTranscript(currentSession.value.id, transcript)
      if (currentSession.value) {
        currentSession.value.transcripts.push(saved)
      }
      return saved
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to save transcript'
      throw e
    } finally {
      saving.value = false
    }
  }

  // Export session
  async function exportSession(
    format: 'json' | 'txt' | 'srt' = 'json'
  ): Promise<void> {
    if (!currentSession.value) return

    try {
      const blob = await authApi.exportSession(currentSession.value.id, format)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `${currentSession.value.title || 'session'}.${format}`
      document.body.appendChild(a)
      a.click()
      document.body.removeChild(a)
      URL.revokeObjectURL(url)
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Export failed'
      throw e
    }
  }

  // End current session
  async function endSession(): Promise<void> {
    if (!currentSession.value) return

    // Flush any pending transcripts
    await flushTranscripts()

    // Update status to completed
    await updateCurrentSession({ status: 'completed' })
    currentSession.value = null
  }

  // Clear current session (without saving)
  function clearSession(): void {
    currentSession.value = null
    pendingTranscripts.value = []
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
