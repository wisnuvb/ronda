package schedule

import (
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/model"
)

// in switches the program's zone for one test and restores the Jakarta zone the other tests use.
func in(t *testing.T, zone string) *time.Location {
	t.Helper()
	if err := model.SetTimezone(zone); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.SetTimezone("Asia/Jakarta") })
	return model.Loc()
}

// A window "02:00 for 60 min" must start at 02:00 on the wall clock on the day the clocks change, and
// stay 60 elapsed minutes long. Computed as midnight + 2 h it would start at 03:00 (spring) or 01:00 (autumn).
func TestWindowStartsAtTheWallClockAcrossDaylightSaving(t *testing.T) {
	ny := in(t, "America/New_York")
	// Sunday 8 Mar 2026: clocks jump 02:00 -> 03:00 in New York. Sunday 1 Nov 2026: they go back 02:00 -> 01:00.
	cases := []struct {
		name     string
		day      time.Time // a moment on the day of the window
		wantHour int
	}{
		{"an ordinary Sunday", time.Date(2026, 3, 1, 12, 0, 0, 0, ny), 2},
		{"day after the spring change", time.Date(2026, 3, 15, 12, 0, 0, 0, ny), 2},
		{"day after the autumn change", time.Date(2026, 11, 8, 12, 0, 0, 0, ny), 2},
	}
	w := model.Window{Days: []int{0}, Start: "02:00", Minutes: 60}
	for _, c := range cases {
		var got time.Time
		for _, s := range occurrences(w, c.day, 0) {
			if s.Year() == c.day.Year() && s.YearDay() == c.day.YearDay() {
				got = s
			}
		}
		if got.IsZero() {
			t.Fatalf("%s: no start on that day", c.name)
		}
		if got.In(ny).Hour() != c.wantHour || got.In(ny).Minute() != 0 {
			t.Errorf("%s: starts at %s, want 02:00 wall clock", c.name, got.In(ny))
		}
	}
}

func TestWindowIsActiveForItsWallClockHourAfterTheClocksGoBack(t *testing.T) {
	ny := in(t, "America/New_York")
	w := []model.Window{{Days: []int{0}, Start: "08:00", Minutes: 120}}
	// Sunday 1 Nov 2026 is 25 hours long. 08:00-10:00 local is the window, whatever the offset.
	if _, ok := Active(w, time.Date(2026, 11, 1, 8, 30, 0, 0, ny), 0); !ok {
		t.Error("08:30 local on the day the clocks go back must be inside a window that starts at 08:00")
	}
	if _, ok := Active(w, time.Date(2026, 11, 1, 7, 30, 0, 0, ny), 0); ok {
		t.Error("07:30 local is before the window")
	}
	if _, ok := Active(w, time.Date(2026, 11, 1, 10, 30, 0, 0, ny), 0); ok {
		t.Error("10:30 local is after a two hour window")
	}
}

func TestNextStartFollowsTheConfiguredZone(t *testing.T) {
	in(t, "Europe/Berlin")
	w := []model.Window{{Days: []int{3}, Start: "03:00", Minutes: 30}} // Wednesdays 03:00 Berlin
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)                // Wednesday 1 Jul, 02:00 Berlin (CEST, UTC+2)
	next, ok := NextStart(w, from, 0)
	if !ok {
		t.Fatal("no next start")
	}
	if want := time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC); !next.Equal(want) { // 03:00 CEST = 01:00 UTC
		t.Fatalf("next = %s, want %s", next.UTC(), want)
	}
}
