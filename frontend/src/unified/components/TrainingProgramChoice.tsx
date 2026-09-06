import type { TrainingProgramInfo } from '../../api'
import { useMessages } from '../../i18n'
import { Icon } from './Icon'

interface TrainingProgramChoiceProps {
  program: TrainingProgramInfo
  /** null = not answered yet. */
  value: boolean | null
  busy?: boolean
  onChange: (optIn: boolean) => void
}

/**
 * The two answers to the Speechmatics training programme, as radio cards. The
 * join card carries the discount; the decline card makes clear it costs
 * nothing but the discount. Both are always available, so the choice is real.
 */
export function TrainingProgramChoice({ program, value, busy = false, onChange }: TrainingProgramChoiceProps) {
  const t = useMessages().onboarding.training
  const choices = [
    { value: true, icon: 'sparkles' as const, title: t.join.title(program.discountPercent), blurb: t.join.blurb },
    { value: false, icon: 'shield' as const, title: t.decline.title, blurb: t.decline.blurb },
  ]
  return (
    <>
      <div aria-label={t.groupAria} className="dt-onboarding__choices" role="radiogroup">
        {choices.map((choice) => {
          const selected = value === choice.value
          return (
            <button
              aria-checked={selected}
              className={`dt-onboarding__choice${selected ? ' is-selected' : ''}`}
              disabled={busy}
              key={String(choice.value)}
              onClick={() => onChange(choice.value)}
              role="radio"
              type="button"
            >
              <span className="dt-onboarding__choice-icon"><Icon name={choice.icon} size={20} /></span>
              <span className="dt-onboarding__choice-body">
                <strong>{choice.title}</strong>
                <small>{choice.blurb}</small>
              </span>
              <span aria-hidden="true" className="dt-onboarding__choice-check">
                {selected && <Icon name="check" size={14} />}
              </span>
            </button>
          )
        })}
      </div>
      <p className="dt-onboarding__note">
        {t.note.before}
        <a href="/privacy#share" rel="noreferrer" target="_blank">{t.note.link}</a>
        {t.note.after}
      </p>
    </>
  )
}
