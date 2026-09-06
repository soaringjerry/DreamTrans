package handlers

import (
	"net/http"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
)

func melbourne(t *testing.T) *time.Location {
	t.Helper()
	location, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func TestTimetableRoutesParse(t *testing.T) {
	projectID := "11111111-1111-4111-8111-111111111111"
	slotID := "22222222-2222-4222-8222-222222222222"
	route, status, err := parseAIProjectRoute("/api/ai/projects/" + projectID + "/timetable")
	if err != nil || status != http.StatusOK || route.Resource != "timetable" || route.ResourceID != "" {
		t.Fatalf("collection route = %#v %d %v", route, status, err)
	}
	route, status, err = parseAIProjectRoute("/api/ai/projects/" + projectID + "/timetable/" + slotID)
	if err != nil || status != http.StatusOK || route.ResourceID != slotID {
		t.Fatalf("item route = %#v %d %v", route, status, err)
	}
	if _, status, err = parseAIProjectRoute("/api/ai/projects/" + projectID + "/timetable/not-a-uuid"); err == nil || status != http.StatusBadRequest {
		t.Fatalf("bad slot id accepted: %d %v", status, err)
	}
	if _, status, err = parseAIProjectRoute("/api/ai/projects/" + projectID + "/timetable/" + slotID + "/extra"); err == nil || status != http.StatusNotFound {
		t.Fatalf("extra path accepted: %d %v", status, err)
	}
}

func TestTimetableSlotsFromDropsUnknownZones(t *testing.T) {
	slots := timetableSlotsFrom([]models.CourseSlot{
		{ID: "ok", ProjectID: "p", Weekday: 1, StartMinute: 600, EndMinute: 720, Timezone: "Australia/Melbourne"},
		{ID: "bad", ProjectID: "p", Weekday: 1, StartMinute: 600, EndMinute: 720, Timezone: "Mars/Olympus"},
	}, map[string]time.Time{"p": time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)})
	if len(slots) != 1 || slots[0].ID != "ok" || slots[0].WeekStart.IsZero() {
		t.Fatalf("slots = %#v", slots)
	}
}

func TestMatchSessionToTimetable(t *testing.T) {
	loc := melbourne(t)
	// Monday 10:00–12:00 course A, Monday 12:00–13:00 course B (back to back),
	// Wednesday 14:00–15:00 course A tutorial.
	slots := []timetableSlot{
		{ID: "a-mon", ProjectID: "A", Weekday: 1, StartMinute: 600, EndMinute: 720, Location: loc},
		{ID: "b-mon", ProjectID: "B", Weekday: 1, StartMinute: 720, EndMinute: 780, Location: loc},
		{ID: "a-wed", ProjectID: "A", Weekday: 3, StartMinute: 840, EndMinute: 900, Location: loc},
	}
	monday := func(hour, minute int) time.Time {
		return time.Date(2026, 3, 2, hour, minute, 0, 0, loc) // a Monday
	}
	cases := []struct {
		name    string
		start   time.Time
		length  time.Duration
		want    string
		overlap time.Duration
	}{
		{"whole lecture", monday(10, 2), 110 * time.Minute, "a-mon", 110 * time.Minute},
		{"started early, ran the lecture", monday(9, 45), 130 * time.Minute, "a-mon", 115 * time.Minute},
		{"short test during lecture", monday(10, 30), 10 * time.Minute, "a-mon", 10 * time.Minute},
		{"back to back: larger overlap wins", monday(11, 50), 65 * time.Minute, "b-mon", 55 * time.Minute},
		{"unknown length at class start", monday(10, 0), 0, "a-mon", 0},
		{"unknown length fifteen minutes early", monday(9, 45), 0, "a-mon", 0},
		{"unknown length just before lecture ends belongs to next class", monday(11, 55), 0, "b-mon", 0},
		{"unknown length too early", monday(9, 30), 0, "", 0},
		{"unknown length in the evening", monday(19, 0), 0, "", 0},
		{"known length outside every class", monday(15, 0), time.Hour, "", 0},
		{"lunch recording barely touching class", monday(12, 50), 3 * time.Hour, "", 0},
		{"wednesday tutorial", time.Date(2026, 3, 4, 14, 5, 0, 0, loc), 50 * time.Minute, "a-wed", 50 * time.Minute},
		{"sunday", time.Date(2026, 3, 8, 10, 0, 0, 0, loc), time.Hour, "", 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			match := matchSessionToTimetable(test.start, test.length, slots)
			got := ""
			if match != nil {
				got = match.Slot.ID
			}
			if got != test.want {
				t.Fatalf("slot = %q, want %q", got, test.want)
			}
			if match != nil && match.Overlap != test.overlap {
				t.Fatalf("overlap = %v, want %v", match.Overlap, test.overlap)
			}
		})
	}
	if matchSessionToTimetable(time.Time{}, time.Hour, slots) != nil || matchSessionToTimetable(monday(10, 0), time.Hour, nil) != nil {
		t.Fatal("zero start or no slots must not match")
	}
}

func TestMatchSessionToTimetableUsesSlotZoneAndWeekStart(t *testing.T) {
	loc := melbourne(t)
	slots := []timetableSlot{{
		ID: "mon", ProjectID: "A", Weekday: 1, StartMinute: 600, EndMinute: 720, Location: loc,
		WeekStart: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC),
	}}
	// 23:00 UTC Sunday is 10:00 Monday in Melbourne (AEDT, +11).
	sundayUTC := time.Date(2026, 3, 1, 23, 0, 0, 0, time.UTC)
	if match := matchSessionToTimetable(sundayUTC, time.Hour, slots); match == nil {
		t.Fatal("UTC Sunday evening is the Melbourne Monday lecture")
	}
	// A week earlier is before the course began.
	if match := matchSessionToTimetable(sundayUTC.AddDate(0, 0, -7), time.Hour, slots); match != nil {
		t.Fatal("sessions before week_start must not be filed")
	}
	// Across a daylight-saving change the wall clock still decides: Melbourne
	// leaves AEDT on 2026-04-05; Monday 2026-04-06 10:00 is 00:00 UTC.
	afterDST := time.Date(2026, 4, 6, 0, 0, 0, 0, time.UTC)
	if match := matchSessionToTimetable(afterDST, time.Hour, slots); match == nil {
		t.Fatal("wall-clock slot must follow the zone across DST")
	}
}

func TestSlotWindowIsDeterministicBetweenEqualCourses(t *testing.T) {
	loc := melbourne(t)
	slots := []timetableSlot{
		{ID: "z", ProjectID: "Z", Weekday: 1, StartMinute: 600, EndMinute: 720, Location: loc},
		{ID: "a", ProjectID: "A", Weekday: 1, StartMinute: 600, EndMinute: 720, Location: loc},
	}
	match := matchSessionToTimetable(time.Date(2026, 3, 2, 10, 0, 0, 0, loc), time.Hour, slots)
	if match == nil || match.Slot.ID != "a" {
		t.Fatalf("tie must break on slot id, got %#v", match)
	}
}
