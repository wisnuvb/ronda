package schedule

import (
	"testing"
	"time"

	"github.com/wisnuvb/ronda/internal/model"
)

// 2026-11-05 is a Thursday, so the 6th is Friday, the 7th Saturday and the 8th Sunday.
func wib(day int, hhmm string) time.Time {
	var h, m int
	_, _ = timeFmt(hhmm, &h, &m)
	return time.Date(2026, 11, day, h, m, 0, 0, jakarta)
}

func timeFmt(s string, h, m *int) (int, error) {
	*h = int(s[0]-'0')*10 + int(s[1]-'0')
	*m = int(s[3]-'0')*10 + int(s[4]-'0')
	return 0, nil
}

const (
	sun, mon, wed, fri, sat = 0, 1, 3, 5, 6
)

func win(start string, mins int, days ...int) model.Window {
	return model.Window{Days: days, Start: start, Minutes: mins}
}

func TestActiveBoundaries(t *testing.T) {
	ws := []model.Window{win("02:00", 120, sat)}
	for _, c := range []struct {
		at     time.Time
		active bool
	}{
		{wib(7, "01:59"), false},
		{wib(7, "02:00"), true}, // the start itself is inside
		{wib(7, "03:59"), true},
		{wib(7, "04:00"), false}, // the end itself is outside
		{wib(6, "03:00"), false}, // right time of day, wrong day
		{wib(8, "03:00"), false},
	} {
		end, ok := Active(ws, c.at, 0)
		if ok != c.active {
			t.Errorf("at %s: active=%v, want %v", c.at.Format("Mon 15:04"), ok, c.active)
		}
		if ok && !end.Equal(wib(7, "04:00")) {
			t.Errorf("at %s: ends %s, want Sat 04:00", c.at.Format("Mon 15:04"), end.Format("Mon 15:04"))
		}
	}
}

func TestWindowRunningPastMidnight(t *testing.T) {
	// Friday 23:00 for three hours runs until Saturday 02:00: Saturday is not in Days, yet at 01:00 it is active.
	ws := []model.Window{win("23:00", 180, fri)}
	if end, ok := Active(ws, wib(7, "01:00"), 0); !ok || !end.Equal(wib(7, "02:00")) {
		t.Fatalf("a window that began on Friday evening is still running on Saturday 01:00: %v %v", end, ok)
	}
	if _, ok := Active(ws, wib(7, "02:00"), 0); ok {
		t.Fatal("it is over at 02:00")
	}
	if _, ok := Active(ws, wib(6, "22:59"), 0); ok {
		t.Fatal("it has not begun at 22:59")
	}
}

func TestWindowsAreEvaluatedInWIBNotUTC(t *testing.T) {
	// 02:00 WIB is 19:00 UTC the day before. Asking in UTC must give the same answer.
	ws := []model.Window{win("02:00", 60, sat)}
	inUTC := time.Date(2026, 11, 6, 19, 30, 0, 0, time.UTC) // = Sat 02:30 WIB
	if _, ok := Active(ws, inUTC, 0); !ok {
		t.Fatal("02:30 WIB on Saturday is inside the window, whatever zone the caller uses")
	}
	if _, ok := Active(ws, time.Date(2026, 11, 7, 2, 30, 0, 0, time.UTC), 0); ok {
		t.Fatal("02:30 UTC is 09:30 WIB, outside")
	}
}

func TestOverlappingWindowsEndWhenTheLastEnds(t *testing.T) {
	ws := []model.Window{win("02:00", 60, sat), win("02:30", 120, sat)}
	if end, ok := Active(ws, wib(7, "02:45"), 0); !ok || !end.Equal(wib(7, "04:30")) {
		t.Fatalf("got %v %v, want active until 04:30", end, ok)
	}
}

func TestSkippingOneRunKeepsTheSchedule(t *testing.T) {
	ws := []model.Window{win("02:00", 120, sat)}
	skip := wib(7, "04:00").UnixMilli() // someone ended this Saturday's run by hand

	if _, ok := Active(ws, wib(7, "03:00"), skip); ok {
		t.Fatal("the run that was ended by hand must stay ended")
	}
	if end, ok := Active(ws, wib(14, "03:00"), skip); !ok || !end.Equal(wib(14, "04:00")) {
		t.Fatalf("next Saturday's run must still happen: %v %v", end, ok)
	}
}

func TestNextStart(t *testing.T) {
	ws := []model.Window{win("02:00", 120, sat), win("22:30", 30, mon, wed)}

	if got, ok := NextStart(ws, wib(5, "12:00"), 0); !ok || !got.Equal(wib(7, "02:00")) {
		t.Fatalf("from Thursday noon: %v %v, want Sat 02:00", got, ok)
	}
	if got, ok := NextStart(ws, wib(7, "03:00"), 0); !ok || !got.Equal(wib(9, "22:30")) {
		t.Fatalf("from inside Saturday's window the next start is Monday 22:30, got %v", got)
	}
	if got, ok := NextStart(ws, wib(7, "02:00"), 0); !ok || got.Equal(wib(7, "02:00")) {
		t.Fatalf("the next start is strictly after now: %v", got)
	}
	if _, ok := NextStart(nil, wib(5, "12:00"), 0); ok {
		t.Fatal("no windows, no next start")
	}
	// A week-long gap is still found.
	if got, ok := NextStart([]model.Window{win("02:00", 60, thu())}, wib(5, "12:00"), 0); !ok || !got.Equal(wib(12, "02:00")) {
		t.Fatalf("next Thursday: %v %v", got, ok)
	}
}

func thu() int { return 4 }

func TestValidate(t *testing.T) {
	ok := []model.Window{win("02:00", 120, sat, sun)}
	if err := Validate(ok); err != nil {
		t.Fatal(err)
	}
	if err := Validate(nil); err != nil {
		t.Fatal("no windows is valid:", err)
	}
	bad := map[string][]model.Window{
		"no days":          {win("02:00", 60)},
		"day out of range": {win("02:00", 60, 7)},
		"negative day":     {win("02:00", 60, -1)},
		"duplicate day":    {win("02:00", 60, sat, sat)},
		"bad time":         {win("2:00", 60, sat)},
		"hour 24":          {win("24:00", 60, sat)},
		"minute 60":        {win("02:60", 60, sat)},
		"letters":          {win("ab:cd", 60, sat)},
		"zero length":      {win("02:00", 0, sat)},
		"over a day":       {win("02:00", 1441, sat)},
		"too many":         make([]model.Window, MaxWindows+1),
	}
	for name, ws := range bad {
		if err := Validate(ws); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}
