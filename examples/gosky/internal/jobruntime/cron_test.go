package jobruntime

import (
	"testing"
	"time"
)

// Failure-mode inventory:
//
//	CR-01 field-count and special-character rejection.
//	CR-02 valid expressions accepted; lists, ranges, steps and names.
//	CR-03 next-time for fixed seconds and daily cron.
//	CR-04 Sunday represented both as 0 and 7.
//	CR-05 fixed delay parsing.
func TestCronValidation(t *testing.T) {
	valid := []string{
		"5 * * * * *",
		"0 0 * * * *",
		"0,30 * * * * *",
		"0 0 9 ? * MON-FRI",
		"0 0 0 1 * ?",
		"0 0 12 1 1 ?",
		"*/10 * * * * *",
		"0 0 0 ? * SUN",
	}
	for _, expression := range valid {
		if !ValidateCron(expression) {
			t.Fatalf("expected valid cron: %q", expression)
		}
	}
	invalid := []string{
		"",
		"* * * * *",     // five fields
		"60 * * * * *",  // second out of range
		"* * * * * * *", // seven fields
		"0 0 0 L * *",   // unsupported special
		"x * * * * *",   // non-numeric
		"0 0 25 * * *",  // hour out of range
		"0 0 * 32 * *",  // day out of range
		"0 0 * * 13 *",  // month out of range
	}
	for _, expression := range invalid {
		if ValidateCron(expression) {
			t.Fatalf("expected invalid cron: %q", expression)
		}
	}
}

func TestCronNextFixedSecond(t *testing.T) {
	from := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	next, err := NextCron("5 * * * * *", from, 3)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	want := []time.Time{
		time.Date(2026, 10, 20, 9, 0, 5, 0, time.UTC),
		time.Date(2026, 10, 20, 9, 1, 5, 0, time.UTC),
		time.Date(2026, 10, 20, 9, 2, 5, 0, time.UTC),
	}
	if len(next) != 3 {
		t.Fatalf("next = %v", next)
	}
	for i := range want {
		if !next[i].Equal(want[i]) {
			t.Fatalf("next[%d] = %v, want %v", i, next[i], want[i])
		}
	}
}

func TestCronNextDailyAndWeekday(t *testing.T) {
	from := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC) // Tuesday
	next, err := NextCron("0 0 9 * * *", from, 2)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if next[0] != time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC) ||
		next[1] != time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC) {
		t.Fatalf("daily next = %v", next)
	}

	// Monday-Friday 09:00: from Tuesday the next two runs are Wed and Thu.
	weekday, err := NextCron("0 0 9 ? * MON-FRI", from, 2)
	if err != nil {
		t.Fatalf("weekday: %v", err)
	}
	if weekday[0].Weekday() != time.Wednesday || weekday[1].Weekday() != time.Thursday {
		t.Fatalf("weekday next = %v", weekday)
	}

	// Sunday both 0 and 7: 2026-10-25 is Sunday.
	seven, err := NextCron("0 0 0 ? * 7", from, 1)
	if err != nil {
		t.Fatalf("sunday 7: %v", err)
	}
	if seven[0] != time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("sunday 7 next = %v", seven[0])
	}
}

func TestFixedDelay(t *testing.T) {
	if !ValidateFixedDelay("30") {
		t.Fatal("30 should be valid")
	}
	for _, value := range []string{"0", "-1", "abc", ""} {
		if ValidateFixedDelay(value) {
			t.Fatalf("%q should be invalid", value)
		}
	}
	seconds, err := ParseFixedDelay(" 45 ")
	if err != nil || seconds != 45 {
		t.Fatalf("parse = %d, %v", seconds, err)
	}
}
