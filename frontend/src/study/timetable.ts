import type { CourseSlot } from '../api'

/**
 * 课表: pure layout for the weekly calendar. Slots become blocks positioned
 * as percentages of a visible hour range; overlapping blocks on the same day
 * share the column width. Times are wall-clock in each slot's own zone; the
 * calendar draws them as written, which is what a student expects to see.
 */

export interface CalendarBlock {
  slot: CourseSlot
  /** 0-based column, Monday first. */
  day: number
  /** Percent offsets inside the day column. */
  top: number
  height: number
  left: number
  width: number
  startMinute: number
  endMinute: number
}

export interface CalendarLayout {
  /** First and last hour on the axis (end exclusive). */
  startHour: number
  endHour: number
  hours: number[]
  blocks: CalendarBlock[]
}

export function minutesOf(time: string): number {
  const [hours, minutes] = time.split(':').map((part) => Number.parseInt(part, 10))
  if (!Number.isFinite(hours) || !Number.isFinite(minutes)) return 0
  return hours * 60 + minutes
}

/** A stable hue per course id so the calendar and the cards agree. */
export function hueOf(text: string): number {
  let hash = 0
  for (const char of text) hash = (hash * 31 + char.charCodeAt(0)) % 360
  return hash
}

/** The browser's IANA zone, or UTC when it cannot say. */
export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

export function layoutTimetable(slots: CourseSlot[]): CalendarLayout {
  const valid = slots
    .map((slot) => ({ slot, startMinute: minutesOf(slot.start), endMinute: minutesOf(slot.end) }))
    .filter(({ slot, startMinute, endMinute }) => slot.weekday >= 1 && slot.weekday <= 7 && endMinute > startMinute)
  // Default working hours; widen to whatever the slots need, whole hours.
  let startHour = 8
  let endHour = 18
  for (const { startMinute, endMinute } of valid) {
    startHour = Math.min(startHour, Math.floor(startMinute / 60))
    endHour = Math.max(endHour, Math.ceil(endMinute / 60))
  }
  endHour = Math.max(endHour, startHour + 4)
  const span = (endHour - startHour) * 60
  const hours: number[] = []
  for (let hour = startHour; hour < endHour; hour++) hours.push(hour)

  const blocks: CalendarBlock[] = []
  for (let day = 0; day < 7; day++) {
    const today = valid
      .filter(({ slot }) => slot.weekday === day + 1)
      .sort((left, right) => left.startMinute - right.startMinute || left.endMinute - right.endMinute)
    // Greedy lanes inside each cluster of mutually overlapping slots.
    let cluster: Array<{ entry: typeof today[number]; lane: number }> = []
    let clusterEnd = -1
    const flush = () => {
      const lanes = cluster.reduce((max, { lane }) => Math.max(max, lane + 1), 0)
      for (const { entry, lane } of cluster) {
        blocks.push({
          slot: entry.slot,
          day,
          top: ((entry.startMinute - startHour * 60) / span) * 100,
          height: ((entry.endMinute - entry.startMinute) / span) * 100,
          left: (lane / lanes) * 100,
          width: 100 / lanes,
          startMinute: entry.startMinute,
          endMinute: entry.endMinute,
        })
      }
      cluster = []
    }
    for (const entry of today) {
      if (cluster.length > 0 && entry.startMinute >= clusterEnd) flush()
      const busy = new Set(cluster
        .filter(({ entry: other }) => other.endMinute > entry.startMinute)
        .map(({ lane }) => lane))
      let lane = 0
      while (busy.has(lane)) lane++
      cluster.push({ entry, lane })
      clusterEnd = Math.max(clusterEnd, entry.endMinute)
    }
    if (cluster.length > 0) flush()
  }
  return { startHour, endHour, hours, blocks }
}
