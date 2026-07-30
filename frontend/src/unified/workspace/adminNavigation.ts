import type { RecorderStatus } from '../components/RecorderBar'

export type AdminNavigationState = 'hidden' | 'enabled' | 'disabled'

export function adminNavigationState(
  role: string | undefined,
  recorderStatus: RecorderStatus,
): AdminNavigationState {
  if (role !== 'admin' && role !== 'super_admin') return 'hidden'
  return recorderStatus === 'idle' ? 'enabled' : 'disabled'
}
