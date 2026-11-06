package history

import (
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/model"
)

// A "day" is a calendar day in the configured zone. When daylight saving changes it is 23 or 25 hours
// long, so a fixed 24 h bucket would put the last hour of one day into the next.
func TestDailyBucketsFollowCalendarDaysAcrossDaylightSaving(t *testing.T) {
	if err := model.SetTimezone("Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.SetTimezone("Asia/Jakarta") })
	berlin := model.Loc()

	// Berlin clocks go back on Sunday 25 Oct 2026 (25 hours). "Now" is Monday 26 Oct 10:00 Berlin.
	now := time.Date(2026, 10, 26, 10, 0, 0, 0, berlin)
	s := newStore(&now)
	at := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 30, 0, 0, berlin) }

	s.Record("a", ok(at(24, 23), 10)) // last hour of Saturday
	s.Record("a", ok(at(25, 0), 10))  // first hour of the 25-hour Sunday
	s.Record("a", ok(at(25, 23), 10)) // last hour of Sunday: must still be Sunday
	s.Record("a", ok(at(26, 0), 10))  // first hour of Monday

	d := s.Daily("a", 3) // Sat 24, Sun 25, Mon 26
	if len(d) != 3 {
		t.Fatalf("len = %d", len(d))
	}
	if d[0].Total != 1 || d[1].Total != 2 || d[2].Total != 1 {
		t.Fatalf("Sat/Sun/Mon should hold 1/2/1 checks: %+v", d)
	}
	if got := time.UnixMilli(d[2].Start).In(berlin); got.Hour() != 0 || got.Day() != 26 {
		t.Fatalf("today starts at %s, want 00:00 on the 26th", got)
	}
	// The gap between two day starts is the length of the earlier day.
	if span := d[1].Start - d[0].Start; span != 24*3600*1000 {
		t.Fatalf("Saturday is %d ms long, want 24 h", span)
	}
	if span := d[2].Start - d[1].Start; span != 25*3600*1000 {
		t.Fatalf("Sunday is %d ms long, want 25 h on the day the clocks go back", span)
	}
}
