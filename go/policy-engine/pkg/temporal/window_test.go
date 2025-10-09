package temporal

import (
	"testing"
	"time"
)

func TestOfficeHours(t *testing.T) {
	w, err := Parse("Mon-Fri 09:00-17:30 Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	cases := map[time.Time]bool{
		time.Date(2026, 10, 12, 9, 0, 0, 0, berlin):    true,  // Monday 09:00
		time.Date(2026, 10, 12, 17, 30, 0, 0, berlin):  false, // end is exclusive
		time.Date(2026, 10, 11, 12, 0, 0, 0, berlin):   false, // Sunday
		time.Date(2026, 10, 12, 7, 30, 0, 0, time.UTC): true,  // 09:30 in Berlin
	}
	for at, want := range cases {
		if got := w.Contains(at, time.Time{}); got != want {
			t.Errorf("%s: got %v", at, got)
		}
	}
}

func TestWrapAroundDaysAndFirstOfJob(t *testing.T) {
	w, _ := Parse("Fri-Mon 00:00-23:59")
	if !w.Contains(time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), time.Time{}) {
		t.Fatal("Sunday should be inside Fri-Mon")
	}
	f, _ := Parse("first 1h")
	start := time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)
	if !f.Contains(start.Add(59*time.Minute), start) || f.Contains(start.Add(61*time.Minute), start) {
		t.Fatal("first-hour window wrong")
	}
	if _, err := Parse("whenever"); err == nil {
		t.Fatal("bad window parsed")
	}
}
