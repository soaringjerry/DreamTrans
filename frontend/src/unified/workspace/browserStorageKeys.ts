const CHAT_HISTORY_PREFIX = 'dt_unified_chat_v2'
const LEGACY_CHAT_HISTORY_PREFIX = 'dt_unified_chat'
const AI_REASONING_PREFIX = 'dt_unified_ai_reasoning_v1'

function encodeKeyPart(value: string): string {
  return encodeURIComponent(value)
}

export function chatHistoryKey(
  ownerId: string | null,
  sessionId: string,
): string {
  const ownerScope = ownerId === null ? 'anonymous' : `user:${ownerId}`
  return `${CHAT_HISTORY_PREFIX}_${encodeKeyPart(ownerScope)}_${encodeKeyPart(sessionId)}`
}

export function legacyChatHistoryKey(sessionId: string): string {
  return `${LEGACY_CHAT_HISTORY_PREFIX}_${sessionId}`
}

export function aiReasoningPreferenceKey(ownerId: string | null): string {
  const ownerScope = ownerId === null ? 'anonymous' : `user:${ownerId}`
  return `${AI_REASONING_PREFIX}_${encodeKeyPart(ownerScope)}`
}
