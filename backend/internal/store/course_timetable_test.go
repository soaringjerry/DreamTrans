package store

import (
	"testing"

	"github.com/dreamtrans/backend/internal/models"
)

func TestParseAndFormatSlotMinute(t *testing.T) {
	good := map[string]int{"00:00": 0, "09:05": 545, "23:59": 1439, "24:00": 1440}
	for text, want := range good {
		got, err := ParseSlotMinute(text)
		if err != nil || got != want {
			t.Fatalf("ParseSlotMinute(%q) = %d, %v; want %d", text, got, err, want)
		}
		if FormatSlotMinute(want) != text {
			t.Fatalf("FormatSlotMinute(%d) = %q", want, FormatSlotMinute(want))
		}
	}
	for _, text := range []string{"", "9:00", "24:01", "25:00", "10:60", "10-00", "10:00:00", "abc"} {
		if _, err := ParseSlotMinute(text); err == nil {
			t.Fatalf("ParseSlotMinute(%q) accepted", text)
		}
	}
}

func TestValidateCourseSlot(t *testing.T) {
	slot := models.CourseSlot{Weekday: 1, Start: "10:00", End: "12:00", Timezone: " Australia/Melbourne ", Label: "  Lecture "}
	if err := ValidateCourseSlot(&slot); err != nil {
		t.Fatal(err)
	}
	if slot.StartMinute != 600 || slot.EndMinute != 720 || slot.Timezone != "Australia/Melbourne" || slot.Label != "Lecture" {
		t.Fatalf("normalized = %#v", slot)
	}
	bad := []models.CourseSlot{
		{Weekday: 0, Start: "10:00", End: "12:00", Timezone: "UTC"},
		{Weekday: 8, Start: "10:00", End: "12:00", Timezone: "UTC"},
		{Weekday: 1, Start: "12:00", End: "10:00", Timezone: "UTC"},
		{Weekday: 1, Start: "10:00", End: "10:00", Timezone: "UTC"},
		{Weekday: 1, Start: "10:00", End: "12:00", Timezone: ""},
		{Weekday: 1, Start: "10:00", End: "12:00", Timezone: "Local"},
		{Weekday: 1, Start: "10:00", End: "12:00", Timezone: "Mars/Olympus"},
		{Weekday: 1, Start: "24:00", End: "24:00", Timezone: "UTC"},
	}
	for _, slot := range bad {
		if err := ValidateCourseSlot(&slot); err == nil {
			t.Fatalf("accepted %#v", slot)
		}
	}
}
