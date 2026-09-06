import { useCallback, useId, useRef, useState } from 'react'
import type { TrainingProgramInfo } from '../../api'
import { useMessages } from '../../i18n'
import { useDialogFocusTrap } from '../hooks/useDialogFocusTrap'
import { Icon } from './Icon'
import { TrainingProgramChoice } from './TrainingProgramChoice'

interface TrainingProgramDialogProps {
  program: TrainingProgramInfo
  onAnswer: (optIn: boolean) => Promise<boolean>
  onDismiss: () => void
}

/**
 * One-time question for accounts that finished onboarding before the training
 * programme existed. Picking an answer saves it and closes the dialog; "later"
 * hides it, and Settings keeps the switch available.
 */
export function TrainingProgramDialog({ program, onAnswer, onDismiss }: TrainingProgramDialogProps) {
  const m = useMessages()
  const t = m.onboarding.training
  const dialogRef = useRef<HTMLElement>(null)
  const titleId = useId()
  const [pending, setPending] = useState<boolean | null>(null)
  const [failed, setFailed] = useState(false)
  useDialogFocusTrap(dialogRef, onDismiss)

  const choose = useCallback(async (optIn: boolean) => {
    setPending(optIn)
    setFailed(false)
    const saved = await onAnswer(optIn)
    if (!saved) {
      setPending(null)
      setFailed(true)
    }
  }, [onAnswer])

  return (
    <div className="dt-onboarding-backdrop">
      <section
        aria-labelledby={titleId}
        aria-modal="true"
        className="dt-onboarding"
        data-step="training"
        ref={dialogRef}
        role="dialog"
        tabIndex={-1}
      >
        <header className="dt-onboarding__header">
          <span />
          <button aria-label={m.common.close} className="dt-icon-button" onClick={onDismiss} type="button">
            <Icon name="close" />
          </button>
        </header>
        <div className="dt-onboarding__body">
          <p className="dt-eyebrow">{t.promptEyebrow}</p>
          <h2 id={titleId}>{t.title(program.discountPercent)}</h2>
          <p className="dt-onboarding__lead">{t.lead}</p>
          <TrainingProgramChoice
            busy={pending !== null}
            onChange={(optIn) => void choose(optIn)}
            program={program}
            value={pending}
          />
          {failed && <div className="dt-form-error" role="alert">{t.saveFailed}</div>}
        </div>
        <footer className="dt-onboarding__footer">
          <button className="dt-button dt-button--text" onClick={onDismiss} type="button">
            {t.later}
          </button>
        </footer>
      </section>
    </div>
  )
}
