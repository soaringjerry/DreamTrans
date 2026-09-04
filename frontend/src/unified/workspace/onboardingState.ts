/**
 * First-run onboarding bookkeeping. One record per owner scope (anonymous or
 * a user id) so a shared browser still greets each new account once, while a
 * returning account that already has sessions is never interrupted.
 */

const ONBOARDING_KEY_PREFIX = 'dt_onboarding_v1'

export interface OnboardingRecord {
  /** Set once the setup wizard was finished or skipped. */
  wizardCompletedAt?: number
  /** Set once the interface tour was finished or skipped. */
  tourCompletedAt?: number
}

export type OnboardingPhase = 'pending' | 'wizard' | 'tour' | 'done'

export function onboardingStorageKey(ownerId: string | null): string {
  const scope = ownerId === null ? 'anonymous' : `user:${ownerId}`
  return `${ONBOARDING_KEY_PREFIX}_${encodeURIComponent(scope)}`
}

export function readOnboardingRecord(
  ownerId: string | null,
  storage: Pick<Storage, 'getItem'> | null = safeStorage(),
): OnboardingRecord {
  if (!storage) return {}
  try {
    const raw = storage.getItem(onboardingStorageKey(ownerId))
    if (!raw) return {}
    const parsed = JSON.parse(raw) as Partial<OnboardingRecord>
    return {
      ...(typeof parsed.wizardCompletedAt === 'number'
        ? { wizardCompletedAt: parsed.wizardCompletedAt }
        : {}),
      ...(typeof parsed.tourCompletedAt === 'number'
        ? { tourCompletedAt: parsed.tourCompletedAt }
        : {}),
    }
  } catch {
    return {}
  }
}

export function writeOnboardingRecord(
  ownerId: string | null,
  record: OnboardingRecord,
  storage: Pick<Storage, 'setItem'> | null = safeStorage(),
): void {
  if (!storage) return
  try {
    storage.setItem(onboardingStorageKey(ownerId), JSON.stringify(record))
  } catch {
    // Private mode or quota: the wizard simply shows again next time.
  }
}

export interface InitialPhaseInput {
  record: OnboardingRecord
  /** The history list has been loaded at least once for this owner. */
  historySettled: boolean
  historyCount: number
  recorderIdle: boolean
}

/**
 * Decides what a freshly opened workspace should do. The wizard only appears
 * for owners with no saved sessions at all; anyone with history is treated as
 * a returning user and marked complete without being shown anything.
 */
export function resolveInitialPhase(input: InitialPhaseInput): OnboardingPhase | 'auto-complete' {
  if (input.record.wizardCompletedAt) return 'done'
  if (!input.historySettled || !input.recorderIdle) return 'pending'
  if (input.historyCount > 0) return 'auto-complete'
  return 'wizard'
}

function safeStorage(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}
