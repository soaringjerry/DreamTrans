import type { CSSProperties } from 'react'
import type { CourseSlot } from '../api'
import { useMessages } from '../i18n'
import { browserTimezone, hueOf, layoutTimetable } from './timetable'

interface WeekCalendarProps {
  slots: CourseSlot[]
  /** Course names by id, for slots that do not carry one. */
  courseNames?: Map<string, string>
}

/** 每周课表: seven columns, one block per class time, coloured by course. */
export function WeekCalendar({ slots, courseNames }: WeekCalendarProps) {
  const t = useMessages().study.view.timetable
  const layout = layoutTimetable(slots)
  const localZone = browserTimezone()
  return (
    <div className="dt-cal__wrap">
      <div className="dt-cal" role="table" aria-label={t.calendarTitle}>
        <div className="dt-cal__head" role="row">
          <span className="dt-cal__corner" />
          {t.weekdays.map((weekday) => (
            <span className="dt-cal__day" key={weekday} role="columnheader">{weekday}</span>
          ))}
        </div>
        <div className="dt-cal__body" style={{ '--hours': layout.hours.length } as CSSProperties}>
          <div className="dt-cal__axis">
            {layout.hours.map((hour) => (
              <span key={hour}>{t.hour(hour)}</span>
            ))}
          </div>
          {t.weekdays.map((weekday, day) => (
            <div className="dt-cal__col" key={weekday} role="cell">
              {layout.hours.map((hour) => <i className="dt-cal__line" key={hour} />)}
              {layout.blocks.filter((block) => block.day === day).map((block) => {
                const name = block.slot.project_name ?? courseNames?.get(block.slot.project_id) ?? ''
                const foreignZone = block.slot.timezone !== localZone
                return (
                  <div
                    className="dt-cal__block"
                    key={block.slot.id}
                    style={{
                      '--hue': hueOf(block.slot.project_id),
                      top: `${block.top}%`,
                      height: `${block.height}%`,
                      left: `${block.left}%`,
                      width: `${block.width}%`,
                    } as CSSProperties}
                    title={`${name} · ${weekday} ${block.slot.start}–${block.slot.end}${block.slot.label ? ` · ${block.slot.label}` : ''}${foreignZone ? ` · ${t.timezone(block.slot.timezone)}` : ''}`}
                  >
                    <strong>{name}</strong>
                    <span>{block.slot.start}–{block.slot.end}</span>
                    {block.slot.label && <small>{block.slot.label}</small>}
                  </div>
                )
              })}
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}
