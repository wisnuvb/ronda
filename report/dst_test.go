package report

import (
	"testing"
	"time"

	"github.com/wisnuvb/ronda/model"
)

// A daily report at 08:00 must fire at 08:00 on the wall clock the day after the clocks change, and the
// slot must not move by the hour that was gained or lost.
func TestReportSlotKeepsTheWallClockAcrossDaylightSaving(t *testing.T) {
	if err := model.SetTimezone("America/New_York"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.SetTimezone("Asia/Jakarta") })
	ny := model.Loc()

	for _, day := range []time.Time{
		time.Date(2026, 3, 8, 12, 0, 0, 0, ny),  // clocks jumped forward that morning
		time.Date(2026, 11, 1, 12, 0, 0, 0, ny), // clocks went back that morning
		time.Date(2026, 7, 4, 12, 0, 0, 0, ny),  // ordinary
	} {
		got := slot(day, Daily, "08:00", 0).In(ny)
		if got.Hour() != 8 || got.Minute() != 0 || got.YearDay() != day.YearDay() {
			t.Errorf("%s: slot = %s, want 08:00 the same day", day.Format("2006-01-02"), got)
		}
	}
}

func TestStampNamesTheZoneAndFollowsDaylightSaving(t *testing.T) {
	if err := model.SetTimezone("Europe/Berlin"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.SetTimezone("Asia/Jakarta") })
	summer := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	winter := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).UnixMilli()
	if got := model.Stamp(summer, "15:04"); got != "14:00 CEST" {
		t.Errorf("summer: %q", got)
	}
	if got := model.Stamp(winter, "15:04"); got != "13:00 CET" {
		t.Errorf("winter: %q", got)
	}
}
