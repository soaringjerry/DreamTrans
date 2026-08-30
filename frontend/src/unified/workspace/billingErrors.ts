/**
 * Insufficient-balance detection shared by every error surface. The backend
 * answers HTTP 402 with `insufficient balance`, the translation WebSocket
 * sends `type: "insufficient_balance"`, the Speechmatics proxy reports
 * `balance is insufficient`, and preflight copy already says 余额不足.
 */
const BALANCE_MARKERS = [
  'insufficient balance',
  'balance is insufficient',
  'insufficient_balance',
  '余额不足',
]

export const INSUFFICIENT_BALANCE_MESSAGE = '余额不足，请先充值后再继续使用。'

export function isInsufficientBalanceMessage(message: string | null | undefined): boolean {
  if (!message) return false
  const normalized = message.toLowerCase()
  return BALANCE_MARKERS.some((marker) => normalized.includes(marker))
}

export function isInsufficientBalanceError(reason: unknown): boolean {
  if (!reason) return false
  if (typeof reason === 'object' && 'status' in reason && (reason as { status?: unknown }).status === 402) {
    return true
  }
  if (reason instanceof Error) return isInsufficientBalanceMessage(reason.message)
  return typeof reason === 'string' && isInsufficientBalanceMessage(reason)
}
