import { useCallback, useEffect, useRef, useState } from 'react'
import {
  readOnboardingRecord,
  resolveInitialPhase,
  writeOnboardingRecord,
  type OnboardingPhase,
} from '../workspace/onboardingState'
import type { RecorderStatus } from '../components/RecorderBar'

interface UseOnboardingInput {
  ownerId: string | null
  historyLoading: boolean
  historyCount: number
  recorderStatus: RecorderStatus
}

export interface OnboardingController {
  phase: OnboardingPhase
  /** Show the setup wizard again (from settings or the empty state). */
  openWizard: () => void
  /** Show the interface tour again. */
  openTour: () => void
  /** Wizard finished; optionally continue into the tour. */
  finishWizard: (next: 'tour' | 'close') => void
  finishTour: () => void
}

/**
 * How long to wait for the first history load before treating the list as
 * settled. The load normally flips `historyLoading` within a frame; this only
 * guards against a repository that never reports loading at all.
 */
const HISTORY_SETTLE_FALLBACK_MS = 1_500

export function useOnboarding({
  ownerId,
  historyLoading,
  historyCount,
  recorderStatus,
}: UseOnboardingInput): OnboardingController {
  const [phase, setPhase] = useState<OnboardingPhase>('pending')
  const [historySettled, setHistorySettled] = useState(false)
  const seenLoadingRef = useRef(false)
  const decidedOwnerRef = useRef<string | null | undefined>(undefined)

  // A new owner (login, logout, account switch) restarts the decision.
  useEffect(() => {
    if (decidedOwnerRef.current === ownerId) return
    decidedOwnerRef.current = ownerId
    seenLoadingRef.current = false
    setHistorySettled(false)
    setPhase('pending')
  }, [ownerId])

  useEffect(() => {
    if (historyLoading) {
      seenLoadingRef.current = true
      return
    }
    if (seenLoadingRef.current) {
      setHistorySettled(true)
      return
    }
    const timer = window.setTimeout(() => setHistorySettled(true), HISTORY_SETTLE_FALLBACK_MS)
    return () => window.clearTimeout(timer)
  }, [historyLoading, ownerId])

  useEffect(() => {
    if (phase !== 'pending') return
    const resolved = resolveInitialPhase({
      record: readOnboardingRecord(ownerId),
      historySettled,
      historyCount,
      recorderIdle: recorderStatus === 'idle',
    })
    if (resolved === 'pending') return
    if (resolved === 'auto-complete') {
      writeOnboardingRecord(ownerId, {
        ...readOnboardingRecord(ownerId),
        wizardCompletedAt: Date.now(),
        tourCompletedAt: Date.now(),
      })
      setPhase('done')
      return
    }
    setPhase(resolved)
  }, [phase, ownerId, historySettled, historyCount, recorderStatus])

  const openWizard = useCallback(() => setPhase('wizard'), [])
  const openTour = useCallback(() => setPhase('tour'), [])

  const finishWizard = useCallback((next: 'tour' | 'close') => {
    writeOnboardingRecord(ownerId, {
      ...readOnboardingRecord(ownerId),
      wizardCompletedAt: Date.now(),
    })
    setPhase(next === 'tour' ? 'tour' : 'done')
  }, [ownerId])

  const finishTour = useCallback(() => {
    writeOnboardingRecord(ownerId, {
      ...readOnboardingRecord(ownerId),
      tourCompletedAt: Date.now(),
    })
    setPhase('done')
  }, [ownerId])

  return { phase, openWizard, openTour, finishWizard, finishTour }
}
