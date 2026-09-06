package handlers

import (
	"sort"
	"time"
	_ "time/tzdata" // slot zones must resolve inside the slim runtime image

	"github.com/dreamtrans/backend/internal/models"
)

// 课表归类 — the matcher. Pure: a session's start and length against the
// user's weekly class slots, no model, no clock.
//
// Rules, in words:
//   - A slot is a weekly wall-clock window in its own timezone; the session's
//     start is converted into that zone to find the local day and weekday.
//   - A recording of known length matches a slot when the two overlap and the
//     overlap covers at least half of the recording or half of the class.
//     Back-to-back classes are separated by the larger overlap; a short test
//     recording inside a class still counts.
//   - A recording whose length is not yet known (it was just created) matches
//     when it starts from twenty minutes before the class until ten minutes
//     before it ends. Nearest class start wins.
//   - A course with a week_start ignores sessions from before that date, so a
//     re-used course does not swallow last semester's recordings.

const (
	// timetableLead is how early a recording may start before the class.
	timetableLead = 20 * time.Minute
	// timetableMinRemaining: a recording begun later than this before the end
	// of a class is not that class.
	timetableMinRemaining = 10 * time.Minute
	// timetableKnownLength: below this the recorded length says nothing yet.
	timetableKnownLength = 5 * time.Minute
)

type timetableSlot struct {
	ID          string
	ProjectID   string
	Weekday     int // ISO, 1 = Monday
	StartMinute int
	EndMinute   int
	Location    *time.Location
	// Zero when the course has no week_start.
	WeekStart time.Time
}

type timetableMatch struct {
	Slot    timetableSlot
	Overlap time.Duration
	// Distance between the recording start and the class start.
	Distance time.Duration
}

// timetableSlotsFrom prepares stored slots for matching, dropping any whose
// zone no longer loads. weekStarts may be nil.
func timetableSlotsFrom(slots []models.CourseSlot, weekStarts map[string]time.Time) []timetableSlot {
	prepared := make([]timetableSlot, 0, len(slots))
	for i := range slots {
		slot := &slots[i]
		location, err := time.LoadLocation(slot.Timezone)
		if err != nil {
			continue
		}
		prepared = append(prepared, timetableSlot{
			ID: slot.ID, ProjectID: slot.ProjectID, Weekday: slot.Weekday,
			StartMinute: slot.StartMinute, EndMinute: slot.EndMinute,
			Location: location, WeekStart: weekStarts[slot.ProjectID],
		})
	}
	return prepared
}

func isoWeekday(t time.Time) int {
	return (int(t.Weekday())+6)%7 + 1
}

// slotWindow returns the slot's absolute window on the local day containing
// startedAt, or false when that day is not the slot's weekday or is before
// the course began.
func (slot *timetableSlot) window(startedAt time.Time) (start, end time.Time, ok bool) {
	local := startedAt.In(slot.Location)
	if isoWeekday(local) != slot.Weekday {
		return time.Time{}, time.Time{}, false
	}
	year, month, day := local.Date()
	if !slot.WeekStart.IsZero() {
		wy, wm, wd := slot.WeekStart.Date()
		if time.Date(year, month, day, 0, 0, 0, 0, time.UTC).Before(time.Date(wy, wm, wd, 0, 0, 0, 0, time.UTC)) {
			return time.Time{}, time.Time{}, false
		}
	}
	start = time.Date(year, month, day, slot.StartMinute/60, slot.StartMinute%60, 0, 0, slot.Location)
	end = time.Date(year, month, day, slot.EndMinute/60, slot.EndMinute%60, 0, 0, slot.Location)
	return start, end, true
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// matchSessionToTimetable picks the slot a recording belongs to, or nil.
func matchSessionToTimetable(startedAt time.Time, length time.Duration, slots []timetableSlot) *timetableMatch {
	if startedAt.IsZero() || len(slots) == 0 {
		return nil
	}
	if length < 0 {
		length = 0
	}
	known := length >= timetableKnownLength
	matches := make([]timetableMatch, 0, 2)
	for i := range slots {
		slot := &slots[i]
		classStart, classEnd, ok := slot.window(startedAt)
		if !ok {
			continue
		}
		match := timetableMatch{Slot: *slot, Distance: absDuration(startedAt.Sub(classStart))}
		if known {
			recordingEnd := startedAt.Add(length)
			overlapStart, overlapEnd := startedAt, recordingEnd
			if classStart.After(overlapStart) {
				overlapStart = classStart
			}
			if classEnd.Before(overlapEnd) {
				overlapEnd = classEnd
			}
			overlap := overlapEnd.Sub(overlapStart)
			if overlap <= 0 {
				continue
			}
			classLength := classEnd.Sub(classStart)
			if overlap*2 < length && overlap*2 < classLength {
				continue
			}
			match.Overlap = overlap
		} else {
			earliest := classStart.Add(-timetableLead)
			latest := classEnd.Add(-timetableMinRemaining)
			if latest.Before(classStart) {
				latest = classStart
			}
			if startedAt.Before(earliest) || startedAt.After(latest) {
				continue
			}
		}
		matches = append(matches, match)
	}
	if len(matches) == 0 {
		return nil
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Overlap != matches[j].Overlap {
			return matches[i].Overlap > matches[j].Overlap
		}
		if matches[i].Distance != matches[j].Distance {
			return matches[i].Distance < matches[j].Distance
		}
		return matches[i].Slot.ID < matches[j].Slot.ID
	})
	best := matches[0]
	return &best
}
